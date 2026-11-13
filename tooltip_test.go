package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
)

// TestSessionTooltipTextIsCompact locks the content rule: a row's hover never
// carries the transcript preview or multi-line metadata. Those lines turned a
// bare core.Text tooltip into a transparent box over the chat pane.
func TestSessionTooltipTextIsCompact(t *testing.T) {
	r := sessionRow{
		Title:         strings.Repeat("很长的会话标题", 30),
		Project:       strings.Repeat("项目名", 30),
		Age:           "35 分钟前",
		Preview:       "你好！我是 pi 里的编程助手，也能读写文件、执行命令、改代码、调试、打包发布。",
		LastTurnTools: 3,
		Unread:        true,
	}
	for name, got := range map[string]string{"title": sessionPreviewText(r), "meta": sessionTooltipMeta(r)} {
		if strings.ContainsAny(got, "\n\r") {
			t.Fatalf("%s tooltip has a line break: %q", name, got)
		}
		if strings.Contains(got, r.Preview) {
			t.Fatalf("%s tooltip repeats the transcript: %q", name, got)
		}
	}
	if n := utf8.RuneCountInString(sessionPreviewText(r)); n > 80 {
		t.Fatalf("title tooltip has %d runes, want <= 80", n)
	}
	if n := utf8.RuneCountInString(sessionTooltipMeta(r)); n > 48 {
		t.Fatalf("meta tooltip has %d runes, want <= 48", n)
	}
}

// TestSessionTooltipPopupIsBounded guards the box itself: a tooltip with no
// max width and no clipping is the one that sprawled across both panes.
func TestSessionTooltipPopupIsBounded(t *testing.T) {
	st := sessionTooltipPopupStyle()
	if st.MaxWidth != core.Pt(240) {
		t.Fatalf("tooltip has no 240pt width cap: %v", st.MaxWidth)
	}
	if st.Overflow != core.Hidden {
		t.Fatalf("tooltip does not clip its text: %v", st.Overflow)
	}
	if st.Direction != core.Column {
		t.Fatalf("tooltip lines are not stacked: %v", st.Direction)
	}
}

// The type assertion is compile-time: base.Tooltip is a HintTooltip, which is
// what core.View.Tooltip needs to mount a popup with its own style instead of
// an unstyled bare text overlay.
var _ core.HintTooltip = base.Tooltip{}
