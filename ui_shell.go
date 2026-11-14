package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"pigo/internal/sessions"
)

// The right panel's bounds: it opens at defaultDockWidth (420), the header's
// full-width button grows it up to dockMaxWidth, and dockBodyMinWidth is the
// narrowest the chat beside it may become, which caps that growth.
const (
	dockMinWidth     = 420
	dockMaxWidth     = 1200
	dockBodyMinWidth = 320
)

// render assembles the whole window. SidebarShell owns the title bar, both
// side panels and their resize handles (PLAN §7 component mapping).
func (s *appState) render() core.Node {
	return core.View{
		TestID: "window",
		Style:  core.Style{Width: core.Pct(100), Height: core.Pct(100), Background: s.colors.Background()},
		// A file dragged from Finder anywhere over the window becomes an
		// attachment; the mask says so before the drop.
		OnFileDragEnter: func() { s.dragActive.Set(true) },
		OnFileDragLeave: func() { s.dragActive.Set(false) },
		OnFileDrop: func(uris []string) {
			s.dragActive.Set(false)
			s.attachFiles(uris)
		},
		Children: []core.Node{
			ui.SidebarShell{
				TestID:  "shell",
				Sidebar: []core.Node{s.sidebar()},
				SidebarActions: []core.Node{
					ui.IconButton{Icon: lucide.Plus, Label: "新建会话", TestID: "new-session", OnPress: s.newSession},
				},
				SidebarFooter: []core.Node{s.footer()},
				Header:        []core.Node{s.headerBar()},
				Actions: []core.Node{
					ui.IconButton{Icon: lucide.Command, Label: "命令面板", TestID: "open-palette",
						OnPress: func() { s.paletteOpen.Set(true) }},
					// 刷新 re-reads what is on screen. It must not restart the
					// agent: killing the child for a list refresh is a drop the
					// user never asked for (and used to leave 未连接 behind).
					ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新视图", TestID: "refresh-view", OnPress: s.refreshAll},
					// The shell's own toggle is off (OnRightOpenChange is nil):
					// this is the one control of the panel, beside refresh.
					ui.IconButton{Icon: lucide.PanelRight, Label: "右侧面板", TestID: "toggle-dock",
						Selected: s.rightOpen.Get, OnPress: s.toggleRightPanel},
				},
				Content: []core.Node{s.chatView()},
				Open:    s.sidebarOpen.Get, OnOpenChange: s.setSidebarOpen,
				ToggleLabel: "切换侧栏", DefaultWidth: defaultSidebarWidth, MinWidth: 200, MaxWidth: 420,
				ResizeLabel:   "调整侧栏宽度",
				Width:         func() core.Points { return s.layout().sidebar.Get() },
				OnWidthChange: s.setSidebarWidth,

				Right:       []core.Node{s.dockPanel()},
				RightHeader: []core.Node{s.dockHeader()},
				RightOpen:   s.rightOpen.Get,
				// nil drops the shell's second panel toggle: the header's own
				// "右侧面板" button is the only one.
				OnRightOpenChange: nil,
				RightResizeLabel:  "调整右侧面板宽度",
				RightDefaultWidth: defaultDockWidth, RightMinWidth: 320, RightMaxWidth: 1200,
				RightWidth:         func() core.Points { return s.layout().dock.Get() },
				OnRightWidthChange: s.setDockWidth,
			},
			s.palette(),
			s.renameDialog(),
			s.deleteDialog(),
			s.subagentDialog(),
			s.branchDialog(),
			s.extensionDialog(),
			s.settingsDialog(),
			s.projectPicker(),
			s.providerDialog(),
			s.providerCatalogDialog(),
			s.modelDialog(),
			s.pluginDialog(),
			s.skillsDialog(),
			s.mcpDialog(),
			s.skillDialog(),
			s.agentDialog(),
			s.trustDialog(),
			s.worktreeDialog(),
			s.debugDialog(),
			s.deleteProjectDialog(),
			s.fullPreviewOverlay(),
			s.dropMask(),
			ui.Toasts{Toaster: s.toaster, Placement: ui.ToastBottomEnd, CloseLabel: "关闭", TestID: "toasts"},
		},
	}
}

// dockHeader is the right panel's tab bar: every open tab with its own close
// button, then the panel's actions — a "+" that opens a tab from a menu,
// refresh and full width. There is no second control of the panel: the shell's
// own toggle is off, so this bar and the header's one button are all of it.
func (s *appState) dockHeader() core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexGrow: 1}, Children: []core.Node{
		s.dockAddButton(),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Height: core.Pct(100), Position: core.Relative, Overflow: core.Hidden}, Children: []core.Node{
			core.Dynamic(func(cx *core.Scope) core.Node { return s.dockTabStrip(cx) }),
			ui.Scrollbar{Scroll: func() *core.Ref { return s.dockTabScroll }, Horizontal: true,
				Mode: func() base.ScrollbarMode { return base.ScrollbarAlways }, Label: "滚动页签", TestID: "dock-tabs-scrollbar",
				Style: core.Style{Height: core.Pt(6)}},
		}},
		ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新", TestID: "dock-refresh", OnPress: s.refreshDock},
		ui.IconButton{
			IconFunc: func() *core.SVG {
				if s.dockFullWidth() {
					return lucide.Minimize2()
				}
				return lucide.Maximize2()
			},
			LabelFunc: func() string {
				if s.dockFullWidth() {
					return "还原面板宽度"
				}
				return "全宽面板"
			},
			TestID: "dock-fullwidth", OnPress: s.toggleDockFullWidth,
		},
	}}
}

