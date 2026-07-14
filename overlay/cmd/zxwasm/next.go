//go:build js

// Spectrum Next machine assembly for the wasm bridge.
//
// This is the essential wiring of cmd/zx_go's newNextEmulator +
// wireNextSubsystems with everything browser-irrelevant stripped:
// no Fyne, no env-var debug tracers, no warm-boot snapshots, no
// esxDOS host-directory shim (we are always in SD-image mode, where
// the guest's own divMMC/+3DOS code does all filesystem work), and
// no host audio yet (WebAudio is a later step).
//
// ROMs arrive via install.RegisterROM (the page fetches them over
// HTTP and injects the bytes before calling zxBootNext) -- there is
// no real filesystem under GOOS=js. The SD card is a raw image
// (tbblue.mmc / sd.img) passed in as a byte slice; guest writes stay
// in wasm memory for the session.
package main

import (
	"errors"
	"fmt"

	"github.com/conorarmstrong/zx_go/pkg/ay"
	"github.com/conorarmstrong/zx_go/pkg/keyboard"
	"github.com/conorarmstrong/zx_go/pkg/memory"
	"github.com/conorarmstrong/zx_go/pkg/next"
	"github.com/conorarmstrong/zx_go/pkg/next/compositor"
	"github.com/conorarmstrong/zx_go/pkg/next/copper"
	"github.com/conorarmstrong/zx_go/pkg/next/dac"
	"github.com/conorarmstrong/zx_go/pkg/next/divmmc"
	"github.com/conorarmstrong/zx_go/pkg/next/dma"
	"github.com/conorarmstrong/zx_go/pkg/next/install"
	"github.com/conorarmstrong/zx_go/pkg/next/keymap"
	"github.com/conorarmstrong/zx_go/pkg/next/layer2"
	"github.com/conorarmstrong/zx_go/pkg/next/nextregs"
	"github.com/conorarmstrong/zx_go/pkg/next/palette"
	rtcpkg "github.com/conorarmstrong/zx_go/pkg/next/rtc"
	"github.com/conorarmstrong/zx_go/pkg/next/sdcard"
	"github.com/conorarmstrong/zx_go/pkg/next/sprite"
	"github.com/conorarmstrong/zx_go/pkg/next/tilemap"
	uartpkg "github.com/conorarmstrong/zx_go/pkg/next/uart"
	"github.com/conorarmstrong/zx_go/pkg/peripherals"
	"github.com/conorarmstrong/zx_go/pkg/roms"
	"github.com/conorarmstrong/zx_go/pkg/ula"
	"github.com/conorarmstrong/zx_go/pkg/z80"
)

// nextFrameTStates is the 128K/+3-timing ULA frame length NextZXOS
// boots in (NR$03 default "011") -- (456*311)/2 per the FPGA frame
// geometry. Matches frameTStatesForModel in cmd/zx_go.
const nextFrameTStates = 70908

// dmaPortBus adapts the ULA's port dispatch to the zxnDMA's IOBus
// contract (ReadPort returns a bare byte).
type dmaPortBus struct{ u *ula.ULA }

func (b dmaPortBus) WritePort(port uint16, val byte) { b.u.WritePort(port, val) }
func (b dmaPortBus) ReadPort(port uint16) byte {
	v, _ := b.u.ReadPort(port)
	return v
}

