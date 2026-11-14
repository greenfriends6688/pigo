package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpeningASessionShowsIt(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "--"+strings.ReplaceAll(strings.TrimPrefix(cwd, "/"), "/", "-")+"--")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","id":"s1","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","role":"user","message":{"role":"user","content":"打开这个会话"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "2026-01-01_s1.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.Press("id:session-0")
	app.Advance(200 * time.Millisecond)
	if got := app.Find("id:window-title").Text; !strings.Contains(got, "打开这个会话") {
		t.Fatalf("title = %q; tree:\n%s", got, app.Dump())
	}
}
