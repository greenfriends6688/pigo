package main

// Agio's settings surface, copied into native: the color schemes and the
// film grain. The rest of native's settings stay where they are.

import (
	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"
)

// agioAppearanceGroups is Agio's look for 通用: one scheme for light and one
// for dark, and the window's grain.
func (s *appState) agioAppearanceGroups() []ui.SettingsGroup {
	return []ui.SettingsGroup{
		{ID: "schemes-light", Title: "浅色配色", Items: []ui.SettingsItem{{
			ID: "light-scheme", Title: "浅色", Stacked: true,
			Control: s.schemeGroup(lightSchemes, s.lightScheme, func(id string) {
				s.lightScheme.Set(id)
				s.applyTheme()
				s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
			}, "scheme-light-"),
		}}},
		{ID: "schemes-dark", Title: "深色配色", Items: []ui.SettingsItem{{
			ID: "dark-scheme", Title: "深色", Stacked: true,
			Control: s.schemeGroup(darkSchemes, s.darkScheme, func(id string) {
				s.darkScheme.Set(id)
				s.applyTheme()
				s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
			}, "scheme-dark-"),
		}}},
		{ID: "grain", Title: "背景颗粒", Items: []ui.SettingsItem{
			{ID: "grain-on", Title: "胶片颗粒", Description: "在窗口背景上撒一层细颗粒",
				Control: ui.Switch{Checked: s.grainOn.Get, TestID: "grain",
					OnChange: func(on bool) {
						s.grainOn.Set(on)
						s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
					}}},
			{ID: "grain-amount", Title: "颗粒强度", Description: "1–100，默认 40",
				Control: core.Dynamic(func(*core.Scope) core.Node {
					return ui.Slider{Value: func() float64 { return float64(grainAmount(s.grainAmount.Get())) },
						Min: 1, Max: 100, Step: 1, Label: "颗粒强度",
						OnChange: func(v float64) {
							s.grainAmount.Set(int(v))
							s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
						}}
				})},
		}},
	}
}

// schemeGroup is one appearance's schemes as radio cards of their colors.
func (s *appState) schemeGroup(schemes []colorScheme, chosen *core.Signal[string], choose func(string), testID string) core.Node {
	selected := func() string { return schemeOf(schemes, chosen.Get()).ID }
	options := make([]core.Node, 0, len(schemes))
	for _, sc := range schemes {
		sc := sc
		options = append(options, schemeOption(sc, func() bool { return selected() == sc.ID }, func() { choose(sc.ID) }, testID+sc.ID))
	}
	return base.RadioGroup{Label: "配色", Children: options,
		Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 4}}
}

// schemeOption is a scheme's card: its chrome, page and accent as squares
// over its name.
func schemeOption(sc colorScheme, checked func() bool, choose func(), testID string) core.Node {
	name := schemeName(sc.ID)
	edge := sc.Text.Faded(0.14)
	var squares []core.Node
	for _, c := range append(append([]core.Color{}, sc.Chrome...), sc.Page, sc.Accent) {
		squares = append(squares, core.View{Style: core.Style{Width: core.Pt(12), Height: core.Pt(12), Background: c,
			CornerRadius: 3, BorderWidth: 1, BorderColor: edge}})
	}
	return core.Func(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		return base.Radio{Label: name, TestID: testID, Checked: checked, OnChange: func(bool) { choose() },
			StyleFunc: func() core.Style {
				st := core.Style{Gap: 6, Padding: core.Symmetric(8, 10), AlignItems: core.AlignCenter, CornerRadius: ui.RadiusControl,
					Hover: &core.Style{Background: colors.Hover()}}
				if checked() {
					st.Background = ui.Alpha(colors.Accent(), 0.12)
					st.Hover = &core.Style{Background: st.Background}
				}
				return st
			},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, Gap: 3}, Children: squares},
				core.Text{Content: name, MaxLines: 1, Style: core.Style{FontSize: 12}, StyleFunc: func() core.Style {
					if checked() {
						return core.Style{Color: colors.Accent(), FontWeight: ui.Medium}
					}
					return core.Style{Color: colors.Muted()}
				}},
			}}
	})
}

// agioSystemGroups is Agio's system half of 通用: the language and keeping
// the machine awake while an agent works.
func (s *appState) agioSystemGroups() []ui.SettingsGroup {
	save := func() { s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked()) }
	return []ui.SettingsGroup{{ID: "agio-system", Title: "系统", Items: []ui.SettingsItem{
		{ID: "language", Title: "语言", Description: "界面与系统组件显示的语言",
			Control: core.Dynamic(func(*core.Scope) core.Node {
				return ui.Select[string]{Options: []ui.Option[string]{
					{Value: "", Label: "跟随系统"}, {Value: "zh-Hans", Label: "简体中文"},
					{Value: "zh-Hant", Label: "繁體中文"}, {Value: "en", Label: "English"},
				}, Value: s.language.Get, Label: "语言", TestID: "language",
					OnChange: func(v string) {
						s.language.Set(v)
						s.cx.SetLocale(v)
						save()
					}, Style: core.Style{Width: core.Pt(140)}}
			})},
		{ID: "keep-awake", Title: "阻止空闲休眠", Description: "代理工作时让电脑保持唤醒，屏幕仍可关闭",
			Control: ui.Switch{Checked: s.keepAwake.Get, TestID: "keep-awake",
				OnChange: func(on bool) { s.keepAwake.Set(on); save() }}},
		{ID: "keep-display-awake", Title: "阻止屏幕关闭/锁定", Description: "代理工作时保持屏幕常亮并解锁",
			Control: ui.Switch{Checked: s.keepDisplayAwake.Get, TestID: "keep-display-awake",
				OnChange: func(on bool) { s.keepDisplayAwake.Set(on); save() }}},
	}}}
}
