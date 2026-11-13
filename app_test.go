package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/nsgotest"

	"pigo/internal/resources"
)

func TestMain(m *testing.M) { nsgotest.Main(m) }

// isolatedApp starts the real window with the agent subprocess and the pi
// CLI disabled, and points the agent directory at a throwaway tree so the
// test never reads the user's ~/.pi/agent.
func isolatedApp(t *testing.T) *nsgotest.App {
	t.Helper()
	return isolatedAppAt(t, t.TempDir())
}

// isolatedAppAt is isolatedApp with the agent directory the caller owns, so a
// test can put sessions in it before the window scans.
func isolatedAppAt(t *testing.T, agentDir string) *nsgotest.App {
	t.Helper()
	t.Setenv("PIGO_NO_AGENT", "1")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	return nsgotest.Start(t, Root, nsgotest.Size(1200, 800))
}

// openFiles opens the dock's Files tab; the panel starts closed so the
// transcript keeps the window.
func openFiles(t *testing.T, app *nsgotest.App) {
	t.Helper()
	if !app.Exists("id:right-panel") {
		app.Press("id:toggle-dock")
	}
	app.WaitUntil(10*time.Second, func() bool {
		return app.Exists("id:files-tree") || app.Exists("id:files-no-project")
	})
}

// scrollChild is the first scroll view inside v, for tests that need its
// offset or content size.
func scrollChild(v *nsgotest.View) *nsgotest.View {
	for _, c := range v.Children {
		if c.Content.Height > 0 || c.Content.Width > 0 {
			return c
		}
		if found := scrollChild(c); found != nil {
			return found
		}
	}
	return nil
}

// textOf joins a view's own text and its descendants'.
func textOf(v *nsgotest.View) string {
	if v == nil {
		return ""
	}
	out := v.Text
	for _, c := range v.Children {
		out += textOf(c)
	}
	return out
}

func TestShellRendersNativeLayout(t *testing.T) {
	app := isolatedApp(t)
	app.Advance(3 * time.Second) // virtual clock: drives the session watcher
	app.WaitUntil(5*time.Second, func() bool {
		return app.Exists("id:shell") && app.Exists("id:conversation") && app.Exists("id:composer")
	})
	if got := app.Find("id:window-title").Text; !strings.Contains(got, "会话") {
		t.Fatalf("unexpected window title: %q", got)
	}
	// PLAN §9.6: list containers must grow and shrink or Scroll cannot scroll.
	list := app.Find("id:session-list")
	if list.Frame.Size.Height <= 0 || list.Frame.Size.Height > 800 {
		t.Fatalf("session list has an invalid viewport height: %+v", list.Frame)
	}
	// The status strip and the session count left the sidebar: the capsule in
	// the title bar is the one place live state shows.
	if !app.Exists("id:status-capsule") {
		t.Fatal("the title bar has no status capsule")
	}
	if app.Exists("id:status") || app.Exists("id:session-count") {
		t.Fatal("the sidebar still carries its old status strip")
	}
}

// TestRightPanelStartsClosed keeps the transcript full width until a panel
// is asked for.
func TestRightPanelStartsClosed(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	if app.Exists("id:right-panel") {
		t.Fatal("the files panel opened by itself")
	}
	app.Press("id:toggle-dock")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
}

// TestComposerFooterStaysQuiet hides the stats row and the queue button
// while there is nothing to report.
func TestComposerFooterStaysQuiet(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	if app.Exists("id:clear-queue") {
		t.Fatal("the empty composer still offered to clear a queue")
	}
	if app.Exists("id:session-stats") {
		t.Fatal("an empty stats row still showed under the composer")
	}
}

// TestContextMeterShowsAValue makes the context window readable without a
// hover.
func TestContextMeterShowsAValue(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:context-meter") })
	if got := textOf(app.FindMounted("id:context-meter")); strings.TrimSpace(got) == "" {
		t.Fatal("the context meter is an unlabeled icon")
	}
}

// TestMentionEnterInsertsTheFirstMatch makes @ usable from the keyboard:
// Enter takes the match instead of sending the half-typed mention.
func TestMentionEnterInsertsTheFirstMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("note"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	app.Type("id:composer", "@note")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:mention-option-0") })
	app.Submit("id:composer")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(app.Find("id:composer").Text, "@note.txt")
	})
}

