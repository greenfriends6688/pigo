package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
)

// approvalIndex is the highlighted option of the composer's approval card.
// It lives outside the view so a re-render of the card keeps the highlight;
// a new request resets it (resetApproval).
var approvalIndex *core.Signal[int]

// approvalFor is the request the highlight belongs to: syncExtensions re-sets
// the same request on every frame while it waits, and only a different id
// should move the highlight back to the first option.
var approvalFor string

// resetApproval returns the card to its first option for a request that just
// arrived.
func resetApproval(reqID string) {
	if approvalFor == reqID {
		return
	}
	approvalFor = reqID
	if approvalIndex != nil {
		approvalIndex.Set(0)
	}
}

// approvalCard answers a blocking extension `select` right above the
// composer instead of in a modal dialog: the question usually refers to the
// message being typed, and a dialog covered the very text it was about.
// 1–9 pick an option directly, ↑↓/Enter move and confirm; the card owns
// those keys only while it is on screen.
func (s *appState) approvalCard() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		req := s.extCurrent.Get()
		if !s.extOpen.Get() || req.ID == "" || req.Method != "select" || len(req.Options) == 0 {
			return nil
		}
		if approvalIndex == nil {
			approvalIndex = core.NewSignal(cx, 0)
		}
		options := req.Options
		index := approvalIndex.Get()
		if index < 0 || index >= len(options) {
			index = 0
		}
		choose := func(i int) {
			if i < 0 || i >= len(options) {
				return
			}
			approvalIndex.Set(0)
			s.replyExtension(req, options[i], false, false)
		}
		for n := 1; n <= 9 && n <= len(options); n++ {
			i := n - 1
			cx.OnKeyWith(fmt.Sprint(n), core.KeyOptions{When: core.KeyAlways}, func() { choose(i) })
		}
		cx.OnKeyWith("up", core.KeyOptions{When: core.KeyAlways, Repeat: true}, func() {
			approvalIndex.Set((index - 1 + len(options)) % len(options))
		})
		cx.OnKeyWith("down", core.KeyOptions{When: core.KeyAlways, Repeat: true}, func() {
			approvalIndex.Set((index + 1) % len(options))
		})
		cx.OnKeyWith("enter", core.KeyOptions{When: core.KeyAlways}, func() { choose(index) })

		colors := ui.ColorsOf(cx)
		title := req.Title
		if title == "" {
			title = "需要选择"
		}
		rows := make([]core.Node, 0, len(options))
		for i, option := range options {
			at, label := i, option
			rowStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Padding: core.Symmetric(5, 7), CornerRadius: 6}
			if at == index {
				rowStyle.Background = ui.Alpha(colors.Accent(), 0.14)
			}
			rows = append(rows, core.View{
				Role: core.RoleButton, Label: label, TestID: fmt.Sprintf("approval-option-%d", at),
				OnPress: func(core.PressEvent) { choose(at) },
				Style:   rowStyle,
				StyleFunc: func() core.Style {
					if at == index {
						return core.Style{}
					}
					return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.06)},
						Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.1)}}
				},
				Children: []core.Node{
					core.View{Style: core.Style{Width: core.Pt(16), Height: core.Pt(16), CornerRadius: 4,
						AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
						Background: ui.Alpha(colors.Text(), 0.06)}, Children: []core.Node{
						core.Text{Content: fmt.Sprint(at + 1),
							Style: core.Style{FontSize: 10, Color: colors.Muted()}},
					}},
					core.Text{Content: label, MaxLines: 2, Style: core.Style{FontSize: 12, Color: colors.Text()}},
				},
			})
		}
		var subtitle core.Node
		if strings.TrimSpace(req.Message) != "" {
			subtitle = core.Text{Content: req.Message, Selectable: core.SelectableOn, MaxLines: 3,
				Style: core.Style{FontSize: 11, LineHeight: 16, Color: colors.Muted()}}
		}
		return core.View{
			TestID: "approval-card",
			Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth),
				Padding: core.Symmetric(9, 11), Gap: 7, CornerRadius: 10, BorderWidth: 1, Background: colors.Surface()},
			StyleFunc: func() core.Style { return core.Style{BorderColor: ui.Alpha(colors.Accent(), 0.4)} },
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 7}, Children: []core.Node{
					ui.Icon(lucide.ShieldQuestionMark, 14, colors.Accent),
					core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold,
						Color: colors.Text()}},
					core.View{Style: core.Style{FlexGrow: 1}},
					core.Text{Content: "1–9 选择 · ↵ 确认", MaxLines: 1,
						Style: core.Style{FontSize: 10, Color: colors.Faint()}},
				}},
				subtitle,
				core.Scroll{Style: core.Style{MaxHeight: core.Pt(220), FlexShrink: 1}, Children: []core.Node{
					core.View{Style: core.Style{Gap: 2}, Children: rows},
				}},
				core.View{Style: core.Style{Direction: core.Row, Gap: 6}, Children: []core.Node{
					ui.Button{Text: "取消", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "approval-cancel",
						OnPress: func() { s.replyExtension(req, "", false, true) }},
				}},
			},
		}
	})
}