// dockKindLabel names a tool tab.
func dockKindLabel(kind string) string {
	if strings.HasPrefix(kind, "side:") {
		return "侧聊"
	}
	switch kind {
	case "review":
		return "审查"
	case "git":
		return "Git"
	case "terminal":
		return "终端"
	case "subagents":
		return "子代理"
	}
	return "文件"
}

// dockTabKeys is the strip's order: the open tool tabs in a fixed order, then
// the open files in the order they were opened.
func (s *appState) dockTabKeys() []string {
	open := s.dockOpenKinds.GetUntracked()
	out := make([]string, 0, len(open)+len(s.fileTabs.GetUntracked()))
	for _, kind := range []string{"files", "review", "git", "terminal", "subagents"} {
		if slices.Contains(open, kind) {
			out = append(out, kind)
		}
	}
	for _, tab := range s.sideTabs.Get() {
		out = append(out, sideTabKey(tab.ID))
	}
	for _, path := range s.fileTabs.Get() {
		out = append(out, "file:"+path)
	}
	return out
}

// dockTabStrip draws the open tabs: a press shows one, its close button drops
// it. A file tab reads as its file name, with a dot while it holds an unsaved
// draft.
func (s *appState) dockTabStrip(cx *core.Scope) core.Node {
	keys := s.dockTabKeys()
	active := s.dock.Get()
	dirty := s.fileDirty.Get()
	rows := make([]core.Node, 0, len(keys))
	for _, key := range keys {
		k := key
		label := dockKindLabel(k)
		icon := lucide.Folder
		if path, ok := strings.CutPrefix(k, "file:"); ok {
			label = baseName(path)
			if dirty[path] {
				label = "• " + label
			}
			icon = lucide.FileText
		} else if path, ok := strings.CutPrefix(k, "side:"); ok {
			icon = lucide.MessageSquare
			if tab := s.sideTab(path); tab != nil {
				label = tab.Title
				if tab.Subagent {
					icon = lucide.Bot
				}
			}
		} else {
			switch k {
			case "review":
				icon = lucide.Diff
			case "git":
				icon = lucide.GitBranch
			case "terminal":
				icon = lucide.Terminal
			case "subagents":
				icon = lucide.Bot
			}
		}
		on := k == active
		textStyle := core.Style{FontSize: 12, Color: s.colors.Muted()}
		tabStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 0,
			Height: core.Pt(26), CornerRadius: radiusInline, Padding: core.Edges{Left: 8, Right: 4}}
		if on {
			textStyle.Color = s.colors.Text()
			textStyle.FontWeight = core.FontWeightSemibold
			tabStyle.Background = ui.Alpha(s.colors.Text(), 0.08)
		}
		tab := core.View{
			Role: core.RoleButton, Label: label, TestID: "dock-tab-" + tabID(k),
			OnPress: func(core.PressEvent) {
				// A file goes through openFile, which also picks the view its
				// kind wants; a tool tab just shows itself.
				if path, ok := strings.CutPrefix(k, "file:"); ok {
					s.openFile(path)
					return
				}
				s.openDock(k)
			},
			Style: tabStyle,
			StyleFunc: func() core.Style {
				return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.06)}}
			},
			Children: []core.Node{
				ui.Icon(icon, 11, s.colors.Muted),
				core.View{Style: core.Style{MaxWidth: core.Pt(120), FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					core.Text{Content: label, MaxLines: 1, Style: textStyle},
				}},
			},
		}
		// The same actions from a right click, for a target with no X in
		// sight: a narrow panel scrolls the strip.
		// The X is the tab's sibling, not its child: a press target nested in
		// a press target is claimed by the outer one on a real window, and the
		// close button then only reopened the tab.
		rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter},
			Children: []core.Node{
				ui.ContextMenu{
					Trigger: tab, TestID: "dock-tab-menu-" + tabID(k),
					Children: []core.Node{
						ui.MenuItem{Text: "关闭页签", Icon: lucide.X, TestID: "dock-tab-menu-close-" + tabID(k),
							OnSelect: func() { s.closeDockTabKey(k) }},
						ui.MenuItem{Text: "关闭其他页签", Icon: lucide.X, TestID: "dock-tab-menu-others-" + tabID(k),
							OnSelect: func() { s.closeOtherDockTabs(k) }},
						ui.MenuItem{Text: "全部关闭", Icon: lucide.X, TestID: "dock-tab-menu-all-" + tabID(k),
							OnSelect: s.closeAllDockTabs},
					},
				},
				ui.IconButton{Icon: lucide.X, Label: "关闭 " + label, Size: 26, IconSize: 13,
					TestID: "dock-tab-close-" + tabID(k), OnPress: func() { s.closeDockTabKey(k) }},
			}})
	}
	if len(rows) == 0 {
		return core.Text{Content: "没有打开的页签", MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}}
	}
	return core.Scroll{Ref: s.dockTabScroll, Horizontal: true, HideIndicator: true, TestID: "dock-tabs-scroll",
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
		Children: []core.Node{core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2},
			Children: rows}}}
}

