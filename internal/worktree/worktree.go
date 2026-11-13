// Package worktree wraps the git worktree commands the project picker uses.
package worktree

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Worktree is one row of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string
	Main   bool
}

// List reads the worktrees of repo, dropping prunable or missing ones.
func List(repo string) ([]Worktree, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path == "" {
			return
		}
		if st, err := os.Stat(cur.Path); err == nil && st.IsDir() {
			list = append(list, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			cur.Branch = "(detached)"
		case line == "prunable":
			cur.Path = ""
		}
	}
	flush()
	if len(list) > 0 {
		list[0].Main = true
	}
	return list, nil
}

// DefaultPath is where a new branch of repo goes: a sibling worktrees folder
// named after the repository, then the sanitized branch.
func DefaultPath(repo, branch string) string {
	repo = filepath.Clean(repo)
	return filepath.Join(filepath.Dir(repo)+"-worktrees", sanitize(branch))
}

// Create adds a worktree for branch at path. When create is true the branch
// is created from base (HEAD when empty); otherwise it must already exist.
func Create(repo, path, branch, base string, create bool) error {
	args := []string{"worktree", "add"}
	if create {
		args = append(args, "-b", branch)
	}
	args = append(args, "--", path)
	if !create {
		args = append(args, branch)
	} else if base != "" {
		args = append(args, base)
	}
	_, err := git(repo, args...)
	return err
}

// Remove deletes the worktree at path; force is needed when it is dirty.
func Remove(repo, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := git(repo, args...)
	return err
}

// Exists reports whether path is already a worktree of repo.
func Exists(repo, path string) bool {
	list, err := List(repo)
	if err != nil {
		return false
	}
	for _, w := range list {
		if filepath.Clean(w.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

var branchCleaner = regexp.MustCompile(`[\/\\:*?"<>|\s]+`)

func sanitize(branch string) string {
	name := strings.Trim(branchCleaner.ReplaceAllString(branch, "-"), "-")
	if name == "" {
		name = "worktree"
	}
	return name
}

func git(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("worktree: git %s timed out", strings.Join(args, " "))
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("worktree: git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}
