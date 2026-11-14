package main

import (
	"strconv"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/commitgraph"
)

// gitView is the dock's Git tab: the project's history as ui/commitgraph
// draws it, with the branch line beside every commit. Activating a row copies
// its full hash.
func (s *appState) gitView() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Padding: core.Edges{Top: 8, Right: 10, Bottom: 4, Left: 10}}, Children: []core.Node{
				ui.IconButton{Icon: lucide.GitBranch, Label: "工作树", Size: 24, TestID: "git-worktrees", OnPress: s.openWorktreeDialog},
				core.Text{ContentFunc: s.gitHeadLabel, TestID: "git-head", MaxLines: 1,
					Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
				ui.Button{Text: "刷新", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "git-refresh",
					OnPress: s.loadGit},
				core.View{Style: core.Style{FlexGrow: 1}},
				core.Text{ContentFunc: s.gitCountLabel, MaxLines: 1,
					Style: core.Style{FontSize: 11, Color: s.colors.Muted()}},
			}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				commitgraph.View{
					Commits:    s.gitCommits.Get,
					Selection:  s.gitSel,
					OnActivate: s.copyCommit,
					Empty: core.Text{Content: "这个项目还没有提交", TestID: "git-empty",
						Style: core.Style{FontSize: 12.5, Color: s.colors.Muted()}},
					Label:  "Git 历史",
					TestID: "git-graph",
				},
			}},
		},
	}
}

func (s *appState) gitHeadLabel() string {
	if s.gitLoading.Get() {
		return "读取 git log…"
	}
	head := s.gitHead.Get()
	if head == "" {
		return "无 HEAD"
	}
	return "HEAD " + head
}

func (s *appState) gitCountLabel() string {
	return strconv.Itoa(len(s.gitCommits.Get())) + " 次提交"
}
