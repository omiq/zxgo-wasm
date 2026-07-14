//go:build js

package main

import _ "embed"

// autoexecBAS is a tokenised NextBASIC program (generated with Remy Sharp's
// txt2bas: `#autostart 10` + `10 .nexload /imported/prog.nex`). Written to
// c:/nextzxos/autoexec.bas on the virtual SD so NextZXOS auto-loads the user's
// program at boot -- no keystroke macro, no timing race.
//
//go:embed data/autoexec.bas
var autoexecBAS []byte
