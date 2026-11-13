// Package term starts the user's shell directly (PLAN §4): no sidecar, no
// /api/terminal route, no SSE bridge. One Tab owns one Terminal and one View.
package term

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oneo-me/nsgo/base/terminal"
	"github.com/oneo-me/nsgo/core"
)

// The two themes are made once: a View is rebuilt on every frame, and the
// emulator recognizes a theme by pointer, rebuilding its palette (and
// dropping the colors a program set) whenever the pointer changes.
var (
	lightTheme = terminal.LightTheme()
	darkTheme  = terminal.DarkTheme()
)

// FallbackWindow is how long a freshly started login shell has to show that
// it runs. A shell that ends inside it with a non-zero code (129 is SIGHUP,
// the one that bites under a bare PTY) is replaced by the same shell without
// -l.
const FallbackWindow = 800 * time.Millisecond

// startPoll is how often a starting shell is asked whether it is alive.
const startPoll = 10 * time.Millisecond

// State is where a tab's shell stands.
type State string

const (
	// StateRunning means the shell is up (or starting, before it speaks).
	StateRunning State = "running"
	// StateExited means the shell ended; Status.Code is its code.
	StateExited State = "exited"
	// StateFailed means the shell could not be started at all.
	StateFailed State = "failed"
)

// Status describes the shell of a tab.
type Status struct {
	State State
	// Code is the exit code when State is StateExited.
	Code int
	// Err is why the shell could not start when State is StateFailed.
	Err string
}

// String renders the status as a short line: "running", "exited(129)" or
// "failed(exec: ...)".
func (s Status) String() string {
	switch s.State {
	case StateRunning:
		return "running"
	case StateExited:
		return fmt.Sprintf("exited(%d)", s.Code)
	case StateFailed:
		if s.Err == "" {
			return "failed"
		}
		return "failed(" + s.Err + ")"
	}
	return string(s.State)
}

// startConfig is what a starter needs to run a program in a pty.
type startConfig struct {
	Argv    []string
	Dir     string
	Env     []string
	OnTitle func(title string)
	OnExit  func(code int)
}

// shellProc is all a tab needs from a running shell: the real one is a
// terminal.Terminal, and tests bring their own so the startup, fallback and
// restart logic runs without a pseudo-terminal.
type shellProc interface {
	Done() <-chan struct{}
	ExitCode() int
	Text() string
	Resize(cols, rows int)
	Close() error
}

// starter starts a program; the package's default is an nsgo terminal, and
// tests swap it for one that runs a known program.
type starter func(startConfig) (shellProc, error)

