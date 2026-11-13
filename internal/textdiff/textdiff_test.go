package textdiff

import (
	"strings"
	"testing"
)

func TestUnifiedEdit(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk"
	new := "a\nb\nX\nd\ne\nf\ng\nh\ni\nj\nk\nNEW"
	patch := Unified("src/a.txt", old, new, 3)
	if !strings.Contains(patch, "--- a/src/a.txt") || !strings.Contains(patch, "+++ b/src/a.txt") {
		t.Fatalf("missing headers:\n%s", patch)
	}
	if !strings.Contains(patch, "-c") || !strings.Contains(patch, "+X") || !strings.Contains(patch, "+NEW") {
		t.Fatalf("missing changes:\n%s", patch)
	}
	add, del := Stats(patch)
	if add != 2 || del != 1 {
		t.Fatalf("stats add=%d del=%d, want 2/1\n%s", add, del, patch)
	}
	// Hunk header lines must be well-formed.
	if !strings.Contains(patch, "@@ -") {
		t.Fatalf("missing hunk header:\n%s", patch)
	}
}

func TestUnifiedSame(t *testing.T) {
	if p := Unified("a.txt", "x\ny", "x\ny", 3); p != "" {
		t.Fatalf("same text produced %q", p)
	}
}

func TestUnifiedWrite(t *testing.T) {
	patch := Unified("new.go", "", "package main\n", 3)
	if !strings.Contains(patch, "+package main") {
		t.Fatalf("write patch:\n%s", patch)
	}
}

func TestMultiHunk(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "line"+strings.Repeat("x", i%7))
	}
	old := strings.Join(lines, "\n")
	changed := append([]string(nil), lines...)
	changed[2] = "CHANGED-A"
	changed[50] = "CHANGED-B"
	patch := Unified("f.txt", old, strings.Join(changed, "\n"), 3)
	hunks := 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			hunks++
		}
	}
	if hunks != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", hunks, patch)
	}
}
