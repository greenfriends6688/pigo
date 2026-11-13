package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Options configures the RPC child process. NodePath/RpcBundle are resolved
// when empty. stderr is written only to LogPath and is never parsed.
type Options struct {
	// PiPath is the user's own pi executable. When set the child is
	// `pi --mode rpc ...` — the pi with this machine's models, extensions and
	// sessions — and NodePath/RpcBundle are not used.
	PiPath                             string
	NodePath, RpcBundle, Cwd, AgentDir string
	SessionPath, SessionID, ForkID     string
	Tools, ExcludeTools                string
	NoTools                            bool
	NoMCP                              bool
	// NoExtensions starts the child with -ne, for when a user extension
	// cannot load and pi exits before answering the first command.
	NoExtensions bool
	LogPath      string
	ExtraArgs    []string
}

// Transport is the LF-only JSONL boundary to the RPC child process.
type Transport interface {
	// Send serializes cmd as one JSON line plus '\n' on stdin. It is
	// concurrency-safe.
	Send(cmd Command) error
	// Frames yields raw, LF-split lines (without the trailing '\n').
	Frames() <-chan []byte
	// Done is closed when stdout reaches EOF and the child has been reaped.
	Done() <-chan struct{}
	// Err reports the reason the transport ended (nil for a clean EOF).
	Err() error
	// StderrTail is what the child last wrote to stderr, for the window to
	// show when it needs to explain a failure.
	StderrTail() string
	// Close stops the child: stdin EOF first, then the process group.
	Close() error
}

// ErrClosed is returned by Send after the transport has been closed.
var ErrClosed = errors.New("agent: transport closed")

// How long a child gets to exit on its own before it is signalled. pi needs
// about a second and a half to shut an rpc session down (flush the session
// file, stop MCP servers), and a slow machine is slower than that.
const (
	shutdownGrace  = 8 * time.Second
	terminateGrace = 3 * time.Second
)

// waitClosed reports whether done closed within d.
func waitClosed(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	logFile *os.File
	pid     int

	frames   chan []byte
	done     chan struct{}
	stopping chan struct{}

	errMu  sync.Mutex
	err    error
	logMu  sync.Mutex
	stderr *tailWriter

	writeMu sync.Mutex
	closed  bool

	closeOnce sync.Once
}

// NewStdioTransport starts `node <bundle>/rpc-entry.js --mode rpc ...` and wires
// up its stdin/stdout. Only stdout is framed; stderr goes to LogPath (or is
// discarded) and is never parsed. The child is started in its own process group
// so Close can kill the whole tree.
func NewStdioTransport(opts Options) (Transport, error) {
	node := opts.NodePath
	if node == "" {
		node = ResolveNode()
	}
	bundle := opts.RpcBundle
	if bundle == "" {
		bundle = ResolveBundle("")
	}
	// Two ways to reach pi's RPC: the user's own pi, or the bundled copy of
	// its RPC entry that we ship for machines without one.
	pi, usePi := opts.PiPath, opts.PiPath != ""
	if usePi {
		if st, err := os.Stat(pi); err != nil || st.IsDir() {
			return nil, fmt.Errorf("agent: pi not runnable at %s", pi)
		}
	} else if bundle == "" {
		return nil, errors.New("agent: rpc bundle not found")
	}
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = ResolveAgentDir()
	}
	// pi aborts at startup when trust.json has a legacy "trustedDirs" key or
	// any other value besides true/false/null. Repair it before every spawn,
	// not just once at app start: another pi client can rewrite the file at
	// any time, and a child that never boots looks exactly like a lost link.
	sanitizeTrustStore(agentDir)

	// The EPIPE guard must be registered before the bundle is evaluated, and it
	// has to cover the npm fallback bundle too, which we never patch.
	var args []string
	var cmd *exec.Cmd
	if usePi {
		args = buildArgs(opts)
		cmd = exec.Command(pi, args...)
		// pi is a `#!/usr/bin/env node` script: the ::env lookup needs the
		// directory holding pi (where nvm and ~/.local keep node) on PATH.
		cmd.Env = PathWithDir(pi)
	} else {
		args = guardArgs(bundle)
		args = append(args, bundle)
		args = append(args, buildArgs(opts)...)
		cmd = exec.Command(node, args...)
		cmd.Env = os.Environ()
	}
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = append(cmd.Env, "PI_CODING_AGENT_DIR="+agentDir)
	setProcessGroup(cmd)

	var logFile *os.File
	// The tail is kept whether or not a log file exists: it is what the
	// disconnect card shows, and a user should not have to find a file to
	// learn why pi died.
	tail := newTailWriter(40)
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o755); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		logFile = f
		cmd.Stderr = io.MultiWriter(f, tail)
	} else {
		cmd.Stderr = tail
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}

	t := &stdioTransport{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		logFile:  logFile,
		pid:      cmd.Process.Pid,
		frames:   make(chan []byte, 128),
		done:     make(chan struct{}),
		stopping: make(chan struct{}),
		stderr:   tail,
	}
	if usePi {
		t.logf("spawn pid=%d cwd=%s pi=%s args=%v PATH=%q", t.pid, opts.Cwd, pi, args, os.Getenv("PATH"))
	} else {
		t.logf("spawn pid=%d cwd=%s node=%s args=%v", t.pid, opts.Cwd, node, args)
	}
	go t.readPump()
	return t, nil
}