// TestSessionRowFitsTheSidebar catches the overflowing titles that made the
// list look like loose text over the panel.
func TestSessionRowFitsTheSidebar(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	title := strings.Repeat("很长的会话标题", 12)
	body := `{"type":"session","id":"long","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","role":"user","message":{"role":"user","content":"` + title + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "2026-01-01_long.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	list := app.FindMounted("id:session-list")
	row := app.FindMounted("id:session-0")
	if row.Frame.Size.Width > list.Frame.Size.Width {
		t.Fatalf("session row is %v wide inside a %v list", row.Frame.Size.Width, list.Frame.Size.Width)
	}
}

// TestAtMentionPalette lists the project's files after @.
func TestAtMentionPalette(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("note"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	app.WaitUntil(5*time.Second, func() bool { return len(app.FindMounted("id:composer").Text) >= 0 })
	app.Type("id:composer", "@")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:mention-option-0") })
}

// TestDockTabsAndSettingsDialog covers the two things the user asked for:
// files and the terminal live in the right panel's tabs, and settings is a
// dialog rather than a page.
func TestDockTabsAndSettingsDialog(t *testing.T) {
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-files") })

	app.Press("id:dock-tab-terminal")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:term-tabs") })

	app.Press("id:dock-tab-files")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })

	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-dialog") })
	if app.Exists("id:conversation") == false {
		t.Fatal("the chat must stay mounted behind the settings dialog")
	}
}

// TestFileTreePreviewsOnOnePress covers the dock's file tree: one press on a
// file must show its text beside the tree, without a second click.
func TestFileTreePreviewsOnOnePress(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello-from-test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	app := isolatedApp(t)
	openFiles(t, app)
	app.Press("role:button hello.txt")
	app.WaitUntil(10*time.Second, func() bool {
		// base/editor draws one Text per row, so the preview's content is in
		// the window's texts rather than in the container's own Text.
		return app.Exists("id:files-preview") &&
			strings.Contains(strings.Join(app.Texts(), "\n"), "hello-from-test")
	})
	// The file has a tab of its own; the tree is still one press away, and a
	// folder there expands in place.
	app.Press("id:dock-tab-files")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
	app.Press("role:button sub")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
}

func TestCommandPalette(t *testing.T) {
	app := isolatedApp(t)
	app.Press("id:open-palette")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:palette-command") })
}

// TestGitTabShowsHistory opens the dock's Git tab in a real repository and
// checks that the commit graph shows the subject git log reported.
func TestGitTabShowsHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "native git test")
	t.Chdir(dir)

	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:dock-tab-review") })
	app.Press("id:dock-tab-review")
	app.Press("id:review-tab-history")
	app.WaitUntil(15*time.Second, func() bool {
		return app.Exists("id:git-graph") &&
			strings.Contains(strings.Join(app.Texts(), "\n"), "native git test")
	})
}

// TestProjectPickerOpens covers the filepicker dialog: the sidebar's project
// button opens it on the current directory with a usable Choose button.
func TestProjectPickerOpens(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:nav-open-project") })
	app.Press("id:nav-open-project")
	app.WaitUntil(10*time.Second, func() bool {
		return app.Exists("id:project-picker") && app.Exists("id:filepicker-choose")
	})
	// The folder is the current project, so choosing it closes the dialog
	// without switching anything.
	app.Press("id:filepicker-choose")
	app.Advance(time.Second)
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:project-picker") })
}

func TestMentionPaletteAcceptsWithEnter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	app.Type("id:composer", "看下 @alpha")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:mention-option-0") })
	app.Key("enter")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(app.Find("id:composer").Text, "@alpha.txt")
	})
}

func TestStatusCapsuleShowsDisconnected(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:capsule-offline") })
	// The capsule carries the action inline instead of explaining the
	// failure in the 11pt status strip.
	if !app.Exists("id:capsule-reconnect") {
		t.Fatal("the offline capsule must offer the reconnect action")
	}
}

// TestComposerRefusesWithoutConnection keeps the old optimistic-message
// behaviour out: a prompt no live process received must not appear in the
// transcript.
func TestComposerRefusesWithoutConnection(t *testing.T) {
	app := isolatedApp(t)
	app.Type("id:composer", "你好，原生版")
	app.Press("role:button 发送")
	// No live pi process means the prompt never leaves the window: a
	// ghost bubble the agent never received is worse than a visible hint.
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "未连接")
	})
	if strings.Contains(strings.Join(app.Texts(), "\n"), "你好，原生版") {
		t.Fatal("a disconnected composer must not insert an optimistic bubble")
	}
}

