package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// capsuleAnchors keys the capsule's overlay anchor by scope, so the panel
// opens beside the capsule that asked for it.
var capsuleAnchors sync.Map // *core.Scope -> *core.Ref

func (s *appState) capsuleAnchor() *core.Ref {
	if v, ok := capsuleAnchors.Load(s.cx); ok {
		return v.(*core.Ref)
	}
	ref := &core.Ref{}
	actual, _ := capsuleAnchors.LoadOrStore(s.cx, ref)
	return actual.(*core.Ref)
}

// statusCapsule sits in the title bar and answers, at a glance, the four
// questions a long task raises: which branch, is it still running (and for
// how long), is anything waiting for me, is the agent still connected. It
// used to float over the transcript's top right, where it covered the first
// turn; the sidebar's 11pt status strip was the only other place that said
// so and is gone. Pressing the capsule itself, not one of its pills, opens
// the full panel behind it.
func (s *appState) statusCapsule() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		ref := s.capsuleAnchor()
		capsule := core.View{
			Ref: ref, Role: core.RoleButton, Label: "状态面板", TestID: "status-capsule",
			OnPress: func(core.PressEvent) {
				if s.statusPanel.GetUntracked() {
					s.statusPanel.Set(false)
					return
				}
				s.loadSubagents()
				s.statusPanel.Set(true)
			},
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Wrap: core.Wrap,
				Padding: core.Symmetric(3, 3), CornerRadius: radiusCard},
			StyleFunc: func() core.Style {
				return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)}}
			},
			Children: s.capsulePills(cx),
		}
		nodes := []core.Node{capsule}
		if s.statusPanel.Get() {
			colors := ui.ColorsOf(cx)
			nodes = append(nodes, core.Overlay{
				Anchor: ref, Placement: core.PlaceBottom, Align: core.AlignEnd, Offset: 6, Margin: 8,
				Style: core.Style{Width: core.Pt(264), Padding: core.All(6), CornerRadius: radiusCard,
					Background: colors.Raised(), BorderWidth: 1, BorderColor: colors.Border(),
					Shadows: []core.BoxShadow{ui.Shadow(0, 6, 18, 0, ui.Black(0.18))}},
				OnPressOutside: func(core.Point) { s.statusPanel.Set(false) },
				OnCancel:       func() { s.statusPanel.Set(false) },
				TestID:         "status-panel",
				Children:       s.statusPanelSections(cx),
			})
		}
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, FlexShrink: 0},
			Children: nodes}
	})
}

// statusPanelSections is the capsule's panel: the facts in full, each
// opening the surface that owns it. Model and context answer "what is
// answering, with how much room"; the rest answer "where am I, is it
// running, is anything waiting for me".
func (s *appState) statusPanelSections(cx *core.Scope) []core.Node {
	colors := ui.ColorsOf(cx)
	section := func(title, value, testID string, icon func() *core.SVG, open func()) core.Node {
		return core.View{
			Role: core.RoleButton, Label: title + "：" + value, TestID: testID,
			OnPress: func(core.PressEvent) {
				s.statusPanel.Set(false)
				open()
			},
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Width: core.Pct(100),
				Padding: core.Symmetric(6, 6), CornerRadius: radiusControl},
			StyleFunc: func() core.Style {
				return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.06)},
					Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.1)}}
			},
			Children: []core.Node{
				ui.Icon(icon, 13, colors.Muted),
				core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: fontLabel, Color: colors.Text()}},
				core.View{Style: core.Style{FlexGrow: 1}},
				core.Text{Content: value, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: colors.Muted()}},
			},
		}
	}
	git := strings.TrimSpace(s.gitText.Get())
	if git == "" {
		git = "工作区干净"
	}
	run := s.runElapsedLabel()
	if run == "" {
		run = "空闲"
	}
	queues := strings.TrimSpace(s.queues.Get())
	if queues == "" {
		queues = "无队列"
	}
	model := strings.TrimSpace(s.modelName.Get())
	if model == "" {
		model = "未选择"
	}
	context := "未知"
	if window := s.ctxWindow.Get(); window > 0 {
		used := s.ctxUsed.Get()
		share := min(max(float64(used)/float64(window), 0), 1)
		context = fmt.Sprintf("%s / %s · %.0f%%", shortCount(int64(used)), shortCount(int64(window)), share*100)
	}
	// Both live in the settings dialog's model page: it is where the model is
	// picked and where the model's context window is configured.
	openModels := func() {
		s.category.Set("models")
		s.settingsOpen.Set(true)
	}
	return []core.Node{
		section("模型", model, "status-panel-model", lucide.Cpu, openModels),
		section("上下文", context, "status-panel-context", lucide.Gauge, openModels),
		section("Git", git, "status-panel-git", lucide.GitBranch, func() { s.openDock("review") }),
		section("运行", run, "status-panel-run", lucide.LoaderCircle, func() { s.openDock("terminal") }),
		section("队列", queues, "status-panel-queue", lucide.ListChecks, func() { s.openDock("terminal") }),
		section("子代理", fmt.Sprintf("%d 个", len(s.subItems.Get())), "status-panel-subagents", lucide.Bot,
			func() { s.openDock("subagents") }),
	}
}

