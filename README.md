# zxgo-wasm

A WebAssembly build for Conor Armstrong's
[zx_go](https://github.com/conorarmstrong/zx_go) ZX Spectrum Next
emulator, so the emulator runs in a web browser.

This is what drives the `zxnext` platform in my retro online IDE at
[ide.retrogamecoders.com](https://ide.retrogamecoders.com).

The repo contains minimal changes needed to compile the core to
WASM/JS. Everything else comes from the original repo unchanged.

## Build

You need Go (with wasm support, i.e. any recent version) and git.

```bash
./apply.sh            # clones, applies changes, and builds
```

That produces `zx_go-wasm-build/cmd/zxwasm/web/zx.wasm`. To try it in a browser:

```bash
cd zx_go-wasm-build
cmd/zxwasm/web/serve.sh          # builds, stages assets, serves on :8791
# http://localhost:8791/index.html          48K Spectrum (works out of the box)
# http://localhost:8791/index.html?next=1   Spectrum Next (needs ROMs, see below)
```

The 48K machine boots with no extra files. The Spectrum Next path needs the
licensed NextZXOS ROMs and an SD image, which are not distributed here (see
below).

## ROMs and licensing

No ROMs are shipped in this repository. The classic 48K ROMs live in upstream
and are `//go:embed`'d into the binary at build time; that redistribution is
upstream's, under upstream's terms. The licensed NextZXOS ROMs are never bundled
at all. The host page fetches them at runtime and hands them to the emulator via
`zxRegisterROM`, so you supply your own copy from an official Spectrum Next
distribution.

The overlay code is MIT, the same as upstream, from which it is derived. See
`LICENSE`.