// logf stamps one line into the child's log. The log is the only place the
// disconnect card can point at, so a spawn and its exit always belong
// together — without timestamps the file was an undated pile of stack traces.
func (t *stdioTransport) logf(format string, args ...any) {
	if t.logFile == nil {
		return
	}
	t.logMu.Lock()
	defer t.logMu.Unlock()
	_, _ = fmt.Fprintf(t.logFile, "[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// guardArgs preloads the EPIPE guard that bundle-rpc.sh ships next to
// rpc-entry.js. Node needs an absolute --require path and it has to come
// before the script, so this returns the flags that lead the argv.
func guardArgs(bundle string) []string {
	guard := filepath.Join(filepath.Dir(bundle), "epipe-guard.cjs")
	if _, err := os.Stat(guard); err != nil {
		return nil
	}
	return []string{"--require", guard}
}

// buildArgs assembles the CLI flags that must be set at process start (they are
// not RPC commands: changing tools/models requires a new process).
func buildArgs(opts Options) []string {
	args := []string{"--mode", "rpc"}
	if opts.SessionPath != "" {
		args = append(args, "--session", opts.SessionPath)
	}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if opts.ForkID != "" {
		args = append(args, "--fork", opts.ForkID)
	}
	if opts.Tools != "" {
		args = append(args, "--tools", opts.Tools)
	}
	if opts.ExcludeTools != "" {
		args = append(args, "--exclude-tools", opts.ExcludeTools)
	}
	if opts.NoTools {
		args = append(args, "--no-tools")
	}
	if opts.NoMCP {
		args = append(args, "--no-mcp")
	}
	if opts.NoExtensions {
		args = append(args, "-ne")
	}
	args = append(args, opts.ExtraArgs...)
	return args
}

// readPump is the only reader of stdout. It does nothing but split lines and
// push them onto the buffered frames channel; parsing and callbacks happen on
// the Client's dispatch goroutine, never here. After EOF it reaps the child,
// records the reason and closes both channels exactly once.
func (t *stdioTransport) readPump() {
	// Keep draining stdout after shutdown starts. A child writing its last
	// frames to a pipe nobody reads dies on EPIPE (an unhandled 'error' event
	// in pi's bundle), and a blocked send here used to force Close() into its
	// SIGKILL fallback.
	rerr := splitLinesWithStop(t.stdout, t.frames, t.stopping)
	werr := t.cmd.Wait()

	t.errMu.Lock()
	if werr != nil {
		t.err = werr
	} else {
		t.err = rerr
	}
	t.errMu.Unlock()
	t.logf("exit pid=%d wait=%v read=%v", t.pid, werr, rerr)

	// pi starts MCP servers and tool shells in its own process group. A pi that
	// exits without stopping them leaves orphans behind, and the next spawn
	// hangs while it waits for a server whose port, database or lock an orphan
	// still holds — which the window showed as 未连接 with pi running and idle.
	// Sweeping the group costs one signal when there is nothing left.
	terminateGroup(t.pid)

	close(t.frames)
	close(t.done)

	t.writeMu.Lock()
	t.closed = true
	t.writeMu.Unlock()
	_ = t.stdin.Close()
	if t.logFile != nil {
		_ = t.logFile.Close()
	}
}

// Send writes one LF-terminated JSON line. json.Marshal never escapes U+2028 or
// U+2029 into a newline, so LF framing stays intact.
func (t *stdioTransport) Send(cmd Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.closed {
		return ErrClosed
	}
	_, err = t.stdin.Write(data)
	return err
}

// Frames returns the raw line channel (closed once stdout hits EOF).
func (t *stdioTransport) Frames() <-chan []byte { return t.frames }

// Done returns the channel closed when the transport has fully stopped.
func (t *stdioTransport) Done() <-chan struct{} { return t.done }

// Err returns the EOF/exit reason.
func (t *stdioTransport) Err() error {
	t.errMu.Lock()
	defer t.errMu.Unlock()
	return t.err
}

// StderrTail returns the last stderr lines the child wrote.
func (t *stdioTransport) StderrTail() string { return t.stderr.String() }

// Close signals TERM to the process group, waits up to 5s, then SIGKILLs the
// group. It is idempotent.
func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.cmd.Process == nil {
			return
		}
		// Tell readPump to drop frames but keep reading first, so it is never
		// the reason the child has to be killed.
		close(t.stopping)
		// Closing stdin is pi's own shutdown signal: its rpc mode finishes
		// whatever it was writing and exits 0. Give it that chance before
		// signalling, or every switch kills a child mid-write — which is how
		// a session file ends up half a record long.
		t.writeMu.Lock()
		t.closed = true
		t.writeMu.Unlock()
		_ = t.stdin.Close()

		if !waitClosed(t.done, shutdownGrace) {
			t.logf("term pid=%d: still running after %s", t.pid, shutdownGrace)
			terminateGroup(t.pid)
			if !waitClosed(t.done, terminateGrace) {
				t.logf("kill pid=%d: still running after SIGTERM", t.pid)
				killGroup(t.pid)
				<-t.done
			}
		}
	})
	return nil
}