// tabID is a tab key's TestID suffix: a kind as is, a file by its name.
func tabID(key string) string {
	if path, ok := strings.CutPrefix(key, "file:"); ok {
		return "file-" + baseName(path)
	}
	if path, ok := strings.CutPrefix(key, "side:"); ok {
		return "side-" + baseName(path)
	}
	return key
}

// dockAddButton is the header's "+": a menu of the panel's four tools, so a
// closed tab comes back without hunting for its key.
func (s *appState) dockAddButton() core.Node {
	return ui.Menu{
		Trigger:   ui.Button{Variant: ui.ButtonGhost, Icon: lucide.Plus, Label: "新建页签", Side: 28, IconSize: 15},
		Placement: core.PlaceBottom, Align: core.AlignEnd,
		Label: "新建页签", TestID: "dock-add",
		Children: []core.Node{
			ui.MenuItem{Text: "文件", Icon: lucide.Folder, TestID: "dock-add-files", OnSelect: func() { s.openDock("files") }},
			ui.MenuItem{Text: "审查", Icon: lucide.Diff, TestID: "dock-add-review", OnSelect: func() { s.openDock("review") }},
			ui.MenuItem{Text: "Git 历史", Icon: lucide.GitBranch, TestID: "dock-add-git", OnSelect: func() { s.openDock("git") }},
			ui.MenuItem{Text: "终端", Icon: lucide.Terminal, TestID: "dock-add-terminal", OnSelect: func() { s.openDock("terminal") }},
			ui.MenuItem{Text: "子代理", Icon: lucide.Bot, TestID: "dock-add-subagents", OnSelect: func() { s.openDock("subagents") }},
			ui.MenuItem{Text: "新建侧聊", Icon: lucide.MessageSquarePlus, TestID: "dock-add-side", OnSelect: func() { s.startSideChat() }},
		},
	}
}

// dockFullWidth says whether the panel is wider than the width it opens at.
func (s *appState) dockFullWidth() bool {
	return s.layout().dock.Get() > defaultDockWidth
}

// toggleDockFullWidth grows the panel to the window's width minus the sidebar
// and the chat's least width, or returns it to the width it opens at.
func (s *appState) toggleDockFullWidth() {
	if s.dockFullWidth() {
		s.setDockWidth(dockMinWidth)
		return
	}
	width := s.cx.Env().Window().Width - s.layout().sidebar.GetUntracked() - dockBodyMinWidth
	width = max(dockMinWidth, min(dockMaxWidth, width))
	s.setDockWidth(width)
}

// closeDockTabKey drops one tab: a file's tab closes as a file (asking about
// an unsaved draft), a tool tab leaves the strip. Closing the active tab shows
// its neighbour, so the panel never lands on nothing while it is open.
func (s *appState) closeDockTabKey(key string) {
	if path, ok := strings.CutPrefix(key, "file:"); ok {
		s.closeFileTab(path)
		return
	}
	if path, ok := strings.CutPrefix(key, "side:"); ok {
		s.closeSideTab(path)
		return
	}
	open := make([]string, 0, 4)
	for _, kind := range s.dockOpenKinds.GetUntracked() {
		if kind != key {
			open = append(open, kind)
		}
	}
	s.dockOpenKinds.Set(open)
	if s.dock.GetUntracked() != key {
		return
	}
	keys := s.dockTabKeys()
	if len(keys) > 0 {
		s.dock.Set(keys[0])
		return
	}
	s.dock.Set("")
}

// closeOtherDockTabs keeps one tab and closes every other.
func (s *appState) closeOtherDockTabs(keep string) {
	for _, key := range s.dockTabKeys() {
		if key != keep {
			s.closeDockTabKey(key)
		}
	}
	if keep != "" {
		s.openDock(keep)
	}
}

// closeAllDockTabs empties the strip; the panel keeps its empty state, so a
// tab comes back from the "+" menu.
func (s *appState) closeAllDockTabs() {
	for _, key := range s.dockTabKeys() {
		s.closeDockTabKey(key)
	}
}

// refreshAll is the header's 刷新: the library, the open dock tab and the
// repository state are re-read; the agent process is left alone. 重连 on the
// status capsule / disconnect card is the only thing that restarts it.
func (s *appState) refreshAll() {
	go func() {
		_ = s.store.Refresh()
		s.cx.Post(func() {
			s.publishSessions()
			// status has no visible strip of its own; the toast is the only
			// place a person learns the refresh happened.
			s.notify(ui.ToastInfo, "已刷新", "会话列表 · 侧栏 · 仓库状态")
		})
	}()
	s.refreshDock()
	if root := s.projectRoot(); root != "" {
		s.loadGitState(root)
	}
}

