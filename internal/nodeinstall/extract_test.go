package nodeinstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     e.mode,
			Size:     int64(len(e.body)),
			Typeflag: flag,
		}
		if flag == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if flag == tar.TypeReg && e.body != "" {
			if _, err := io.WriteString(tw, e.body); err != nil {
				t.Fatalf("write body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractTarGz(t *testing.T) {
	archive := buildTarGz(t, []tarEntry{
		{name: "node-v22.0.0-test/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "node-v22.0.0-test/bin/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "node-v22.0.0-test/bin/node", body: "#!/bin/sh\necho v22.0.0\n", mode: 0o755},
		{name: "node-v22.0.0-test/README.md", body: "readme\n", mode: 0o644},
	})

	dst := t.TempDir()
	if err := extractTarGz(dst, bytes.NewReader(archive)); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	bin := filepath.Join(dst, "node-v22.0.0-test", "bin", "node")
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read extracted node: %v", err)
	}
	if !bytes.Contains(got, []byte("v22.0.0")) {
		t.Errorf("extracted node content = %q", got)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat extracted node: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("extracted node mode = %v, want executable", fi.Mode())
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("extracted node perm = %o, want 755", fi.Mode().Perm())
	}

	readme := filepath.Join(dst, "node-v22.0.0-test", "README.md")
	if _, err := os.Stat(readme); err != nil {
		t.Errorf("README.md missing: %v", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	cases := []struct {
		name  string
		entry string
	}{
		{name: "parent traversal", entry: "../escape.txt"},
		{name: "nested traversal", entry: "node-v22.0.0-test/../../escape.txt"},
		{name: "absolute path", entry: "/tmp/escape.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			archive := buildTarGz(t, []tarEntry{
				{name: c.entry, body: "pwned\n", mode: 0o644},
			})
			base := t.TempDir()
			dst := filepath.Join(base, "dst")
			if err := extractTarGz(dst, bytes.NewReader(archive)); err == nil {
				t.Fatalf("extractTarGz(%q) = nil error, want rejection", c.entry)
			}
			if _, err := os.Stat(filepath.Join(base, "escape.txt")); err == nil {
				t.Fatalf("archive entry %q escaped the destination", c.entry)
			}
			if _, err := os.Stat("/tmp/escape.txt"); err == nil {
				t.Fatalf("archive entry %q wrote an absolute path", c.entry)
			}
		})
	}
}

func TestExtractTarGzRejectsBadGzip(t *testing.T) {
	if err := extractTarGz(t.TempDir(), bytes.NewReader([]byte("not gzip"))); err == nil {
		t.Fatal("extractTarGz(non gzip) = nil error, want error")
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "node-v22.0.0-test/bin/node"); err != nil {
		t.Errorf("safeJoin(valid) error: %v", err)
	}
	for _, bad := range []string{"", "..", "../x", "/etc/passwd", "a/../../b"} {
		if _, err := safeJoin(root, bad); err == nil {
			t.Errorf("safeJoin(%q) = nil error, want rejection", bad)
		}
	}
}
