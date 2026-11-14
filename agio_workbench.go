package main

// The window's design components. The sidebar, transcript, dock and dialogs
// are native's; chatview supplies the shell, the conversation container, the
// composer's model/effort/context controls and the settings dialog.

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"

	"pigo/agio/chatview"
	"pigo/agio/transcript"
)

// workbench is the window root: chatview's shell with native's content.
func (s *appState) workbench(cx *core.Scope) core.Node {
	chatview.AppVersion = appVersion()
	app := chatview.App{
		Model:   s.chatModel,
		Dir:     s.currentCwd(),
		Name:    "", // native installs its own menu bar
		Updates: false,
		Sidebar: func(cx *core.Scope, _ *core.Signal[string]) core.Node { return s.sidebar() },
		NewSession: func() {
			s.selected.Set("")
			s.newSession()
		},
		KeepSelection: true,
		Chat:          s.chatNode,
		NewTaskChat: func(cx *core.Scope, id string, onSend func()) core.Node {
			return chatview.Conversation{Model: s.chatModel, ID: id, TestID: "conversation", State: s.conversation,
				Turn: s.componentTurn, Composer: s.componentComposer, Overlay: s.componentOverlay,
				TurnLimit: s.turnLimit.Get, OnSend: onSend}
		},
		Title:    func(cx *core.Scope, _ string) core.Node { return s.titleChip() },
		Selected: s.selected,
		Projects: s.agioProjects,
		Header: func(dir string) []core.Node {
			return []core.Node{
				core.Dynamic(func(cx *core.Scope) core.Node { return s.branchChip() }),
				core.View{Style: core.Style{FlexGrow: 1}},
				s.statusCapsule(),
				core.Dynamic(func(cx *core.Scope) core.Node {
					return ui.IconButton{Icon: lucide.Command, Label: "命令面板", TestID: "open-palette",
						OnPress: func() { s.paletteOpen.Set(true) }}
				}),
				core.Dynamic(func(cx *core.Scope) core.Node {
					return ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新视图", TestID: "refresh-view",
						OnPress: s.refreshAll}
				}),
				core.Dynamic(func(cx *core.Scope) core.Node {
					return ui.IconButton{Icon: lucide.PanelRight, Label: "右侧面板", TestID: "toggle-dock",
						Selected: s.rightOpen.Get, OnPress: s.toggleRightPanel}
				}),
			}
		},
		Right:                []core.Node{s.dockPanel()},
		RightHeader:          []core.Node{s.dockGrip(), s.dockHeader()},
		RightOpen:            s.rightOpen,
		RightWidth:           s.layout().dock,
		RightDefaultWidth:    defaultDockWidth,
		RightOnWidthChange:   s.setDockWidth,
		SidebarOpen:          s.sidebarOpen,
		SidebarWidth:         s.layout().sidebar,
		SidebarOnWidthChange: s.setSidebarWidth,
		SidebarOnOpenChange:  s.setSidebarOpen,
		Settings: func(own []ui.SettingsCategory) []ui.SettingsCategory {
			return append(s.settingsCategories(), own...)
		},
		SettingsKey:      func() string { return s.settingsKeyForComponents() },
		SettingsCategory: s.category,
		SettingsOpen:     s.settingsOpen,
		OnAppearanceChange: func(a core.Appearance) {
			switch a {
			case core.AppearanceLight:
				s.setAppearance("light")
			case core.AppearanceDark:
				s.setAppearance("dark")
			default:
				s.setAppearance("system")
			}
		},
		ChromeStyle: s.chromeStyle,
		Backdrop:    s.grainBackdrop(),
		TestID:      "shell",
	}
	return core.View{TestID: "window", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100), Background: s.colors.Background()},
		OnFileDragEnter: func() { s.dragActive.Set(true) },
		OnFileDragLeave: func() { s.dragActive.Set(false) },
		OnFileDrop:      func(uris []string) { s.dragActive.Set(false); s.attachFiles(uris) },
		Children:        append([]core.Node{app}, s.dialogs()...)}
}

// chatNode is one conversation: chatview's container with native's transcript
// and composer.
func (s *appState) chatNode(cx *core.Scope, id string) core.Node {
	conv := chatview.Conversation{Model: s.chatModel, ID: id, TestID: "conversation", State: s.conversation,
		Turn:      s.componentTurn,
		Composer:  s.componentComposer,
		Overlay:   s.componentOverlay,
		TurnLimit: s.turnLimit.Get,
	}
	chat := core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		StyleFunc: func() core.Style {
			// The turn rail sits in this gutter on wide windows.
			if s.cx.Env().Window().Width >= 1100 && len(s.turns.Get()) >= 2 {
				return core.Style{Padding: core.Edges{Left: 42}}
			}
			return core.Style{}
		}, Children: []core.Node{conv}}
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Position: core.Relative},
		Children: []core.Node{chat, s.minimap(), s.bindFindKey(), s.composerHistoryKeys()}}
}

// componentTurn renders a turn with native's cards; the projection only
// carries the identity the container needs.
func (s *appState) componentTurn(cx *core.Scope, t *transcript.Turn, last bool) core.Node {
	return s.turnNode(cx, func() turnRow {
		for _, r := range s.turns.GetUntracked() {
			if r.ID == t.ID {
				return r
			}
		}
		return turnRow{ID: t.ID, Prompt: t.Prompt, Running: t.Status == transcript.Running}
	})
}

