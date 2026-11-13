// Package sessions discovers and indexes pi coding-agent session files.
//
// pi stores each conversation as a JSONL file under
// <agentDir>/sessions/--<encoded-cwd>--/<timestamp>_<uuid>.jsonl. Only the Go
// standard library is used; pi's own auth.json and sessions-index.sqlite are
// never read or written.
package sessions

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Info describes one session file. It is a value type (only strings, times,
// ints and bools), so copying it is a full copy and callers may freely keep or
// modify returned values.
//
// ID/Path identify the session; Name is the last session_info.name;
// FirstMessage is the first user message truncated to 200 runes; Cwd is the
// working directory from the header; ParentSession is the header's
// parentSession (fork / sub-agent relation); ProjectKey is the normalized cwd;
// Branch/IsWorktree come from <cwd>/.git/HEAD; Created/Modified are the header
// timestamp and the file mtime; MessageCount counts message entries seen;
// Pinned/Archived are pigo-only metadata.
type Info struct {
	ID, Path, Name, FirstMessage, Cwd, ParentSession, ProjectKey, Branch string
	Created, Modified                                                    time.Time
	MessageCount                                                         int
	IsWorktree, Pinned, Archived, Side, Unread                           bool
	// Preview is the last assistant line of the newest turn and LastTurnTools
	// how many tool calls it made: what a row's hover card shows.
	Preview       string
	LastTurnTools int
}

// Group is a set of sessions sharing the same ProjectKey. Items are sorted by
// Modified (newest first).
type Group struct {
	ProjectKey, Title string
	Items             []Info
}

const (
	// firstMessageLimit is the maximum number of runes kept from the first user message.
	firstMessageLimit = 200
	// headSize and tailSize bound the bytes read per file, making a single
	// session O(1) regardless of its size. session_info is appended at the tail.
	headSize = 16 * 1024
	tailSize = 64 * 1024
	// cacheVersion and metaVersion guard against incompatible on-disk files.
	cacheVersion = 1
	metaVersion  = 1
	// maxWarnings caps warning growth during a refresh.
	maxWarnings = 100
)

// DirName encodes a working directory into pi's session directory name:
// "--" + cwd with a single leading "/" or "\" removed + every "/", "\" and ":"
// replaced by "-" + "--". Spaces and non-ASCII characters are preserved.
func DirName(cwd string) string {
	s := cwd
	if len(s) > 0 && (s[0] == '/' || s[0] == '\\') {
		s = s[1:]
	}
	s = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(s)
	return "--" + s + "--"
}

// DefaultAgentDir returns $PI_CODING_AGENT_DIR when set, otherwise ~/.pi/agent.
func DefaultAgentDir() string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

// SessionRoot returns <agentDir>/sessions.
func SessionRoot(agentDir string) string {
	return filepath.Join(agentDir, "sessions")
}

// projectKey normalizes a cwd for grouping: separators are unified and the
// result is lower-cased so Windows paths (case-insensitive, backslash separated)
// collapse to the same key as their forward-slash equivalents.
func projectKey(cwd string) string {
	s := strings.ReplaceAll(cwd, "\\", "/")
	return strings.ToLower(strings.TrimRight(s, "/"))
}

// projectTitle derives a short display name for a group from its cwd.
func projectTitle(cwd, key string) string {
	s := strings.TrimRight(strings.ReplaceAll(cwd, "\\", "/"), "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	if s != "" {
		return s
	}
	if key != "" {
		return key
	}
	return "Unknown"
}

// gitInfo reads <cwd>/.git/HEAD to obtain the checked-out branch. A .git
// directory is a normal repository; a .git file containing "gitdir:" marks a
// linked worktree whose HEAD lives at the referenced gitdir. Any failure is
// silently ignored so a session is never rejected because of git metadata.
func gitInfo(cwd string) (branch string, worktree bool) {
	if cwd == "" {
		return "", false
	}
	dotgit := filepath.Join(cwd, ".git")
	st, err := os.Stat(dotgit)
	if err != nil {
		return "", false
	}

	headPath := ""
	if !st.IsDir() {
		b, e := os.ReadFile(dotgit)
		if e != nil {
			return "", false
		}
		line := strings.TrimSpace(string(b))
		const marker = "gitdir:"
		if !strings.HasPrefix(strings.ToLower(line), marker) {
			return "", false
		}
		worktree = true
		gd := strings.TrimSpace(line[len(marker):])
		if gd == "" {
			return "", true
		}
		gd = strings.ReplaceAll(gd, "\\", "/")
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(cwd, gd)
		}
		headPath = filepath.Join(gd, "HEAD")
	} else {
		headPath = filepath.Join(dotgit, "HEAD")
	}

	b, err := os.ReadFile(headPath)
	if err != nil {
		return "", worktree
	}
	ref := strings.TrimSpace(string(b))
	const refMarker = "ref:"
	if strings.HasPrefix(strings.ToLower(ref), refMarker) {
		ref = strings.TrimSpace(ref[len(refMarker):])
		const heads = "refs/heads/"
		if strings.HasPrefix(ref, heads) {
			ref = ref[len(heads):]
		}
	}
	return ref, worktree
}

// truncateRunes truncates s to at most n runes, never splitting a UTF-8 rune.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
