package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oneo-me/nsgo/core"
)

// nativePrefs is the window's own project memory. It lives beside the
// sessions rather than in pi's settings.json, so pi never sees keys it does
// not own (PLAN §3.5).
type nativePrefs struct {
	Project    string `json:"project"`
	ShowHidden bool   `json:"showHidden"`
	TreeFont   int    `json:"treeFont"`
	Appearance string `json:"appearance"`
	// CollapseSteps keeps a finished turn folded; false (the default)
	// shows its thinking and tool work at once.
	CollapseSteps bool `json:"collapseSteps"`
	HideThinking  bool `json:"hideThinking"`
	// TermFont and TermTheme are the dock terminal's own look: a point size
	// from 11 to 18, and "auto" (follow the window), "light" or "dark".
	TermFont  int    `json:"termFont"`
	TermTheme string `json:"termTheme"`
	// Window layout: widths, open flags and the dock's tab survive a restart.
	SidebarWidth float64 `json:"sidebarWidth"`
	DockWidth    float64 `json:"dockWidth"`
	SidebarOpen  *bool   `json:"sidebarOpen"`
	DockOpen     *bool   `json:"dockOpen"`
	DockTab      string  `json:"dockTab"`
	// DockTabs are the tool tabs the panel keeps open.
	DockTabs    []string `json:"dockTabs"`
	SidebarView string   `json:"sidebarView"`
	ChatWidth   int      `json:"chatWidth"`
	// The project list's own arrangement: pinned groups, their order, and a
	// display name per group (the group key is the project path).
	ProjectPin   []string          `json:"projectPin"`
	ProjectOrder []string          `json:"projectOrder"`
	ProjectAlias map[string]string `json:"projectAlias"`
	SendOnEnter  *bool             `json:"sendOnEnter"`
	// Notification channels are independent; pointers keep "never set" apart
	// from an explicit false so the defaults (desktop on, sound off, in-app
	// on) survive an older prefs file.
	NotifyDesktop *bool `json:"notifyDesktop"`
	NotifySound   bool  `json:"notifySound"`
	NotifyInApp   *bool `json:"notifyInApp"`
}

func prefsPath(agentDir string) string {
	return filepath.Join(agentDir, "pigo", "prefs.json")
}

// The terminal's allowed looks. Anything else in prefs.json — an older
// file, a hand edit — reads as the default size and the follow-the-window
// theme.
const (
	defaultTermFont  = 13
	minTermFont      = 11
	maxTermFont      = 18
	defaultTermTheme = "auto"
)

// clampTermFont brings a size into 11–18.
func clampTermFont(size int) int {
	if size < minTermFont {
		return minTermFont
	}
	if size > maxTermFont {
		return maxTermFont
	}
	return size
}

// normalizeTermTheme keeps the three themes the settings offer and reads
// everything else as "auto".
func normalizeTermTheme(theme string) string {
	switch theme {
	case "light", "dark":
		return theme
	}
	return defaultTermTheme
}

func loadPrefs(agentDir string) nativePrefs {
	var p nativePrefs
	if data, err := os.ReadFile(prefsPath(agentDir)); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	return p
}

