package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base/terminal"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"piwebnative/internal/term"
)

// ensureTerminal opens a shell only when the dock first shows the terminal,
// so a headless run never loads the terminal emulator library.
func (s *appState) ensureTerminal() {
	s.termMu.Lock()
	hasTab := len(s.termTabs) > 0
	s.termMu.Unlock()
	if hasTab {
		return
	}
	s.openTerminal(s.currentCwd())
}

// openTerminal starts one shell and makes it the active tab. The shell's
// title is reported asynchronously by the emulator; only then does the tab
// learn it, so the strip follows the running program.
//
// Starting a shell waits for its prompt (up to half a second, twice when the
// login shell falls back), so the wait runs off the UI thread: doing it there
// froze the window on the first press of the terminal tab.
func (s *appState) openTerminal(cwd string) {
	s.termMu.Lock()
	if s.termStarting {
		s.termMu.Unlock()
		return
	}
	s.termStarting = true
	s.termMu.Unlock()
	go func() {
		var id string
		tab, err := s.terms.Open(cwd, term.DefaultShell(),
			func(title string) {
				s.cx.Post(func() {
					s.termMu.Lock()
					if t := s.termTabs[id]; t != nil && title != "" {
						t.Title = title
					}
					s.termMu.Unlock()
					s.termRev.Set(s.termRev.GetUntracked() + 1)
					if title != "" {
						s.status.Set("终端：" + title)
					}
				})
			},
			func(code int) {
				s.cx.Post(func() {
					s.status.Set("终端已退出，可点终端栏的重启")
					s.termRev.Set(s.termRev.GetUntracked() + 1)
				})
			},
		)
		s.termMu.Lock()
		s.termStarting = false
		if err == nil && tab != nil {
			id = tab.ID
			s.termTabs[tab.ID] = tab
			s.termOrder = append(s.termOrder, tab.ID)
		}
		ids := s.termIDsLocked()
		s.termMu.Unlock()
		s.cx.Post(func() {
			if err != nil || tab == nil {
				s.status.Set("终端不可用：" + err.Error())
				return
			}
			s.termTabIDs.Set(ids)
			s.activeTerm.Set(tab.ID)
			// The shell is whole now: take the keyboard once its view mounted.
			s.cx.AfterFunc(80*time.Millisecond, tab.Focus)
		})
	}()
}

// termIDsLocked lists the tabs in the order they were opened. Iterating the
// map made the strip reshuffle on every repaint.
func (s *appState) termIDsLocked() []string {
	ids := make([]string, 0, len(s.termOrder))
	for _, id := range s.termOrder {
		if s.termTabs[id] != nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// closeTerminalTab ends the process and drops its tab, keeping one alive at
// the end so the panel is never stranded on a dead id.
func (s *appState) closeTerminalTab(id string) {
	s.termMu.Lock()
	tab := s.termTabs[id]
	delete(s.termTabs, id)
	for i, open := range s.termOrder {
		if open == id {
			s.termOrder = append(s.termOrder[:i], s.termOrder[i+1:]...)
			break
		}
	}
	ids := s.termIDsLocked()
	s.termMu.Unlock()
	if tab != nil {
		_ = tab.Close()
	}
	s.termTabIDs.Set(ids)
	s.closeTerminalSearch()
	if len(ids) == 0 {
		s.activeTerm.Set("")
		return
	}
	if s.activeTerm.Get() == id {
		s.activeTerm.Set(ids[0])
	}
}

func (s *appState) activeTab() *term.Tab {
	id := s.activeTerm.Get()
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return s.termTabs[id]
}

func (s *appState) newTerminalTab() {
	s.openTerminal(s.currentCwd())
}

// terminalView is the dock's terminal tab: the strip of tabs and the live
// emulator of the selected one.
func (s *appState) terminalView() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Edges{Top: 6, Right: 8, Bottom: 6, Left: 8}}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					ui.ContextMenu{Trigger: s.terminalTabs(), TestID: "term-tabs-menu", Children: []core.Node{
						ui.MenuItem{Text: "重命名当前标签…", TestID: "term-rename-menu", OnSelect: s.openTermRename},
						ui.MenuItem{Text: "在终端中搜索…", Icon: lucide.Search, TestID: "term-search-menu", OnSelect: s.openTerminalSearch},
						ui.MenuItem{Text: "关闭当前标签", Icon: lucide.X, TestID: "term-close-menu",
							OnSelect: func() { s.closeTerminalTab(s.activeTerm.GetUntracked()) }},
					}},
				}},
				s.terminalStateLabel(),
				ui.IconButton{Icon: lucide.Plus, Label: "新标签", TestID: "term-new", OnPress: s.newTerminalTab},
				ui.IconButton{Icon: lucide.Eraser, Label: "清屏", TestID: "term-clear", OnPress: func() {
					if tab := s.activeTab(); tab != nil {
						tab.Terminal.ClearScreen()
					}
				}},
			}},
			s.terminalSearchBar(),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					tab := s.activeTab()
					if tab == nil {
						return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
							Children: []core.Node{core.Text{Content: "终端未启动，点上方 + 打开 $SHELL",
								TestID: "term-empty", Style: core.Style{FontSize: 12.5, Color: s.colors.Muted()}}}}
					}
					return tab.ViewWith("终端 "+tab.ID, s.termFont.Get(), s.termTheme.Get())
				}),
			}},
			s.terminalRenameDialog(),
		},
	}
}

