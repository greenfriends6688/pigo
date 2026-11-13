package nodeinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in                  string
		major, minor, patch int
		wantErr             bool
	}{
		{in: "v22.16.0", major: 22, minor: 16, patch: 0},
		{in: "22.16.0", major: 22, minor: 16, patch: 0},
		{in: "  v22.14.0\n", major: 22, minor: 14, patch: 0},
		{in: "V23.1.0", major: 23, minor: 1, patch: 0},
		{in: "v22.0.0-rc.1", major: 22, minor: 0, patch: 0},
		{in: "v22.16.0+build.7", major: 22, minor: 16, patch: 0},
		{in: "", wantErr: true},
		{in: "v", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "22.16", wantErr: true},
		{in: "22.16.0.1", wantErr: true},
		{in: "22..0", wantErr: true},
		{in: "22.x.0", wantErr: true},
		{in: "-22.0.0", wantErr: true},
		{in: "v22.16.0 ", major: 22, minor: 16, patch: 0},
	}
	for _, c := range cases {
		major, minor, patch, err := ParseVersion(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseVersion(%q) = %d.%d.%d, want error", c.in, major, minor, patch)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseVersion(%q) unexpected error: %v", c.in, err)
			continue
		}
		if major != c.major || minor != c.minor || patch != c.patch {
			t.Errorf("ParseVersion(%q) = %d.%d.%d, want %d.%d.%d", c.in, major, minor, patch, c.major, c.minor, c.patch)
		}
	}
}

const indexFixture = `[
  {
    "version": "v23.1.0",
    "date": "2024-10-16",
    "files": ["darwin-arm64-tar", "darwin-x64-tar", "linux-arm64", "linux-x64", "win-x64-zip"],
    "lts": false
  },
  {
    "version": "v22.14.0",
    "date": "2025-02-11",
    "files": ["darwin-arm64-tar", "darwin-x64-tar", "linux-arm64", "linux-x64", "win-x64-zip"],
    "lts": "Jod"
  },
  {
    "version": "v22.16.0",
    "date": "2025-05-06",
    "files": ["darwin-arm64-tar", "darwin-x64-tar", "linux-arm64", "linux-x64", "win-x64-zip"],
    "lts": "Jod"
  },
  {
    "version": "v20.19.0",
    "date": "2025-03-20",
    "files": ["darwin-arm64-tar", "darwin-x64-tar", "linux-arm64", "linux-x64", "win-x64-zip"],
    "lts": "Iron"
  }
]`

func TestLatestV22URL(t *testing.T) {
	cases := []struct {
		goos, goarch string
		wantURL      string
		wantFile     string
	}{
		{
			goos: "darwin", goarch: "arm64",
			wantURL:  "https://nodejs.org/dist/v22.16.0/node-v22.16.0-darwin-arm64.tar.gz",
			wantFile: "node-v22.16.0-darwin-arm64.tar.gz",
		},
		{
			goos: "darwin", goarch: "amd64",
			wantURL:  "https://nodejs.org/dist/v22.16.0/node-v22.16.0-darwin-x64.tar.gz",
			wantFile: "node-v22.16.0-darwin-x64.tar.gz",
		},
		{
			goos: "linux", goarch: "amd64",
			wantURL:  "https://nodejs.org/dist/v22.16.0/node-v22.16.0-linux-x64.tar.gz",
			wantFile: "node-v22.16.0-linux-x64.tar.gz",
		},
		{
			goos: "linux", goarch: "arm64",
			wantURL:  "https://nodejs.org/dist/v22.16.0/node-v22.16.0-linux-arm64.tar.gz",
			wantFile: "node-v22.16.0-linux-arm64.tar.gz",
		},
	}
	for _, c := range cases {
		gotURL, gotFile, gotVersion, err := LatestV22URL([]byte(indexFixture), c.goos, c.goarch)
		if err != nil {
			t.Fatalf("LatestV22URL(%s/%s) error: %v", c.goos, c.goarch, err)
		}
		if gotURL != c.wantURL {
			t.Errorf("LatestV22URL(%s/%s) url = %q, want %q", c.goos, c.goarch, gotURL, c.wantURL)
		}
		if gotFile != c.wantFile {
			t.Errorf("LatestV22URL(%s/%s) filename = %q, want %q", c.goos, c.goarch, gotFile, c.wantFile)
		}
		if gotVersion != "v22.16.0" {
			t.Errorf("LatestV22URL(%s/%s) version = %q, want v22.16.0", c.goos, c.goarch, gotVersion)
		}
	}
}