// TestFileTreeStaysInTheProject covers the tree's scope and the hidden-file
// switch: the root is the project, not the home directory, and dotfiles only
// appear when asked for.
func TestFileTreeStaysInTheProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("normal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".secret"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	app := isolatedApp(t)
	openFiles(t, app)
	if got := app.Find("id:files-project").Text; got != filepath.Base(dir) {
		t.Fatalf("files-project = %q, want %q", got, filepath.Base(dir))
	}
	if app.Exists("id:files-up") {
		t.Fatal("the tree must not offer to navigate above the project")
	}
	joined := strings.Join(app.Texts(), "\n")
	if strings.Contains(joined, ".secret") {
		t.Fatal("a dotfile shows without the hidden switch")
	}
	if strings.Contains(strings.Join(app.Texts(), "\n"), "列目录失败") {
		t.Fatalf("the tree listed a file as a directory: %v", app.Texts())
	}
	app.Press("id:files-hidden")
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), ".secret")
	})
}

// TestNoProjectAsksForOne checks that launching from home shows a project
// prompt instead of the whole home directory.
func TestNoProjectAsksForOne(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "private.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Chdir(home)

	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:files-no-project") })
	if app.Exists("id:files-tree") {
		t.Fatal("the tree walked the home directory")
	}
	if strings.Contains(strings.Join(app.Texts(), "\n"), "private.txt") {
		t.Fatal("a home file leaked into the dock")
	}
}

// TestFileTabsAndDiffSwitch opens two files in tabs and flips a changed file
// between its patch and its source.
func TestFileTabsAndDiffSwitch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "hello-from-test\n")
	write("b.txt", "second-file\n")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "a.txt", "b.txt")
	run("commit", "-q", "-m", "base")
	write("a.txt", "hello-from-test\nchanged-line\n")
	t.Chdir(dir)

	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(app.Find("id:git-status").Text, "改动")
	})
	app.Press("role:button a.txt")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:file-mode") })
	app.Press("role:tab 源码")
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "changed-line")
	})
	app.Press("id:dock-tab-files")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
	app.Press("role:button b.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-file-b.txt") })
	app.Press("label:关闭 a.txt")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:dock-tab-file-a.txt") })
}