// capsulePills builds the visible pills; empty when there is nothing to
// say — a connected idle session with a clean tree stays quiet, except for
// one faint chip that still opens the panel.
func (s *appState) capsulePills(cx *core.Scope) []core.Node {
	colors := ui.ColorsOf(cx)
	var pills []core.Node

	// Running: the wall-clock seconds the current run has spent.
	if !s.runStart.IsZero() {
		pills = append(pills, core.View{TestID: "capsule-running", Style: capsuleStyle(colors),
			Children: []core.Node{
				ui.Icon(lucide.LoaderCircle, 11, colors.Accent),
				core.Text{ContentFunc: func() string {
					return "运行 " + s.runElapsedLabel()
				}, MaxLines: 1, Style: capsuleText(colors)},
			}})
	}

	// Queue: steering / follow-up prompts pi is holding.
	if s.queuesActive() {
		pills = append(pills, core.View{Role: core.RoleButton, Label: "撤回队列", TestID: "capsule-queue",
			OnPress: func(core.PressEvent) { s.recallQueue() },
			Style:   capsuleStyle(colors),
			Children: []core.Node{
				ui.Icon(lucide.ListChecks, 11, colors.Warn),
				core.Text{ContentFunc: s.queues.Get, MaxLines: 1, Style: capsuleText(colors)},
			}})
	}

	// Disconnected: the loudest state, with the reconnect action inline. While
	// the automatic restart is in flight the pill says so instead of throwing
	// the same red 未连接 at a child that is already on its way back.
	if !s.connected.Get() {
		if s.connecting || s.reconnecting.Get() {
			// A spawn or an automatic restart is in flight; a normal start-up
			// must not flash 连接中/未连接 at the user.
		} else if !s.nodeReady.Get() {
			// No runtime yet: the first launch is downloading Node, which looks
			// exactly like a dropped link. Say what it is doing and keep the
			// retry next to it.
			pills = append(pills, core.View{TestID: "capsule-node", Style: capsuleStyle(colors),
				Children: []core.Node{
					ui.Icon(lucide.LoaderCircle, 11, colors.Warn),
					core.Text{ContentFunc: s.nodeStatus.Get, MaxLines: 1, Style: capsuleText(colors)},
					ui.Button{Text: "重试", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "capsule-node-retry",
						OnPress: s.reconnect},
				}})
		} else {
			pills = append(pills, core.View{TestID: "capsule-offline", Style: capsuleStyle(colors),
				StyleFunc: func() core.Style {
					return core.Style{BorderColor: ui.Alpha(colors.Danger(), 0.5)}
				},
				Children: []core.Node{
					ui.Icon(lucide.CircleAlert, 11, colors.Danger),
					core.Text{Content: "未连接", MaxLines: 1, Style: capsuleText(colors)},
					ui.Button{Text: "重连", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "capsule-reconnect",
						OnPress: s.reconnect},
				}})
		}
	}

	// An idle, connected, clean session says nothing, but the panel still
	// needs a door: one faint chip with the idle dot.
	if len(pills) == 0 {
		pills = append(pills, core.View{Style: capsuleStyle(colors), Children: []core.Node{
			ui.Icon(lucide.Activity, 11, colors.Up),
			core.Text{Content: "空闲", MaxLines: 1, Style: capsuleText(colors)},
		}})
	}
	return pills
}

// capsuleStyle is one pill: quiet surface, hairline border, full rounding.
func capsuleStyle(colors ui.Colors) core.Style {
	return core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5,
		Height: core.Pt(24), Padding: core.Symmetric(0, 9), CornerRadius: radiusCard,
		Background: ui.Alpha(colors.Text(), 0.05), BorderWidth: 1,
		BorderColor: colors.Border()}
}

func capsuleText(colors ui.Colors) core.Style {
	return core.Style{FontSize: fontMeta, Color: colors.Muted()}
}

// chatEmptyActions gives the empty transcript somewhere to go: the old one
// only spoke and offered nothing to press.
func (s *appState) chatEmptyActions() core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Gap: 8, JustifyContent: core.JustifyCenter}, Children: []core.Node{
		ui.Button{Text: "打开项目…", Variant: ui.ButtonSecondary, TestID: "empty-open-project", OnPress: s.openProjectPicker},
		ui.Button{Text: "新建会话", Variant: ui.ButtonPrimary, TestID: "empty-new-session", OnPress: s.newSession},
		ui.Button{Text: "重连", Variant: ui.ButtonGhost, TestID: "empty-reconnect", OnPress: s.reconnect},
	}}
}
