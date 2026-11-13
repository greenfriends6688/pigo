//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func crashLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pi-web-native.log")
	}
	return filepath.Join(home, "Library", "Logs", "PiWebNative.log")
}

// redirectStderr repoints fd 2, which is where the Go runtime writes a fatal
// error, at the log file.
func redirectStderr(f *os.File) {
	_ = syscall.Dup2(int(f.Fd()), 2)
}
