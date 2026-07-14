//go:build js

// nexload macro for the wasm bridge -- a port of cmd/zx_go's
// nexload_macro.go with the Fyne/emulator plumbing removed. It drives
// the genuine NextZXOS `.nexload` dot command via injected keystrokes:
// wait for the menu wait-loop PC, SPACE through the welcome screen,
// open the Command Line, type `.nexload <sdPath>`, ENTER. Faithful to
// hardware -- the OS sets up the runtime environment .nex programs
// expect, unlike direct bank injection.
package main

import "strings"

// nextMenuLoopPC is the PC the NextZXOS ROM spins at while waiting for
// a key at the welcome screen and main menu.
const nextMenuLoopPC = 0x0c90

// nexKeyMatrix maps command-line characters onto Spectrum keyboard
// matrix (row, mask) presses. Symbols use SYMBOL SHIFT (row 7, 0x02).
var nexKeyMatrix = func() map[rune][][2]int {
	sym := [2]int{7, 0x02}
	letters := map[rune][2]int{
		'a': {1, 0x01}, 'b': {7, 0x10}, 'c': {0, 0x08}, 'd': {1, 0x04}, 'e': {2, 0x04},
		'f': {1, 0x08}, 'g': {1, 0x10}, 'h': {6, 0x10}, 'i': {5, 0x04}, 'j': {6, 0x08},
		'k': {6, 0x04}, 'l': {6, 0x02}, 'm': {7, 0x04}, 'n': {7, 0x08}, 'o': {5, 0x02},
		'p': {5, 0x01}, 'q': {2, 0x01}, 'r': {2, 0x08}, 's': {1, 0x02}, 't': {2, 0x10},
		'u': {5, 0x08}, 'v': {0, 0x10}, 'w': {2, 0x02}, 'x': {0, 0x04}, 'y': {5, 0x10},
		'z': {0, 0x02},
		'0': {4, 0x01}, '1': {3, 0x01}, '2': {3, 0x02}, '3': {3, 0x04}, '4': {3, 0x08},
		'5': {3, 0x10}, '6': {4, 0x10}, '7': {4, 0x08}, '8': {4, 0x04}, '9': {4, 0x02},
	}
	m := map[rune][][2]int{
		' ':  {{7, 0x01}},
		'.':  {sym, {7, 0x04}},
		'/':  {sym, {0, 0x10}},
		'-':  {sym, {6, 0x08}},
		'\'': {sym, {4, 0x08}},
	}
	for r, k := range letters {
		m[r] = [][2]int{k}
	}
	return m
}()

type macroStep struct {
	keys     [][2]int
	frames   int
	waitMenu bool
}

type nexloadMacro struct {
	steps []macroStep
	idx   int
	frame int
	keyOn bool
}

// newNexloadMacro builds the keystroke macro loading sdPath (absolute
// SD path, e.g. "/imported/PROGRAM.NEX"). Timings mirror the sequence
// verified natively.
func newNexloadMacro(sdPath string) *nexloadMacro {
	var steps []macroStep
	hold := func(keys [][2]int, frames int) { steps = append(steps, macroStep{keys: keys, frames: frames}) }
	wait := func(frames int) { steps = append(steps, macroStep{frames: frames}) }

	steps = append(steps, macroStep{waitMenu: true}) // boot to the welcome screen
	hold([][2]int{{7, 0x01}}, 40)                    // SPACE -> "Start NextZXOS"
	wait(140)                                        // settle on the main menu
	hold([][2]int{{0, 0x01}, {4, 0x10}}, 6)          // cursor DOWN -> Command Line
	wait(10)
	hold([][2]int{{6, 0x01}}, 6) // ENTER -> command prompt
	wait(92)
	for _, c := range ".nexload " + strings.ToLower(sdPath) {
		if keys, ok := nexKeyMatrix[c]; ok {
			hold(keys, 4)
			wait(10)
		}
	}
	wait(15)
	hold([][2]int{{6, 0x01}}, 6) // ENTER -> run NEXLOAD
	wait(1500)                   // let the OS load and start the program

	return &nexloadMacro{steps: steps}
}

// tick advances the macro one frame; call after the frame executes so
// pressed keys are seen by the next frame's scan. True when finished.
func (nm *nexloadMacro) tick(m *machine) bool {
	if nm.idx >= len(nm.steps) {
		nm.releaseAll(m)
		return true
	}
	s := &nm.steps[nm.idx]
	if nm.frame == 0 {
		nm.releaseAll(m)
		for _, k := range s.keys {
			m.kbd.PressMatrixKey(k[0], byte(k[1]), true)
		}
		nm.keyOn = len(s.keys) > 0
	}
	nm.frame++
	if s.waitMenu {
		if m.cpu.PC == nextMenuLoopPC || nm.frame > 900 {
			nm.idx++
			nm.frame = 0
		}
		return false
	}
	if nm.frame >= s.frames {
		nm.idx++
		nm.frame = 0
	}
	return false
}

func (nm *nexloadMacro) releaseAll(m *machine) {
	if !nm.keyOn {
		return
	}
	for row := 0; row < 8; row++ {
		m.kbd.PressMatrixKey(row, 0xFF, false)
	}
	nm.keyOn = false
}