// terminalStarter is the real starter: one nsgo terminal per attempt.
func terminalStarter(c startConfig) (shellProc, error) {
	t, err := terminal.New(terminal.Options{
		Command:    c.Argv,
		Dir:        c.Dir,
		Env:        c.Env,
		Scrollback: 10 << 20,
		OnTitle:    c.OnTitle,
		OnExit:     c.OnExit,
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// tabOptions are the knobs NewTab does not expose: the starter (tests) and
// the window a login shell has to prove itself.
type tabOptions struct {
	start  starter
	window time.Duration
}

// Tab is a single terminal tab.
type Tab struct {
	ID string
	// Title is the label the app shows; the package never writes it, so the
	// app stays its only author.
	Title string
	// Terminal is the emulator of the shell running now: Restart and the
	// fallback to a plain shell put another one here.
	Terminal *terminal.Terminal
	// Cwd is the directory the shell runs in.
	Cwd string
	// Fallback reports that the login shell died at startup and a plain
	// interactive shell took its place.
	Fallback bool
	// ExitCode is the code of the most recent shell that ended; a fallback
	// records the code of the login shell that failed.
	ExitCode int
	// OnFocus runs when the view takes the keyboard focus; a press on the
	// terminal takes it by itself, so this is for observers.
	OnFocus func()

	// openMu serializes launching and closing, which must never overlap.
	openMu sync.Mutex

	mu       sync.Mutex
	shell    string
	env      []string
	onTitle  func(string)
	onExit   func(int)
	start    starter
	window   time.Duration
	gen      int      // the attempt whose callbacks still count
	pending  []string // titles seen while starting, delivered after
	starting bool
	status   Status
	closed   bool
	ref      terminal.Ref
}

// NewTab starts a login shell in cwd. A login shell that ends with a
// non-zero code inside FallbackWindow (129, SIGHUP, is the usual one) is
// replaced once by the same shell without -l, and the tab records it in
// Fallback and ExitCode.
func NewTab(id, cwd, shell string, onTitle func(string), onExit func(int)) (*Tab, error) {
	return newTab(id, cwd, shell, onTitle, onExit, tabOptions{})
}

// newTab is NewTab with the internals tests need to point the shell at a
// known program and shorten the fallback window.
func newTab(id, cwd, shell string, onTitle func(string), onExit func(int), o tabOptions) (*Tab, error) {
	if shell == "" {
		shell = DefaultShell()
	}
	start := o.start
	if start == nil {
		start = terminalStarter
	}
	window := o.window
	if window <= 0 {
		window = FallbackWindow
	}
	t := &Tab{
		ID:      id,
		Cwd:     resolveCwd(cwd),
		shell:   shell,
		env:     shellEnv(),
		onTitle: onTitle,
		onExit:  onExit,
		start:   start,
		window:  window,
		status:  Status{State: StateRunning},
	}
	if err := t.launch(); err != nil {
		return nil, err
	}
	return t, nil
}

// View renders this tab at the default size, following the window's
// appearance. Resize is driven by the View's own size; no Conn or
// ResizeByConn is needed for a local shell. A press on the view focuses its
// hidden input by itself (terminal.View doc), and AutoFocus takes the
// keyboard on mount; OnFocus reports both to Tab.OnFocus.
func (t *Tab) View(label string) terminal.View {
	return t.ViewWith(label, defaultFontSize, "auto")
}

// defaultFontSize is the size of a View that asks for none.
const defaultFontSize = 13

// ViewWith renders this tab at fontSize points in the given theme. A size of
// zero or less is the default 13; "light" and "dark" pin both appearances to
// one palette, and anything else (auto) keeps the usual light/dark pair, so
// the emulator follows the window. The two palettes are the package's fixed
// pointers: the emulator recognizes a theme by pointer and rebuilds it (and
// drops OS-set colors) whenever the pointer changes.
func (t *Tab) ViewWith(label string, fontSize int, theme string) terminal.View {
	t.mu.Lock()
	trm := t.Terminal
	onFocus := t.OnFocus
	t.mu.Unlock()
	if fontSize <= 0 {
		fontSize = defaultFontSize
	}
	light, dark := lightTheme, darkTheme
	switch theme {
	case "light":
		light, dark = lightTheme, lightTheme
	case "dark":
		light, dark = darkTheme, darkTheme
	}
	v := terminal.View{
		Terminal:  trm,
		Label:     label,
		TestID:    "terminal-" + t.ID,
		AutoFocus: true,
		Theme:     light,
		DarkTheme: dark,
		Font:      core.Font{Size: core.Points(fontSize)},
		Padding:   terminal.Padding{X: 6, Y: 4},
		Style:     core.Style{FlexGrow: 1, FlexShrink: 1},
		NoKeyBar:  true,
		Ref:       &t.ref,
	}
	if onFocus != nil {
		v.OnFocus = onFocus
	}
	return v
}

// Focus gives the terminal the keyboard again, for the app after a search
// bar or a dialog took it.
func (t *Tab) Focus() { t.ref.Focus() }

// Status reports whether the shell runs, ended or never started.
func (t *Tab) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// Restart closes the shell and starts it again in the same cwd with the same
// strategy (login shell, plain shell as the fallback), keeping ID and the
// title and exit callbacks.
func (t *Tab) Restart() error { return t.launch() }

// launch starts the shell of the tab, falling back once to a plain shell.
func (t *Tab) launch() error {
	t.openMu.Lock()
	defer t.openMu.Unlock()

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("term: tab is closed")
	}
	old := t.Terminal
	t.gen++ // the shell going away must not report titles or exits
	t.Terminal = nil
	t.Fallback = false
	t.ExitCode = 0
	t.starting = true
	t.pending = nil
	t.status = Status{State: StateRunning}
	t.mu.Unlock()
	// Closing hangs up the old shell; its callbacks are already ignored.
	if old != nil {
		_ = old.Close()
	}

	trm, code, alive, err := t.attempt(true)
	if err != nil || (!alive && code != 0) {
		// The login shell failed to start, or ended inside the window with a
		// non-zero code: try the same shell without -l, once.
		if trm != nil {
			_ = trm.Close()
		}
		t.mu.Lock()
		t.Fallback = true
		t.ExitCode = code
		t.mu.Unlock()
		trm, code, alive, err = t.attempt(false)
	}
	if err != nil {
		t.mu.Lock()
		t.starting = false
		t.status = Status{State: StateFailed, Err: err.Error()}
		t.mu.Unlock()
		return fmt.Errorf("term: %w", err)
	}

	t.mu.Lock()
	if v, ok := trm.(*terminal.Terminal); ok {
		t.Terminal = v
	}
	t.starting = false
	if alive {
		t.status = Status{State: StateRunning}
	} else {
		t.ExitCode = code
		t.status = Status{State: StateExited, Code: code}
	}
	t.mu.Unlock()
	t.drainTitles()
	return nil
}

// attempt starts one shell — with -l when login — and waits for it to show
// that it runs. It reports the process, the exit code of a shell that ended,
// whether one runs and the error that kept it from starting.
func (t *Tab) attempt(login bool) (shellProc, int, bool, error) {
	argv := []string{t.shell}
	if login {
		argv = append(argv, "-l")
	}
	t.mu.Lock()
	t.gen++
	gen := t.gen
	t.mu.Unlock()
	trm, err := t.start(startConfig{
		Argv:    argv,
		Dir:     t.Cwd,
		Env:     t.env,
		OnTitle: func(title string) { t.gotTitle(gen, title) },
		OnExit:  func(code int) { t.gotExit(gen, code) },
	})
	if err != nil {
		return nil, 0, false, err
	}
	if trm == nil {
		return nil, 0, false, errors.New("term: no shell process")
	}
	// A terminal no view shows yet would start half a second later at
	// 80×24; start it now, at that size, so a broken shell shows at once
	// and a good one has its prompt before the first frame. The view
	// resizes it as it mounts.
	trm.Resize(80, 24)
	alive, code := t.probe(trm)
	return trm, code, alive, nil
}

// probe waits for the shell to prove it runs: it wrote something (its
// prompt), or the window passed without it ending. A shell that ends inside
// the window is not alive, and its code is returned.
func (t *Tab) probe(trm shellProc) (alive bool, code int) {
	deadline := time.Now().Add(t.window)
	for {
		if ended(trm) {
			return false, trm.ExitCode()
		}
		if strings.TrimSpace(trm.Text()) != "" {
			// The exit notice is text too: look again for the end.
			if ended(trm) {
				return false, trm.ExitCode()
			}
			return true, 0
		}
		if !time.Now().Before(deadline) {
			return true, 0
		}
		time.Sleep(startPoll)
	}
}

func ended(trm shellProc) bool {
	select {
	case <-trm.Done():
		return true
	default:
		return false
	}
}

// gotTitle takes a title from the emulator. While the tab starts the title
// is held: a caller's callback may look up the tab by ID, which it learns
// only once NewTab (or Manager.Open) returns, so delivering now would drop
// the title.
func (t *Tab) gotTitle(gen int, title string) {
	if title == "" {
		return
	}
	t.mu.Lock()
	if gen != t.gen || t.closed {
		t.mu.Unlock()
		return
	}
	if t.starting {
		if len(t.pending) == 0 || t.pending[len(t.pending)-1] != title {
			if len(t.pending) >= 8 {
				t.pending = t.pending[1:]
			}
			t.pending = append(t.pending, title)
		}
		t.mu.Unlock()
		return
	}
	cb := t.onTitle
	t.mu.Unlock()
	if cb != nil {
		cb(title)
	}
}

// drainTitles delivers the titles seen while starting, once the tab is whole.
func (t *Tab) drainTitles() {
	t.mu.Lock()
	pending := t.pending
	t.pending = nil
	cb := t.onTitle
	t.mu.Unlock()
	if cb == nil {
		return
	}
	for _, title := range pending {
		cb(title)
	}
}

// gotExit takes the exit of a shell. The death of a shell the tab is
// replacing — the login shell that fell back, or one being restarted — is
// the tab's business, not the app's.
func (t *Tab) gotExit(gen int, code int) {
	t.mu.Lock()
	if gen != t.gen || t.closed {
		t.mu.Unlock()
		return
	}
	t.ExitCode = code
	t.status = Status{State: StateExited, Code: code}
	starting, cb := t.starting, t.onExit
	t.mu.Unlock()
	if starting || cb == nil {
		return
	}
	cb(code)
}

// Close hangs up the shell and releases the emulator. The app's exit
// callback does not run: the tab is going away on purpose.
func (t *Tab) Close() error {
	t.openMu.Lock()
	defer t.openMu.Unlock()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.gen++
	trm := t.Terminal
	t.mu.Unlock()
	if trm == nil {
		return nil
	}
	return trm.Close()
}

// DefaultShell resolves $SHELL, falling back to zsh on macOS and sh elsewhere.
func DefaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// shellEnv is the environment programs inside the terminal see.
func shellEnv() []string {
	return append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
}

// resolveCwd makes cwd absolute and keeps the shell somewhere that exists: a
// closed worktree used to leave the tab dead on a chdir error.
func resolveCwd(cwd string) string {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		} else {
			cwd = "."
		}
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return "."
	}
	return cwd
}