// refreshDock reloads what the right panel shows: the file tree re-reads its
// directory, the history re-reads git log, and the terminal keeps the tabs it
// has (refreshing a shell makes no sense; + opens another). The empty state
// holds nothing to reload.
func (s *appState) refreshDock() {
	switch key := s.dock.Get(); {
	case key == "":
		return
	case key == "terminal":
		s.ensureTerminal()
	case key == "review":
		s.loadGit()
		s.loadGitState(s.projectRoot())
	case key == "subagents":
		s.loadSubagents()
	case key == "files":
		s.reloadTree()
	case strings.HasPrefix(key, "side:"):
		if tab := s.sideTab(sideTabPath(key)); tab != nil {
			s.rebuildSide(tab)
		}
	case strings.HasPrefix(key, "file:"):
		s.loadDiff(strings.TrimPrefix(key, "file:"))
	}
}

func (s *appState) dockPanel() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		switch key := s.dock.Get(); {
		case key == "":
			return s.dockEmptyState()
		case key == "terminal":
			return s.terminalView()
		case key == "review":
			return s.reviewView()
		case key == "git":
			return s.gitView()
		case key == "subagents":
			return s.subagentsPanel()
		case key == "files":
			return s.filesView()
		case strings.HasPrefix(key, "side:"):
			return s.sidePanel(sideTabPath(key))
		case strings.HasPrefix(key, "file:"):
			return s.fileTabView()
		}
		return s.filesView()
	})
}

// dockEmptyState is the panel with no tab open: PI NEXT's five cards in two
// columns, which open one of the four tools or a side chat without walking a
// menu. It holds no heading, as PI NEXT's does not.
func (s *appState) dockEmptyState() core.Node {
	open := func(tab string) func(core.PressEvent) {
		return func(core.PressEvent) { s.openDock(tab) }
	}
	return core.View{
		TestID: "dock-empty",
		Style:  core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Padding: core.Symmetric(24, 16)},
		Children: []core.Node{core.View{
			Style: core.Style{Display: core.DisplayGrid, GridTemplateColumns: "repeat(2, minmax(0, 1fr))", Gap: 8},
			Children: []core.Node{
				s.dockEmptyCard("dock-empty-files", "文件", lucide.Folder, open("files")),
				s.dockEmptyCard("dock-empty-review", "审查", lucide.Diff, open("review")),
				s.dockEmptyCard("dock-empty-git", "Git 历史", lucide.GitBranch, open("git")),
				s.dockEmptyCard("dock-empty-terminal", "终端", lucide.Terminal, open("terminal")),
				s.dockEmptyCard("dock-empty-subagents", "子代理", lucide.Bot, open("subagents")),
				s.dockEmptyCard("dock-empty-sidechat", "新建侧聊", lucide.MessageSquarePlus, func(core.PressEvent) { s.startSideChat() }),
			},
		}},
	}
}

// dockEmptyCard is one card of the empty state: an icon and a name, 44 points
// tall, in a field that lightens under the pointer.
func (s *appState) dockEmptyCard(testID, label string, icon func() *core.SVG, press func(core.PressEvent)) core.Node {
	return core.View{
		Role: core.RoleButton, Label: label, TestID: testID, OnPress: press,
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 10, Height: core.Pt(44), MinWidth: core.Pt(0),
			Padding: core.Symmetric(0, 10), CornerRadius: radiusCard, Background: s.colors.Field(),
			BorderWidth: 1, BorderColor: s.colors.FieldBorder()},
		StyleFunc: func() core.Style {
			return core.Style{
				Transition: core.Transition{Duration: 120 * time.Millisecond, Properties: core.TransitionBackground | core.TransitionBorder},
				Hover:      &core.Style{Background: s.colors.Hover(), BorderColor: s.colors.Border()},
				Pressed:    &core.Style{Background: s.colors.Pressed()},
			}
		},
		Children: []core.Node{
			ui.Icon(icon, 15, s.colors.Muted),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.Text{Content: label, MaxLines: 1, Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightMedium, Color: s.colors.Text()}},
			}},
		},
	}
}

func (s *appState) headerTitle() string {
	sel := s.selected.Get()
	if sel == "" {
		if root := s.projectRoot(); root != "" {
			return "新会话 · " + projectName(root)
		}
		return "新会话 · 未选择项目"
	}
	for _, r := range s.searchRows.Get() {
		if r.ID == sel {
			return r.Title
		}
	}
	return "会话"
}

// sidebarViews switches the session column between the projects tree and a
// flat, newest-first list (PLAN §7): the tree answers "where does this
// live", the recents answer "what did I touch last".
var sidebarViews = []ui.Option[string]{
	{Value: "projects", Label: "项目", Icon: lucide.Folder, TestID: "sidebar-view-projects"},
	{Value: "recent", Label: "最近", Icon: lucide.Clock, TestID: "sidebar-view-recent"},
}

