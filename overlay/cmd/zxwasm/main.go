//go:build js

// Command zxwasm is the path-B in-browser bridge spike: it compiles ONLY the
// emulator core (pkg/z80, pkg/memory, pkg/ula, pkg/keyboard) to wasm and
// exposes a tiny syscall/js API. No Fyne app, no oto -- the host page owns the
// <canvas>, the animation loop, keyboard, and (later) WebAudio.
//
// JS API installed on the global object:
//
//	zxFrame(pixBuf Uint8Array) -> {w,h}   advance one 50Hz frame, blit RGBA into pixBuf
//	zxAudio(sndBuf Uint8Array) -> nSamps  drain queued 44.1kHz mono int16-LE audio
//	zxKey(row, mask, down)                press/release a raw ZX matrix key
//	zxType(charCode)                      type an ASCII rune via the keyboard helper
//	zxRegisterROM(name, bytes)            inject a Next ROM blob (no filesystem on wasm)
//	zxBootNext(sdImage) -> null | errstr  replace the machine with a Spectrum Next
//
// The default machine is the classic 48K (its ROMs are go:embed'd in
// pkg/roms/data, so memory.New needs no filesystem). The host page can switch
// to a Spectrum Next by registering the NextZXOS ROMs then calling zxBootNext
// with a raw SD-card image; the machine assembly lives in next.go.
package main

import (
	"fmt"
	"syscall/js"

	"fyne.io/fyne/v2"

	"github.com/conorarmstrong/zx_go/pkg/keyboard"
	"github.com/conorarmstrong/zx_go/pkg/memory"
	"github.com/conorarmstrong/zx_go/pkg/next/install"
	"github.com/conorarmstrong/zx_go/pkg/next/nextregs"
	"github.com/conorarmstrong/zx_go/pkg/next/sdcard"
	"github.com/conorarmstrong/zx_go/pkg/roms"
	"github.com/conorarmstrong/zx_go/pkg/ula"
	"github.com/conorarmstrong/zx_go/pkg/z80"
)

// frameTStates48K is the 48K ULA frame length at 3.5 MHz. ExecuteFrame fires
// the maskable INT internally, so one call per animation frame runs the
// machine. The Next runs the longer +3/128K frame (see next.go).
const frameTStates48K = 69888

type machine struct {
	cpu    *z80.CPU
	ula    *ula.ULA
	kbd    *keyboard.Keyboard
	frameT int           // ULA frame length in 3.5 MHz T-states
	macro  *nexloadMacro // when non-nil, drives .nexload keystrokes each frame

	// Next-only: warm-reload support.
	disp          *nextregs.Dispatcher // NextReg dispatcher, for the NR$02 reset poke
	sdBytes       []byte               // live SD image the card aliases; zxRunNex injects here
	firstLoadDone bool                 // first program written (cold boot reads autoexec); later loads hard-reset

	// audioAccum holds mono 44.1 kHz samples produced by frame() (including
	// wall-clock catch-up frames), drained by zxAudio. Capped at audioAccumCap
	// so a paused/hidden tab that stops draining can't grow it unbounded or
	// build latency -- excess is dropped oldest-first.
	audioAccum []int16
}

// audioAccumCap bounds the undrained audio backlog (~8 frames ~ 160 ms at
// 882 samples/frame). Past this the oldest samples are discarded.
const audioAccumCap = 882 * 8

func newMachine() *machine {
	kbd := keyboard.New()
	mem, err := memory.New("roms", roms.Model48K) // ROMs come from the embedded FS on wasm
	if err != nil {
		panic(err)
	}
	u := ula.New(mem, kbd)
	u.EnableAudioCapture() // no oto sink on wasm; pull frames via RenderAudioFrame
	cpu := z80.New(mem, u)
	return &machine{cpu: cpu, ula: u, kbd: kbd, frameT: frameTStates48K}
}

var uint8Array = js.Global().Get("Uint8Array")

func (m *machine) frame(this js.Value, args []js.Value) any {
	m.kbd.Tick() // advance/release any typed-character key pulse for this frame
	m.cpu.ExecuteFrame(m.frameT)
	if m.macro != nil && m.macro.tick(m) {
		m.macro = nil
	}
	// Synthesise this frame's audio (beeper + DAC + tape + active AY) and queue
	// it for zxAudio. Runs for catch-up frames too, so no samples are dropped
	// when a throttled rAF advances several guest frames per tick.
	m.audioAccum = append(m.audioAccum, m.ula.RenderAudioFrame()...)
	if len(m.audioAccum) > audioAccumCap {
		m.audioAccum = m.audioAccum[len(m.audioAccum)-audioAccumCap:]
	}
	img := m.ula.Render()
	// Only blit when a real Uint8Array framebuffer was passed. CopyBytesToJS
	// PANICS (and, unrecovered, exits the whole Go runtime -- killing the
	// emulator) if dst is any other type; a stray/mistyped call must not be
	// able to do that.
	if len(args) > 0 && args[0].InstanceOf(uint8Array) {
		js.CopyBytesToJS(args[0], img.Pix)
	}
	b := img.Bounds()
	return map[string]any{"w": b.Dx(), "h": b.Dy()}
}

