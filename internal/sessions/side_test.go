package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkSideWritesMetadataAndBoundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := `{"type":"session","version":3,"id":"abc","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}` + "\n" +
		`{"type":"model_change","id":"m1","parentId":null,"provider":"p","modelId":"m","timestamp":"2026-01-01T00:00:01Z"}` + "\n" +
		`{"type":"message","id":"u1","parentId":"m1","message":{"role":"user","content":"hi"},"timestamp":"2026-01-01T00:00:02Z"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MarkSide(path, "parent-id"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	for _, want := range []string{`"pi-web:side"`, `"parentSessionId":"parent-id"`, `"pi-web:side-boundary"`, SideBoundaryText} {
		if !strings.Contains(text, want) {
			t.Fatalf("side chat lost %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, `"parentId":null`) {
		t.Fatalf("metadata entry should start the branch:\n%s", text)
	}
	if IsSide(path) == false {
		t.Fatal("IsSide did not see the side entry")
	}
	if SideParent(path) != "parent-id" {
		t.Fatalf("parent = %q", SideParent(path))
	}
}

func TestChildrenOfFiltersByParent(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, parent string) string {
		path := filepath.Join(sessDir, name)
		body := `{"type":"session","id":"` + name + `","cwd":"/tmp"`
		if parent != "" {
			body += `,"parentSession":"` + parent + `"`
		}
		body += "}\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	parent := write("p.jsonl", "")
	child := write("c1.jsonl", parent)
	_ = write("c2.jsonl", filepath.Join(sessDir, "other.jsonl"))
	st := NewStore(dir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	got := st.ChildrenOf(parent)
	if len(got) != 1 || got[0].Path != child {
		t.Fatalf("ChildrenOf = %+v", got)
	}
	if len(st.ChildrenOf("")) != 0 {
		t.Fatal("an empty parent must match nothing")
	}
}