func (s *appState) sidebar() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Height: core.Pct(100), Width: core.Pct(100), Padding: core.All(8), Gap: 8},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					core.Dynamic(func(cx *core.Scope) core.Node {
						if s.searchOpen.Get() {
							return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: []core.Node{
								core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
									ui.Input{Value: s.query.Get, OnChange: s.applySearch, Placeholder: "搜索会话", TestID: "session-search",
										AutoFocus: true, Style: core.Style{Width: core.Pct(100)}},
								}},
								ui.IconButton{Icon: lucide.X, Label: "关闭搜索", TestID: "session-search-close", Size: 24,
									OnPress: func() {
										s.query.Set("")
										s.applySearch("")
										s.searchOpen.Set(false)
									}},
							}}
						}
						return ui.Tabs[string]{
							Variant:  ui.TabsSegmented,
							Options:  sidebarViews,
							Value:    s.sidebarView.Get,
							OnChange: s.setSidebarView,
							Equal:    true,
							Size:     ui.SizeSmall,
							Label:    "会话视图",
							TestID:   "sidebar-view-tabs",
						}
					}),
				}},
				core.Show{When: func() bool { return !s.searchOpen.Get() }, Then: func(*core.Scope) core.Node {
					return ui.IconButton{Icon: lucide.Search, Label: "搜索会话", TestID: "session-search-toggle",
						Size: 24, OnPress: func() { s.searchOpen.Set(true) }}
				}},
				ui.IconButton{Icon: lucide.FolderOpen, Label: "打开项目", TestID: "nav-open-project", Size: 24,
					OnPress: s.openProjectPicker},
				core.Show{When: func() bool { return s.sidebarView.Get() == "projects" }, Then: func(*core.Scope) core.Node {
					return ui.IconButton{Icon: lucide.ChevronsUpDown, Label: "折叠或展开全部项目", TestID: "sessions-fold-all", Size: 24,
						OnPress: s.foldAllGroups}
				}},
				ui.IconButton{Icon: lucide.Archive, Label: "归档会话", TestID: "session-archived-toggle", Size: 24,
					Selected: s.archiveView.Get, OnPress: s.toggleArchiveView},
			}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)},
				OnLayout: func(b core.Bounds) { s.sideListH.Set(b.Size.Height) },
				Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
					if s.sessionsLoading.Get() && s.sidebarCount() == 0 {
						return ui.Skeleton{Lines: 5, Avatar: true, TestID: "sessions-loading",
							Style: core.Style{Padding: core.All(8)}}
					}
					return ui.List{
						Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Height: core.Pt(s.sideListH.Get())},
						Count: func() int { return s.sidebarCount() },
						Key:   s.sessionItemKey,
						Row: func(cx *core.Scope, r core.ListRow) core.Node {
							// Rows read the current list themselves so pin,
							// archive and selection repaint without a rebuild.
							return core.Dynamic(func(*core.Scope) core.Node { return s.sessionListRow(cx, r) })
						},
						Selection: s.sessionSel,
						OnActivate: func(i int) {
							if it, ok := s.sidebarItemAt(i); ok && !it.Header {
								s.openSession(it.Row.ID)
							}
						},
						RowLabel:      s.sessionItemLabel,
						EstimatedSize: 46,
						Gap:           1,
						Label:         "会话",
						Empty: core.Text{Content: "暂无会话", TestID: "session-empty",
							Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
						TestID: "session-list",
					}
				})}},
		},
	}
}

// sidebarCount is how many rows the current view shows: the grouped tree's
// items, or one per session in the flat recents list, which has no project
// headers (the store already sorted them by Modified, newest first).
func (s *appState) sidebarCount() int {
	if s.sidebarView.Get() == "recent" {
		return len(s.searchRows.Get())
	}
	return len(s.sessionItems.Get())
}

// sidebarItemAt reads one row of the current view. The recents list is the
// search rows in order, so both views answer by index in constant time and a
// virtualized row never holds a stale slice.
func (s *appState) sidebarItemAt(i int) (sessionItem, bool) {
	if s.sidebarView.Get() == "recent" {
		rows := s.searchRows.Get()
		if i < 0 || i >= len(rows) {
			return sessionItem{}, false
		}
		r := rows[i]
		return sessionItem{Group: r.Group, GroupLabel: r.Project, Ordinal: i, Row: r}, true
	}
	items := s.sessionItems.Get()
	if i < 0 || i >= len(items) {
		return sessionItem{}, false
	}
	return items[i], true
}

func (s *appState) sessionItemKey(i int) any {
	it, ok := s.sidebarItemAt(i)
	if !ok {
		return "row:" + strconv.Itoa(i)
	}
	if it.Header {
		return "group:" + it.Group
	}
	// Flags that change a row's menu must change its key, or the virtualized
	// list keeps the node built with the old actions.
	r := it.Row
	return fmt.Sprintf("%t/%t/%s", r.Pinned, r.Archived, r.ID)
}

func (s *appState) sessionItemLabel(i int) string {
	it, ok := s.sidebarItemAt(i)
	if !ok {
		return ""
	}
	if it.Header {
		return it.GroupLabel
	}
	return it.Row.Title
}

// sessionRowGroup names the row's hover group: the rename and delete buttons
// keep their width but only show, and only take presses, under the pointer.
const sessionRowGroup = "session-row"

