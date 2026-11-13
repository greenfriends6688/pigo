package main

import (
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// generalSettingsCategory is PI NEXT's 通用 page: 外观, 终端, 输入, 会话,
// 侧边栏与文件 and 工具, in that order and with its row texts. Every row here
// writes a signal that changes something.
//
// Rows an earlier version reported read-only (语言, 正文字号, 背景透明度, 字体,
// 间距, 减少动效, 选区浮窗, 缓存未命中提示, 文件浏览器位置) are gone: NSGo follows
// the system for all of them or has no API yet, and a row that only says what
// the system already did is a switch the user cannot flip.
func (s *appState) generalSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "general", Title: "通用", Section: "偏好", Icon: lucide.Settings,
		Description: "显示方式、输入、文件与工具的默认值",
		Groups: []ui.SettingsGroup{
			{
				ID: "general-appearance", Title: "外观",
				Items: []ui.SettingsItem{
					{ID: "theme", Title: "主题", Description: "选择浅色或深色主题，也可以跟随系统设置。",
						Control: s.themeCards()},
					{ID: "chat-width", Title: "会话阅读宽度", Description: "设置消息内容和输入框的最大宽度。",
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[int]{
								Options: []ui.Option[int]{{Value: 640, Label: "640"}, {Value: 720, Label: "720"}, {Value: 800, Label: "800"}, {Value: 860, Label: "860"}},
								Value:   func() int { return int(s.chatWidth.Get()) }, Label: "会话阅读宽度", TestID: "chat-width",
								OnChange: func(v int) {
									s.chatWidth.Set(core.Points(v))
									s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
								}, Style: core.Style{Width: core.Pt(110)},
							}
						})},
				},
			},
			{
				ID: "general-terminal", Title: "终端",
				Items: []ui.SettingsItem{
					{ID: "terminal-font", Title: "终端字号", Description: "仅影响右侧终端，不影响会话正文",
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[int]{
								Options: []ui.Option[int]{{Value: 11, Label: "11"}, {Value: 12, Label: "12"}, {Value: 13, Label: "13"}, {Value: 14, Label: "14"}, {Value: 15, Label: "15"}, {Value: 16, Label: "16"}, {Value: 17, Label: "17"}, {Value: 18, Label: "18"}},
								Value:   s.termFont.Get, Label: "终端字号", TestID: "terminal-font",
								OnChange: func(v int) {
									s.termFont.Set(v)
									s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
								}, Style: core.Style{Width: core.Pt(100)},
							}
						})},
					{ID: "terminal-theme", Title: "终端主题", Description: "跟随窗口外观，或固定浅色/深色",
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[string]{
								Options: []ui.Option[string]{{Value: "auto", Label: "跟随系统"}, {Value: "light", Label: "浅色"}, {Value: "dark", Label: "深色"}},
								Value:   s.termTheme.Get, Label: "终端主题", TestID: "terminal-theme",
								OnChange: func(v string) {
									s.termTheme.Set(v)
									s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
								}, Style: core.Style{Width: core.Pt(140)},
							}
						})},
				},
			},
			{
				ID: "general-input", Title: "输入",
				Items: []ui.SettingsItem{
					{ID: "send-on-enter", Title: "Enter 发送", Description: "关闭后 Enter 换行，⌘Enter 发送",
						Control: ui.Switch{Checked: s.sendOnEnter.Get, OnChange: func(v bool) {
							s.sendOnEnter.Set(v)
							s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
						}, TestID: "send-on-enter"}},
					{ID: "notify-sound", Title: "完成提示音", Description: "一轮结束时播放提示音",
						Control: ui.Switch{Checked: s.notifySound.Get, OnChange: func(v bool) {
							s.notifySound.Set(v)
							s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
						}, TestID: "notify-sound"}},
					{ID: "notify-done", Title: "桌面通知", Description: "macOS 通知中心",
						Control: ui.Switch{Checked: s.notifyDone.Get, OnChange: func(v bool) {
							s.setSettingBool("notifyOnDone", v, s.notifyDone)
							s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
						}, TestID: "notify-done"}},
					{ID: "notify-in-app", Title: "应用内提示", Description: "窗口内 toast，不打扰系统",
						Control: ui.Switch{Checked: s.notifyInApp.Get, OnChange: func(v bool) {
							s.notifyInApp.Set(v)
							s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
						}, TestID: "notify-in-app"}},
					{ID: "expand-thinking", Title: "默认展开思考块", Description: "一轮结束后仍展开思考与工具；关闭后折叠成一行",
						Control: ui.Switch{Checked: s.expandThinking.Get, OnChange: s.setExpandThinking, TestID: "expand-thinking"}},
					{ID: "hide-thinking", Title: "隐藏思考文字", Description: "只显示工具与回答，不渲染模型的推理文字",
						Control: ui.Switch{Checked: s.hideThinking.Get, OnChange: s.setHideThinking, TestID: "hide-thinking"}},
				},
			},
			{
				ID: "general-session", Title: "会话",
				Items: []ui.SettingsItem{
					{ID: "auto-compaction", Title: "自动压缩上下文", Description: "写入 settings.json，并同步到当前会话",
						Control: ui.Switch{Checked: s.autoCompaction.Get, OnChange: func(v bool) {
							s.setSettingBool("autoCompaction", v, s.autoCompaction)
							s.syncSessionSettings(s.current())
						}, TestID: "auto-compaction"}},
					{ID: "auto-retry", Title: "自动重试", Description: "模型调用失败时重试，并同步到当前会话",
						Control: ui.Switch{Checked: s.autoRetry.Get, OnChange: func(v bool) {
							s.setSettingBool("autoRetry", v, s.autoRetry)
							s.syncSessionSettings(s.current())
						}, TestID: "auto-retry"}},
				},
			},
			{
				ID: "general-sidebar", Title: "侧边栏与文件",
				Items: []ui.SettingsItem{
					{ID: "tree-font", Title: "文件树字号", Description: "也可在文件面板用 +/- 按钮缩放",
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[int]{
								Options: []ui.Option[int]{{Value: 10, Label: "10"}, {Value: 11, Label: "11"}, {Value: 12, Label: "12"}, {Value: 13, Label: "13"}, {Value: 14, Label: "14"}, {Value: 16, Label: "16"}},
								Value:   s.treeFont.Get, Label: "文件树字号", TestID: "tree-font",
								OnChange: func(v int) {
									s.treeFont.Set(v)
									s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
								}, Style: core.Style{Width: core.Pt(120)},
							}
						})},
					{ID: "show-hidden", Title: "显示隐藏文件", Description: "在文件树里显示 . 开头的文件",
						Control: ui.Switch{Checked: s.showHidden.Get, OnChange: s.setShowHidden, TestID: "show-hidden"}},
				},
			},
			{
				ID: "general-tools", Title: "工具",
				Actions: []core.Node{ui.Button{Text: "取回可用模型", TestID: "get-models",
					OnPress: func() { s.call(s.newCommand("get_available_models")) }}},
				Items: []ui.SettingsItem{
					{ID: "tool-mode", Title: "工具模式", Description: "默认 / 只读 / 无；切换后重连当前会话",
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[string]{
								Options: []ui.Option[string]{{Value: "default", Label: "默认"}, {Value: "read", Label: "只读"}, {Value: "none", Label: "无"}},
								Value:   s.toolMode.Get, Label: "工具模式", TestID: "tool-mode", OnChange: s.setToolMode,
								Style: core.Style{Width: core.Pt(110)},
							}
						})},
					{ID: "no-mcp", Title: "禁用 MCP", Description: "启动时追加 --no-mcp",
						Control: ui.Switch{Checked: s.noMCP.Get, OnChange: func(v bool) { s.setSettingBool("noMCP", v, s.noMCP) }, TestID: "no-mcp"}},
				},
			},
		},
	}
}

