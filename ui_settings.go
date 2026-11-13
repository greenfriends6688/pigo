package main

import (
	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"
)

// settingsDialog is the settings surface as a dialog over the window, not a
// page (PLAN §7). Every write goes through internal/config, which owns the
// read/write boundary of §3.5; auth.json is never opened here.
//
// The shell copies PI NEXT's: one 1040×720 surface with a 偏好/应用 sidebar
// and no search field, which PI NEXT does not have either.
func (s *appState) settingsDialog() core.Node {
	return ui.SettingsDialog{
		Open:         s.settingsOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.settingsOpen.Set(open) },
		OnClose:      func() { s.settingsOpen.Set(false) },
		Label:        "设置",
		TestID:       "settings-dialog",
		Width:        1040,
		Height:       720,
		Content: func(narrow func() bool) core.Node {
			return ui.Settings{
				TestID:     "settings",
				Title:      "设置",
				EmptyText:  "没有匹配的设置",
				CloseLabel: "关闭",
				HideSearch: true,
				OnClose:    func() { s.settingsOpen.Set(false) },
				Category:   s.category,
				Query:      s.settingsQuery,
				Narrow:     narrow,
				Categories: s.settingsCategories(),
			}
		},
	}
}

func (s *appState) info(key string) string {
	return s.settingsInfo.Get()[key]
}

func (s *appState) infoText(key, fallback string) core.Node {
	return core.Text{
		ContentFunc: func() string {
			if v := s.info(key); v != "" {
				return v
			}
			return fallback
		},
		Style:    core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()},
		MaxLines: 8,
	}
}

// settingsCategories is PI NEXT's sidebar in its order: one destination per
// subject. Appearance, conversation options and tools share 通用; project
// trust sits at the end of MCP. The key table and the about block are gone:
// they described the same few facts the app already shows elsewhere. The IDs
// stay the ones the command palette and the tests know, except plugins, which
// keep "extensions" and only change their title.
func (s *appState) settingsCategories() []ui.SettingsCategory {
	return []ui.SettingsCategory{
		s.generalSettingsCategory(),
		s.modelSettingsCategory(),
		s.agentsSettingsCategory(),
		s.skillsSettingsCategory(),
		s.extensionsSettingsCategory(),
		s.mcpSettingsCategory(),
	}
}