// ResolveNode finds the node binary. GUI apps start with a minimal PATH, so a
// failed LookPath falls back to the login shell's `command -v node`.
func ResolveNode() string {
	if p, err := exec.LookPath("node"); err == nil {
		return p
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	if out, err := exec.Command(shell, "-lc", "command -v node").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			return p
		}
	}
	return "node"
}

// ResolveBundle locates rpc-entry.js. Search order: resourceDir/rpc, then the
// directories beside the executable, then the npm-global install. Returns an
// empty string when none exists.
//
// The Windows and Linux packages flatten the app's resources next to the
// binary (rpc/), while a macOS .app keeps them under Contents/Resources; both
// layouts have to be searched or those builds start with no bundle at all.
func ResolveBundle(resourceDir string) string {
	exeDir := ""
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}
	for _, c := range bundleCandidates(exeDir, resourceDir) {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// bundleCandidates is ResolveBundle's search order, in one testable slice.
func bundleCandidates(exeDir, resourceDir string) []string {
	var candidates []string
	if resourceDir != "" {
		candidates = append(candidates, filepath.Join(resourceDir, "rpc", "rpc-entry.js"))
	}
	if exeDir != "" {
		candidates = append(candidates,
			filepath.Join(exeDir, "rpc", "rpc-entry.js"),
			filepath.Join(exeDir, "resources", "rpc", "rpc-entry.js"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home,
			".npm-global", "lib", "node_modules", "@earendil-works",
			"pi-coding-agent", "dist", "bundle", "rpc-entry.js"))
	}
	return candidates
}

// ResolveAgentDir returns $PI_CODING_AGENT_DIR or ~/.pi/agent.
func ResolveAgentDir() string {
	if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".pi", "agent")
	}
	return filepath.Join(home, ".pi", "agent")
}