// hoverOnly hides a row action until the pointer is on its row.
func hoverOnly(group string, child core.Node) core.Node {
	return core.View{Style: core.Style{
		Visibility: core.VisibilityHidden,
		GroupStyle: &core.GroupStyle{Name: group, Hover: &core.Style{Visibility: core.VisibilityVisible}},
	}, Children: []core.Node{child}}
}

// sessionListRow draws one virtualized row: a project header or a session
// with its title and directory, with a context menu for rename/pin/archive.
func (s *appState) sessionListRow(cx *core.Scope, row core.ListRow) core.Node {
	it, ok := s.sidebarItemAt(row.Index())
	if !ok {
		return core.View{}
	}
	i := row.Index()
	if it.Header {
		collapsed := s.collapsed.Get()[it.Group]
		chevron := lucide.ChevronDown
		if collapsed {
			chevron = lucide.ChevronRight
		}
		header := core.View{
			Role: core.RoleButton, Label: it.GroupLabel, TestID: "session-group-" + fmt.Sprint(i),
			OnPress: func(core.PressEvent) { s.toggleGroup(it.Group) },
			Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Width: core.Pct(100), MinWidth: core.Pt(0), Padding: core.Edges{Top: 10, Right: 6, Bottom: 2, Left: 4}, CornerRadius: radiusInline},
			StyleFunc: func() core.Style {
				return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
					Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.09)}}
			},
			Children: []core.Node{
				ui.Icon(chevron, 12, s.colors.Muted),
				ui.Icon(lucide.Folder, 13, s.colors.Muted),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					core.Text{Content: it.GroupLabel, MaxLines: 1,
						Style: core.Style{FontSize: 13.5, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
				}},
				core.Text{Content: fmt.Sprint(it.Count), MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
			},
		}
		// The header's own key lives in the "group:" prefix: session rows use
		// the session path, so the two menus never answer for each other.
		groupKey := "group:" + it.Group
		return ui.ContextMenu{
			Style: core.Style{Width: core.Pct(100), MinWidth: core.Pt(0)},
			Open:  func() bool { return s.ctxMenu.Get() == groupKey },
			OnOpenChange: func(open bool, _ base.Reason) {
				if open {
					s.ctxMenu.Set(groupKey)
				} else if s.ctxMenu.GetUntracked() == groupKey {
					s.ctxMenu.Set("")
				}
			},
			Trigger:  header,
			Children: s.projectMenuItems(it.Group, it.GroupLabel),
			TestID:   "session-group-menu",
		}
	}
	r := it.Row
	selected := s.selected.Get() == r.ID
	titleStyle := core.Style{FontSize: fontLabel, Color: s.colors.Text()}
	if selected {
		titleStyle.FontWeight = core.FontWeightSemibold
		titleStyle.Color = s.colors.Accent()
	}
	title := r.Title
	if r.Pinned {
		title = "★ " + title
	}
	age := r.Age
	if age == "" {
		age = " "
	}
	// The running dot marks the session whose turn is streaming, and only
	// while it is the open one: a background session's state is not known
	// here, so it stays unmarked rather than guessing.
	running := s.running.Get() && selected
	unread := r.Unread && !selected
	subtitle := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5}, Children: []core.Node{
		core.Show{When: func() bool { return unread }, Then: func(*core.Scope) core.Node {
			return core.View{TestID: "session-unread-" + strconv.Itoa(it.Ordinal),
				Style: core.Style{Width: core.Pt(6), Height: core.Pt(6), CornerRadius: 3, Background: s.colors.Accent()}}
		}},
		core.Text{Content: age, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
		core.Show{When: func() bool { return running }, Then: func(*core.Scope) core.Node {
			return core.View{TestID: "session-running-" + strconv.Itoa(it.Ordinal),
				Style: core.Style{Width: core.Pt(5), Height: core.Pt(5), CornerRadius: 2.5, Background: s.colors.Up()}}
		}},
	}}
	content := core.View{
		Role: core.RoleButton, Label: title,
		// A bare core.Text tooltip mounts in an unstyled overlay: three long
		// lines of preview text grew into a transparent box across the whole
		// window, with the transcript showing through it. base.Tooltip keeps
		// the platform's "never takes the pointer" behaviour but owns its
		// popup, so it stays a small opaque chip beside the row.
		TestID:  "session-" + strconv.Itoa(it.Ordinal),
		OnPress: func(core.PressEvent) { s.openSession(r.ID) },
		// Group admits the hover actions at the row's end; the background
		// follows the same state.
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 7, Width: core.Pct(100), MinWidth: core.Pt(0), Padding: core.Edges{Top: 5, Right: 4, Bottom: 5, Left: 6}, CornerRadius: radiusControl, Group: sessionRowGroup},
		StyleFunc: func() core.Style {
			return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
				Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.09)}}
		},
		Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				// One line for every row, the open one too: a session that
				// wraps to two lines moved its neighbours and needed a
				// second press to land on.
				core.Text{Content: title, MaxLines: 1, Style: titleStyle},
				subtitle,
			}},
			hoverOnly(sessionRowGroup, ui.IconButton{Icon: lucide.Pencil, Label: "重命名会话", Size: 20, IconSize: 12,
				TestID: "session-rename-" + strconv.Itoa(it.Ordinal), OnPress: func() { s.beginRename(r.ID, r.Title) }}),
			hoverOnly(sessionRowGroup, ui.IconButton{Icon: lucide.Trash, Label: "删除会话", Size: 20, IconSize: 12,
				TestID: "session-delete-" + strconv.Itoa(it.Ordinal), OnPress: func() { s.beginDelete(r) }}),
		},
	}
	pinLabel := "固定"
	if r.Pinned {
		pinLabel = "取消固定"
	}
	unreadItem := ui.MenuItem{Text: "标为未读", Icon: lucide.Circle, TestID: "session-unread",
		OnSelect: func() { s.setSessionUnread(r.ID, true) }}
	if r.Unread {
		unreadItem = ui.MenuItem{Text: "标为已读", Icon: lucide.Check, TestID: "session-read",
			OnSelect: func() { s.setSessionUnread(r.ID, false) }}
	}
	archiveItem := ui.MenuItem{Text: "归档", Icon: lucide.Archive, TestID: "session-archive",
		OnSelect: func() { s.archiveSession(r.ID) }}
	if r.Archived || s.archiveView.Get() {
		archiveItem = ui.MenuItem{Text: "取消归档", Icon: lucide.ArchiveRestore, TestID: "session-unarchive",
			OnSelect: func() { s.unarchiveSession(r.ID) }}
	}
	return ui.ContextMenu{
		Style: core.Style{Width: core.Pct(100), MinWidth: core.Pt(0)},
		Open:  func() bool { return s.ctxMenu.Get() == r.ID },
		OnOpenChange: func(open bool, _ base.Reason) {
			if open {
				s.ctxMenu.Set(r.ID)
			} else if s.ctxMenu.GetUntracked() == r.ID {
				s.ctxMenu.Set("")
			}
		},
		Trigger: content,
		Children: []core.Node{
			ui.MenuItem{Text: "打开", TestID: "session-open", OnSelect: func() { s.openSession(r.ID) }},
			ui.MenuSeparator{},
			ui.MenuItem{Text: pinLabel, Icon: lucide.Pin, TestID: "session-pin",
				OnSelect: func() { s.togglePin(r) }},
			ui.MenuItem{Text: "重命名…", Icon: lucide.Pencil, TestID: "session-rename",
				OnSelect: func() { s.beginRename(r.ID, r.Title) }},
			ui.MenuItem{Text: "分叉会话", Icon: lucide.GitBranch, TestID: "session-fork",
				OnSelect: func() { s.forkSessionFromRow(r.ID) }},
			unreadItem,
			archiveItem,
			ui.MenuSeparator{},
			ui.MenuItem{Text: "删除…", Icon: lucide.Trash, Variant: ui.MenuItemDanger, TestID: "session-delete",
				OnSelect: func() { s.beginDelete(r) }},
		},
		TestID: "session-menu",
	}
}

