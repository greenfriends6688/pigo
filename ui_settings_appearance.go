package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// appearanceSettingsCategory is the window's look. The window theme and the
// terminal's own size and theme are the writable rows; NSGo's Environment is
// what decides the text scale and reduce-motion, and Scope has no
// SetReduceMotion or font-size/density entry, so the remaining rows report
// what is in effect instead of offering switches that would change nothing.
func (s *appState) appearanceSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "appearance", Title: "外观", Section: "应用", Icon: lucide.Palette, Description: appearanceCategoryDesc,
		Groups: []ui.SettingsGroup{
			{
				ID: "appearance-theme", Title: "主题",
				Items: []ui.SettingsItem{{
					ID: "theme", Title: "外观", Description: "立即应用到整个窗口", Leading: ui.Icon(lucide.Palette, 15, s.colors.Muted),
					Control: core.Dynamic(func(*core.Scope) core.Node {
						return ui.Select[string]{
							Options: []ui.Option[string]{{Value: "system", Label: "跟随系统"}, {Value: "light", Label: "浅色"}, {Value: "dark", Label: "深色"}},
							Value:   s.appearance.Get, OnChange: s.setAppearance, Label: "外观", TestID: "appearance", Style: core.Style{Width: core.Pt(180)},
						}
					}),
				}},
			},
			{
				ID: "appearance-type", Title: "字号",
				Items: []ui.SettingsItem{
					{ID: "ui-font", Title: uiFontTitle, Description: uiFontDesc, Leading: ui.Icon(lucide.FileType, 15, s.colors.Muted),
						Content: s.readOnlyValue(func() string {
							scale := s.cx.Env().FontScale()
							if scale == 1 {
								return "13（默认）"
							}
							return fmt.Sprintf("13（系统文字 ×%.2g）", scale)
						}, "ui-font-value")},
					{ID: "code-font", Title: codeFontTitle, Description: codeFontDesc, Leading: ui.Icon(lucide.FileCode, 15, s.colors.Muted),
						Content: s.readOnlyValue(func() string { return "12" }, "code-font-value")},
				},
			},
			{
				ID: "appearance-terminal", Title: "终端",
				Items: []ui.SettingsItem{
					{ID: "terminal-font", Title: "终端字号", Description: termFontDesc, Leading: ui.Icon(lucide.Terminal, 15, s.colors.Muted),
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[int]{
								Options: termFontOptions, Value: s.termFont.Get, Label: "终端字号", TestID: "terminal-font",
								OnChange: s.setTermFont, Style: core.Style{Width: core.Pt(100)},
							}
						})},
					{ID: "terminal-theme", Title: "终端主题", Description: termThemeDesc, Leading: ui.Icon(lucide.Contrast, 15, s.colors.Muted),
						Control: core.Dynamic(func(*core.Scope) core.Node {
							return ui.Select[string]{
								Options: []ui.Option[string]{{Value: "auto", Label: "跟随系统"}, {Value: "light", Label: "浅色"}, {Value: "dark", Label: "深色"}},
								Value:   s.termTheme.Get, Label: "终端主题", TestID: "terminal-theme",
								OnChange: s.setTermTheme, Style: core.Style{Width: core.Pt(140)},
							}
						})},
				},
			},
			{
				ID: "appearance-density", Title: "间距与动效",
				Items: []ui.SettingsItem{
					{ID: "density", Title: densityTitle, Description: densityDesc, Leading: ui.Icon(lucide.ListChecks, 15, s.colors.Muted),
						Content: s.readOnlyValue(func() string { return "舒适" }, "density-value")},
					{ID: "reduce-motion", Title: motionTitle, Description: motionDesc, Leading: ui.Icon(lucide.Sparkles, 15, s.colors.Muted),
						Content: s.readOnlyValue(func() string {
							if s.cx.Env().ReduceMotion() {
								return "已开启（跟随系统）"
							}
							return "未开启（跟随系统）"
						}, "reduce-motion-value")},
				},
			},
		},
	}
}

// The terminal rows: its text size and its own theme, both remembered in
// prefs.json. The window theme above stays the app's.
const (
	termFontDesc  = "11–18 点，终端立即重排"
	termThemeDesc = "「跟随系统」随窗口明暗切换；也可固定浅色或深色"
)

// termFontOptions are the sizes the terminal offers, 11 to 18 points.
var termFontOptions = func() []ui.Option[int] {
	options := make([]ui.Option[int], 0, maxTermFont-minTermFont+1)
	for size := minTermFont; size <= maxTermFont; size++ {
		options = append(options, ui.Option[int]{Value: size, Label: fmt.Sprint(size)})
	}
	return options
}()

// setTermFont sizes the dock terminal's text and remembers it.
func (s *appState) setTermFont(size int) {
	s.termFont.Set(clampTermFont(size))
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// setTermTheme pins the dock terminal to light or dark, or puts it back on
// the window's appearance, and remembers it.
func (s *appState) setTermTheme(theme string) {
	s.termTheme.Set(normalizeTermTheme(theme))
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// readOnlyValue is a right-aligned, muted value for rows the app can only
// report, not change.
func (s *appState) readOnlyValue(text func() string, testID string) core.Node {
	return core.Text{ContentFunc: text, TestID: testID, MaxLines: 1,
		Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
}

// appVersion is the bundle's CFBundleShortVersionString, read once from the
// Info.plist next to the executable. A binary run from a checkout (go test,
// go run) has no bundle, so it reports itself as a development build.
var appVersionOnce sync.Once

func appVersion() string {
	value := devBuildText
	appVersionOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		plist, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "..", "Info.plist"))
		if err != nil {
			return
		}
		if v := plistString(string(plist), "CFBundleShortVersionString"); v != "" {
			value = v
		}
	})
	return value
}

// plistString returns the value of key in an XML property list, or "".
func plistString(plist, key string) string {
	marker := "<key>" + key + "</key>"
	i := strings.Index(plist, marker)
	if i < 0 {
		return ""
	}
	rest := plist[i+len(marker):]
	start := strings.Index(rest, "<string>")
	if start < 0 {
		return ""
	}
	rest = rest[start+len("<string>"):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
