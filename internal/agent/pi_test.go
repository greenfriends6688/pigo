package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePiVersion(t *testing.T) {
	cases := map[string]PiVersion{
		"1.4.0\n":         {1, 4, 0},
		"pi v1.4.7":       {1, 4, 7},
		"pi 1.12.3 (abc)": {1, 12, 3},
		"2.0.0":           {2, 0, 0},
	}
	for text, want := range cases {
		got, err := ParsePiVersion(text)
		if err != nil {
			t.Fatalf("ParsePiVersion(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("ParsePiVersion(%q) = %v, want %v", text, got, want)
		}
	}
	if _, err := ParsePiVersion("no version here"); err == nil {
		t.Fatal("ParsePiVersion accepted text without a version")
	}
}

func TestPiVersionSupported(t *testing.T) {
	supported := []PiVersion{{1, 4, 0}, {1, 4, 9}, {1, 9, 2}}
	unsupported := []PiVersion{{1, 3, 9}, {0, 9, 0}, {2, 0, 0}}
	for _, v := range supported {
		if !v.Supported() {
			t.Fatalf("%v should be supported", v)
		}
	}
	for _, v := range unsupported {
		if v.Supported() {
			t.Fatalf("%v should not be supported", v)
		}
	}
}

func TestDiscoverPiPrefersPiExecutable(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "my-pi")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 1.4.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_EXECUTABLE", fake)
	if got := DiscoverPi(); got != fake {
		t.Fatalf("DiscoverPi = %q, want %q", got, fake)
	}
}

func TestPathWithDirPutsItFirst(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	env := PathWithDir("/opt/pi/bin/pi")
	var path string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			path = strings.TrimPrefix(kv, "PATH=")
		}
	}
	if !strings.HasPrefix(path, "/opt/pi/bin"+string(os.PathListSeparator)) {
		t.Fatalf("PATH = %q, want the pi directory first", path)
	}
	// Other variables survive untouched.
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			found = true
		}
	}
	if !found {
		t.Fatal("PathWithDir dropped the rest of the environment")
	}
}
