//go:build !js

// Command mksd builds a bootable FAT32 SD-card image from a NextZXOS
// distro directory tree -- the same sdcard.BuildFAT32 path the native
// GUI uses for folder-mode SD. The wasm bridge can't walk a host
// directory (no filesystem under GOOS=js), so serve.sh runs this once
// natively and serves the resulting image to the browser.
//
// Usage: mksd [-root roms/next/sd] [-o sd.img] [-size 256]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/conorarmstrong/zx_go/pkg/next/sdcard"
)

func main() {
	root := flag.String("root", "roms/next/sd", "distro directory to pack")
	out := flag.String("o", "sd.img", "output image path")
	size := flag.Int("size", 256, "image size in MB")
	flag.Parse()

	// Wrap the standard boot filter to also drop autoexec.1st -- the NextZXOS
	// welcome program. It runs at boot and WAITS for a keypress, which blocks
	// the autoexec.bas we inject to auto-.nexload the user's program. Without
	// the welcome, our autoexec.bas runs straight away.
	bootFilter := sdcard.NextBootFilter()
	skip := func(hostPath string, isDir bool) bool {
		if !isDir && strings.EqualFold(filepath.Base(hostPath), "autoexec.1st") {
			return true
		}
		return bootFilter(hostPath, isDir)
	}

	img, err := sdcard.BuildFAT32(*root, sdcard.FAT32Opts{
		SizeMB:      *size,
		VolumeLabel: "ZXNEXT",
		SkipFile:    skip,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "mksd: BuildFAT32(%s): %v\n", *root, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, img, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "mksd: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d MB) from %s\n", *out, len(img)>>20, *root)
}
