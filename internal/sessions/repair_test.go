package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSession(t *testing.T, dir, name, header string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRepairCwdRewritesHeaderAndKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	body := `{"type":"session","id":"abc","cwd":"/gone/project","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","id":"m1","message":{"role":"user","content":"你好"}}` + "\n" +
		`{"type":"session_info","name":"旧会话"}` + "\n"
	src := writeSession(t, dir, "2026-01-01_abc.jsonl", body)

	dest, err := RepairCwd(src, "/Users/me", filepath.Join(dir, "repaired"))
	if err != nil {
		t.Fatal(err)
	}
	if dest == src || !strings.HasSuffix(dest, "2026-01-01_abc.jsonl") {
		t.Fatalf("unexpected copy path %q", dest)
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("copied %d lines, want 3", len(lines))
	}
	if !strings.Contains(lines[0], `"cwd":"/Users/me"`) || !strings.Contains(lines[0], `"id":"abc"`) {
		t.Fatalf("header not repaired: %s", lines[0])
	}
	if !strings.Contains(lines[1], "你好") || !strings.Contains(lines[2], "旧会话") {
		t.Fatalf("history changed: %v", lines[1:])
	}
	info, err := Read(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Cwd != "/Users/me" || info.ID != "abc" {
		t.Fatalf("repaired info = %+v", info)
	}
}

func TestRepairCwdReusesTheCopy(t *testing.T) {
	dir := t.TempDir()
	src := writeSession(t, dir, "s.jsonl", `{"type":"session","id":"a","cwd":"/gone"}`+"\n")
	dest, err := RepairCwd(src, "/Users/me", filepath.Join(dir, "repaired"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(`{"type":"session","id":"a","cwd":"/later"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := RepairCwd(src, "/Users/me", filepath.Join(dir, "repaired"))
	if err != nil {
		t.Fatal(err)
	}
	if again != dest {
		t.Fatalf("second copy = %q, want %q", again, dest)
	}
	out, err := os.ReadFile(again)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "/later") {
		t.Fatal("the existing copy was overwritten")
	}
}