// sessionTooltipPopupStyle is the tooltip card's box: narrow, clipped and
// opaque (the theme colours arrive through PopupStyleFunc). Without these
// bounds the tooltip grew with its text and covered the transcript.
func sessionTooltipPopupStyle() core.Style {
	return core.Style{
		Direction: core.Column, Gap: 2, MaxWidth: core.Pt(240),
		Padding: core.Symmetric(6, 8), CornerRadius: 8, BorderWidth: 1,
		Overflow: core.Hidden,
	}
}

// sessionPreviewText is the row's hover text: only the whole title the row
// had to cut. The old tooltip also carried the project, the last assistant
// message and the tool count on separate lines; a plain core.Text tooltip
// has no width or background of its own, so that block rendered as a huge
// transparent overlay across the chat pane. The transcript is already on
// screen — hovering a row is not the place to repeat it.
func sessionPreviewText(r sessionRow) string {
	return clipRunes(strings.TrimSpace(r.Title), 80)
}

// sessionTooltipMeta is the tooltip's single small line: project and age,
// clipped so a long project name cannot widen the card.
func sessionTooltipMeta(r sessionRow) string {
	meta := make([]string, 0, 2)
	if r.Project != "" {
		meta = append(meta, clipRunes(r.Project, 24))
	}
	if r.Age != "" {
		meta = append(meta, r.Age)
	}
	return clipRunes(strings.Join(meta, " · "), 48)
}

func clipRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// footer holds the panel switches and the settings entry. Settings is a
// dialog, not a page (PLAN §7), so it has no Selected state.
func (s *appState) footer() core.Node {
	return core.View{
		Style: core.Style{Width: core.Pct(100)},
		Children: []core.Node{
			ui.SidebarButton{Text: "设置", Icon: lucide.Settings, Shortcut: "mod+,", TestID: "nav-settings",
				OnPress: func() { s.settingsOpen.Set(true) }},
		},
	}
}

