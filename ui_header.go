package main

import (
	"strings"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// headerBar is the title bar ZCode-style: the project on the left, the
// session's own title beside it, and the live facts on the right — the
// status capsule and the bar's three actions. Everything here answers
// "where am I, is it alive" without opening a panel.
func (s *appState) headerBar() core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
		FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
		s.projectChip(),
		s.titleChip(),
		s.branchChip(),
		core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}},
		s.statusCapsule(),
	}}
}

// chipStyle is one title-bar chip: quiet surface, hairline border, full
// rounding, as the capsule's pills.
func chipStyle(colors ui.Colors) core.Style {
	return core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5,
		Height: core.Pt(24), Padding: core.Symmetric(0, 8), CornerRadius: radiusCard,
		Background: ui.Alpha(colors.Text(), 0.04), BorderWidth: 1, BorderColor: colors.Border()}
}

// pressableChip wraps a chip's content in a pressable view that lights up
// under the pointer.
func (s *appState) pressableChip(testID, label string, onPress func(core.PressEvent), children ...core.Node) core.Node {
	return core.View{
		Role: core.RoleButton, Label: label, TestID: testID, OnPress: onPress,
		Style: chipStyle(s.colors),
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.08)},
				Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.12)}}
		},
		Children: children,
	}
}

// projectChip names the open project and holds the project menu: browse for
// another one, reveal this one, or trust it so its .pi code may run.
func (s *appState) projectChip() core.Node {
	face := core.View{Style: chipStyle(s.colors), Children: []core.Node{
		ui.Icon(lucide.Folder, 12, s.colors.Muted),
		core.Text{ContentFunc: func() string {
			root := s.projectRoot()
			if root == "" {
				return "未选择项目"
			}
			return projectName(root)
		}, TestID: "header-project", MaxLines: 1,
			Style: core.Style{FontSize: fontLabel, Color: s.colors.Text()}},
		ui.Icon(lucide.ChevronDown, 10, s.colors.Muted),
	}}
	return ui.Menu{
		Trigger:   ui.Button{Variant: ui.ButtonGhost, Label: "项目菜单", Size: ui.SizeSmall},
		Face:      face,
		Placement: core.PlaceBottom,
		Label:     "项目菜单",
		TestID:    "header-project-menu",
		Children: []core.Node{
			ui.MenuItem{Text: "打开项目…", Icon: lucide.FolderOpen, TestID: "header-project-open",
				OnSelect: s.openProjectPicker},
			ui.MenuItem{Text: "在 Finder 中显示", Icon: lucide.FolderSearch, TestID: "header-project-finder",
				OnSelect: func() {
					if root := s.projectRoot(); root != "" {
						s.revealInFinder(root)
					}
				}},
			ui.MenuSeparator{},
			ui.MenuItem{Text: "信任此目录", Icon: lucide.ShieldCheck, TestID: "header-project-trust",
				OnSelect: s.trustProjectDir},
		},
	}
}

// trustProjectDir runs the project gate for the window's project; a trusted
// directory only reports so, since there is nothing left to ask.
func (s *appState) trustProjectDir() {
	root := s.projectRoot()
	if root == "" {
		s.status.Set("先打开一个项目")
		return
	}
	if s.ensureTrustFor(root, "") {
		s.status.Set("已信任 " + projectName(root))
	}
}

// titleChip draws the session title; a double click renames it, the way the
// rest of the desktop does. The pointer resting on it opens a card with the
// whole title the bar had to cut, the project, and whether pi is answering.
func (s *appState) titleChip() core.Node {
	// The chip's flex lives on this wrapper: the card wraps the trigger in
	// views of its own, and a title that cannot shrink would push the bar's
	// chips out to the right. A column, so the trigger is stretched to the
	// width the row leaves and its text still ends in an ellipsis.
	return core.View{Style: core.Style{Direction: core.Column, FlexShrink: 1, MinWidth: core.Pt(0)},
		Children: []core.Node{
			ui.HoverCard{
				// Uncontrolled like the rail's cards: the pointer resting on the
				// title opens it after a short wait, the card keeps it open while
				// it is read, and the title keeps its own presses and look.
				OpenDelay:  350 * time.Millisecond,
				CloseDelay: 250 * time.Millisecond,
				Placement:  core.PlaceBottom,
				Align:      core.AlignStart,
				TestID:     "header-title-hover",
				// 360 wide, past the card's 320 default: a long title reads here.
				Style: core.Style{Width: core.Pt(360), MaxWidth: core.Pt(360), Gap: 4},
				Trigger: core.View{
					Role: core.RoleButton, Label: "会话标题，双击重命名",
					TestID: "header-title",
					OnPress: func(ev core.PressEvent) {
						if ev.Clicks >= 2 {
							s.renameCurrentSession()
						}
					},
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinWidth: core.Pt(0), FlexShrink: 1,
						Padding: core.Symmetric(2, 4), CornerRadius: radiusControl},
					StyleFunc: func() core.Style {
						return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)},
							Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.08)}}
					},
					Children: []core.Node{core.Text{ContentFunc: s.headerTitle, TestID: "window-title", MaxLines: 1,
						Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}}},
				},
				Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node { return s.titleHoverCard() })},
			}}}
}

// titleHoverCard is the title's card: the whole title the bar had to cut,
// the project it lives in, and whether pi is answering there.
func (s *appState) titleHoverCard() core.Node {
	return core.View{Style: core.Style{Direction: core.Column, Gap: 4}, Children: []core.Node{
		core.Text{ContentFunc: s.hoverTitle, MaxLines: 4,
			Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
		core.Text{ContentFunc: func() string {
			if root := s.projectRootNow(); root != "" {
				return projectName(root)
			}
			return "未选择项目"
		}, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
		core.Text{ContentFunc: func() string {
			if s.connected.Get() {
				return "已连接"
			}
			return "未连接"
		}, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
	}}
}

// hoverTitle is the title alone: the bar's also names the project, which
// the card says on a line of its own.
func (s *appState) hoverTitle() string {
	sel := s.selected.Get()
	if sel == "" {
		return "新会话"
	}
	for _, r := range s.searchRows.Get() {
		if r.ID == sel {
			return r.Title
		}
	}
	return "会话"
}

// renameCurrentSession opens the rename dialog for the open session, or says
// there is none rather than offering a dialog that cannot save.
func (s *appState) renameCurrentSession() {
	id := s.selected.GetUntracked()
	if id == "" {
		s.notify(ui.ToastWarning, "没有打开的会话", "先打开一个会话再重命名")
		return
	}
	for _, r := range s.rows.GetUntracked() {
		if r.ID == id {
			s.beginRename(id, r.Title)
			return
		}
	}
	s.beginRename(id, "")
}

// branchChip shows the git branch of the project and opens the review dock,
// where the diff of that branch lives.
func (s *appState) branchChip() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		branch := branchOf(s.gitText.Get())
		if branch == "" {
			return nil
		}
		return s.pressableChip("header-branch", "打开审阅", func(core.PressEvent) { s.openDock("review") },
			ui.Icon(lucide.GitBranch, 11, s.colors.Muted),
			core.Text{Content: branch, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}})
	})
}

// branchOf takes the branch out of the file dock's status text, which reads
// "main · 3 处改动" or "工作区干净".
func branchOf(git string) string {
	git = strings.TrimSpace(git)
	if git == "" || git == "工作区干净" {
		return ""
	}
	if i := strings.Index(git, " · "); i > 0 {
		git = git[:i]
	}
	return strings.TrimSuffix(git, "（worktree）")
}