func TestSessionDeleteRemovesTheFile(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessDir, "2026-01-01_abc.jsonl")
	body := `{"type":"session","id":"abc","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","role":"user","message":{"role":"user","content":"待删除的会话"}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.RightClick("id:session-0")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-delete") })
	app.Press("id:session-delete")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:delete-dialog") })
	app.Press("id:delete-submit")
	app.WaitUntil(10*time.Second, func() bool {
		_, err := os.Stat(sessionFile)
		return os.IsNotExist(err)
	})
}

// TestOpenProjectRetargetsTheTree picks another folder in the project picker
// and checks the tree, title and composer follow it.
func TestOpenProjectRetargetsTheTree(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "home-marker.txt"), []byte("from-home"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:files-open-project") })
	app.Press("id:files-open-project")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:project-picker") })
	app.Press("text:主目录")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:filepicker-choose") })
	app.Press("id:filepicker-choose")
	app.WaitUntil(10*time.Second, func() bool {
		return app.Find("id:files-project").Text == filepath.Base(home)
	})
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "home-marker.txt")
	})
}

// TestArchiveToggleAndRestore archives the only session, finds it in the
// archived view and restores it.
func TestArchiveToggleAndRestore(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","id":"abc","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","role":"user","message":{"role":"user","content":"归档测试"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "2026-01-01_abc.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.RightClick("id:session-0")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-archive") })
	app.Press("id:session-archive")
	app.WaitUntil(10*time.Second, func() bool { return !app.Exists("id:session-0") })

	app.Press("id:session-archived-toggle")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.RightClick("id:session-0")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-unarchive") })
	app.Press("id:session-unarchive")
	app.WaitUntil(10*time.Second, func() bool { return !app.Exists("id:session-0") })
	app.Press("id:session-archived-toggle")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
}

// TestFileMenuMentions puts a file's path into the composer from the tree's
// context menu.
// TestFileTreeKeepsHeightWhenFileSelected guards the layout bug where
// choosing a file shrank the tree to a percentage and left it clipped.
func TestFileTreeKeepsHeightWhenFileSelected(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("file-%02d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
	before := app.Find("id:files-scroll").Frame.Size.Height
	app.Press("role:button file-00.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-preview-pane") })
	if before < 100 {
		t.Fatalf("the tree started clipped: %.0f", before)
	}
	app.Press("id:dock-tab-files")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-tree") })
	if after := app.Find("id:files-scroll").Frame.Size.Height; after < before*0.9 {
		t.Fatalf("the tree shrank after a file tab: before %.0f after %.0f", before, after)
	}
	scroll := scrollChild(app.Find("id:files-tree"))
	if scroll == nil {
		t.Fatal("the tree has no scroll view")
	}
	if scroll.Content.Height <= scroll.Frame.Size.Height {
		t.Fatalf("the tree cannot scroll: content %.0f, view %.0f", scroll.Content.Height, scroll.Frame.Size.Height)
	}
}

func TestFileMenuMentions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("note"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.Press("role:button note.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:file-mention") })
	app.Press("id:file-mention")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(app.Find("id:composer").Text, "@note.txt")
	})
}

func waitFileContains(t *testing.T, path, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("%s does not contain %q: %s", path, want, data)
}

// TestSettingsModelsEditsProvider edits the provider in models.json and keeps
// the fields the form does not show.
func TestSettingsModelsEditsProvider(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	body := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://old","api":"openai-completions","headers":{"X-Test":"1"},"models":[{"id":"a-1","name":"A 1"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-edit-acme") })
	app.Press("id:provider-edit-acme")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-dialog") })
	app.Clear("id:provider-base")
	app.Type("id:provider-base", "https://new")
	app.Press("id:provider-submit")
	waitFileContains(t, modelsPath, "https://new", 5*time.Second)
	data, _ := os.ReadFile(modelsPath)
	if !strings.Contains(string(data), "X-Test") {
		t.Fatalf("unknown provider fields were dropped: %s", data)
	}
}

// TestModelsAddWritesModelsJSON adds a model through the dialog and checks
// the fields pi-web's form has land in models.json.
func TestModelsAddWritesModelsJSON(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	body := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://old","api":"openai-completions","models":[{"id":"a-1","name":"A 1","compat":{"x":1}}]}}}`
	if err := os.WriteFile(modelsPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-row-acme") })
	app.Press("id:provider-row-acme")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:model-add") })
	app.Press("id:model-add")
	app.Type("id:model-id", "a-9")
	app.Type("id:model-name", "A 9")
	app.Type("id:model-context", "128000")
	app.Type("id:model-cost-input", "3")
	app.Press("id:model-save")
	waitFileContains(t, modelsPath, `"a-9"`, 5*time.Second)
	data, _ := os.ReadFile(modelsPath)
	for _, want := range []string{`"contextWindow": 128000`, `"cost"`, `"compat"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("models.json lost %s: %s", want, data)
		}
	}
}

// TestReviewStagesAndCommits covers the Review tab's changes list, staging
// and commit box against a real repository.
func TestReviewStagesAndCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	if err := os.WriteFile(filepath.Join(repo, "note.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("GIT_AUTHOR_NAME", "测试")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "测试")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-review") })
	app.Press("id:dock-tab-review")
	// The changes list starts scoped to the conversation; staging and
	// committing are the working tree's, so the test opens that scope.
	app.Press("id:review-scope-workspace")
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "未暂存 · 1")
	})
	app.Press("role:button note.txt")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:review-diff") })
	app.Press("id:review-stage")
	app.WaitUntil(10*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "已暂存 · 1")
	})
	app.Type("id:review-message", "review commit")
	app.Press("id:review-commit")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(run("log", "--oneline"), "review commit") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the review commit never landed")
}

func seedMCP(t *testing.T, agentDir string) string {
	t.Helper()
	path := filepath.Join(agentDir, "mcp.json")
	body := `{"mcpServers":{"files":{"command":"npx","args":["-y","server"]}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSettingsMCPToggleAndAdd(t *testing.T) {
	agentDir := t.TempDir()
	path := seedMCP(t, agentDir)
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-mcp") })
	app.Press("id:settings-nav-mcp")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:mcp-enabled-files") })
	app.Press("id:mcp-enabled-files")
	waitFileContains(t, path, `"enabled": false`, 5*time.Second)
	app.Press("id:mcp-enabled-files")
	waitFileContains(t, path, `"files"`, 5*time.Second)

	app.Press("id:mcp-add")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:mcp-dialog") })
	app.Type("id:mcp-name", "search")
	app.Type("id:mcp-command", "npx")
	app.Type("id:mcp-args", "-y search-server")
	app.Press("id:mcp-submit")
	waitFileContains(t, path, `"search"`, 5*time.Second)
}

func TestSettingsSkillsToggleAndPreview(t *testing.T) {
	agentDir := t.TempDir()
	skillDir := filepath.Join(agentDir, "skills", "alpha")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	body := "---\nname: alpha\ndescription: Alpha skill\n---\n\n# Alpha\n"
	if err := os.WriteFile(skillPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-skills") })
	app.Press("id:settings-nav-skills")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:skill-enabled-alpha") })
	app.Press("id:skill-enabled-alpha")
	waitFileContains(t, skillPath, "disable-model-invocation: true", 5*time.Second)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:skill-open-alpha") })
	app.Press("id:skill-open-alpha")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:skill-dialog") })
}

func TestSettingsAgentEdit(t *testing.T) {
	agentDir := t.TempDir()
	agentsDir := filepath.Join(agentDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agentsDir, "reviewer.md")
	if err := os.WriteFile(path, []byte("---\nname: reviewer\ndescription: Reviews\n---\nYou review.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-agents") })
	app.Press("id:settings-nav-agents")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:agent-edit-reviewer") })
	app.Press("id:agent-edit-reviewer")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:agent-dialog") })
	app.Focus("id:agent-body")
	app.Type("id:agent-body", "\nEXTRA-INSTRUCTION")
	app.Press("id:agent-submit")
	waitFileContains(t, path, "EXTRA-INSTRUCTION", 5*time.Second)
}

func TestTrustGateForProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi", "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PIGO_TRUST_GATE", "1")
	agentDir := t.TempDir()
	app := isolatedAppAt(t, agentDir)
	app.Press("id:new-session")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:trust-dialog") })
	app.Press("id:trust-submit")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:trust-dialog") })
	trustPath := filepath.Join(agentDir, "trust.json")
	waitFileContains(t, trustPath, dir, 5*time.Second)
	if !requiresTrust(dir) {
		t.Fatal("requiresTrust missed the project config")
	}
}

func TestWorktreeDialogCreates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	t.Chdir(repo)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-review") })
	app.Press("id:dock-tab-review")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:git-worktrees") })
	app.Press("id:git-worktrees")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:worktree-dialog") })
	path := filepath.Join(t.TempDir(), "wt")
	app.Type("id:worktree-branch", "feat-x")
	app.Clear("id:worktree-path")
	app.Type("id:worktree-path", path)
	app.Press("id:worktree-create")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		app.Advance(100 * time.Millisecond)
		if !app.Exists("id:worktree-dialog") {
			break
		}
	}
	if app.Exists("id:worktree-dialog") {
		t.Fatal("the worktree dialog stayed open")
	}
	app.Press("id:dock-tab-files")
	app.WaitUntil(5*time.Second, func() bool {
		return app.Exists("id:files-project") && app.Find("id:files-project").Text == "wt"
	})
}

func TestDocumentPreviewShowsSystemOpen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.Press("role:button doc.pdf")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-doc") && app.Exists("id:file-open-external") })
}