// themeCards is PI NEXT's three theme cards: 浅色, 深色 and 跟随系统, each a
// 56×36 swatch with its name under it, the chosen one ringed in the accent.
// A radio group's worth of choices, so no Select.
func (s *appState) themeCards() core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 8},
		Children: []core.Node{
			s.themeCard("light", "浅色", "theme-light"),
			s.themeCard("dark", "深色", "theme-dark"),
			s.themeCard("system", "跟随系统", "theme-system"),
		}}
}

// themeCard is one of themeCards.
func (s *appState) themeCard(value, label, testID string) core.Node {
	on := func() bool { return s.appearance.Get() == value }
	return ui.Press{
		OnPress:  func() { s.setAppearance(value) },
		Label:    label,
		Selected: on,
		TestID:   testID,
		Style:    core.Style{Direction: core.Column, AlignItems: core.AlignCenter, Gap: 5, Padding: core.All(2)},
		Children: []core.Node{
			core.View{Style: core.Style{Width: core.Pt(56), Height: core.Pt(36), CornerRadius: radiusCard,
				BorderCurve: core.BorderCurveContinuous, BorderWidth: 1},
				StyleFunc: func() core.Style {
					if on() {
						return core.Style{BorderWidth: 2, BorderColor: s.colors.Accent()}
					}
					return core.Style{BorderColor: s.colors.Border()}
				},
				Children: []core.Node{s.themeSwatch(value)}},
			core.Text{Content: label, MaxLines: 1, Style: core.Style{FontSize: 10.5},
				StyleFunc: func() core.Style {
					if on() {
						return core.Style{Color: s.colors.Text(), FontWeight: core.FontWeightMedium}
					}
					return core.Style{Color: s.colors.Muted()}
				}},
		},
	}
}

// themeSwatch is a card's preview: a light card, a dark one, or both halves
// for 跟随系统.
func (s *appState) themeSwatch(value string) core.Node {
	light, dark := core.RGB(0xFFFFFF), core.RGB(0x1C1C1E)
	switch value {
	case "light":
		return core.View{Style: core.Style{FlexGrow: 1, Background: light}}
	case "dark":
		return core.View{Style: core.Style{FlexGrow: 1, Background: dark}}
	}
	return core.View{Style: core.Style{Direction: core.Row, FlexGrow: 1, CornerRadius: radiusCard, Overflow: core.Hidden}, Children: []core.Node{
		core.View{Style: core.Style{FlexGrow: 1, Background: light}},
		core.View{Style: core.Style{FlexGrow: 1, Background: dark}},
	}}
}

// setShowHidden shows or hides dotfiles from 通用, dropping the cached
// listings so the tree re-reads them, and remembering the choice.
func (s *appState) setShowHidden(v bool) {
	s.showHidden.Set(v)
	s.savePrefsAsync(s.projectRoot(), v)
	s.dirs.mu.Lock()
	s.dirs.dirs = make(map[string][]filesEntry)
	s.dirs.asked = make(map[string]bool)
	s.dirs.index = make(map[string]filesEntry)
	s.dirs.mu.Unlock()
	s.treeRev.Set(s.treeRev.GetUntracked() + 1)
}
