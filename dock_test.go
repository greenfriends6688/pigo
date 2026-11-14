package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
)

// TestDockResizes keeps the right panel's header grip live: dragging it
// widens the panel, which the shell's edge handle lost under the
// transcript's scroll view.
func TestDockResizes(t *testing.T) {
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:right-panel") })
	before := app.FindMounted("id:right-panel").Frame.Size.Width
	app.Drag("id:dock-grip", core.Point{X: -80})
	app.Advance(300 * time.Millisecond)
	after := app.FindMounted("id:right-panel").Frame.Size.Width
	if after <= before {
		t.Fatalf("the dock did not widen: %v -> %v", before, after)
	}
}

// TestDockTabCloses keeps the tab strip's X live: a press closes that tab,
// not just its content.
func TestDockTabCloses(t *testing.T) {
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tab-close-files") })
	app.Press("id:dock-tab-close-files")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:dock-tab-files") })
}

// TestFileTabCloses keeps a file tab's X live too.
func TestFileTabCloses(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:files-tree") })
	app.Press("role:button note.txt")
	app.WaitUntil(10*time.Second, func() bool { return app.Exists("id:dock-tab-file-note.txt") })
	app.Press("id:dock-tab-close-file-note.txt")
	app.WaitUntil(10*time.Second, func() bool { return !app.Exists("id:dock-tab-file-note.txt") })
}

// TestDockTabScrollbarShows keeps a visible bar on the tab strip, not only
// the platform's overlay indicator.
func TestDockTabScrollbarShows(t *testing.T) {
	app := isolatedApp(t)
	openFiles(t, app)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:dock-tabs-scrollbar") })
	v := app.FindMounted("id:dock-tabs-scrollbar")
	if v == nil || v.Frame.Size.Height <= 0 || v.Frame.Size.Width <= 0 {
		t.Fatalf("scrollbar frame = %+v", v)
	}
}
