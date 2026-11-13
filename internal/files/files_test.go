package files

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestListAndMentionIndex(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "huge"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"README.md", "src/main.go", "src/pkg/中文 文件.ts", "node_modules/huge/index.js"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(p)), []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := List(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 { // README.md, node_modules, src
		t.Fatalf("unexpected root listing: %v", entries)
	}
	if !entries[0].IsDir || entries[0].Name != "node_modules" {
		t.Fatalf("directories must come first: %v", entries)
	}
	index, err := MentionIndex(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(index, "|")
	if !strings.Contains(joined, "src/pkg/中文 文件.ts") {
		t.Fatalf("mention index lost a spaced/CJK filename: %v", index)
	}
	if strings.Contains(joined, "node_modules") {
		t.Fatalf("mention index must skip node_modules: %v", index)
	}
}

func TestMentionIndexIsBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		if err := os.WriteFile(filepath.Join(root, "f"+strings.Repeat("x", i)+".txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	index, err := MentionIndex(root, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 5 {
		t.Fatalf("limit not honored: %d", len(index))
	}
}

func TestTextAndBinary(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "a.txt")
	bin := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(text, []byte("你好 world\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02, 0x03}, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadText(text, 1024)
	if err != nil || got != "你好 world\n" {
		t.Fatalf("ReadText = %q, %v", got, err)
	}
	if _, err := ReadText(bin, 1024); !errors.Is(err, ErrBinary) {
		t.Fatalf("want ErrBinary, got %v", err)
	}
	if ok, err := IsBinary(bin); err != nil || !ok {
		t.Fatalf("IsBinary = %v, %v", ok, err)
	}
	if ok, err := IsBinary(text); err != nil || ok {
		t.Fatalf("text must not be binary: %v, %v", ok, err)
	}
}

func TestGitLogReadsHistory(t *testing.T) {
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
	run("commit", "-q", "--allow-empty", "-m", "first commit")

	commits, head, err := GitLog(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Subject != "first commit" {
		t.Fatalf("unexpected commits: %+v", commits)
	}
	if len(commits[0].Hash) < 7 || len(head) < 7 || head != commits[0].Hash[:len(head)] {
		t.Fatalf("head %q does not match hash %q", head, commits[0].Hash)
	}

	run("commit", "-q", "--allow-empty", "-m", "second")
	commits, _, err = GitLog(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Subject != "second" {
		t.Fatalf("limit or order ignored: %+v", commits)
	}
	if len(commits[0].Parents) != 1 {
		t.Fatalf("the second commit must have one parent: %+v", commits[0])
	}

	if _, _, err := GitLog(t.TempDir(), 5); err == nil {
		t.Fatal("a plain directory must not look like a git repository")
	}
}

func TestGitLogEmptyRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	commits, head, err := GitLog(root, 10)
	if err != nil {
		t.Fatalf("an empty repository is not an error: %v", err)
	}
	if len(commits) != 0 || head != "" {
		t.Fatalf("empty repository returned %v, head %q", commits, head)
	}
}

func TestGitBranchOrdinaryAndWorktree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/feature/native\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	branch, worktree := GitBranch(root)
	if branch != "feature/native" || worktree {
		t.Fatalf("branch = %q, worktree = %v", branch, worktree)
	}

	wt := t.TempDir()
	gitdir := filepath.Join(wt, "real-git")
	if err := os.MkdirAll(gitdir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: real-git\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	branch, worktree = GitBranch(wt)
	if branch != "main" || !worktree {
		t.Fatalf("worktree branch = %q, worktree = %v", branch, worktree)
	}
}
