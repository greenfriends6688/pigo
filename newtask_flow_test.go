//go:build integration

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"

	"pigo/internal/agent"
)

// TestNewTaskStaysUntilTheFirstPrompt keeps the hero page from flashing to
// an empty conversation when the blank agent connects: the first prompt is
// what leaves New task.
func TestNewTaskStaysUntilTheFirstPrompt(t *testing.T) {
	if agent.DiscoverPi() == "" {
		t.Skip("pi is not installed")
	}
	agentDir := t.TempDir()
	project := t.TempDir()
	t.Setenv("PIGO_NO_AGENT", "")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	_ = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{}}`), 0o600)
	t.Chdir(project)

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 800))
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:new-task-project") })
	// The blank child answers get_state within a few seconds; the hero page
	// must still be there afterwards.
	app.Advance(6 * time.Second)
	if !app.Exists("id:new-task-project") {
		t.Fatal("New task flashed to an empty conversation after the blank agent connected")
	}
	if got := app.Find("id:window-title").Text; !strings.Contains(got, "新会话") {
		t.Fatalf("title = %q, want New task", got)
	}
}