func TestLatestV22URLErrors(t *testing.T) {
	if _, _, _, err := LatestV22URL([]byte(indexFixture), "plan9", "amd64"); err == nil {
		t.Error("LatestV22URL(plan9/amd64) = nil error, want unsupported platform error")
	}
	if _, _, _, err := LatestV22URL([]byte("not json"), "linux", "amd64"); err == nil {
		t.Error("LatestV22URL(invalid json) = nil error, want parse error")
	}
	if _, _, _, err := LatestV22URL([]byte(`[]`), "linux", "amd64"); err == nil {
		t.Error("LatestV22URL(empty list) = nil error, want error")
	}
	oldOnly := `[{"version":"v20.19.0","files":["linux-x64"]},{"version":"v23.1.0","files":["linux-x64"]}]`
	if _, _, _, err := LatestV22URL([]byte(oldOnly), "linux", "amd64"); err == nil {
		t.Error("LatestV22URL(no v22) = nil error, want error")
	}
	// Numeric comparison, not string comparison: v22.9.0 must beat v22.10.0? No,
	// v22.10.0 > v22.9.0 numerically, so it must be selected.
	numeric := `[{"version":"v22.9.0","files":["linux-x64"]},{"version":"v22.10.0","files":["linux-x64"]}]`
	_, file, version, err := LatestV22URL([]byte(numeric), "linux", "amd64")
	if err != nil {
		t.Fatalf("LatestV22URL(numeric) error: %v", err)
	}
	if version != "v22.10.0" || file != "node-v22.10.0-linux-x64.tar.gz" {
		t.Errorf("LatestV22URL(numeric) = %q / %q, want v22.10.0", version, file)
	}
	// Windows only has zip archives; the URL is still reported so callers can
	// produce a precise "not supported" error.
	_, winFile, _, err := LatestV22URL([]byte(indexFixture), "windows", "amd64")
	if err != nil {
		t.Fatalf("LatestV22URL(windows/amd64) error: %v", err)
	}
	if !strings.HasSuffix(winFile, ".zip") {
		t.Errorf("LatestV22URL(windows/amd64) filename = %q, want .zip", winFile)
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("fake node tarball payload")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	const name = "node-v22.16.0-darwin-arm64.tar.gz"

	manifest := strings.Join([]string{
		strings.Repeat("0", 64) + "  node-v22.16.0-linux-x64.tar.gz",
		digest + "  " + name,
		"",
	}, "\n")

	if err := VerifySHA256(data, manifest, name); err != nil {
		t.Errorf("VerifySHA256(correct) error: %v", err)
	}
	if err := VerifySHA256(data, strings.ToUpper(digest)+"  "+name+"\n", name); err != nil {
		t.Errorf("VerifySHA256(uppercase digest) error: %v", err)
	}
	if err := VerifySHA256([]byte("tampered"), manifest, name); err == nil {
		t.Error("VerifySHA256(tampered) = nil error, want checksum mismatch")
	}
	if err := VerifySHA256(data, manifest, "node-v22.16.0-linux-arm64.tar.gz"); err == nil {
		t.Error("VerifySHA256(missing line) = nil error, want not found error")
	}
}

func TestSystemNodeAtLeast22(t *testing.T) {
	done := make(chan struct{})
	var path, version string
	var ok bool
	go func() {
		defer close(done)
		path, version, ok = SystemNodeAtLeast22()
	}()
	select {
	case <-done:
	case <-time.After(2 * versionTimeout):
		t.Fatal("SystemNodeAtLeast22 did not return in time")
	}
	if ok {
		if path == "" {
			t.Error("SystemNodeAtLeast22 returned ok with empty path")
		}
		major, _, _, err := ParseVersion(version)
		if err != nil {
			t.Fatalf("SystemNodeAtLeast22 version %q does not parse: %v", version, err)
		}
		if major < minMajor {
			t.Errorf("SystemNodeAtLeast22 returned ok for version %q", version)
		}
	}
}

func TestEnsureCancelledContext(t *testing.T) {
	// A cancelled context must fail fast (or succeed instantly via the system
	// node short-circuit) without touching the network or leaving temp dirs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()

	var events []Progress
	start := time.Now()
	path, err := Ensure(ctx, dir, func(p Progress) { events = append(events, p) })
	elapsed := time.Since(start)

	if err == nil {
		if path == "" {
			t.Fatal("Ensure returned empty path without error")
		}
		return
	}
	if elapsed > versionTimeout {
		t.Errorf("Ensure with cancelled context took %v", elapsed)
	}
	if len(events) == 0 {
		t.Fatal("Ensure reported no progress events")
	}
	if last := events[len(events)-1]; last.Stage != stageFailed || last.Err == nil {
		t.Errorf("last progress = %+v, want failed stage with error", last)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read install dir: %v", readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".nodeinstall-") {
			t.Errorf("temporary directory %q left behind", e.Name())
		}
	}
}