// audio drains queued mono samples into the Uint8Array in args[0] as
// little-endian int16 (2 bytes/sample) and returns the number of SAMPLES
// written. The host reinterprets the bytes as an Int16Array and feeds the
// audio worklet. Drained samples are removed; a stalled host just backs up to
// audioAccumCap and drops the oldest (see frame). Returns 0 if the buffer is
// missing/mistyped or nothing is queued.
func (m *machine) audio(this js.Value, args []js.Value) any {
	if len(args) == 0 || !args[0].InstanceOf(uint8Array) {
		return 0
	}
	capSamples := args[0].Get("length").Int() / 2
	n := len(m.audioAccum)
	if n > capSamples {
		n = capSamples
	}
	if n == 0 {
		return 0
	}
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		s := uint16(m.audioAccum[i])
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	js.CopyBytesToJS(args[0], out)
	m.audioAccum = append(m.audioAccum[:0], m.audioAccum[n:]...)
	return n
}

func (m *machine) key(this js.Value, args []js.Value) any {
	m.kbd.PressMatrixKey(args[0].Int(), byte(args[1].Int()), args[2].Bool())
	return nil
}

func (m *machine) typeRune(this js.Value, args []js.Value) any {
	return m.kbd.TypeRune(rune(args[0].Int()))
}

// keyName routes a host key through the real host-key-to-matrix map. name is a
// Fyne key name ("1", "A", "Return", "Space", ...); the host page maps browser
// event keys onto these. Down/up drive a proper press/release, no Tick needed.
func (m *machine) keyName(this js.Value, args []js.Value) any {
	m.kbd.HandleKeyWithModifiers(fyne.KeyName(args[0].String()), args[1].Bool(), args[2].Bool(), false, false, false)
	return nil
}

// cur is the live machine. The exported funcs dispatch through it so
// zxBootNext can swap the whole machine out from under a running page
// (the JS side owns the animation loop; there is no Go-side goroutine
// touching cur concurrently).
var cur *machine

// registerROM copies a JS Uint8Array into the install package's
// in-memory ROM registry. Must be called for each Next ROM before
// zxBootNext -- under GOOS=js there is no filesystem to install into.
func registerROM(this js.Value, args []js.Value) any {
	name := args[0].String()
	buf := make([]byte, args[1].Get("length").Int())
	js.CopyBytesToGo(buf, args[1])
	install.RegisterROM(name, buf)
	return nil
}

// sdPristine is a clean copy of the boot SD image (no user program).
// Each zxRunNex rebuilds the live card from this -- inject autoexec.bas +
// prog.nex into a fresh clone, overwrite the live bytes in place -- so the
// directory never grows and the paths stay fixed. Kept package-level.
var sdPristine []byte

// bootNext replaces the live machine with a Spectrum Next booting off
// the supplied raw SD-card image (Uint8Array). This is the ONE cold
// boot per session (~30s guest time: FPGA loader -> TBBLUE.FW ->
// NextZXOS from SD); every subsequent zxRunNex rebuilds the card +
// resets this same machine. Returns null on success or an error string
// -- panicking across the js boundary would kill the whole Go runtime.
func bootNext(this js.Value, args []js.Value) any {
	sd := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(sd, args[0]) // already a private Go slice; ImageSource aliases it
	sdPristine = append([]byte(nil), sd...)
	// Inject autoexec.bas into the boot image so NextZXOS mounts + runs it on
	// the FIRST cold boot too (it .nexloads /imported/prog.nex; absent on first
	// boot, so it just falls through to the menu). zxRunNex then writes prog.nex
	// and resets to re-run it. Injecting into the mounted image up front avoids
	// the "autoexec written after mount -> not seen" race.
	if _, err := sdcard.AddFileToFAT32(sd, "nextzxos", "autoexec.bas", autoexecBAS); err != nil {
		return "write autoexec.bas: " + err.Error()
	}
	m, err := newNextMachine(sd)
	if err != nil {
		return err.Error()
	}
	cur = m
	return nil
}

