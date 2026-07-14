# zxwasm -- in-browser emulator core (WebAssembly bridge)

Path-B port of zx_go for the online IDE: compile **only the emulator core**
(`pkg/z80`, `pkg/memory`, `pkg/ula`, `pkg/keyboard`) to WebAssembly behind a tiny
`syscall/js` API. The host page owns the `<canvas>`, animation loop, keyboard, and
(later) Web Audio -- no Fyne app, no oto. Lean wasm (~6.8 MB vs the full app's
46 MB).

Boots the **48K Spectrum** (embedded ROMs, no filesystem needed) **and the full
ZX Spectrum Next** -- the genuine FPGA loader -> TBBLUE firmware -> NextZXOS chain
runs end-to-end in Chrome at 50 Hz realtime, with **Web Audio** (beeper + AY + DAC)
and an autoexec loader that runs `.nex` and tokenised NextBASIC (`.bas`) programs.
This is what drives the `zxnext` platform in the retro online IDE at
ide.retrogamecoders.com.

Licensed Next ROMs and the NextZXOS distro are **fetched by the host page** at
runtime and registered via `zxRegisterROM` -- never embedded or committed. The
classic 48K ROMs are the only ones `//go:embed`'d.

## JS API (installed on the global object)

| Call | Purpose |
|------|---------|
| `zxFrame(pixBuf Uint8Array) -> {w,h}` | advance one 50 Hz frame, blit the RGBA framebuffer into `pixBuf` (Next output is 320x240, classic 352x288 -- host reallocs on `{w,h}` change) |
| `zxAudio(sampleBuf Uint8Array) -> nSamples` | drain the frame's accumulated int16-LE audio samples (beeper + AY + DAC mix) |
| `zxKeyName(name, down, shift)` | normal key path -- a Fyne key name (`"A"`, `"1"`, `"Return"`, `"Space"`) through the host-key->matrix map |
| `zxKey(row, mask, down)` | press/release a raw ZX matrix bit (special combos) |
| `zxType(charCode)` | type a SYMBOL-SHIFT rune (punctuation like `"`) -- classic ROM only; the NextZXOS editor misses rune pulses, use held `zxKeyName` presses |
| `zxRegisterROM(name, bytes)` | register a licensed ROM in the in-memory registry (host-fetched, sole source once used) |
| `zxBootNext(sdBytes Uint8Array)` | cold-boot the Next from a FAT32 SD image |
| `zxRunNex(nexBytes)` | inject a `.nex` into the live card + hard-reset -> autoexec runs it |
| `zxRunBas(basBytes)` | inject tokenised NextBASIC (PLUS3DOS) as `autoexec.bas` + reset -> runs it |
| `zxDebug() -> pc` | current Z80 program counter (diagnostics) |
| `zxReady` | boolean the host polls before calling the API |

## Run the demo

```bash
cmd/zxwasm/web/serve.sh          # builds zx.wasm, packs SD from roms/next/sd, serves :8791
# open http://localhost:8791/index.html         (48K)
# open http://localhost:8791/index.html?next=1  (Next -- wait through TBBLUE splash -> welcome -> menu)
```

`web/index.html` is a reference harness: canvas + a **50 Hz wall-clock-paced**
loop (decoupled from rAF so the Next clocks at its native 50 Hz, not the display
refresh), host-key->matrix mapping, and an `AudioWorklet` fed from `zxAudio`
(started on first user gesture per the autoplay policy). `zx.wasm`,
`wasm_exec.js`, and `web/assets/` are build artifacts (git-ignored).
