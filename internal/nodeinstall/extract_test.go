package nodeinstall

import (
	"archive/tar"
	"archive/zip"
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

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractZip(t *testing.T) {
	dst := t.TempDir()
	data := zipOf(t, map[string]string{
		"node-v22.0.0-win-x64/node.exe": "MZ",
		"node-v22.0.0-win-x64/LICENSE":  "mit",
	})
	if err := extractZip(dst, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	exe := filepath.Join(dst, "node-v22.0.0-win-x64", "node.exe")
	if st, err := os.Stat(exe); err != nil || st.Size() != 2 {
		t.Fatalf("node.exe missing or wrong: %v", err)
	}
}

func TestExtractZipRejectsEscape(t *testing.T) {
	for _, name := range []string{"../evil", "/abs/evil"} {
		data := zipOf(t, map[string]string{name: "x"})
		if err := extractZip(t.TempDir(), bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatalf("extractZip(%q) = nil error, want rejection", name)
		}
	}
}

func TestInstallArchiveZipLayout(t *testing.T) {
	data := zipOf(t, map[string]string{"node-v22.0.0-win-x64/node.exe": "MZ"})
	dir := t.TempDir()
	got, err := installArchive(dir, "node-v22.0.0-win-x64.zip", data)
	if err != nil {
		t.Fatalf("installArchive: %v", err)
	}
	want := filepath.Join(dir, "node-v22.0.0-win-x64", "node.exe")
	if got != want {
		t.Fatalf("installArchive = %q, want %q", got, want)
	}
}
