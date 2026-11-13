package main

import (
	"os"
	"path/filepath"
)

// keepCrashLog points the process's stderr at the log file, so the runtime's
// own report of a fatal error — "fatal error: concurrent map writes", an
// unrecovered panic, a throw from a goroutine — is kept for the next launch to
// read instead of vanishing. A build launched from Finder has no terminal, so
// without this the window just went away with no trace anywhere.
//
// crashLogPath and redirectStderr are per platform: the log directory differs,
// and only POSIX lets us repoint fd 2, which is where the runtime writes.
func keepCrashLog() {
	path := crashLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	redirectStderr(f)
	// What log and the app's own writers use.
	os.Stderr = f
}
