package main

import (
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/desktop"
)

// installDesktop adds the macOS application menu, global hot keys and the
// menu-bar tray (PLAN §8 P7). On non-desktop targets these calls are no-ops.
func (s *appState) installDesktop() {
	cx := s.cx
	desktop.SetTitle(cx, "pigo")
	desktop.SetShutdownMode(cx, desktop.ShutdownOnLastWindowClose)
	desktop.SetApplicationMenu(cx, s.applicationMenu())

	// Global唤起：即使窗口没聚焦也能打开命令面板。
	desktop.RegisterHotKey(cx, "mod+shift+p", func() {
		cx.Post(func() {
			s.paletteOpen.Set(true)
			desktop.OrderFront(cx)
		})
	})

	_ = desktop.NewTray(cx, desktop.TrayOptions{
		Title:   "π",
		Tooltip: "pigo",
		Menu: func() []desktop.MenuItem {
			return []desktop.MenuItem{
				{Label: "打开命令面板", OnSelect: func() {
					cx.Post(func() { s.paletteOpen.Set(true) })
				}},
				{Label: "新建会话", OnSelect: func() {
					cx.Post(s.newSession)
				}},
				{Separator: true},
				{Label: "退出", OnSelect: cx.Quit},
			}
		},
	})
}

func (s *appState) applicationMenu() []core.MenuItem {
	return []core.MenuItem{
		{
			Label: "pigo",
			Items: []core.MenuItem{
				{Label: "关于 pigo", OnSelect: func() {
					s.cx.Post(func() {
						s.status.Set("pigo · MIT · 上游 NSGo + pi-web + pi")
					})
				}},
				{Separator: true},
				// The menu item stays, its shortcut does not: keymap.go binds
				// mod+, already, and a menu key equivalent would take the
				// press before the window did.
				{Label: "设置…", OnSelect: func() {
					s.cx.Post(func() { s.settingsOpen.Set(true) })
				}},
				{Separator: true},
				{Label: "退出", Shortcut: "mod+q", OnSelect: s.cx.Quit},
			},
		},
		{
			Label: "文件",
			Items: []core.MenuItem{
				{Label: "新建会话", OnSelect: func() { s.cx.Post(s.newSession) }},
				{Label: "重连", Shortcut: "mod+r", OnSelect: func() { s.cx.Post(s.reconnect) }},
				{Separator: true},
				{Label: "关闭窗口", Shortcut: "mod+w", OnSelect: func() { desktop.CloseWindow(s.cx) }},
			},
		},
		{
			Label: "视图",
			Items: []core.MenuItem{
				{Label: "文件与 Git", OnSelect: func() { s.cx.Post(func() { s.openDock("files") }) }},
				{Label: "审查", OnSelect: func() { s.cx.Post(func() { s.openDock("review") }) }},
				{Label: "终端", OnSelect: func() { s.cx.Post(func() { s.openDock("terminal") }) }},
				{Separator: true},
				{Label: "打开项目…", Shortcut: "mod+o", OnSelect: func() { s.cx.Post(s.openProjectPicker) }},
				{Label: "命令面板", OnSelect: func() { s.cx.Post(func() { s.paletteOpen.Set(true) }) }},
			},
		},
		{
			Label: "会话",
			Items: []core.MenuItem{
				{Label: "停止当前运行", Shortcut: "mod+.", OnSelect: func() { s.cx.Post(s.stop) }},
				{Label: "清空队列", OnSelect: func() { s.call(s.newCommand("clear_queue")) }},
				{Label: "压缩上下文", OnSelect: func() { s.call(s.newCommand("compact")) }},
				{Separator: true},
				{Label: "导出 HTML", OnSelect: func() { s.call(s.newCommand("export_html")) }},
			},
		},
	}
}