// runNex loads a .nex (name unused, bytes Uint8Array) by the autoexec
// route -- NO keystroke macro, so nothing to race:
//
//  1. clone the pristine SD image (fresh, no growing directory)
//  2. write our tokenised autoexec.bas -> c:/nextzxos/autoexec.bas
//     (it does `.nexload /imported/prog.nex` with autostart)
//  3. write the program -> c:/imported/prog.nex (fixed path)
//  4. overwrite the live card bytes in place (both are 64 MB)
//  5. if NextZXOS already reached its menu, hard-reset (NR$02 bit 1) --
//     re-mounts the card fresh and re-runs autoexec, RAM-resident (~2s,
//     no 30s SD reload). On the first load the cold boot is still in
//     progress and will read the just-written autoexec when it gets there.
func runNex(this js.Value, args []js.Value) any {
	if cur == nil || cur.sdBytes == nil || sdPristine == nil {
		return "no Next machine booted -- call zxBootNext first"
	}
	data := make([]byte, args[1].Get("length").Int())
	js.CopyBytesToGo(data, args[1])

	// C/asm output is a .nex. NextZXOS can't run a .nex directly, so the
	// autoexec is a fixed loader stub that .nexloads it: write the embedded
	// autoexec.bas (`10 .nexload /imported/prog.nex`) + the user's .nex.
	return cur.rebuildAndRun([]sdFile{
		{"nextzxos", "autoexec.bas", autoexecBAS},
		{"imported", "prog.nex", data},
	})
}

// runBas loads a tokenised NextBASIC program (PLUS3DOS .bas, bytes in
// args[1]). A NextBASIC program IS a valid autoexec, so there is no .nex
// wrapper and no .nexload indirection: the user's tokenised bytes are
// written straight to c:/nextzxos/autoexec.bas and NextZXOS RUNs them at
// boot (the .bas must carry a PLUS3DOS autostart line -- the IDE's txt2bas
// tool sets it). Shares the exact clone->overwrite->reset path as runNex.
func runBas(this js.Value, args []js.Value) any {
	if cur == nil || cur.sdBytes == nil || sdPristine == nil {
		return "no Next machine booted -- call zxBootNext first"
	}
	data := make([]byte, args[1].Get("length").Int())
	js.CopyBytesToGo(data, args[1])

	return cur.rebuildAndRun([]sdFile{
		{"nextzxos", "autoexec.bas", data},
	})
}

// sdFile is one file to inject into the FAT32 card (dir, name, contents).
type sdFile struct {
	dir, name string
	data      []byte
}

// rebuildAndRun is the shared warm-reload core behind runNex/runBas: clone
// the pristine SD image, inject the given files, overwrite the live 64 MB
// card in place, then either let the in-progress cold boot pick it up (first
// load) or hard-reset (NR$02 bit 1) so NextZXOS re-mounts + re-runs autoexec
// RAM-resident (~2s, no 30s SD reload). Directory never grows -- fixed paths,
// fresh clone each time. Returns nil on success or an error string.
func (m *machine) rebuildAndRun(files []sdFile) any {
	fresh := append([]byte(nil), sdPristine...)
	for _, f := range files {
		if _, err := sdcard.AddFileToFAT32(fresh, f.dir, f.name, f.data); err != nil {
			return "write " + f.name + ": " + err.Error()
		}
	}
	if len(fresh) != len(m.sdBytes) {
		return "sd image size changed unexpectedly"
	}
	copy(m.sdBytes, fresh) // overwrite the live card in place; ImageSource sees it

	m.macro = nil // autoexec drives the load now
	if !m.firstLoadDone {
		// First program: the cold boot is still in progress and will mount +
		// run autoexec once it gets there. No reset needed.
		m.firstLoadDone = true
	} else {
		m.disp.WriteReg(0x02, 0x02)
	}
	return nil
}

// expose registers a js.Func that recovers from any panic in fn, so a
// single bad call (e.g. a mistyped framebuffer arg) can never propagate
// out and exit the whole Go runtime -- which would kill the emulator.
// On panic it returns the error string to JS.
func expose(name string, fn func(js.Value, []js.Value) any) {
	js.Global().Set(name, js.FuncOf(func(this js.Value, args []js.Value) (result any) {
		defer func() {
			if r := recover(); r != nil {
				result = "panic: " + fmt.Sprint(r)
			}
		}()
		return fn(this, args)
	}))
}

func main() {
	cur = newMachine()
	expose("zxFrame", func(t js.Value, a []js.Value) any { return cur.frame(t, a) })
	expose("zxAudio", func(t js.Value, a []js.Value) any { return cur.audio(t, a) })
	expose("zxKey", func(t js.Value, a []js.Value) any { return cur.key(t, a) })
	expose("zxType", func(t js.Value, a []js.Value) any { return cur.typeRune(t, a) })
	expose("zxKeyName", func(t js.Value, a []js.Value) any { return cur.keyName(t, a) })
	expose("zxRegisterROM", registerROM)
	expose("zxBootNext", bootNext)
	expose("zxRunNex", runNex)
	expose("zxRunBas", runBas)
	// Debug peek: current PC plus nexload-macro progress (-1 = no macro).
	expose("zxDebug", func(t js.Value, a []js.Value) any {
		step, frame := -1, -1
		if cur.macro != nil {
			step, frame = cur.macro.idx, cur.macro.frame
		}
		return map[string]any{"pc": int(cur.cpu.PC), "macroStep": step, "macroFrame": frame}
	})
	js.Global().Set("zxReady", js.ValueOf(true)) // host polls this to know the API is installed
	select {}                                    // keep the Go runtime alive for the exported funcs
}