// newNextMachine builds a cold-boot Spectrum Next wired for SD-image
// mode. sdImage is the raw card image (FAT32, e.g. tbblue.mmc); the
// FPGA bootrom (embedded GPLv3 fallback when not registered) boots
// TBBLUE.FW from it and hands off to NextZXOS exactly as native does.
func newNextMachine(sdImage []byte) (*machine, error) {
	kbd := keyboard.New()
	mem, err := memory.New("roms", roms.ModelNext)
	if err != nil {
		return nil, fmt.Errorf("next: memory.New: %w", err)
	}
	u := ula.New(mem, kbd)
	u.EnableAudioCapture() // no oto sink on wasm; pull frames via RenderAudioFrame
	cpu := z80.New(mem, u)

	// Spec-faithful narrow frame-INT pulse in +3/128K timing -- the
	// default Next behaviour (see configureClassicIntTiming native).
	assert, pulse := next.FrameIntTiming(0x03, false)
	cpu.IntAssertTstate = uint64(assert)
	cpu.IntPulseTstates = uint64(pulse)

	pm := peripherals.NewPeripheralManager(mem, "roms")
	u.SetPeripherals(pm)

	cpu.Variant = z80.VariantZ80N

	disp := nextregs.New()
	ayEngine := ay.NewEngine()
	if existing := u.AY(); existing != nil {
		ayEngine.SetChip(0, existing)
	}
	l2 := layer2.New(mem)
	pal := palette.NewBank()
	prio := next.NewLayerPriority()
	sprites := sprite.New()
	cop := copper.New()
	cop.SetRegWriter(disp)
	rtcEngine := rtcpkg.New()
	uartEngine := uartpkg.New()
	keymapEngine := keymap.New()
	tilemapLayer := tilemap.New(mem)
	dmaEngine := dma.New(mem)
	dacBank := dac.New()

	divROM, err := install.LoadROM(install.DivMMCROM)
	if err != nil && !errors.Is(err, install.ErrROMNotInstalled) {
		return nil, fmt.Errorf("next: divMMC ROM load: %w", err)
	}
	pager := divmmc.New(divROM)
	mem.SetDivMMCRAM(pager)
	pager.SetMultifaceActiveFn(mem.MultifaceActive)

	if mfROM, mferr := install.LoadROM(install.MultifaceROM); mferr == nil {
		mem.SetMultifaceROM(mfROM)
	} else if !errors.Is(mferr, install.ErrROMNotInstalled) {
		return nil, fmt.Errorf("next: Multiface ROM load: %w", mferr)
	}

	// FRAMES bumper -- emulates the load-bearing part of the
	// TBBLUE.FW-preinstalled IM-1 handler at divMMC bank-1 $2009.
	pager.SetFramesBumper(func() {
		lo := uint16(mem.Read(0x5C78)) | (uint16(mem.Read(0x5C79)) << 8)
		lo++
		mem.Write(0x5C78, byte(lo))
		mem.Write(0x5C79, byte(lo>>8))
		if lo == 0 {
			mem.Write(0x5C7A, mem.Read(0x5C7A)+1)
		}
	})
	pager.SetRom3Query(mem.DivMMCRom3Gate)

	// FPGA bootrom BEFORE next.Wire so WireReset seeds the NR$02
	// reset_type history from FPGABootROMActive() (the "100" seed).
	fpgaROM, err := install.LoadROM(install.FPGABootROM)
	if err != nil {
		return nil, fmt.Errorf("next: FPGA bootrom load: %w", err)
	}
	mem.SetFPGABootROM(fpgaROM)

	next.Wire(next.WireOpts{
		Dispatcher:  disp,
		Memory:      mem,
		CPU:         cpu,
		AYEngine:    ayEngine,
		Layer2:      l2,
		Palette:     pal,
		Priority:    prio,
		Sprites:     sprites,
		Copper:      cop,
		RTC:         rtcEngine,
		UART:        uartEngine,
		Keymap:      keymapEngine,
		Tilemap:     tilemapLayer,
		DivMMCPager: pager,
	})
	cpu.NextRegs = disp
	u.SetNextRegs(disp)

	u.SetNextAY(ayEngine)
	comp := compositor.New(pal, l2)
	comp.SetSprites(sprites)
	u.SetNextSpritePort(sprites)
	comp.SetTilemap(tilemapLayer)
	comp.SetPrioritySource(prio)
	u.SetNextCompositor(comp)
	comp.SetULAPalette(u.Palette())

	// Live NextReg hooks the umbrella Wire doesn't cover -- copied
	// from wireNextSubsystems (see its comments for the why of each).
	disp.SetOnWrite(0x4C, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x4C, val&0x0F)
		comp.SetTilemapTransparency(val & 0x0F)
	})
	disp.SetOnRead(0x1F, func(*nextregs.Dispatcher) byte { return byte(u.ActiveVideoLine() & 0xFF) })
	disp.SetOnRead(0x1E, func(*nextregs.Dispatcher) byte { return byte((u.ActiveVideoLine() >> 8) & 0x01) })
	disp.SetOnWrite(0x2F, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x2F, val&0x03)
		tilemapLayer.SetScrollX(int(val&0x03)<<8 | int(d.ReadReg(0x30)))
	})
	disp.SetOnWrite(0x30, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x30, val)
		tilemapLayer.SetScrollX(int(d.ReadReg(0x2F)&0x03)<<8 | int(val))
	})
	disp.SetOnWrite(0x31, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x31, val)
		tilemapLayer.SetScrollY(int(val))
	})
	disp.SetOnWrite(0x14, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x14, val)
		comp.SetTransparency(val)
	})
	expandRGB332 := func(val byte) (byte, byte, byte) {
		r3, g3, b2 := (val>>5)&7, (val>>2)&7, val&3
		return r3<<5 | r3<<2 | r3>>1, g3<<5 | g3<<2 | g3>>1, b2<<6 | b2<<4 | b2<<2 | b2
	}
	disp.SetOnWrite(0x4A, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x4A, val)
		comp.SetFallbackColour(expandRGB332(val))
	})
	comp.SetFallbackColour(expandRGB332(0xE3))
	disp.SetOnWrite(0x4B, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x4B, val)
		comp.SetSpriteTransparency(val)
	})
	disp.SetOnWrite(0x68, func(d *nextregs.Dispatcher, val byte) {
		d.Store(0x68, val)
		u.SetULAOutputDisabled(val&0x80 != 0)
	})

	u.SetNextDMA(dmaEngine)
	dmaEngine.SetIOBus(dmaPortBus{u})
	dmaEngine.SetCycleSink(func(n uint64) { cpu.SetTstates(cpu.Tstates() + n) })
	dmaEngine.SetClock(func() uint64 { return cpu.Tstates() })
	cpu.AddPreFetchHook("zxndma-step", func(uint16) { dmaEngine.Step(cpu.Tstates()) })

	u.SetNextI2C(rtcpkg.NewBus(rtcEngine))
	u.SetNextCopper(cop)
	u.SetNextDAC(dacBank)

	cpu.AddPreFetchHook("divmmc", pager.Step)
	cpu.SetRETNHook(func() {
		if mem.MultifaceActive() {
			mem.SetMultifaceActive(false)
		}
		pager.HandleRETN()
	})
	cpu.AddPostFetchHook("divmmc-pageout", pager.PostStep)
	u.SetNextDivMMC(pager)

	src, _ := sdcard.NewImageSource(sdImage, false)
	card := sdcard.NewCard(src)
	card.SetSDHC(true) // block-addressed, what the FPGA bootrom expects
	pager.SetCard(card)

	// divMMC overlay shadows the bottom 16 KB, then classic peripherals.
	mem.PeripheralRead = func(addr uint16) (byte, bool) {
		if val, ok := pager.HandleRead(addr); ok {
			return val, true
		}
		return pm.HandleMemoryRead(addr)
	}
	mem.PeripheralWrite = func(addr uint16, val byte) bool {
		if pager.HandleWrite(addr, val) {
			return true
		}
		return pm.HandleMemoryWrite(addr, val)
	}

	kbd.SetNMICallback(func() { cpu.PendingNMI.Store(true) })
	cpu.NMICallback = func() {
		if pm.IsMultifaceEnabled() {
			pm.HandleNMI()
		}
	}

	return &machine{
		cpu:     cpu,
		ula:     u,
		kbd:     kbd,
		frameT:  nextFrameTStates,
		disp:    disp,
		sdBytes: sdImage, // ImageSource aliases this slice; zxRunNex injects into it
	}, nil
}
