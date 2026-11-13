package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
)

// The transcript's error surfaces. turnErrorCard (ui_chat.go) explains a hard
// failure of one turn; the cards here cover the two failures a turn cannot:
// the connection itself dropping, and a tool call failing inside a run that
// otherwise finished.

// agentLogPath is the subprocess log the transport appends to, so the
// disconnect card can hand it to the system viewer.
func (s *appState) agentLogPath() string {
	return filepath.Join(s.agentDir, "pigo", "agent.log")
}

// logAgent appends one line to the same file the transports write. A window
// that says 未连接 has to leave a trace: without this the log held the child's
// spawn and exit but not whether the app ever got a reply, so every report of
// "it is not connected" ended in guesswork.
func (s *appState) logAgent(format string, args ...any) {
	path := s.agentLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	_, _ = f.WriteString(line)
}

// disconnectCard sits above the composer while the agent link is down: a
// transcript that has stopped answering should say why instead of waiting
// silently for the next send to fail.
func (s *appState) disconnectCard() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if s.connected.Get() {
			return nil
		}
		// The automatic restart is already working on it: the card is for a
		// link that stayed down, not for the second it takes to come back.
		if s.reconnecting.Get() {
			return nil
		}
		// A window that never connected has nothing to reattach; the empty
		// conversation already explains itself.
		if s.selected.Get() == "" && len(s.turns.Get()) == 0 && s.current() == nil {
			return nil
		}
		colors := ui.ColorsOf(cx)
		return core.View{
			TestID: "disconnect-card",
			Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth),
				Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(8, 12),
				CornerRadius: 10, BorderWidth: 1, Background: ui.Alpha(colors.Danger(), 0.06)},
			StyleFunc: func() core.Style { return core.Style{BorderColor: ui.Alpha(colors.Danger(), 0.35)} },
			Children: []core.Node{
				ui.Icon(lucide.Unplug, 14, colors.Danger),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					core.Text{Content: "与 agent 的连接已断开", MaxLines: 1,
						Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
					core.Text{ContentFunc: func() string {
						if s.lastDisconnect != "" {
							return s.lastDisconnect
						}
						return "发送消息前请先重连；日志里有子进程退出的原因"
					}, MaxLines: 2,
						Style: core.Style{FontSize: 10.5, Color: colors.Muted()}},
				}},
				// A folder macOS will not open is fixed in System Settings, not in
				// this window: put the pane one press away.
				core.Show{When: func() bool { return strings.Contains(s.lastDisconnect, "隐私与安全性") }, Then: func(*core.Scope) core.Node {
					return ui.Button{Text: "打开隐私设置", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "disconnect-privacy",
						OnPress: func() {
							s.cx.OpenURL("x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders", func(bool) {})
						}}
				}},
				ui.Button{Text: "查看日志", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "disconnect-log",
					OnPress: func() { s.openExternal(s.agentLogPath(), "text/plain") }},
				ui.Button{Text: "重连", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "disconnect-reconnect",
					OnPress: s.reconnect},
			},
		}
	})
}

// failedToolBody wraps a failed call's expanded body in the red gutter and
// keeps the stderr it returned: the status icon says "failed", the body has
// to say what it failed with.
func (s *appState) failedToolBody(t toolRow, inner chat.View) chat.View {
	return func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		var body core.Node
		switch {
		case inner != nil:
			body = core.Func(inner)
		case strings.TrimSpace(t.Partial) != "":
			body = core.Text{Content: t.Partial, Selectable: core.SelectableOn,
				Style: core.Style{FontSize: 11.5, LineHeight: 17, FontFamily: core.FontMonospace, Color: colors.Muted()}}
		default:
			body = core.Text{Content: "工具失败，没有返回输出", Style: core.Style{FontSize: 11.5, Color: colors.Muted()}}
		}
		return core.View{
			TestID: "tool-failed-" + t.ID,
			Style: core.Style{Borders: core.Edges{Left: 2}, Padding: core.Edges{Left: 8}, Gap: 4,
				FlexShrink: 1, MinWidth: core.Pt(0)},
			StyleFunc: func() core.Style { return core.Style{BorderColors: core.EdgeColors{Left: colors.Danger()}} },
			Children: []core.Node{
				core.Text{Content: "stderr", MaxLines: 1,
					Style: core.Style{FontSize: 10, LetterSpacing: 0.4, Color: colors.Danger()}},
				body,
			},
		}
	}
}
