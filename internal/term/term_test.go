package term

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDefaultShellIsAbsolute(t *testing.T) {
	s := DefaultShell()
	if s == "" || s[0] != '/' {
		t.Fatalf("expected an absolute shell path, got %q", s)
	}
}

func TestManagerClosesEmptySet(t *testing.T) {
	m := NewManager()
	if len(m.Tabs()) != 0 {
		t.Fatal("a new manager must own no tabs")
	}
	if err := m.CloseAll(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close("missing"); err != nil {
		t.Fatal(err)
	}
}

func TestViewIsSizedToFillContainer(t *testing.T) {
	// A View can be constructed without starting a shell; check the layout
	// contract from PLAN §9.6 (grow + shrink, or Scroll will not scroll).
	v := (&Tab{ID: "x"}).View("Terminal")
	if v.Style.FlexGrow != 1 || v.Style.FlexShrink != 1 {
		t.Fatalf("terminal view must grow and shrink: %+v", v.Style)
	}
	if v.TestID != "terminal-x" {
		t.Fatalf("unexpected TestID %q", v.TestID)
	}
	if !v.AutoFocus {
		t.Fatal("a terminal view takes the keyboard on mount")
	}
	if v.Ref == nil {
		t.Fatal("the view needs its Ref so the app can focus it again")
	}
}

// TestViewThemesAreStable guards the emulator's palette: it knows a theme by
// pointer and rebuilds the colors whenever it changes, so a repaint must not
// hand it fresh ones (which also dropped what a program set with OSC 4/10/11).
func TestViewThemesAreStable(t *testing.T) {
	a := (&Tab{ID: "x"}).View("Terminal")
	b := (&Tab{ID: "x"}).View("Terminal")
	if a.Theme != b.Theme || a.DarkTheme != b.DarkTheme {
		t.Fatal("views must share the package's themes")
	}
	if a.DarkTheme.Background == 0 || a.DarkTheme == a.Theme {
		t.Fatal("the dark and light themes must differ")
	}
}

func TestStatusString(t *testing.T) {
	cases := []Status{
		{State: StateRunning},
		{State: StateExited, Code: 129},
		{State: StateFailed, Err: "boom"},
	}
	want := []string{"running", "exited(129)", "failed(boom)"}
	for i, s := range cases {
		if got := s.String(); got != want[i] {
			t.Fatalf("Status%+v.String() = %q, want %q", s, got, want[i])
		}
	}
}

// fakeShell is a shell that runs nothing: Done, ExitCode and Text are what a
// test sets, so the startup and fallback logic is checked without a pty.
type fakeShell struct {
	mu   sync.Mutex
	done chan struct{}
	code int
	text string
	sent bool
}

func newFakeShell(text string) *fakeShell {
	return &fakeShell{done: make(chan struct{}), text: text}
}

func deadFakeShell(code int) *fakeShell {
	f := newFakeShell("")
	f.code = code
	close(f.done)
	return f
}

func (f *fakeShell) Done() <-chan struct{} { return f.done }

func (f *fakeShell) ExitCode() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.code
}

func (f *fakeShell) Text() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text
}

func (f *fakeShell) Resize(cols, rows int) { f.mu.Lock(); f.sent = true; f.mu.Unlock() }

func (f *fakeShell) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.sent {
		f.sent = true
		close(f.done)
	}
	return nil
}

// attempt is one start a fakeLog saw.
type attempt struct {
	argv []string
	cfg  startConfig
}

type fakeLog struct {
	mu       sync.Mutex
	attempts []attempt
}

func (l *fakeLog) starts() []attempt {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]attempt(nil), l.attempts...)
}

// fakeStart builds each attempt's process; it returns nil for an error.
func fakeStart(build func(n int, c startConfig) (shellProc, error)) (starter, *fakeLog) {
	log := &fakeLog{}
	st := func(c startConfig) (shellProc, error) {
		log.mu.Lock()
		log.attempts = append(log.attempts, attempt{argv: append([]string(nil), c.Argv...), cfg: c})
		n := len(log.attempts)
		log.mu.Unlock()
		p, err := build(n, c)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, errors.New("boom")
		}
		return p, nil
	}
	return st, log
}

func loginArgv(argv []string) bool {
	for _, a := range argv {
		if a == "-l" {
			return true
		}
	}
	return false
}

