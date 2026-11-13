package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// In-transcript find (⌘F). The terminal already had a find bar; the
// transcript did not, so finding one sentence in a long session meant
// scrolling by hand. This bar floats over the transcript, counts the turns
// whose prompt, steps or answer hold the query, and jumps between them
// through the conversation's own scroll position.

// findMatches returns the indexes of the turns holding query, matched
// case-insensitively across the prompt, the intermediate steps and the
// answer. A blank query matches nothing.
func findMatches(turns []turnRow, query string) []int {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	var out []int
	for i, t := range turns {
		if turnMatches(t, needle) {
			out = append(out, i)
		}
	}
	return out
}

// turnMatches reports whether one turn's text holds the lower-cased needle.
func turnMatches(t turnRow, needle string) bool {
	var b strings.Builder
	b.WriteString(t.Prompt)
	b.WriteByte('\n')
	b.WriteString(t.Final)
	for _, step := range t.Steps {
		b.WriteByte('\n')
		b.WriteString(step)
	}
	return strings.Contains(strings.ToLower(b.String()), needle)
}

// toggleFind opens the transcript's find bar, closes it when it is already
// open, and otherwise leaves ⌘F to the terminal's search while that tab is
// showing (this binding is the newest, so it has to keep that behavior).
func (s *appState) toggleFind() {
	if s.findOpen.GetUntracked() {
		s.findOpen.Set(false)
		return
	}
	if s.rightOpen.GetUntracked() && s.dock.GetUntracked() == "terminal" {
		s.openTerminalSearch()
		return
	}
	s.findQuery.Set("")
	s.findIndex.Set(0)
	s.findOpen.Set(true)
}

// closeFind hides the bar and keeps the transcript where the last match was.
func (s *appState) closeFind() { s.findOpen.Set(false) }

// findStep moves to the previous (delta < 0) or next match, wrapping at the
// ends, and scrolls the conversation to that turn's share of the transcript.
func (s *appState) findStep(delta int) {
	turns := s.turns.GetUntracked()
	matches := findMatches(turns, s.findQuery.GetUntracked())
	if len(matches) == 0 {
		return
	}
	index := s.findIndex.GetUntracked()
	if index < 0 || index >= len(matches) {
		index = 0
	} else {
		index = (index + delta + len(matches)) % len(matches)
	}
	s.findIndex.Set(index)
	s.scrollToTurn(matches[index], len(turns))
}

// findJump restarts at the first match, for a query that just changed.
func (s *appState) findJump(query string) {
	s.findQuery.Set(query)
	s.findIndex.Set(0)
	turns := s.turns.GetUntracked()
	if matches := findMatches(turns, query); len(matches) > 0 {
		s.scrollToTurn(matches[0], len(turns))
	}
}

// scrollToTurn scrolls to a turn's proportional share of the transcript,
// the same geometry the minimap uses.
func (s *appState) scrollToTurn(index, total int) {
	if total <= 0 || index < 0 {
		return
	}
	fraction := (float32(index) + 0.5) / float32(total)
	s.jumpToFraction(min(max(fraction, 0), 1))
}

// findCountLabel is "3 / 12", or why there is nothing to count.
func (s *appState) findCountLabel() string {
	query := strings.TrimSpace(s.findQuery.Get())
	if query == "" {
		return ""
	}
	matches := findMatches(s.turns.Get(), query)
	if len(matches) == 0 {
		return "无结果"
	}
	index := s.findIndex.Get()
	if index < 0 || index >= len(matches) {
		index = 0
	}
	return fmt.Sprintf("%d / %d", index+1, len(matches))
}

// findBar floats the search field over the transcript's top edge. Esc and
// Enter are bound only while it shows, so they belong to the caret and the
// composer whenever it is closed.
func (s *appState) findBar() core.Node {
	return core.Show{When: s.findOpen.Get, Then: func(cx *core.Scope) core.Node {
		cx.OnKeyWith("escape", core.KeyOptions{When: core.KeyAlways}, s.closeFind)
		cx.OnKeyWith("enter", core.KeyOptions{When: core.KeyAlways}, func() { s.findStep(1) })
		cx.OnKeyWith("shift+enter", core.KeyOptions{When: core.KeyAlways}, func() { s.findStep(-1) })
		colors := ui.ColorsOf(cx)
		return core.View{
			TestID: "find-bar",
			Style: core.Style{Position: core.Absolute, Top: core.Pt(8), Left: core.Pt(0), Right: core.Pt(0),
				AlignItems: core.AlignCenter},
			Children: []core.Node{core.View{
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Width: core.Pt(400),
					Padding: core.Symmetric(4, 8), CornerRadius: 9, BorderWidth: 1,
					Background: colors.Surface(),
					Shadows:    []core.BoxShadow{ui.Shadow(0, 4, 16, 0, ui.Black(0.2))}},
				StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
				Children: []core.Node{
					ui.Icon(lucide.Search, 13, colors.Muted),
					ui.Input{Value: s.findQuery.Get, OnChange: s.findJump, Placeholder: "在会话里搜索",
						AutoFocus: true, TestID: "find-input",
						Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}},
					core.Text{ContentFunc: s.findCountLabel, MaxLines: 1, TestID: "find-count",
						Style: core.Style{FontSize: 10.5, Color: colors.Muted()}},
					ui.IconButton{Icon: lucide.ChevronUp, Label: "上一处", TestID: "find-prev",
						OnPress: func() { s.findStep(-1) }},
					ui.IconButton{Icon: lucide.ChevronDown, Label: "下一处", TestID: "find-next",
						OnPress: func() { s.findStep(1) }},
					ui.IconButton{Icon: lucide.X, Label: "关闭搜索", TestID: "find-close", OnPress: s.closeFind},
				},
			}},
		}
	}}
}
