package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForkSideCopiesTheBranchWithoutTouchingTheParent pins the reason side
// chats are copied here and not through pi's clone: pi's clone rebinds the
// parent's process to the clone, and the main conversation would then write
// its answers into the side file. The copy keeps the parent byte-identical,
// gets an id of its own, and carries the side marker and boundary note.
func TestForkSideCopiesTheBranchWithoutTouchingTheParent(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "2026-01-01T00-00-00-000Z_parent.jsonl")
	body := strings.Join([]string{
		`{"type":"session","version":3,"id":"parent-id","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/proj"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"问一句"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-01T00:00:02.000Z","message":{"role":"assistant","content":"答一句"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(parent, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	side, err := ForkSide(parent, "parent-id")
	if err != nil {
		t.Fatalf("ForkSide: %v", err)
	}
	if side == parent {
		t.Fatal("ForkSide returned the parent path")
	}
	if filepath.Dir(side) != dir {
		t.Fatalf("side file %s is not beside its parent", side)
	}

	info, err := Read(side)
	if err != nil {
		t.Fatalf("read side: %v", err)
	}
	if info.ID == "" || info.ID == "parent-id" {
		t.Fatalf("side id = %q, want a fresh id", info.ID)
	}
	if !info.Side {
		t.Fatal("the copy is not marked as a side chat")
	}
	if got := SideParent(side); got != "parent-id" {
		t.Fatalf("parentSessionId = %q", got)
	}
	if info.MessageCount != 2 {
		t.Fatalf("side inherited %d messages, want 2", info.MessageCount)
	}

	after, err := os.ReadFile(parent)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatal("ForkSide changed the parent session file")
	}
}

// TestForkSideRefusesAnEmptyFile keeps the UI honest: a session pi has not
// written yet cannot be copied into a side chat.
func TestForkSideRefusesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "empty.jsonl")
	if _, err := ForkSide(parent, ""); err == nil {
		t.Fatal("ForkSide accepted a missing file")
	}
	if err := os.WriteFile(parent, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ForkSide(parent, ""); err == nil {
		t.Fatal("ForkSide accepted a file with no header")
	}
}