// Manager owns all open terminal tabs and closes them on app exit.
type Manager struct {
	mu   sync.Mutex
	tabs []*Tab
	seq  int
}

// NewManager creates an empty tab manager.
func NewManager() *Manager { return &Manager{} }

// Open starts a new terminal tab. Its title callback is held until the tab
// owns an ID, so a shell that sets its title as it starts still names the
// tab it belongs to.
func (m *Manager) Open(cwd, shell string, onTitle func(string), onExit func(int)) (*Tab, error) {
	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("t%d", m.seq)
	m.mu.Unlock()
	tab, err := NewTab(id, cwd, shell, onTitle, onExit)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.tabs = append(m.tabs, tab)
	m.mu.Unlock()
	// Titles that arrived between the shell starting and the tab being
	// registered are delivered now, when a lookup by ID finds it.
	tab.drainTitles()
	return tab, nil
}

// Tabs returns a snapshot of the open tabs.
func (m *Manager) Tabs() []*Tab {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Tab(nil), m.tabs...)
}

// Close closes one tab by ID.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	var found *Tab
	kept := m.tabs[:0]
	for _, t := range m.tabs {
		if t.ID == id && found == nil {
			found = t
			continue
		}
		kept = append(kept, t)
	}
	m.tabs = kept
	m.mu.Unlock()
	if found == nil {
		return nil
	}
	return found.Close()
}

// CloseAll closes every tab. Errors are joined into the last one so callers
// can log them without masking the rest of the cleanup.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	tabs := m.tabs
	m.tabs = nil
	m.mu.Unlock()
	var first error
	for _, t := range tabs {
		if err := t.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
