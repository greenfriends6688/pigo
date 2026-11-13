package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGrepFindsLinesAndSkipsNoise(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "src", "main.go"), []byte("package main\n// Needle here\nfunc main() {}\n"))
	writeTestFile(t, filepath.Join(root, "notes.md"), []byte("nothing\nneedle again\n"))
	// Binary, .git and oversized files must not be searched.
	writeTestFile(t, filepath.Join(root, "blob.bin"), []byte("needle\x00binary"))
	writeTestFile(t, filepath.Join(root, ".git", "config"), []byte("needle"))
	big := strings.Repeat("x", GrepMaxFileSize+1) + "\nneedle\n"
	writeTestFile(t, filepath.Join(root, "big.txt"), []byte(big))

	matches, err := Grep(root, "needle", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches, got %+v", matches)
	}
	byPath := map[string]Match{}
	for _, m := range matches {
		byPath[m.Path] = m
	}
	if m, ok := byPath["src/main.go"]; !ok || m.Line != 2 || !strings.Contains(m.Text, "Needle") {
		t.Fatalf("case-insensitive line match lost: %+v", matches)
	}
	if m, ok := byPath["notes.md"]; !ok || m.Line != 2 {
		t.Fatalf("line number wrong: %+v", matches)
	}
}

func TestGrepHonorsLimitAndEmptyQuery(t *testing.T) {
	root := t.TempDir()
	var body strings.Builder
	for i := 0; i < 9; i++ {
		body.WriteString("hit\n")
	}
	writeTestFile(t, filepath.Join(root, "a.txt"), []byte(body.String()))

	matches, err := Grep(root, "hit", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Fatalf("limit not honored: %d", len(matches))
	}
	if matches[2].Line != 3 {
		t.Fatalf("line order wrong: %+v", matches)
	}
	empty, err := Grep(root, "   ", 10)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty query = %v, %v", empty, err)
	}
}

func TestGrepShortensLongLines(t *testing.T) {
	root := t.TempDir()
	long := "needle" + strings.Repeat("z", 1000)
	writeTestFile(t, filepath.Join(root, "min.js"), []byte(long))
	matches, err := Grep(root, "needle", 5)
	if err != nil || len(matches) != 1 {
		t.Fatalf("grep = %v, %v", matches, err)
	}
	if n := len([]rune(matches[0].Text)); n > 320 {
		t.Fatalf("long line not shortened: %d runes", n)
	}
}
