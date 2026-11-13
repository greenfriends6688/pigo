//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/nsgotest"
)

// TestOpenRealSessionEndToEnd drives the whole path the user hits: the
// sidebar row, connect(), the RPC child, get_messages, the reducer and the
// transcript. It is opt-in (`-tags integration`) because it spawns node.
func TestOpenRealSessionEndToEnd(t *testing.T) {
	src := os.Getenv("PI_SESSION_FILE")
	if src == "" {
		t.Skip("set PI_SESSION_FILE to a real session .jsonl")
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// The live session grows past the transcript's 40-turn page. A bounded
	// copy keeps the first real prompt on screen while still driving the
	// real path: sidebar row, RPC child, get_messages, reducer and view.
	if lines := bytes.SplitAfter(body, []byte("\n")); len(lines) > 100 {
		body = bytes.Join(lines[:100], nil)
	}
	want := firstUserText(body)
	if want == "" {
		t.Fatal("the session has no user message to look for")
	}
	agentDir := t.TempDir()
	sessionsDir := filepath.Join(agentDir, "sessions", "copy")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, filepath.Base(src)), body, 0o600); err != nil {
		t.Fatal(err)
	}
	// Models keep the composer's model picker real; a machine without
	// models.json still passes the transcript checks.
	if data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi", "agent", "models.json")); err == nil {
		_ = os.WriteFile(filepath.Join(agentDir, "models.json"), data, 0o600)
	}
	// PROBE_COPY replays the machine's own settings/trust files, for the runs
	// that have to show whether a user config is what breaks a session.
	for _, name := range strings.Split(os.Getenv("PROBE_COPY"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi", "agent", name)); err == nil {
			_ = os.WriteFile(filepath.Join(agentDir, name), data, 0o600)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 800))
	app.WaitUntil(20*time.Second, func() bool { return app.Exists("id:session-0") })
	app.Press("id:session-0")

	app.WaitUntil(60*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), want)
	})
	joined := strings.Join(app.Texts(), "\n")
	t.Logf("status: %q", app.Find("id:status-capsule").Text)
	// A session that opens is not the same as one that stays open: the child
	// may die a few seconds later, which is exactly what "总是断连" looked
	// like. Twenty seconds of a live window must leave it connected.
	for i := 0; i < 10; i++ {
		app.Advance(2 * time.Second)
		if app.Exists("id:capsule-offline") || app.Exists("id:capsule-reconnecting") || app.Exists("id:error-card") {
			t.Fatalf("connection dropped after %ds: %s", (i+1)*2, app.Dump())
		}
	}
	t.Logf("texts: %d", len(app.Texts()))
	if !strings.Contains(joined, want) {
		t.Errorf("transcript is missing %q; got %.400s", want, joined)
	}
	if strings.Contains(joined, "启动失败") || strings.Contains(joined, "get_state 失败") {
		t.Errorf("the agent failed to start: %.400s", joined)
	}
	if !app.Exists("id:thinking-picker") {
		t.Error("the composer shows no thinking-level control after connecting")
	}
	// Typing @ opens the inline mention list, built from the project index.
	app.Type("id:composer", "@")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:mention-option-0") })
	app.Clear("id:composer")
	// `!command` runs pi's shell tool and shows its output above the composer.
	app.Type("id:composer", "!echo native-bash-test")
	app.Press("role:button 发送")
	app.WaitUntil(30*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "native-bash-test")
	})
}

// firstUserText is the opening of the first user message in a session file,
// enough to prove the transcript rendered it.
func firstUserText(body []byte) string {
	for _, line := range bytes.Split(body, []byte("\n")) {
		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "message" {
			continue
		}
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(entry.Message, &msg) != nil || msg.Role != "user" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(msg.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					return firstRunes(b.Text, 18)
				}
			}
		}
		var text string
		if json.Unmarshal(msg.Content, &text) == nil && strings.TrimSpace(text) != "" {
			return firstRunes(text, 18)
		}
	}
	return ""
}

func firstRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// TestSideChatOpensBesideTheParent drives 新建侧聊 for real: the fork is a
// session file of its own, its child connects with the read-mostly tools, and
// its inherited history lands in the right dock's tab while the main
// transcript stays exactly where it was. It is opt-in (`-tags integration`)
// because it spawns node.
func TestSideChatOpensBesideTheParent(t *testing.T) {
	src := os.Getenv("PI_SESSION_FILE")
	if src == "" {
		t.Skip("set PI_SESSION_FILE to a real session .jsonl")
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if lines := bytes.SplitAfter(body, []byte("\n")); len(lines) > 100 {
		body = bytes.Join(lines[:100], nil)
	}
	want := firstUserText(body)
	if want == "" {
		t.Fatal("the session has no user message to look for")
	}
	agentDir := t.TempDir()
	sessionsDir := filepath.Join(agentDir, "sessions", "copy")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(sessionsDir, filepath.Base(src))
	if err := os.WriteFile(parentFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi", "agent", "models.json")); err == nil {
		_ = os.WriteFile(filepath.Join(agentDir, "models.json"), data, 0o600)
	}
	// PROBE_COPY replays the machine's own settings/trust files, for the runs
	// that have to show whether a user config is what breaks a session.
	for _, name := range strings.Split(os.Getenv("PROBE_COPY"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".pi", "agent", name)); err == nil {
			_ = os.WriteFile(filepath.Join(agentDir, name), data, 0o600)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)

	app := nsgotest.Start(t, Root, nsgotest.Size(1200, 800))
	app.WaitUntil(20*time.Second, func() bool { return app.Exists("id:session-0") })
	app.Press("id:session-0")
	app.WaitUntil(60*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), want)
	})

	app.Press("id:start-side-chat")
	app.WaitUntil(20*time.Second, func() bool { return app.Exists("id:side-panel") })
	// The side child reads the copy's history, so its first turn is there once
	// the process connected — no prompt (and no model call) is sent.
	app.WaitUntil(60*time.Second, func() bool { return app.Exists("id:side-turn-0") })
	joined := strings.Join(app.Texts(), "\n")
	if !strings.Contains(textOf(app.Find("id:conversation")), want) {
		t.Error("the side chat took the main transcript away")
	}
	if strings.Contains(joined, "侧聊连接已断开") || strings.Contains(joined, "启动失败") {
		t.Errorf("the side child failed to connect: %.400s", joined)
	}

	// The parent file is not where the side chat writes: its bytes must not
	// change while the tab connects and loads.
	before, err := os.ReadFile(parentFile)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	after, err := os.ReadFile(parentFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the side chat wrote into the parent's session file")
	}

	// Closing the tab drops the side process and its panel.
	matches, err := filepath.Glob(filepath.Join(sessionsDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		if m != parentFile {
			app.Press("id:dock-tab-close-side-" + filepath.Base(m))
		}
	}
	app.WaitUntil(10*time.Second, func() bool { return !app.Exists("id:side-panel") })
}