// TestLoginShellFallsBack is the bug the user hit: a login shell that dies at
// startup (129, SIGHUP) must be replaced once by the same shell without -l,
// and the tab must say so.
func TestLoginShellFallsBack(t *testing.T) {
	st, log := fakeStart(func(n int, c startConfig) (shellProc, error) {
		if loginArgv(c.Argv) {
			return deadFakeShell(129), nil
		}
		return newFakeShell("$ "), nil
	})
	tab, err := newTab("t1", t.TempDir(), "/bin/zsh", nil, nil,
		tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !tab.Fallback {
		t.Fatal("a login shell that exits non-zero must fall back")
	}
	if tab.ExitCode != 129 {
		t.Fatalf("ExitCode = %d, want 129", tab.ExitCode)
	}
	if got := tab.Status(); got.State != StateRunning {
		t.Fatalf("the fallback shell must run, status %s", got)
	}
	starts := log.starts()
	if len(starts) != 2 {
		t.Fatalf("want a login attempt and a plain one, got %d", len(starts))
	}
	if !loginArgv(starts[0].argv) || loginArgv(starts[1].argv) {
		t.Fatalf("attempts out of order: %q then %q", starts[0].argv, starts[1].argv)
	}
	if tab.Cwd == "" {
		t.Fatal("the tab must remember its cwd for a restart")
	}
}

// TestCleanLoginExitDoesNotFallBack: only a non-zero end means the login
// shell is unusable; one that exits 0 leaves the tab exited, no retry.
func TestCleanLoginExitDoesNotFallBack(t *testing.T) {
	st, log := fakeStart(func(int, startConfig) (shellProc, error) {
		return deadFakeShell(0), nil
	})
	tab, err := newTab("t5", t.TempDir(), "/bin/zsh", nil, nil,
		tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Fallback {
		t.Fatal("a login shell that exits 0 must not fall back")
	}
	if st := tab.Status(); st.State != StateExited || st.Code != 0 {
		t.Fatalf("status = %s, want exited(0)", st)
	}
	if starts := log.starts(); len(starts) != 1 {
		t.Fatalf("want one start, got %d", len(starts))
	}
}

// TestLoginShellThatRunsDoesNotFallBack checks the healthy path: the prompt
// comes, and only one shell is started.
func TestLoginShellThatRunsDoesNotFallBack(t *testing.T) {
	st, log := fakeStart(func(int, startConfig) (shellProc, error) {
		return newFakeShell("zsh% "), nil
	})
	tab, err := newTab("t2", t.TempDir(), "/bin/zsh", nil, nil,
		tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if tab.Fallback || tab.ExitCode != 0 {
		t.Fatalf("a shell that runs must not fall back: %+v", tab.Status())
	}
	if starts := log.starts(); len(starts) != 1 || !loginArgv(starts[0].argv) {
		t.Fatalf("want one login start, got %v", starts)
	}
}

// TestExitAfterStartIsReported checks the app hears about a shell that ends
// while the user watches, and the tab's status follows.
func TestExitAfterStartIsReported(t *testing.T) {
	var mu sync.Mutex
	var exits []int
	sh := newFakeShell("$ ")
	st, log := fakeStart(func(int, startConfig) (shellProc, error) { return sh, nil })
	tab, err := newTab("t3", t.TempDir(), "/bin/zsh", nil, func(code int) {
		mu.Lock()
		exits = append(exits, code)
		mu.Unlock()
	}, tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	starts := log.starts()
	if len(starts) != 1 {
		t.Fatalf("want one start, got %d", len(starts))
	}
	// The emulator reports the end on its own goroutine.
	starts[0].cfg.OnExit(7)
	mu.Lock()
	got := append([]int(nil), exits...)
	mu.Unlock()
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("onExit calls = %v, want one with 7", got)
	}
	if st := tab.Status(); st.State != StateExited || st.Code != 7 {
		t.Fatalf("status = %s, want exited(7)", st)
	}
	if tab.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", tab.ExitCode)
	}

	// A restart brings a shell back in the same tab.
	sh2 := newFakeShell("$ ")
	st2, _ := fakeStart(func(int, startConfig) (shellProc, error) { return sh2, nil })
	tab.start = st2
	if err := tab.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if st := tab.Status(); st.State != StateRunning {
		t.Fatalf("after restart status = %s", st)
	}
}

// TestCloseIsNotAnExit: closing a tab on purpose must not tell the app its
// shell exited.
func TestCloseIsNotAnExit(t *testing.T) {
	var mu sync.Mutex
	var exits int
	sh := newFakeShell("$ ")
	st, log := fakeStart(func(int, startConfig) (shellProc, error) { return sh, nil })
	tab, err := newTab("t4", t.TempDir(), "/bin/zsh", nil, func(int) {
		mu.Lock()
		exits++
		mu.Unlock()
	}, tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	starts := log.starts()
	starts[0].cfg.OnExit(129) // the hang-up arrives after Close
	mu.Lock()
	defer mu.Unlock()
	if exits != 0 {
		t.Fatalf("onExit ran %d times for a closed tab", exits)
	}
}

// TestRestartKeepsIDAndCwd restarts a shell and checks the tab is the same
// tab: same ID, same directory, same strategy, same view.
func TestRestartKeepsIDAndCwd(t *testing.T) {
	dir := t.TempDir()
	st, log := fakeStart(func(int, startConfig) (shellProc, error) {
		return newFakeShell("$ "), nil
	})
	tab, err := newTab("t7", dir, "/bin/zsh", nil, nil,
		tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if tab.ID != "t7" {
		t.Fatalf("ID changed to %q", tab.ID)
	}
	if tab.Cwd != dir {
		t.Fatalf("Cwd = %q, want %q", tab.Cwd, dir)
	}
	if st := tab.Status(); st.State != StateRunning {
		t.Fatalf("the restarted shell is not running: %s", st)
	}
	if v := tab.View("Terminal"); v.TestID != "terminal-t7" {
		t.Fatalf("the view lost the ID: %q", v.TestID)
	}
	starts := log.starts()
	if len(starts) != 2 {
		t.Fatalf("want two starts, got %d", len(starts))
	}
	for i, s := range starts {
		if !loginArgv(s.argv) || s.cfg.Dir != dir {
			t.Fatalf("start %d: %q in %q, want the login strategy in the cwd", i, s.argv, s.cfg.Dir)
		}
	}
}

func TestRestartFailureIsReported(t *testing.T) {
	st, _ := fakeStart(func(n int, c startConfig) (shellProc, error) {
		if n > 1 {
			return nil, errors.New("boom")
		}
		return newFakeShell("$ "), nil
	})
	tab, err := newTab("t8", t.TempDir(), "/bin/zsh", nil, nil,
		tabOptions{start: st, window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := tab.Restart(); err == nil {
		t.Fatal("a restart that cannot start must report it")
	}
	if got := tab.Status(); got.State != StateFailed || !strings.Contains(got.String(), "boom") {
		t.Fatalf("status = %s, want failed(boom)", got)
	}
}

// TestTitleHeldUntilTheTabIsWhole is the id race: a title that arrives while
// the shell starts must wait, or the app's callback looks up a tab whose ID
// it does not know yet and drops the title.
func TestTitleHeldUntilTheTabIsWhole(t *testing.T) {
	var mu sync.Mutex
	var got []string
	tab := &Tab{ID: "t1", starting: true, onTitle: func(title string) {
		mu.Lock()
		got = append(got, title)
		mu.Unlock()
	}}
	tab.gotTitle(tab.gen, "build")
	if len(got) != 0 {
		t.Fatalf("title delivered while starting: %q", got)
	}
	tab.mu.Lock()
	tab.starting = false
	tab.mu.Unlock()
	tab.drainTitles()
	if len(got) != 1 || got[0] != "build" {
		t.Fatalf("titles = %q, want [build]", got)
	}
	// Once the tab is whole, titles go straight through...
	tab.gotTitle(tab.gen, "test")
	// ...and a title from a shell the tab replaced is ignored.
	tab.gotTitle(tab.gen+1, "stale")
	if len(got) != 2 || got[1] != "test" {
		t.Fatalf("titles = %q, want [build test]", got)
	}
}

// TestShellAnswers checks the whole local path: the shell starts, its prompt
// reaches the screen, and typed input comes back. A blank terminal in the
// window would fail this before any layout question.
func TestShellAnswers(t *testing.T) {
	tab, err := NewTab("t", t.TempDir(), DefaultShell(), nil, nil)
	if err != nil {
		t.Skipf("shell unavailable: %v", err)
	}
	defer tab.Close()
	if st := tab.Status(); st.State != StateRunning {
		t.Skipf("no usable pty here: %s (fallback %v)", st, tab.Fallback)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.TrimSpace(tab.Terminal.Text()) != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	tab.Terminal.Send([]byte("echo TERM_OK\n"))
	for time.Now().Before(deadline) {
		if strings.Contains(tab.Terminal.Text(), "TERM_OK") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the shell never echoed; status %s; screen:\n%s", tab.Status(), tab.Terminal.Text())
}
