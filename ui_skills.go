package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"pigo/internal/skills"
)

// skillsDialog searches skills.sh and installs a package with the skills
// CLI, as pi-web's Settings › Skills does. The result list is the registry's
// most-installed matches; installing uses `npx skills add <pkg> -y --agent pi -g`.
func (s *appState) skillsDialog() core.Node {
	return ui.Dialog{
		Open:         s.skillSearchOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.skillSearchOpen.Set(open) },
		Title:        "安装技能",
		Description:  "搜索 skills.sh；安装写入 pi 的全局技能目录。",
		Style:        core.Style{Width: core.Pt(560)},
		Children: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
			busy := s.skillBusy.Get()
			rows := []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
					core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
						ui.Input{Value: s.skillQuery.Get, OnChange: s.skillQuery.Set,
							Placeholder: "搜索技能，例如 pdf、browser", TestID: "skill-search-input",
							OnSubmit: func(string) { s.searchSkills() },
							Style:    core.Style{Width: core.Pct(100)}},
					}},
					ui.Button{Text: "搜索", Variant: ui.ButtonPrimary, TestID: "skill-search",
						Disabled: func() bool { return busy || strings.TrimSpace(s.skillQuery.Get()) == "" },
						OnPress:  s.searchSkills},
				}},
			}
			if busy {
				rows = append(rows, ui.Skeleton{Lines: 3, TestID: "skill-searching"})
			}
			hits := s.skillHits.Get()
			if !busy && len(hits) == 0 && strings.TrimSpace(s.skillQuery.Get()) != "" {
				rows = append(rows, core.Text{Content: "没有匹配的技能", TestID: "skill-search-empty",
					Style: core.Style{FontSize: 12, Color: s.colors.Muted()}})
			}
			for i, hit := range hits {
				h := hit
				rows = append(rows, core.View{TestID: fmt.Sprintf("skill-hit-%d", i),
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(6, 4)},
					Children: []core.Node{
						ui.Icon(lucide.Sparkles, 14, s.colors.Muted),
						core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
							core.Text{Content: h.Package, MaxLines: 1, Style: core.Style{FontSize: 12, Color: s.colors.Text()}},
							core.Text{Content: h.Installs, MaxLines: 1, Style: core.Style{FontSize: 10.5, Color: s.colors.Faint()}},
						}},
						ui.Button{Text: "安装", Variant: ui.ButtonSecondary, Size: ui.SizeSmall,
							TestID:   fmt.Sprintf("skill-install-%d", i),
							Disabled: func() bool { return false },
							OnPress:  func() { s.installSkill(h.Package) }},
					}})
			}
			return core.View{Style: core.Style{Gap: 6}, Children: rows}
		})},
		Footer: []core.Node{
			ui.Button{Text: "关闭", TestID: "skill-close", OnPress: func() { s.skillSearchOpen.Set(false) }},
			ui.Button{Text: "刷新已安装", Variant: ui.ButtonGhost, TestID: "skill-refresh", OnPress: func() {
				go s.refreshSettings()
			}},
		},
		TestID: "skill-dialog",
	}
}

// openSkillsSearch shows the dialog and searches when there is a query.
func (s *appState) openSkillsSearch() {
	s.skillSearchOpen.Set(true)
	if strings.TrimSpace(s.skillQuery.GetUntracked()) != "" {
		s.searchSkills()
	}
}

// searchSkills asks the registry in the background.
func (s *appState) searchSkills() {
	query := strings.TrimSpace(s.skillQuery.GetUntracked())
	if query == "" || s.skillBusy.GetUntracked() {
		return
	}
	s.skillBusy.Set(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		hits, err := skills.Search(ctx, query, 50)
		s.cx.Post(func() {
			s.skillBusy.Set(false)
			if err != nil {
				s.notify(ui.ToastWarning, "技能搜索失败", err.Error())
				return
			}
			s.skillHits.Set(hits)
		})
	}()
}

// installSkill runs the skills CLI for one package and rereads settings.
func (s *appState) installSkill(pkg string) {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" || s.skillBusy.GetUntracked() {
		return
	}
	s.skillBusy.Set(true)
	go func() {
		out, err := skills.Install(context.Background(), pkg)
		s.cx.Post(func() {
			s.skillBusy.Set(false)
			if err != nil {
				s.notify(ui.ToastError, "安装技能失败", firstLine(strings.TrimSpace(out+" "+err.Error()), 160))
				return
			}
			s.notify(ui.ToastSuccess, "已安装技能", pkg)
			go s.refreshSettings()
		})
	}()
}
