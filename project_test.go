package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectDir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	saved := t.TempDir()

	if got := resolveProjectDir(project, home, saved); got != project {
		t.Fatalf("the launching project must win over the saved one: got %q", got)
	}
	if got := resolveProjectDir(home, home, saved); got != saved {
		t.Fatalf("a launch from home restores the saved project: got %q", got)
	}
	if got := resolveProjectDir(project, home, ""); got != project {
		t.Fatalf("a real working directory is a project: got %q", got)
	}
	if got := resolveProjectDir(home, home, ""); got != "" {
		t.Fatalf("home is not a project: got %q", got)
	}
	if got := resolveProjectDir("/", home, ""); got != "" {
		t.Fatalf("the filesystem root is not a project: got %q", got)
	}
	if got := resolveProjectDir(home, home, filepath.Join(saved, "gone")); got != "" {
		t.Fatalf("a missing saved project leaves no project: got %q", got)
	}

	t.Setenv("PIGO_PROJECT", saved)
	if got := resolveProjectDir(home, home, ""); got != saved {
		t.Fatalf("the environment overrides the fallback: got %q", got)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := savePrefs(dir, nativePrefs{Project: "/tmp/x", ShowHidden: true}); err != nil {
		t.Fatal(err)
	}
	got := loadPrefs(dir)
	if got.Project != "/tmp/x" || !got.ShowHidden {
		t.Fatalf("prefs = %+v", got)
	}
	if _, err := os.Stat(prefsPath(dir)); err != nil {
		t.Fatal(err)
	}
}
