// Package agent defines the tool-neutral surface for running coding agents:
// catalogs of models, sessions in a directory and the events they report. Each supported CLI (pi, Codex, Claude Code and CodeBuddy Code)
// implements Driver in its own package, those speaking the Agent Client
// Protocol through package acp. Nothing here depends on UI code; callers deliver events to
// their own thread.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ClientInfo is how the application introduces itself to agents that ask,
// and names its own requests in their protocols.
type ClientInfo struct {
	// Name is a short identifier, such as "agio"; Title the product name.
	Name, Title, Version string
}

// Client is the application's identity; set it once before starting
// drivers.
var Client = ClientInfo{Name: "agio", Title: "Agio", Version: "1"}

// Tool identifies an agent CLI.
type Tool string

const (
	Pi        Tool = "pi"
	Codex     Tool = "codex"
	Claude    Tool = "claude"
	CodeBuddy Tool = "codebuddy"
)

// Tools lists the supported CLIs in presentation order.
var Tools = []Tool{Pi, Codex, Claude, CodeBuddy}

// retired names tools no longer supported. Their tasks' keys still parse to
// them, so those tasks report the tool unavailable instead of running pi.
var retired = map[Tool]string{"opencode": "OpenCode"}

// Name is the product name shown in the interface.
func (t Tool) Name() string {
	switch t {
	case Codex:
		return "Codex"
	case Claude:
		return "Claude Code"
	case CodeBuddy:
		return "CodeBuddy Code"
	}
	if name, ok := retired[t]; ok {
		return name
	}
	return "pi"
}

// Model is one selectable model of a tool.
type Model struct {
	Tool Tool
	// ID is the tool's own identifier: provider/id for pi, the model or
	// alias for other tools.
	ID string
	// Name is the specific display name; Provider groups pi's models; Detail
	// is the concrete model behind an alias or a short description.
	Name, Provider, Detail string
	// Efforts are the accepted reasoning levels, empty when the model has
	// no choice; DefaultEffort is the tool's default among them.
	Efforts       []string
	DefaultEffort string
	// Tiers are the optional service tiers besides standard processing.
	Tiers         []Tier
	ContextWindow int64
	Images        bool
	Default       bool
}

// Tier is a service tier, such as Codex's priority processing.
type Tier struct {
	ID, Name, Description string
}

// Key identifies a model across tools. pi keys stay provider/id, as stored
// before other tools were supported; other tools prefix their name.
func (m Model) Key() string { return Key(m.Tool, m.ID) }

// Key builds a model key from a tool and its model ID.
func Key(tool Tool, id string) string {
	if tool == Pi || tool == "" {
		return id
	}
	return string(tool) + ":" + id
}

// ParseKey splits a model key. Keys without a known tool prefix are pi's.
func ParseKey(key string) (Tool, string) {
	if prefix, id, ok := strings.Cut(key, ":"); ok {
		for _, t := range Tools {
			if prefix == string(t) {
				return t, id
			}
		}
		if _, ok := retired[Tool(prefix)]; ok {
			return Tool(prefix), id
		}
	}
	return Pi, key
}

// ThinkingOff reports whether level turns thinking off: "off" in pi and
// "none" in Codex. Agio does not offer either.
func ThinkingOff(level string) bool {
	return level == "off" || level == "none"
}

// HasEffort reports whether level is one of the model's levels.
func (m Model) HasEffort(level string) bool {
	for _, l := range m.Efforts {
		if l == level {
			return true
		}
	}
	return false
}

// HasTier reports whether id is standard ("") or one of the model's tiers.
func (m Model) HasTier(id string) bool {
	if id == "" {
		return true
	}
	for _, t := range m.Tiers {
		if t.ID == id {
			return true
		}
	}
	return false
}

// Catalog is a tool's models and its configured defaults.
type Catalog struct {
	Models []Model
	// Model is the default model's ID; Effort its default level.
	Model, Effort string
}

// Settings choose a session's model (the tool's own ID), reasoning level and
// service tier ("" is standard).
type Settings struct {
	Model, Effort, Tier string
}

// Image is inline image content for a prompt.
type Image struct {
	Path, Data, MimeType string
}

// Prompt is one user message. Images go inline only to models accepting
// them; Paths are referenced by path.
type Prompt struct {
	Text   string
	Images []Image
	Paths  []string
}

// Message returns the text with referenced paths appended.
func (p Prompt) Message() string {
	if len(p.Paths) == 0 {
		return p.Text
	}
	return p.Text + "\n\nAttached files:\n- " + strings.Join(p.Paths, "\n- ")
}

// StartOptions start a session in Dir, resuming Cursor when set. Title names
// a new session where the tool supports it.
type StartOptions struct {
	Dir, Cursor, Title string
	Settings           Settings
}

