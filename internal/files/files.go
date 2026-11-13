// Package files replaces pi-web's file and Git HTTP routes with direct
// filesystem and `git` access (PLAN §1, §8 P5).
package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Entry is one child of a directory.
type Entry struct {
	Name    string
	Path    string
	IsDir   bool
	IsLink  bool
	Size    int64
	ModTime int64 // Unix seconds
}

// List reads a single directory level. os.ReadDir is O(n) for that level and
// returns sorted results, so a repository with 100k files stays fast as long
// as the tree is walked lazily one directory at a time.
func List(dir string, max int) ([]Entry, error) {
	return ListHidden(dir, max, false)
}

// ListHidden is List with the system's hidden entries shown when showHidden is
// set. Build directories stay hidden either way.
func ListHidden(dir string, max int, showHidden bool) ([]Entry, error) {
	if max <= 0 {
		max = 5000
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, min(len(entries), max))
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") && name != ".pi" && name != ".git" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // a file vanished between ReadDir and Info; skip it
		}
		isDir := e.IsDir()
		isLink := e.Type()&fs.ModeSymlink != 0
		if isLink {
			// macOS makes /etc, /tmp, /var and /home symlinks; treating them
			// as files is why pressing one tried to read it as text.
			if target, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = target.IsDir()
			}
		}
		out = append(out, Entry{
			Name:    name,
			Path:    filepath.Join(dir, name),
			IsDir:   isDir,
			IsLink:  isLink,
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		})
	}
	// Directories first, then name. os.ReadDir already sorts by name.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// skipDirs are never walked when building the @-mention index.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".next": true, "dist": true,
	"build": true, "vendor": true, ".cache": true, "__pycache__": true,
	"target": true, ".venv": true, "venv": true, "Pods": true,
}

// MentionIndex builds the relative paths offered by the `@` completion.
// It is bounded in both depth and count so huge repositories cannot stall
// the UI thread.
func MentionIndex(root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5000
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip rather than fail the index
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDirs[name] || strings.HasPrefix(name, ".") && name != ".pi" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return out, err
	}
	return out, nil
}

// ErrBinary is returned when a preview would render as garbage.
var ErrBinary = errors.New("files: binary content")

// IsBinary reports whether the first bytes look like a non-text file.
func IsBinary(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if n == 0 {
		if err == nil || errors.Is(err, io.EOF) {
			return false, nil // an empty file is text
		}
		return false, err
	}
	buf = buf[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return true, nil
	}
	if !utf8.Valid(buf) {
		// A multi-byte rune may be cut off at the boundary; only clearly
		// invalid data counts as binary.
		if !utf8.Valid(buf[:max(0, n-4)]) {
			return true, nil
		}
	}
	return false, nil
}

// ReadText previews up to limit bytes of a text file.
func ReadText(path string, limit int64) (string, error) {
	if limit <= 0 {
		limit = 1 << 20
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, min(limit, 8<<20))
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	b := buf[:n]
	if bytes.IndexByte(b, 0) >= 0 {
		return "", ErrBinary
	}
	return string(b), nil
}

// GitEntry is one `git status --porcelain` line.
type GitEntry struct {
	Path   string
	Status string // two-letter XY code, e.g. " M" or "??"
}

// GitStatus is the repository state of a working directory.
type GitStatus struct {
	Branch     string
	IsWorktree bool
	Entries    []GitEntry
	Clean      bool
}

