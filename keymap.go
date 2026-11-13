package main

import "github.com/oneo-me/nsgo/core"

// installKeys registers the window's application-level shortcuts. macOS
// menus already cover the standard items; these are the ones a coding
// client is expected to answer wherever the focus is.
func (s *appState) installKeys() {
	s.cx.OnKey("mod+k", func() { s.paletteOpen.Set(true) })
	s.cx.OnKey("mod+shift+p", func() { s.paletteOpen.Set(true) })
	s.cx.OnKey("mod+n", s.newSession)
	s.cx.OnKey("mod+,", func() { s.settingsOpen.Set(true) })

	s.cx.OnKey("mod+b", s.toggleSidebar)
	s.cx.OnKey("mod+shift+e", func() { s.openDock("files") })
	s.cx.OnKey("mod+shift+g", func() { s.openDock("review") })
	s.cx.OnKey("mod+j", func() { s.openDock("terminal") })
	// The command palette advertises these, so they must actually exist.
	s.cx.OnKey("mod+1", func() { s.openDock("files") })
	s.cx.OnKey("mod+2", func() { s.openDock("terminal") })
	s.cx.OnKey("mod+3", func() { s.openDock("review") })
	s.cx.OnKey("mod+o", s.openProjectPicker)

	s.cx.OnKey("mod+shift+[", func() { s.stepSession(-1) })
	s.cx.OnKey("mod+shift+]", func() { s.stepSession(1) })

	s.cx.OnKey("mod+f", func() {
		if s.rightOpen.GetUntracked() && s.dock.GetUntracked() == "terminal" {
			s.openTerminalSearch()
		}
	})

	s.cx.OnKeyWith("mod+s", core.KeyOptions{When: core.KeyAlways}, func() { s.saveFile() })

	s.cx.OnKey("mod+shift+l", s.toggleAppearance)

	// Esc's layers, after the overlays and dialogs that take it first:
	// leave a read-only transcript, stop the run, close the dock, then the
	// sidebar. Stopping beats closing a panel so a runaway answer can
	// always be stopped with one key.
	s.cx.OnBackPress(func() bool {
		switch {
		case s.readOnly.GetUntracked():
			s.readOnly.Set(false)
			return true
		case s.running.GetUntracked():
			s.stop()
			return true
		case s.rightOpen.GetUntracked():
			s.setDockOpen(false)
			return true
		case s.sidebarOpen.GetUntracked() && s.cx.Env().Window().Width < 900:
			s.setSidebarOpen(false)
			return true
		}
		return false
	})
}

// toggleSidebar flips the session column, leaving the transcript the width
// it frees; the layout remembers which way it was left.
func (s *appState) toggleSidebar() {
	s.setSidebarOpen(!s.sidebarOpen.GetUntracked())
}

// toggleAppearance flips light and dark, resolving "system" against the
// current scheme.
func (s *appState) toggleAppearance() {
	current := s.appearance.GetUntracked()
	next := "dark"
	if current == "dark" || current == "system" && s.cx.Env().Appearance() == core.AppearanceDark {
		next = "light"
	}
	s.setAppearance(next)
}

// stepSession moves the selection through the visible session list, clamped
// at both ends, and opens it the way a click would.
func (s *appState) stepSession(delta int) {
	rows := s.searchRows.GetUntracked()
	if len(rows) == 0 {
		return
	}
	at := -1
	selected := s.selected.GetUntracked()
	for i, r := range rows {
		if r.ID == selected {
			at = i
			break
		}
	}
	next := at + delta
	if at < 0 {
		next = 0
	}
	next = min(max(next, 0), len(rows)-1)
	s.openSession(rows[next].ID)
}