// Info describes a started session. Cursor resumes it after a restart;
// Settings are the effective ones, which may differ from those requested.
type Info struct {
	Cursor        string
	Settings      Settings
	ContextWindow int64
	ContextTokens int64
}

// Driver runs one tool. Methods may block and are called off the UI thread.
type Driver interface {
	Tool() Tool
	// Catalog reads models and defaults without starting a conversation.
	Catalog(ctx context.Context, dir string) (Catalog, error)
	// Start launches a session. Events and exit are reported in order, and
	// may be reported before Start returns, so callbacks must not wait for it.
	Start(o StartOptions, onEvent func(Event), onExit func(error)) (Session, Info, error)
	// History reads a saved conversation as settled events: each turn
	// starts with UserMessage, and events carry their time when known.
	History(cursor, dir string) ([]Event, error)
}

// Session is one live conversation process.
type Session interface {
	// Prompt sends a message and returns once the tool accepted it.
	Prompt(ctx context.Context, p Prompt) error
	// Abort stops the current run and returns after its Settled event was
	// reported, so a following prompt never receives it.
	Abort(ctx context.Context) error
	// Configure applies settings to later runs.
	Configure(ctx context.Context, s Settings) error
	// Close asks the process to exit, terminating it after grace. It never
	// signals other processes.
	Close(grace time.Duration)
}

// EventType is the kind of an Event.
type EventType int

const (
	// Started reports that the agent began working.
	Started EventType = iota
	// UserMessage is a prompt; live, only those the agent started itself.
	UserMessage
	// TextDelta appends Text to the assistant text segment; TextDone
	// replaces it with its final content.
	TextDelta
	TextDone
	// ThinkingDelta and ThinkingDone do the same for reasoning.
	ThinkingDelta
	ThinkingDone
	// ToolStart names a tool call with an identifying Preview; ToolOutput
	// replaces its output so far; ToolEnd settles it, with output when
	// Text is set and Failed on error.
	ToolStart
	ToolOutput
	ToolEnd
	// Usage reports context tokens and window; zero values are unknown.
	Usage
	// Title renames the conversation.
	Title
	// Error explains a failure of the current run; an empty Text clears an
	// earlier one after a successful retry.
	Error
	// Cancelled marks the current run as stopped by the user.
	Cancelled
	// Settled ends the current run; Aborted when it was stopped.
	Settled
)

var eventNames = [...]string{"started", "user_message", "text_delta", "text_done", "thinking_delta", "thinking_done",
	"tool_start", "tool_output", "tool_end", "usage", "title", "error", "cancelled", "settled"}

// String names the event type in snake case, as exports record it.
func (t EventType) String() string {
	if t >= 0 && int(t) < len(eventNames) {
		return eventNames[t]
	}
	return fmt.Sprintf("event(%d)", int(t))
}

// Event is one change reported by a session or read from history.
type Event struct {
	Type EventType
	// Segment identifies the text, thinking or tool step within a run.
	Segment string
	Text    string
	Tool    string
	Preview string
	Failed  bool
	Aborted bool
	Tokens  int64
	Window  int64
	Time    time.Time
}

// LookPath finds an executable on env's PATH, honoring PATHEXT on Windows.
// There a symbolic link is returned as its target: a server started over SSH
// can neither stat nor start through links a user created, such as WinGet's
// Links\bun.exe, yet resolves them.
func LookPath(name string, env []string) string {
	extensions := []string{""}
	if runtime.GOOS == "windows" {
		extensions = nil
		if filepath.Ext(name) != "" {
			extensions = append(extensions, "")
		}
		pathext := EnvValue(env, "PATHEXT")
		if pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		for _, ext := range strings.Split(pathext, ";") {
			if strings.HasPrefix(ext, ".") {
				extensions = append(extensions, strings.ToLower(ext))
			}
		}
	}
	for _, dir := range filepath.SplitList(EnvValue(env, "PATH")) {
		if dir == "" {
			continue
		}
		for _, ext := range extensions {
			if path := linkTarget(filepath.Join(dir, name+ext)); IsExecutable(path) {
				return path
			}
		}
	}
	return ""
}

func linkTarget(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		return path
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	return path
}

// IsExecutable reports whether path is an executable file.
func IsExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0)
}

// EnvValue returns the last value of key in env, case-insensitively on Windows.
func EnvValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && (k == key || runtime.GOOS == "windows" && strings.EqualFold(k, key)) {
			return v
		}
	}
	return ""
}

// FirstLine returns the first line of s, marking omitted lines with " …".
func FirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

// Preview picks the argument that identifies a tool call.
func Preview(raw json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	for _, key := range []string{"command", "cmd", "path", "file_path", "pattern", "query", "url", "description"} {
		if v, ok := args[key].(string); ok && v != "" {
			return FirstLine(v)
		}
	}
	return ""
}
