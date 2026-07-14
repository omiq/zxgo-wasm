//go:build js

package main

import "errors"

// flushSQLite is unsupported in the js/wasm build: modernc.org/sqlite
// (via modernc.org/libc) has no js/wasm support. The in-memory trace
// ring still works; only on-disk flush is disabled.
func (t *traceDB) flushSQLite(path string) (int, error) {
	return 0, errors.New("trace-db SQLite flush unsupported on js/wasm")
}