func TestMarkdownMathAndMermaid(t *testing.T) {
	md := "```mermaid\ngraph TD; A-->B\n```\n\n$$a^2+b^2$$\n"
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node { return markdownView(md, false)(cx) })
	// NSGo draws both: a fenced block as a code block (its language named in
	// the header) and a formula as its TeX in the math part. The MyGo picture
	// adapters that used to render them are gone, so the text is what shows.
	app.WaitUntil(5*time.Second, func() bool {
		joined := strings.Join(app.Texts(), "\n")
		return strings.Contains(joined, "graph TD") && strings.Contains(joined, "a^2+b^2")
	})
}

// TestSessionGroupsCollapse folds a project's rows under its header.
func TestSessionGroupsCollapse(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"one", "two"} {
		body := `{"type":"session","id":"s` + name + `","cwd":"` + cwd + `","timestamp":"2026-01-0` + fmt.Sprint(i+1) + `T00:00:00Z"}` + "\n" +
			`{"type":"message","role":"user","message":{"role":"user","content":"` + name + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(sessDir, "2026-01-0"+fmt.Sprint(i+1)+"_s"+name+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") && app.Exists("id:session-1") })
	app.Press("id:session-group-0")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:session-0") && !app.Exists("id:session-1") })
	app.Press("id:session-group-0")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-0") })
}

// TestInlineSlashPalette opens the command list while the draft starts with a
// slash.
func TestInlineSlashPalette(t *testing.T) {
	app := isolatedApp(t)
	app.Type("id:composer", "/")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:slash-option-0") })
}

// TestCSVPreviewUsesDataTable shows CSV files as a table.
func TestCSVPreviewUsesDataTable(t *testing.T) {
	dir := t.TempDir()
	body := "name,count\nalpha,1\nbeta,2\n"
	if err := os.WriteFile(filepath.Join(dir, "rows.csv"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.Press("role:button rows.csv")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:files-table") })
}

// TestFileTreeScrolls checks the dock's tree is a real scroll viewport with
// bounded content, not a list that clips its rows.
func TestFileTreeScrolls(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 60; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file-%02d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.Scroll("id:files-scroll", 800)
	app.WaitUntil(5*time.Second, func() bool {
		s := scrollChild(app.FindMounted("id:files-tree"))
		return s != nil && s.Offset.Y > 0
	})
	s := scrollChild(app.FindMounted("id:files-tree"))
	if s == nil || s.Content.Height < 1000 {
		t.Fatalf("content height = %v, want the whole tree", s)
	}
}

// TestSessionListStaysAboveTheFooter keeps a long session list inside its
// viewport, so it never paints over the count, status or settings.
func TestSessionListStaysAboveTheFooter(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		body := `{"type":"session","id":"s` + fmt.Sprint(i) + `","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
			`{"type":"message","role":"user","message":{"role":"user","content":"会话 ` + fmt.Sprint(i) + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(sessDir, fmt.Sprintf("2026-01-01_s%02d.jsonl", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	list := app.FindMounted("id:session-list")
	if list.Frame.Size.Height > 700 {
		t.Fatalf("the session list grew to %v and would overlap the footer", list.Frame.Size.Height)
	}
	if !app.Exists("id:nav-settings") {
		t.Fatal("the settings row was pushed out")
	}
}

// TestSettingsPagesRender walks every settings category in code, so a page
// that panics while rendering fails here instead of closing the window.
func TestSettingsPagesRender(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"acme":{"name":"Acme","baseUrl":"https://x","models":[{"id":"a-1","name":"A"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(`{"mcpServers":{"files":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings") })
	for _, id := range []string{"general", "models", "agents", "skills", "extensions", "mcp"} {
		app.Press("id:settings-nav-" + id)
		app.Settle()
		if !app.Exists("id:settings") {
			t.Fatalf("settings page %q closed the dialog", id)
		}
	}
	// The provider catalog renders and closes.
	app.Press("id:settings-nav-models")
	app.Press("id:provider-add")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-catalog") })
	app.Press("id:catalog-close")
	app.Advance(time.Second)
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:provider-catalog") })
}

// TestModelPresetAddsProvider adds a provider from pi's catalog.
func TestModelPresetAddsProvider(t *testing.T) {
	agentDir := t.TempDir()
	store := `{"acme":{"models":[{"id":"a-1","name":"A 1","baseUrl":"https://a/v1","api":"openai-completions"},{"id":"a-2","name":"A 2"}]}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models-store.json"), []byte(store), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-add") })
	app.Press("id:provider-add")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-catalog") })
	app.Press("id:catalog-acme")
	waitFileContains(t, filepath.Join(agentDir, "models.json"), `"acme"`, 5*time.Second)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-edit-acme") })
}

// TestDefaultModelSelectWritesSettings chooses a default model from the
// configured provider and clears it again.
func TestDefaultModelSelectWritesSettings(t *testing.T) {
	agentDir := t.TempDir()
	modelsJSON := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://a","models":[{"id":"a-1","name":"A One"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:default-model") })
	app.Press("id:default-model")
	app.WaitUntil(5*time.Second, func() bool {
		return app.Exists("role:option A One · acme")
	})
	app.Press("role:option A One · acme")
	settingsPath := filepath.Join(agentDir, "settings.json")
	waitFileContains(t, settingsPath, `"defaultModel": "a-1"`, 5*time.Second)
	waitFileContains(t, settingsPath, `"defaultProvider": "acme"`, 5*time.Second)
}

// TestModelPickerOpensFloatingPanel checks the model chip opens its popover
// over the composer, using the configured providers when no live session
// exists yet.
func TestModelPickerOpensFloatingPanel(t *testing.T) {
	agentDir := t.TempDir()
	modelsJSON := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://a","models":[{"id":"a-1","name":"A One"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:model-picker") })
	app.Press("id:model-picker")
	app.WaitUntil(5*time.Second, func() bool {
		// No live session, so no thinking level; the model rows are the test's
		// subject: configured providers stand in for pi's catalog.
		return app.Exists("text:A One")
	})
}
func TestTerminalStartsShell(t *testing.T) {
	app := isolatedApp(t)
	app.Press("id:toggle-dock")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-terminal") })
	app.Press("id:dock-tab-terminal")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:terminal-t1") })
	app.Advance(2 * time.Second)
	for _, text := range app.Texts() {
		if strings.Contains(text, "Process exited") {
			t.Fatalf("the shell exited at once: %s", text)
		}
	}
}

// TestEnabledModelsSwitchWritesSettings toggles one model and checks
// settings.json's enabledModels, the switch pi-web has and the native
// Models page lacked.
func TestEnabledModelsSwitchWritesSettings(t *testing.T) {
	agentDir := t.TempDir()
	modelsPath := filepath.Join(agentDir, "models.json")
	body := `{"providers":{"acme":{"name":"Acme","baseUrl":"https://x","api":"openai-completions","models":[{"id":"a-1","name":"A 1"},{"id":"a-2","name":"A 2"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:enabled-model-acme-a-1") })
	app.Press("id:enabled-model-acme-a-1")
	settingsPath := filepath.Join(agentDir, "settings.json")
	waitFileContains(t, settingsPath, "enabledModels", 5*time.Second)
	data, _ := os.ReadFile(settingsPath)
	if !strings.Contains(string(data), "acme/a-2") {
		t.Fatalf("the other model should stay enabled: %s", data)
	}
}

// TestFilePreviewShowsText guards the preview pane: opening a text file
// must show its content, not an empty editor.
func TestFilePreviewShowsText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello preview\nline two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("role:button hello.txt") })
	app.Press("role:button hello.txt")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "hello preview")
	})
}

// TestFileFullscreenPreview opens the preview over the window and closes it.
func TestFileFullscreenPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wide.txt"), []byte("fullscreen preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("role:button wide.txt") })
	app.Press("role:button wide.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:file-fullscreen-open") })
	app.Press("id:file-fullscreen-open")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:file-fullscreen-close") })
	app.Press("id:file-fullscreen-close")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:file-fullscreen") })
}

// TestFilePreviewScrolls guards the editor's own scroll: with MinRows set
// the editor grew with its text and the pane clipped it, so a long file
// could not be scrolled at all.
func TestFilePreviewScrolls(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "line %03d of the file\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("role:button long.txt") })
	app.Press("role:button long.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-preview") })
	app.WaitUntil(5*time.Second, func() bool {
		scroll := app.Find("id:editor-scroll")
		return scroll.Content.Height > scroll.Frame.Size.Height
	})
}

// TestMarkdownFileOpensPreview checks the pi-web-style preview mode and the
// wrap toggle.
func TestMarkdownFileOpensPreview(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Title\n\n- one\n- two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("role:button readme.md") })
	app.Press("role:button readme.md")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:file-mode") && app.Exists("id:file-wrap") })
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "Title")
	})
}

// TestWindowDropAddsMention drags a file over the window: the drop reaches
// the shell, and a non-image becomes an @-mention in the composer.
func TestWindowDropAddsMention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dropped.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:composer") })
	app.DropFiles("id:window", (&url.URL{Scheme: "file", Path: path}).String())
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(app.Find("id:composer").Text, "@dropped.txt")
	})
}

// TestProviderCatalogListsPi covers the provider picker: it lists pi's own
// catalog (subscription providers included), not just what models.json has.
func TestProviderCatalogListsPi(t *testing.T) {
	app := isolatedAppAt(t, t.TempDir())
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-models") })
	app.Press("id:settings-nav-models")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-add") })
	app.Press("id:provider-add")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-catalog") })
	for _, id := range []string{"anthropic", "openai", "deepseek", "google", "openrouter"} {
		if !app.Exists("id:catalog-" + id) {
			t.Fatalf("the catalog does not list %q", id)
		}
	}
	if !app.Exists("text:订阅服务") || !app.Exists("text:API KEY") {
		t.Fatal("the catalog is not grouped into subscriptions and API keys")
	}
	// An OAuth provider is one press away from models.json.
	app.Press("id:catalog-anthropic")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:provider-row-anthropic") })
}

// TestAgentsPageListsBuiltins keeps pi's three own subagents on the Agents
// page, with the switch that turns one off.
func TestAgentsPageListsBuiltins(t *testing.T) {
	app := isolatedAppAt(t, t.TempDir())
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-agents") })
	app.Press("id:settings-nav-agents")
	app.Advance(2 * time.Second)
	for _, name := range []string{"general-purpose", "explore", "plan"} {
		app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:agent-enabled-" + name) })
	}
	// One of pi's profiles opens read-only: its prompt and tool list, no save.
	app.Advance(500 * time.Millisecond)
	app.Press("id:agent-view-explore")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:agent-builtin-note") })
	if app.Exists("id:agent-submit") {
		t.Fatal("the built-in profile offered a save it cannot make")
	}
	app.Advance(time.Second) // the dialog's entrance settles before its footer takes a press
	app.Press("id:agent-close")
	app.Advance(2 * time.Second)
	if app.Exists("id:agent-dialog") {
		t.Fatal("the read-only profile did not close")
	}
}

// TestSidebarMenusCarryProjectAndSessionActions checks the two menus against
// PI NEXT's: the project header arranges and sweeps a project, the row forks
// and marks a session.
func TestSidebarMenusCarryProjectAndSessionActions(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	sessDir := filepath.Join(agentDir, "sessions", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","id":"abc","cwd":"` + cwd + `","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","role":"user","message":{"role":"user","content":"菜单测试"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessDir, "2026-01-01_abc.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Advance(3 * time.Second)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:session-0") })
	app.RightClick("id:session-0")
	for _, id := range []string{"session-unread", "session-fork", "session-pin", "session-rename", "session-archive"} {
		if !app.Exists("id:" + id) {
			t.Fatalf("the session menu is missing %q", id)
		}
	}
	// 标为未读 leaves the row in place with its own dot.
	app.Press("id:session-unread")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-unread-0") })
	app.RightClick("id:session-group-0")
	for _, id := range []string{"session-group-pin", "session-group-rename", "session-group-up", "session-group-down",
		"session-group-archive-old", "session-group-archived", "session-group-delete-all"} {
		if !app.Exists("id:" + id) {
			t.Fatalf("the project menu is missing %q", id)
		}
	}
}

// TestReviewOpenFileShowsTheFile guards the review pane's one jump: pressing
// "在文件面板打开" must show the file, not the tree that no longer holds it.
func TestReviewOpenFileShowsTheFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	run("add", "a.txt")
	run("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	app.Press("id:toggle-dock")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-review") })
	app.Press("id:dock-tab-review")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:review-list") })
	// 变更 starts on this conversation's own files; the working tree is one
	// press away, and that is what a git-status test is about.
	app.Press("id:review-scope-workspace")
	app.Press("role:button a.txt")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:review-diff-pane") })
	app.Press("id:review-open-file")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:files-preview-pane") })
	// The pane opens on the file's tab, not the tree that used to hold it.
	if app.Exists("id:files-tree") {
		t.Fatal("the file opened behind the tree instead of in its own tab")
	}
	// The mode switch appears once the patch has loaded; the file's own text
	// is behind 源码.
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("role:tab 源码") })
	app.Press("role:tab 源码")
	app.WaitUntil(10*time.Second, func() bool {
		return app.Exists("id:files-preview") && strings.Contains(strings.Join(app.Texts(), "\n"), "two")
	})
}

// TestSkillDialogEdits keeps the skills page's viewer editable: the dialog
// used to be read-only, and its editor could outgrow the dialog and draw over
// the chat behind it.
func TestSkillDialogEdits(t *testing.T) {
	agentDir := t.TempDir()
	dir := filepath.Join(agentDir, "skills", "demo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	long := "---\nname: demo\ndescription: 演示\n---\n\n" + strings.Repeat("line of the file\n", 200)
	if err := os.WriteFile(path, []byte(long), 0o600); err != nil {
		t.Fatal(err)
	}
	app := isolatedAppAt(t, agentDir)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-nav-skills") })
	app.Press("id:settings-nav-skills")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:skill-open-demo") })
	app.Press("id:skill-open-demo")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:skill-body") })
	if !app.Exists("id:skill-submit") {
		t.Fatal("the skill dialog has no save: it is read-only")
	}
	// The path row names the file being edited.
	if got := app.Find("id:skill-path").Text; !strings.HasSuffix(got, "SKILL.md") {
		t.Fatalf("skill path = %q", got)
	}
	// The editor stays inside the dialog and scrolls, instead of drawing its
	// whole height over the chat behind it. It reads the long body on open.
	if h := app.Find("id:skill-body").Frame.Size.Height; h > 470 {
		t.Fatalf("the skill editor grew to %.0f, past its 430-point box", h)
	}
	if err := resources.SaveSkill(path, "---\nname: demo\ndescription: 改过\n---\n\n新正文\n"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "新正文") {
		t.Fatal("SaveSkill did not write the file")
	}
}
