package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"
)

func TestModelSwitchWhileSessionOpen(t *testing.T) {
	agentDir := t.TempDir()
	project := t.TempDir()
	models := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://a","models":[{"id":"a-1","name":"Alpha"},{"id":"a-2","name":"Beta"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := sessionDirName(project)
	sessDir := filepath.Join(agentDir, "sessions", dir)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal("live probe")
	line := fmt.Sprintf(`{"type":"session","version":3,"id":"probe","timestamp":"2026-09-22T07:50:04.747Z","cwd":%q,"parentSession":""}`, project)
	msg := fmt.Sprintf(`{"type":"message","id":"m1","parentId":"h","timestamp":"2026-09-22T07:50:22.792Z","message":{"role":"user","content":%s}}`, content)
	if err := os.WriteFile(filepath.Join(sessDir, "2026-09-22T07-50-04-747Z_probe.jsonl"), []byte(line+"\n"+msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIGO_NO_AGENT", "")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 800))
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("text:live probe") })
	app.Press("text:live probe")
	app.WaitUntil(15*time.Second, func() bool { return app.Exists("id:model-picker") })
	app.Advance(3 * time.Second)
	app.Press("id:model-picker")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("text:Beta") })
	app.Press("text:Beta")
	app.Advance(5 * time.Second)
	if !app.Exists("id:composer") {
		t.Fatal("window closed after switching model")
	}
}

func sessionDirName(cwd string) string {
	b := []byte(cwd)
	out := make([]byte, 0, len(b)+4)
	out = append(out, '-', '-')
	for _, c := range b {
		if c == '/' || c == ':' || c == '\\' {
			out = append(out, '-')
			continue
		}
		out = append(out, c)
	}
	out = append(out, '-', '-')
	return string(out)
}