func git(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GitBranch reads .git/HEAD directly (no subprocess), honoring worktrees
// where .git is a file containing `gitdir: ...`.
func GitBranch(root string) (string, bool) {
	dotgit := filepath.Join(root, ".git")
	st, err := os.Lstat(dotgit)
	if err != nil {
		return "", false
	}
	isWorktree := st.Mode()&os.ModeSymlink == 0 && !st.IsDir()
	head := dotgit
	if isWorktree {
		b, err := os.ReadFile(dotgit)
		if err != nil {
			return "", true
		}
		line := strings.TrimSpace(string(b))
		if !strings.HasPrefix(line, "gitdir:") {
			return "", true
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		head = p
	}
	b, err := os.ReadFile(filepath.Join(head, "HEAD"))
	if err != nil {
		return "", isWorktree
	}
	line := strings.TrimSpace(string(b))
	if strings.HasPrefix(line, "ref:") {
		ref := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		return strings.TrimPrefix(ref, "refs/heads/"), isWorktree
	}
	if len(line) >= 7 {
		return line[:7] + " (detached)", isWorktree
	}
	return line, isWorktree
}

// GitState runs `git status --porcelain=v1 --untracked-files=normal`.
func GitState(root string) (GitStatus, error) {
	branch, worktree := GitBranch(root)
	out, err := git(root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return GitStatus{Branch: branch, IsWorktree: worktree}, err
	}
	var entries []GitEntry
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 4 {
			continue
		}
		entries = append(entries, GitEntry{Status: line[:2], Path: strings.TrimSpace(line[3:])})
	}
	return GitStatus{Branch: branch, IsWorktree: worktree, Entries: entries, Clean: len(entries) == 0}, nil
}

// Commit is one row of `git log --date-order`.
type Commit struct {
	Hash    string
	Short   string
	Parents []string
	Author  string
	Subject string
	Unix    int64
}

// GitLog reads the newest commits of the work tree at root and the short hash
// of HEAD. An empty repository (no commits yet) returns no commits and no
// error, so the dock can tell it apart from a directory that is not a repo.
func GitLog(root string, limit int) ([]Commit, string, error) {
	if limit <= 0 {
		limit = 100
	}
	inside, err := git(root, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		return nil, "", fmt.Errorf("files: %s is not a git work tree", root)
	}
	headOut, err := git(root, "rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, "", nil
	}
	head := strings.TrimSpace(string(headOut))
	out, err := git(root, "log", "--date-order", fmt.Sprintf("-n%d", limit),
		"--pretty=format:%H%x1f%P%x1f%an%x1f%at%x1f%s%x1e")
	if err != nil {
		return nil, head, err
	}
	var commits []Commit
	for _, record := range strings.Split(string(out), "\x1e") {
		record = strings.Trim(record, "\n\r")
		if record == "" {
			continue
		}
		fields := strings.Split(record, "\x1f")
		if len(fields) < 5 {
			continue
		}
		unix, _ := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
		c := Commit{
			Hash:    fields[0],
			Author:  fields[2],
			Subject: fields[4],
			Unix:    unix,
		}
		if len(c.Hash) >= 7 {
			c.Short = c.Hash[:7]
		} else {
			c.Short = c.Hash
		}
		if p := strings.TrimSpace(fields[1]); p != "" {
			c.Parents = strings.Fields(p)
		}
		commits = append(commits, c)
	}
	return commits, head, nil
}

// GitDiff returns the diff of one path (or of the whole tree when empty).
func GitDiff(root, path string) (string, error) {
	args := []string{"diff", "--no-color", "--", path}
	if path == "" {
		args = []string{"diff", "--no-color"}
	}
	out, err := git(root, args...)
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return string(out), nil
}

// GitDiffStaged is the patch of path between HEAD and the index.
func GitDiffStaged(root, path string) (string, error) {
	out, err := git(root, "diff", "--cached", "--no-color", "--", path)
	if err != nil {
		return "", fmt.Errorf("git diff --cached: %w", err)
	}
	return string(out), nil
}

// GitStage adds path to the index.
func GitStage(root, path string) error {
	_, err := git(root, "add", "-A", "--", path)
	return err
}

// GitUnstage removes path from the index, leaving its working copy.
func GitUnstage(root, path string) error {
	_, err := git(root, "reset", "-q", "HEAD", "--", path)
	return err
}

// GitCommit records the index with message and returns git's summary.
func GitCommit(root, message string) (string, error) {
	out, err := git(root, "commit", "-m", message)
	if err != nil {
		return string(out), err
	}
	return strings.TrimSpace(string(out)), nil
}
