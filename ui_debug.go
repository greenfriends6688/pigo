package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"
)

// debugTicks is the refresh pulse of the diagnostics dialog, one counter per
// window scope. It lives here, not in appState, so the state surface gains
// only debugOpen; the sync.Map mirrors panelLayouts in project.go.
var debugTicks sync.Map // *core.Scope -> *core.Signal[int]

func (s *appState) debugTick() *core.Signal[int] {
	if v, ok := debugTicks.Load(s.cx); ok {
		return v.(*core.Signal[int])
	}
	sig := core.NewSignal(s.cx, 0)
	actual, _ := debugTicks.LoadOrStore(s.cx, sig)
	return actual.(*core.Signal[int])
}

// debugDialog is a read-only snapshot of what the window and the agent
// currently think, for pasting into a bug report.
func (s *appState) debugDialog() core.Node {
	return ui.Dialog{
		Open:         s.debugOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.debugOpen.Set(open) },
		Title:        "诊断信息",
		Description:  "窗口与 Agent 的只读快照；刷新只重新采集，不改任何状态。",
		Style:        core.Style{Width: core.Pt(680)},
		Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
			if !s.debugOpen.Get() {
				return core.View{}
			}
			// Reading the pulse rebuilds the text on「刷新」; every signal the
			// snapshot reads also keeps it live while the dialog is open.
			_ = s.debugTick().Get()
			return core.Scroll{Style: core.Style{MaxHeight: core.Pt(460)}, Children: []core.Node{
				core.Text{
					Content:    s.debugSnapshot(),
					TestID:     "debug-text",
					Selectable: core.SelectableOn,
					Style:      core.Style{FontSize: 12, FontFamily: core.FontMonospace, Color: s.colors.Text()},
				},
			}}
		})},
		Footer: []core.Node{
			ui.Button{Text: "刷新", Variant: ui.ButtonSecondary, TestID: "debug-refresh", OnPress: func() {
				tick := s.debugTick()
				tick.Set(tick.GetUntracked() + 1)
			}},
			ui.Button{Text: "复制", Variant: ui.ButtonPrimary, TestID: "debug-copy", OnPress: func() {
				s.cx.SetClipboard(s.debugSnapshot())
				s.notify(ui.ToastInfo, "已复制诊断信息", "")
			}},
		},
		TestID: "debug-dialog",
	}
}

// debugSnapshot renders the panel's text from the current signals.
func (s *appState) debugSnapshot() string {
	var b strings.Builder
	line := func(key, value string) {
		if value == "" {
			value = "—"
		}
		fmt.Fprintf(&b, "%-12s%s\n", key, value)
	}
	section := func(title string) {
		fmt.Fprintf(&b, "\n[ %s ]\n", title)
	}

	section("环境")
	line("版本", appVersion())
	line("Agent 目录", s.agentDir)
	node := s.nodePath
	if node == "" {
		node = "未解析"
	}
	line("Node", node)
	line("Node 状态", s.nodeStatus.Get())

	section("会话")
	line("当前会话", s.currentSessionPath())
	line("cwd", s.currentCwd())
	line("项目根", s.projectRoot())
	connected := "未连接"
	if s.connected.Get() {
		connected = "已连接"
	}
	line("连接", connected)
	runState := "否"
	if s.running.Get() {
		runState = "是"
	}
	line("运行中", runState)

	section("模型")
	line("模型", s.modelName.Get())
	thinking := s.thinking.Get()
	if thinking == "" {
		thinking = "默认"
	}
	line("思考等级", thinking)
	used, window := s.ctxUsed.Get(), s.ctxWindow.Get()
	if window > 0 {
		line("上下文", fmt.Sprintf("%d / %d (%d%%)", used, window, used*100/window))
	} else {
		line("上下文", "未知")
	}

	section("对话")
	turns := s.turns.Get()
	line("消息/轮数", fmt.Sprintf("%d 轮（显示 %d）", s.turnTotal.Get(), len(turns)))
	runningTools := 0
	for _, t := range turns {
		for _, tool := range t.Tools {
			if tool.Status == "running" {
				runningTools++
			}
		}
	}
	line("运行中工具", fmt.Sprint(runningTools))
	line("队列", s.queues.Get())

	section("其它")
	line("终端标签", s.debugTerminalTabs())
	line("子代理", fmt.Sprint(len(s.subItems.Get())))
	git := strings.TrimSpace(s.gitText.Get())
	if git == "" {
		git = "未加载"
	}
	line("Git", git)

	b.WriteString("\n只读快照 · 点「刷新」重新采集\n")
	return b.String()
}

// debugTerminalTabs counts the open shells without creating any; the
// terminal manager only exists once the dock has shown the terminal.
func (s *appState) debugTerminalTabs() string {
	if s.termTabs == nil {
		return "终端状态不可用"
	}
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return fmt.Sprint(len(s.termIDsLocked()))
}
