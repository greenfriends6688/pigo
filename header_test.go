package main

import (
	"testing"
	"time"
)

// TestHeaderHidesNonRepoGitChips keeps the title bar quiet outside a
// repository: one "not a Git repository" is enough, and it belongs to the
// review panel, not to two chips in the header.
func TestHeaderHidesNonRepoGitChips(t *testing.T) {
	t.Chdir(t.TempDir())
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:shell") })
	app.Advance(2 * time.Second)
	if app.Exists("id:header-branch") {
		t.Fatal("the header still shows a branch chip outside a repository")
	}
	if app.Exists("id:capsule-git") {
		t.Fatal("the status capsule still repeats the Git state")
	}
}