func savePrefs(agentDir string, p nativePrefs) error {
	path := prefsPath(agentDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// resolveProjectDir picks the directory the file tree, terminals and new
// sessions start in. The last project the user opened wins; without one, a
// real working directory is a project, while a launch from Finder ("/") or
// the home directory is not, so the tree asks for a project instead of
// showing every file on the machine.
func resolveProjectDir(cwd, home, saved string) string {
	if v := strings.TrimSpace(os.Getenv("PIGO_PROJECT")); v != "" {
		if isDir(v) {
			return filepath.Clean(v)
		}
	}
	cwd = filepath.Clean(cwd)
	if cwd != "" && cwd != "/" && cwd != filepath.Clean(home) && isDir(cwd) {
		return cwd
	}
	if saved != "" && isDir(saved) {
		return filepath.Clean(saved)
	}
	return ""
}

// savePrefsAsync writes the window's memory in the background: project,
// appearance, tree settings and the layout that should survive a restart.
func (s *appState) savePrefsAsync(project string, hidden bool) {
	agentDir := s.agentDir
	saved := loadPrefs(agentDir)
	if project != "" {
		saved.Project = project
	}
	saved.ShowHidden = hidden
	saved.TreeFont = s.treeFont.GetUntracked()
	saved.Appearance = s.appearance.GetUntracked()
	saved.CollapseSteps = !s.expandThinking.GetUntracked()
	saved.HideThinking = s.hideThinking.GetUntracked()
	saved.TermFont = clampTermFont(s.termFont.GetUntracked())
	saved.TermTheme = normalizeTermTheme(s.termTheme.GetUntracked())
	open := s.sidebarOpen.GetUntracked()
	dockOpen := s.rightOpen.GetUntracked()
	saved.SidebarOpen = &open
	saved.DockOpen = &dockOpen
	saved.DockTab = s.dock.GetUntracked()
	saved.DockTabs = append([]string(nil), s.dockOpenKinds.GetUntracked()...)
	saved.SidebarView = s.sidebarView.GetUntracked()
	saved.ProjectPin = append([]string(nil), keysOf(s.projectPin)...)
	saved.ProjectOrder = append([]string(nil), s.projectOrder...)
	saved.ProjectAlias = s.projectAlias
	saved.ChatWidth = int(s.chatWidth.GetUntracked())
	enter := s.sendOnEnter.GetUntracked()
	saved.SendOnEnter = &enter
	desktop := s.notifyDone.GetUntracked()
	saved.NotifyDesktop = &desktop
	saved.NotifySound = s.notifySound.GetUntracked()
	inApp := s.notifyInApp.GetUntracked()
	saved.NotifyInApp = &inApp
	if layout := s.layoutIfAny(); layout != nil {
		saved.SidebarWidth = float64(layout.sidebar.GetUntracked())
		saved.DockWidth = float64(layout.dock.GetUntracked())
	}
	go func() { _ = savePrefs(agentDir, saved) }()
}

// panelLayout holds the two dragged panel widths. They live in signals so a
// resize repaints the shell, and outside appState because state.go owns the
// rest of the signals; the map keys them by scope so each window (and each
// headless test) starts from its own prefs.
type panelLayout struct {
	sidebar, dock *core.Signal[core.Points]
}

var panelLayouts sync.Map // *core.Scope -> *panelLayout

// defaultDockWidth is PI NEXT's right panel width; the dock opens at it and
// the header's full-width button returns to it.
const defaultSidebarWidth, defaultDockWidth core.Points = 264, 420

// layout creates the widths on first use, seeding them from prefs. It runs on
// the UI goroutine, during render or a press.
func (s *appState) layout() *panelLayout {
	if v, ok := panelLayouts.Load(s.cx); ok {
		return v.(*panelLayout)
	}
	prefs := loadPrefs(s.agentDir)
	sidebar, dock := core.Points(prefs.SidebarWidth), core.Points(prefs.DockWidth)
	if sidebar <= 0 {
		sidebar = defaultSidebarWidth
	}
	if dock <= 0 {
		dock = defaultDockWidth
	}
	layout := &panelLayout{sidebar: core.NewSignal(s.cx, sidebar), dock: core.NewSignal(s.cx, dock)}
	actual, _ := panelLayouts.LoadOrStore(s.cx, layout)
	return actual.(*panelLayout)
}

// layoutIfAny reads the widths without creating signals, for callers that
// may run off the UI goroutine.
func (s *appState) layoutIfAny() *panelLayout {
	if v, ok := panelLayouts.Load(s.cx); ok {
		return v.(*panelLayout)
	}
	return nil
}

func (s *appState) setSidebarWidth(w core.Points) {
	s.layout().sidebar.Set(w)
	s.saveLayoutSoon()
}

func (s *appState) setDockWidth(w core.Points) {
	s.layout().dock.Set(w)
	s.saveLayoutSoon()
}

// setSidebarOpen flips the session column and remembers it.
func (s *appState) setSidebarOpen(open bool) {
	s.sidebarOpen.Set(open)
	s.saveLayoutSoon()
}

// setDockOpen flips the right panel and remembers it.
func (s *appState) setDockOpen(open bool) {
	s.rightOpen.Set(open)
	s.saveLayoutSoon()
}

// setSidebarView switches the session column between projects and recents.
func (s *appState) setSidebarView(view string) {
	if view != "recent" {
		view = "projects"
	}
	s.sidebarView.Set(view)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// layoutSaveState coalesces the writes a drag produces: a resize reports
// every move, and one prefs file per move would thrash the disk.
type layoutSaveState struct {
	mu      sync.Mutex
	pending bool
}

var layoutSaves sync.Map // *core.Scope -> *layoutSaveState

// saveLayoutSoon persists the layout at most once per 400ms.
func (s *appState) saveLayoutSoon() {
	st, _ := layoutSaves.LoadOrStore(s.cx, &layoutSaveState{})
	state := st.(*layoutSaveState)
	state.mu.Lock()
	if state.pending {
		state.mu.Unlock()
		return
	}
	state.pending = true
	state.mu.Unlock()
	s.cx.AfterFunc(400*time.Millisecond, func() {
		state.mu.Lock()
		state.pending = false
		state.mu.Unlock()
		s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	})
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// projectName is the tree's title for a project directory.
func projectName(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(dir))
}

// projectPins turns the saved list into the set the sidebar reads.
func (p nativePrefs) projectPins() map[string]bool {
	out := make(map[string]bool, len(p.ProjectPin))
	for _, group := range p.ProjectPin {
		out[group] = true
	}
	return out
}

// projectAliases is the saved display names by group, never nil.
func (p nativePrefs) projectAliases() map[string]string {
	out := make(map[string]string, len(p.ProjectAlias))
	for group, name := range p.ProjectAlias {
		if name != "" {
			out[group] = name
		}
	}
	return out
}

// keysOf lists a set's members, for saving it as a slice.
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, on := range m {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
