package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) {
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

func TestCreateListRemove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "project")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "add", "a.txt")
	run(t, repo, "commit", "-q", "-m", "base")

	branch := "feature/one-two"
	path := DefaultPath(repo, branch)
	if !strings.HasSuffix(path, "feature-one-two") {
		t.Fatalf("default path = %q", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Create(repo, path, branch, "", true); err != nil {
		t.Fatal(err)
	}
	list, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Main {
		t.Fatalf("worktrees = %+v", list)
	}
	var found bool
	for _, w := range list {
		if w.Branch == branch {
			found = true
			if !Exists(repo, w.Path) {
				t.Fatal("Exists missed the new worktree")
			}
		}
	}
	if !found {
		t.Fatalf("branch %q not listed: %+v", branch, list)
	}
	if err := Remove(repo, path, false); err != nil {
		t.Fatal(err)
	}
	again, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 {
		t.Fatalf("after remove: %+v", again)
	}
}