// terminalTabs is ui.Tabs over the live shells; each label follows the title
// the shell reports.
func (s *appState) terminalTabs() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		_ = s.termRev.Get()
		ids := s.termTabIDs.Get()
		if len(ids) == 0 {
			return core.Text{Content: "无标签", TestID: "term-tabs", MaxLines: 1,
				Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}}
		}
		options := make([]ui.Option[string], 0, len(ids))
		for i, id := range ids {
			options = append(options, ui.Option[string]{
				Value: id, Label: s.terminalTabLabel(id, i), TestID: "term-tab-" + id,
			})
		}
		return ui.Tabs[string]{
			Options: options,
			Value:   s.activeTerm.Get,
			OnChange: func(id string) {
				s.activeTerm.Set(id)
				s.closeTerminalSearch()
				if tab := s.activeTab(); tab != nil {
					tab.Focus()
				}
			},
			Size:   ui.SizeSmall,
			Label:  "终端标签",
			TestID: "term-tabs",
		}
	})
}

func (s *appState) terminalTabLabel(id string, i int) string {
	s.termMu.Lock()
	title := ""
	if t := s.termTabs[id]; t != nil {
		title = t.Title
	}
	s.termMu.Unlock()
	if title != "" {
		return title
	}
	return "zsh " + strconv.Itoa(i+1)
}

// openTerminalSearch shows the find bar over the active shell. The bar was
// the last piece of base/terminal the window never reached.
func (s *appState) openTerminalSearch() {
	if s.activeTab() == nil {
		s.ensureTerminal()
	}
	s.termSearchOpen.Set(true)
	s.startTermSearchPoll()
}

// closeTerminalSearch ends the emulator's search and hides the bar.
func (s *appState) closeTerminalSearch() {
	if tab := s.activeTab(); tab != nil {
		tab.Terminal.EndSearch()
	}
	if s.termSearch != nil {
		s.termSearch.Set("")
	}
	s.termSearchOpen.Set(false)
}

// setTermSearch starts a fresh search on every keystroke: the emulator
// keeps the selected match when the text is the same, so typing narrows
// rather than restarts.
func (s *appState) setTermSearch(text string) {
	s.termSearch.Set(text)
	if tab := s.activeTab(); tab != nil {
		tab.Terminal.Search(text)
	}
	s.termSearchRev.Set(s.termSearchRev.GetUntracked() + 1)
}

// stepTermSearch moves to the next or previous match.
func (s *appState) stepTermSearch(back bool) {
	if tab := s.activeTab(); tab != nil {
		if back {
			tab.Terminal.SearchPrevious()
		} else {
			tab.Terminal.SearchNext()
		}
	}
	s.termSearchRev.Set(s.termSearchRev.GetUntracked() + 1)
}