// toggleGroup folds a project's sessions under its header.
func (s *appState) toggleGroup(group string) {
	s.collapsed.Update(func(m map[string]bool) map[string]bool {
		out := make(map[string]bool, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		out[group] = !out[group]
		return out
	})
	s.applySearch(s.query.GetUntracked())
}

// foldAllGroups collapses every project; when all are already folded it
// opens them instead, so one button does both.
func (s *appState) foldAllGroups() {
	rows := s.rows.GetUntracked()
	anyOpen := false
	for _, r := range rows {
		if !s.collapsed.GetUntracked()[r.Group] {
			anyOpen = true
			break
		}
	}
	next := make(map[string]bool, len(rows))
	for _, r := range rows {
		next[r.Group] = anyOpen
	}
	s.collapsed.Set(next)
	s.applySearch(s.query.GetUntracked())
}

// openOnlyGroup folds every project but one, so a project's sessions are
// what the sidebar shows without walking the other headers first.
func (s *appState) openOnlyGroup(group string) {
	rows := s.rows.GetUntracked()
	next := make(map[string]bool, len(rows))
	for _, r := range rows {
		next[r.Group] = r.Group != group
	}
	// The chosen group must stay open even when it has no rows of its own
	// (the search may have rebuilt the list under it).
	next[group] = false
	s.collapsed.Set(next)
	s.applySearch(s.query.GetUntracked())
}

// expandGroup opens the project a session belongs to, so selecting a
// session from search or a new session never hides its own row.
func (s *appState) expandGroup(group string) {
	if group == "" || !s.collapsed.GetUntracked()[group] {
		return
	}
	s.collapsed.Update(func(m map[string]bool) map[string]bool {
		out := make(map[string]bool, len(m))
		for k, v := range m {
			out[k] = v
		}
		out[group] = false
		return out
	})
	s.applySearch(s.query.GetUntracked())
}

func (s *appState) openSession(id string) {
	if id == "" || id == "__empty__" {
		return
	}
	// A side chat is not a conversation of its own in the sidebar sense: it
	// belongs in the right panel, beside the parent it was forked from, so a
	// click on its row reopens the tab instead of replacing the transcript.
	if info, ok := s.store.Info(id); ok && info.Side {
		s.openSideTab(id, sessions.SideParent(id))
		return
	}
	// A session opened from the sidebar is editable again; the subagents
	// panel turns the flag back on after the session mounts.
	s.readOnly.Set(false)
	// A session may record another project than the window's; its own .pi
	// config decides trust, not the file tree's current root.
	if cwd, _ := s.sessionCwd(id); !s.ensureTrustFor(cwd, id) {
		return
	}
	prev := s.selected.GetUntracked()
	for _, r := range s.rows.GetUntracked() {
		if r.ID == id {
			s.expandGroup(r.Group)
			break
		}
	}
	if id != prev {
		s.ctxUsed.Set(0)
		s.ctxWindow.Set(0)
		// Keep each session's half-typed message with that session, and
		// clear the run-scoped pieces a previous session left behind.
		if prev != "" {
			s.drafts[prev] = s.draft.GetUntracked()
			s.saveAttachmentsFor(prev)
		}
		s.draft.Set(s.drafts[id])
		s.restoreAttachmentsFor(id)
		s.turnLimit.Set(20)
		// A new transcript measures its own rows; the previous session's
		// heights would be dead weight (and were never allocated at all
		// before, which panicked on the first layout).
		s.turnHeights = make(map[string]core.Points)
		s.conversation.JumpToLatest()
		// The review tab is scoped to the conversation that wrote the files,
		// so a new transcript starts with nothing selected instead of keeping
		// the previous session's path (and its patch) on screen.
		if s.reviewScope.GetUntracked() == "session" {
			s.reviewSel.Clear()
			s.reviewDiff.Set("")
		}
	}
	s.selected.Set(id)
	s.sessionSel.Set(id)
	if os.Getenv("PIGO_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "[debug] openSession %s cwd=%s\n", id, s.currentCwd())
	}
	s.connect(id)
}

// homeDir is the picker's first place, empty when it cannot be found.
func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// projectPath is the current project as the picker shows it.
func (s *appState) projectPath() string {
	return filepath.Clean(s.currentCwd())
}

// dropMask covers the window while files are dragged over it: dropping any
// picture adds it to the composer, any other file becomes an @-mention.
func (s *appState) dropMask() core.Node {
	return core.Show{When: s.dragActive.Get, Then: func(*core.Scope) core.Node {
		return core.View{TestID: "drop-mask",
			Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0),
				ZIndex: 40, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Background: ui.Alpha(s.colors.Background(), 0.82)},
			Children: []core.Node{
				core.View{Style: core.Style{Padding: core.Symmetric(26, 34), Gap: 10, CornerRadius: radiusCard, BorderWidth: 2,
					AlignItems: core.AlignCenter, BorderColor: s.colors.Accent()}, Children: []core.Node{
					ui.Icon(lucide.Paperclip, 24, s.colors.Accent),
					core.Text{Content: "松手添加为附件", Style: core.Style{FontSize: fontTitle, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
					core.Text{Content: "图片进入输入框，其他文件插入 @ 引用", Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
				}},
			}}
	}}
}
