package files

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitShowCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=测试", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=测试", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "first commit")
	commits, _, err := GitLog(root, 10)
	if err != nil || len(commits) != 1 {
		t.Fatalf("log = %v, %v", commits, err)
	}

	out, err := GitShowCommit(root, commits[0].Hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first commit", "a.txt", "+old line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("git show output is missing %q:\n%s", want, out)
		}
	}

	if _, err := GitShowCommit(root, ""); err == nil {
		t.Fatal("an empty hash must be refused")
	}
	if _, err := GitShowCommit(root, "--help"); err == nil {
		t.Fatal("an option-shaped hash must be refused")
	}
	if _, err := GitShowCommit(root, "deadbeef"); err == nil {
		t.Fatal("an unknown hash must fail")
	}
}
