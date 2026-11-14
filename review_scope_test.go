package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pigo/internal/reducer"
)

// TestSessionReviewFilesOnlyListsThisSessionsWrites pins the review scope:
// 变更 shows the files this conversation wrote, in first-touch order, once
// each — not every dirty file the whole project shares. Every session in a
// project used to show the same working-tree diff, which is what this scope
// answers.
func TestSessionReviewFilesOnlyListsThisSessionsWrites(t *testing.T) {
	root := t.TempDir()
	snap := reducer.Snapshot{Tools: []reducer.ToolCall{
		{Name: "read", Args: map[string]any{"path": "notes.md"}},
		{Name: "edit", Args: map[string]any{"path": "pkg/a.go"}},
		{Name: "write", Args: map[string]any{"file_path": "b.md"}},
		{Name: "apply_patch", Args: map[string]any{"patch": "*** Begin Patch"}},
		{Name: "read_file", Args: map[string]any{"path": "c.txt"}},
		{Name: "edit", Args: map[string]any{"path": "pkg/a.go"}},
		{Name: "bash", Args: map[string]any{"command": "ls"}},
	}}
	got := sessionReviewFilesFrom(snap, root)
	want := []string{
		filepath.Join(root, "pkg", "a.go"),
		filepath.Join(root, "b.md"),
	}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("files[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
	// A tool that only read a file must never appear: the list is what the
	// conversation changed.
	for _, p := range got {
		if strings.HasSuffix(p, "notes.md") || strings.HasSuffix(p, "c.txt") {
			t.Fatalf("a read-only call leaked into the changes list: %q", p)
		}
	}
}

// TestReviewChangesStartScopedToTheConversation covers the default scope and
// the switch beside it: an empty session says so instead of pretending the
// working tree is its work, and 工作区 still answers for git status.
func TestReviewStartsOnTheWorkingTree(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	app := isolatedApp(t)
	app.Press("id:toggle-dock")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-review") })
	app.Press("id:dock-tab-review")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:review-list") })
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "不是 Git 仓库")
	})
	if app.Exists("id:review-scope-workspace") {
		t.Fatal("the simple review still carries the scope switch")
	}
	app.Press("id:review-advanced")
	app.WaitUntil(5*time.Second, func() bool {
		return app.Exists("id:review-scope-session") && app.Exists("id:review-scope-workspace")
	})
	app.Press("id:review-scope-session")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "本次会话没有改动文件")
	})
}

// TestHeaderRefreshDoesNotRestartTheAgent keeps the drop the user kept
// seeing out: 刷新 in the title bar used to call reconnect(), which killed
// the child and could leave 未连接 behind. It is a view refresh now.
func TestHeaderRefreshDoesNotRestartTheAgent(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:refresh-view") })
	if app.Exists("id:reconnect") {
		t.Fatal("the header's list refresh is still wired to the agent restart")
	}
	app.Press("id:refresh-view")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "已刷新")
	})
}

// TestSideChatRowOpensInTheRightDock is the placement the user asked for: a
// side chat is a tab of the right panel with its own input, and the main
// transcript is untouched. It used to replace the conversation on screen.
func TestSideChatRowOpensInTheRightDock(t *testing.T) {
	agentDir := t.TempDir()
	sessionsDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	body := strings.Join([]string{
		`{"type":"session","version":3,"id":"side-1","timestamp":"2026-11-13T00:00:00Z","cwd":"` + cwd + `"}`,
		`{"type":"custom","customType":"pi-web:side","data":{"version":1,"parentSessionId":"parent-1","createdAt":"2026-11-13T00:00:00Z"},"id":"m1","parentId":null,"timestamp":"2026-11-13T00:00:00Z"}`,
		`{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-11-13T00:00:00Z","message":{"role":"user","content":"父会话里的问题"}}`,
	}, "\n") + "\n"
	path := filepath.Join(sessionsDir, "side-1.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	app := isolatedAppAt(t, agentDir)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.Press("id:session-0")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:side-panel") })
	if !app.Exists("id:side-input") {
		t.Fatal("the side chat opened without a composer of its own")
	}
	if !app.Exists("id:composer-card") {
		t.Fatal("the main composer disappeared: the side chat took over the window")
	}
	if got := app.Find("id:side-title").Text; !strings.Contains(got, "侧聊") {
		t.Fatalf("side tab title = %q", got)
	}
	// The side chat's inherited history stays in its own panel; the main
	// transcript (a fresh session here) never shows the parent's prompt. The
	// sidebar row does show it as the session's first message, so the check
	// reads the transcript subtree, not every text on screen.
	if strings.Contains(textOf(app.Find("id:conversation")), "父会话里的问题") {
		t.Fatal("the side chat's transcript leaked into the main conversation")
	}
	// Closing the tab takes the panel with it and leaves the window as it was.
	app.Press("id:dock-tab-close-side-side-1.jsonl")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:side-panel") })
}
