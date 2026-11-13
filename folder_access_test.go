package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The folders macOS guards, and the ones it does not: a hang inside the first
// group has to be explained, not just reported.
func TestInsideProtectedFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	protected := []string{
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Desktop", "重构pi web"),
		filepath.Join(home, "Documents", "notes"),
		filepath.Join(home, "Downloads"),
	}
	for _, dir := range protected {
		if !insideProtectedFolder(dir) {
			t.Fatalf("%q should count as protected", dir)
		}
	}
	free := []string{filepath.Join(home, "code"), filepath.Join(home, "Desktopish"), "/tmp", "/opt"}
	for _, dir := range free {
		if insideProtectedFolder(dir) {
			t.Fatalf("%q should not count as protected", dir)
		}
	}
}

func TestFolderAccessReadableDir(t *testing.T) {
	dir := t.TempDir()
	allowed, pending := folderAccess(dir, time.Second)
	if !allowed || pending {
		t.Fatalf("folderAccess(%q) = %v,%v; want readable", dir, allowed, pending)
	}
}

func TestFolderAccessMissingDir(t *testing.T) {
	// A missing directory must not be reported as pending consent: it is a
	// plain error, and the caller has a different message for it.
	allowed, pending := folderAccess(filepath.Join(t.TempDir(), "absent"), time.Second)
	if allowed || pending {
		t.Fatalf("folderAccess(absent) = %v,%v; want refused", allowed, pending)
	}
}
