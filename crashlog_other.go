//go:build !darwin

package main

import (
	"os"
	"path/filepath"
)

// crashLogPath keeps non-macOS logs next to the rest of the app's state.
func crashLogPath() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "pi-web-native.log")
	}
	return filepath.Join(cache, "PiWebNative.log")
}

// redirectStderr is a no-op outside macOS: there is no portable fd-2 dup here
// (linux/arm64 dropped Dup2, Windows never had it), so the log gets everything
// written through os.Stderr and the runtime's own fatal report goes to the
// console a GUI launch does not have.
func redirectStderr(f *os.File) {}
