package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A node that does not answer is a node the app must not use: on Apple silicon
// a broken signature is killed at exec, and trusting the file made every RPC
// child die on start while the window showed only "连接中断".
func TestNodeRunsRejectsABinaryThatCannotRun(t *testing.T) {
	if nodeRuns("/bin/false") {
		t.Fatal("a failing binary must not count as a runnable node")
	}
	system, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node on PATH")
	}
	if !nodeRuns(system) {
		t.Fatalf("%s runs but nodeRuns said no", system)
	}
}

func executableFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// The packaged bundle must carry a node that actually executes; build.sh signs
// it after strip, and this catches a regression before it ships.
func TestBundledNodeRuns(t *testing.T) {
	candidates := []string{filepath.Join("resources", "bin", "node")}
	if r := resourceDir(); r != "" {
		candidates = append(candidates, filepath.Join(r, "bin", "node"))
	}
	for _, p := range candidates {
		st, err := filepath.Abs(p)
		if err != nil || !executableFile(st) {
			continue
		}
		if !nodeRuns(st) {
			t.Fatalf("%s does not run: the bundle would ship a broken node", st)
		}
		return
	}
	t.Skip("no bundled node in this build (B-prime)")
}