// componentComposer keeps native's composer behaviour and adds agio's model,
// effort and context controls from base.
func (s *appState) componentComposer(cx *core.Scope, id string, base chat.Composer) chat.Composer {
	base.Label = "消息"
	base.SendLabel = "发送"
	base.StopLabel = "停止"
	base.PlaceholderFunc = func() string { return "输入消息，@ 引用文件，/ 唤起命令，Enter 发送" }
	base.Draft = s.draft.Get
	base.OnDraftChange = s.onDraftChange
	base.OnSend = func() { s.send(s.draft.GetUntracked()) }
	base.OnStop = s.stop
	base.OnKeyPress = s.composerKey
	base.OnFocus = s.composerFocused.Set
	base.SubmitOnEnter = s.sendOnEnter.Get
	base.SendDisabled = func() bool { return s.readOnly.Get() }
	base.OnDropFiles = s.attachFiles
	base.OnPaste = s.pasteImages
	base.Attachments = s.attachmentsView
	base.HasAttachments = func() bool { return len(s.attachments.Get()) > 0 }
	model := base.Accessory
	base.Accessory = func(cx *core.Scope) core.Node {
		return core.Group{
			chat.IconAction{Label: "附件", Icon: lucide.Paperclip, TestID: "attach-files", OnPress: s.pickAttachments},
			chat.IconAction{Label: "新建侧聊", Icon: lucide.MessageSquarePlus, TestID: "start-side-chat", OnPress: s.startSideChat},
			s.dictationAction(),
			model(cx),
		}
	}
	// Native's context meter is a hover card with an entrance animation;
	// chatview's is a click popover, which reads as a delayed, static window.
	base.Trailing = func(cx *core.Scope) core.Node { return s.contextMeter() }
	footer := base.Footer
	base.Footer = func(cx *core.Scope) core.Node {
		return core.Group{
			core.Show{When: func() bool { return s.banner.Get() != "" }, Then: func(*core.Scope) core.Node {
				return core.Text{ContentFunc: s.banner.Get, TestID: "banner", MaxLines: 1,
					Style: core.Style{FontSize: fontLabel, Color: s.colors.Warn()}}
			}},
			core.Show{When: s.running.Get, Then: func(*core.Scope) core.Node {
				return chat.Shimmer{Text: func() string { return "生成中" }, Size: 12.5,
					Color: s.colors.Muted, Highlight: s.colors.Text}
			}},
			core.Show{When: func() bool { return strings.TrimSpace(s.widgetText.Get()) != "" }, Then: func(*core.Scope) core.Node {
				return core.Text{ContentFunc: s.widgetText.Get, TestID: "extension-widgets", MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}}
			}},
			core.Show{When: s.queuesActive, Then: func(*core.Scope) core.Node {
				return core.Group{
					ui.Button{Text: "查看队列", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "open-queue",
						OnPress: func() { s.queuePanel.Set(true) }},
					ui.Button{Text: "撤回队列", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "clear-queue",
						OnPress: func() { s.runCommand("clear-queue") }},
				}
			}},
			footer(cx),
		}
	}
	return base
}

// componentOverlay shows native's bars above the composer.
func (s *appState) componentOverlay(cx *core.Scope, id string) core.Node {
	return core.Group{
		core.Show{When: func() bool { return s.piSetup.Get() != "" }, Then: func(*core.Scope) core.Node { return s.piSetupCard() }},
		s.findBar(),
		s.bashCard(),
		s.inlinePalette(),
		core.Show{When: s.queuePanel.Get, Then: func(cx *core.Scope) core.Node { return s.queuePanelView(cx) }},
		s.approvalCard(),
		core.Show{When: s.readOnly.Get, Then: func(cx *core.Scope) core.Node { return s.readOnlyComposer(cx) }},
		s.loadEarlier(),
	}
}

// agioProjects is the folders New task offers.
func (s *appState) agioProjects() []string {
	var out []string
	for _, g := range s.store.Groups() {
		if len(g.Items) > 0 && g.Items[0].Cwd != "" {
			out = append(out, g.Items[0].Cwd)
		}
	}
	if root := s.projectRoot(); root != "" && !containsString(out, root) {
		out = append([]string{root}, out...)
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// settingsKeyForComponents rebuilds the settings dialog when native's writes
// or the language change.
func (s *appState) settingsKeyForComponents() string {
	return fmt.Sprint(s.settingsRev.Get(), "\x00", s.appearance.Get(), "\x00", s.showHidden.Get())
}

// dialogs are native's overlays, above the shell.
func (s *appState) dialogs() []core.Node {
	return []core.Node{
		s.palette(),
		s.renameDialog(),
		s.deleteDialog(),
		s.subagentDialog(),
		s.branchDialog(),
		s.extensionDialog(),
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
		s.fileOpDialog(),
		s.unsavedDialog(),
		ui.Toasts{Toaster: s.toaster, Placement: ui.ToastBottomEnd, CloseLabel: "关闭", TestID: "toasts"},
	}
}

// dockGrip resizes the right panel from its header edge: the shell's own
// handle sits under the transcript's scroll view on some platforms, so the
// panel gets a grip that is always on top.
func (s *appState) dockGrip() core.Node {
	var start core.Points
	return core.View{
		TestID: "dock-grip", Role: core.RoleAdjustable, Label: "调整右侧面板宽度",
		Style: core.Style{Width: core.Pt(8), Height: core.Pct(100), FlexShrink: 0, Cursor: core.CursorColResize},
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Accent(), 0.18)}}
		},
		OnPan: func(e core.PanEvent) {
			switch e.Phase {
			case core.GestureBegan:
				start = s.layout().dock.GetUntracked()
			case core.GestureChanged:
				s.setDockWidth(start - core.Points(e.Translation.X))
			}
		},
		Children: []core.Node{core.View{Style: core.Style{Width: core.Pt(1), Height: core.Pt(18), Background: s.colors.Border()}}},
	}
}