// startTermSearchPoll repaints the count while the emulator walks the
// scrollback in the frames after each keystroke.
func (s *appState) startTermSearchPoll() {
	if s.termSearching {
		return
	}
	s.termSearching = true
	var tick func()
	tick = func() {
		if !s.termSearchOpen.GetUntracked() {
			s.termSearching = false
			return
		}
		s.termSearchRev.Set(s.termSearchRev.GetUntracked() + 1)
		s.cx.AfterFunc(150*time.Millisecond, tick)
	}
	s.cx.AfterFunc(150*time.Millisecond, tick)
}

// terminalSearchBar is the find bar: the query, its match count and the
// buttons that step through matches.
func (s *appState) terminalSearchBar() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		if !s.termSearchOpen.Get() {
			return nil
		}
		_ = s.termSearchRev.Get()
		status := terminal.SearchStatus{}
		if tab := s.activeTab(); tab != nil {
			status = tab.Terminal.SearchStatus()
		}
		count := "0 / 0"
		if status.Total > 0 {
			count = fmt.Sprintf("%d / %d", status.Selected, status.Total)
		} else if status.Searching {
			count = "搜索中…"
		}
		return core.View{
			TestID: "term-search-bar",
			Style:  core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Edges{Top: 4, Right: 8, Bottom: 4, Left: 8}},
			Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					ui.Input{
						Value: s.termSearch.Get, OnChange: s.setTermSearch,
						Placeholder: "在终端中搜索", TestID: "term-search", AutoFocus: true,
						OnSubmit: func(string) { s.stepTermSearch(false) },
						OnKeyPress: func(key string) {
							if key == "Escape" {
								s.closeTerminalSearch()
							}
						},
						Style: core.Style{Height: core.Pt(24), FontSize: 12},
					},
				}},
				core.Text{Content: count, TestID: "term-search-status", MaxLines: 1,
					Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
				ui.IconButton{Icon: lucide.ChevronUp, Label: "上一个匹配", TestID: "term-search-prev", Size: 24,
					OnPress: func() { s.stepTermSearch(true) }},
				ui.IconButton{Icon: lucide.ChevronDown, Label: "下一个匹配", TestID: "term-search-next", Size: 24,
					OnPress: func() { s.stepTermSearch(false) }},
				ui.IconButton{Icon: lucide.X, Label: "关闭搜索", TestID: "term-search-close", Size: 24,
					OnPress: s.closeTerminalSearch},
			},
		}
	})
}

// terminalStateLabel names the active shell's directory and state, and
// restarts it when it ended. It is the toolbar's one line of context: a
// terminal tab used to say neither where it ran nor why it stopped.
func (s *appState) terminalStateLabel() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		tab := s.activeTab()
		if tab == nil {
			return nil
		}
		_ = s.termRev.Get()
		status := tab.Status()
		label := shortenHome(tab.Cwd)
		if tab.Fallback {
			label += " · 非登录 shell"
		}
		switch status.State {
		case term.StateExited:
			label += fmt.Sprintf(" · 已退出 %d", status.Code)
		case term.StateFailed:
			label += " · 启动失败"
		}
		children := []core.Node{
			core.Text{Content: label, TestID: "term-state", MaxLines: 1,
				Style: core.Style{FontSize: 10.5, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
		}
		// An exited shell restarts when its state is pressed: the toolbar
		// used to carry a button for it, and the state line is where the exit
		// shows anyway.
		if status.State != term.StateRunning {
			return core.View{Role: core.RoleButton, Label: "重启终端", TestID: "term-restart", OnPress: func(core.PressEvent) {
				if err := tab.Restart(); err != nil {
					s.notify(ui.ToastWarning, "终端重启失败", err.Error())
				}
				s.termRev.Set(s.termRev.GetUntracked() + 1)
			}, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 1, MinWidth: core.Pt(0)},
				StyleFunc: func() core.Style {
					return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.06)}}
				}, Children: children}
		}
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 1,
			MinWidth: core.Pt(0)}, Children: children}
	})
}

// shortenHome prints a path with ~ for the user's home.
func shortenHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+string(os.PathSeparator)) {
			return "~" + strings.TrimPrefix(path, home)
		}
	}
	return path
}
