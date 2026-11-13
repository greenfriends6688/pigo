package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
	uidiff "github.com/oneo-me/nsgo/ui/diff"

	"pigo/internal/toolfields"
)

// toolActivity is one tool card in the transcript's work step: a one-line
// summary (verb, subject, ±lines, status) that expands into a diff, the
// tool's output or its raw parameters. Edit/write calls render their
// arguments as a unified patch with ui/diff — the component was always
// there, the transcript just never wired it up. Every family with a better
// summary (read, grep, todo, web, task, MCP) is drawn by ui_toolview.go;
// the generic card here is the fallback, never a guess.
func (s *appState) toolActivity(t toolRow) chat.Activity {
	act := chat.Activity{
		ID:     t.ID,
		Action: toolVerb(t),
		Detail: toolSummary(t),
		Icon:   toolIcon(t),
		Status: toolStatus(t),
		Mono:   true,
	}
	switch {
	case t.Diff != "":
		act.Detail = fmt.Sprintf("%s  +%d −%d", toolSubject(t), t.Add, t.Del)
		act.Icon = lucide.FileDiff
		act.Mono = false
		act.MaxHeight = 340
		act.Content = func(*core.Scope) core.Node {
			return uidiff.View{
				Patch:        func() string { return t.Diff },
				Unified:      true,
				ContextLines: 3,
				Selectable:   true,
				FontSize:     11.5,
				TestID:       "tool-diff-" + t.ID,
			}
		}
	default:
		if card, ok := s.toolViewOf(t); ok {
			act = card.activity(t)
		} else {
			act.Content = toolContent(t)
		}
	}
	// A failed call keeps its stderr and gains the red gutter the error card
	// uses, so a wall of output shows where it broke.
	if t.Status == "failed" || t.IsError {
		act.Content = s.failedToolBody(t, act.Content)
	}
	// A codemode card lists its nested calls below whatever it already shows.
	// The count leads the summary line so the fan-out is visible collapsed.
	if len(t.Nested) > 0 {
		count := fmt.Sprintf("%d 次调用", len(t.Nested))
		if act.Detail != "" {
			act.Detail = count + " · " + act.Detail
		} else {
			act.Detail = count
		}
		act.Content = nestedToolsView(t, act.Content)
		act.MaxHeight = 360
	}
	return act
}

// toolVerb is the short verb the summary row leads with.
func toolVerb(t toolRow) string {
	a := t.Action
	name := strings.ToLower(t.Name)
	switch {
	case strings.HasPrefix(a, "Read"), strings.HasPrefix(a, "读"), strings.Contains(name, "read"):
		return "读取"
	case strings.HasPrefix(a, "Write"), strings.HasPrefix(a, "写"), strings.Contains(name, "write"):
		return "写入"
	case strings.HasPrefix(a, "Edit"), strings.HasPrefix(a, "编辑"), strings.Contains(name, "edit"):
		return "编辑"
	case strings.HasPrefix(a, "Bash"), strings.Contains(a, "shell"), strings.Contains(name, "bash"), strings.Contains(name, "shell"):
		return "运行"
	case strings.HasPrefix(a, "Grep"), strings.HasPrefix(a, "Glob"), strings.Contains(name, "grep"), strings.Contains(name, "glob"):
		return "搜索"
	case strings.Contains(name, "todo"):
		return "计划"
	case strings.HasPrefix(a, "Web"):
		return "访问"
	case strings.Contains(name, "task"), strings.Contains(name, "agent"):
		return "子代理"
	}
	return a
}

// toolSubject is the path, command or pattern the card is about — the chip
// inside the summary row.
func toolSubject(t toolRow) string {
	if t.Args != nil {
		for _, key := range []string{"path", "file_path", "command", "pattern", "query", "url", "description", "agent", "subagent"} {
			if v, ok := t.Args[key].(string); ok && v != "" {
				return v
			}
		}
	}
	if t.Detail != "" {
		return firstLine(t.Detail, 80)
	}
	return t.Action
}

// toolSummary is the collapsed row's trailing text: the args preview.
func toolSummary(t toolRow) string {
	if t.Detail != "" {
		return firstLine(t.Detail, 90)
	}
	return ""
}

// isReadOnlyTool reports whether a tool only looks at things: the family
// whose consecutive completed members collapse into one counted row. Web,
// MCP, todo and subagent cards carry their own summaries, so folding them
// into a read count would throw those away.
func isReadOnlyTool(t toolRow) bool {
	name := strings.ToLower(t.Name)
	switch {
	case strings.HasPrefix(name, "mcp__"), strings.Contains(name, "todo"),
		strings.Contains(name, "web"), strings.Contains(name, "task"), strings.Contains(name, "agent"):
		return false
	case strings.Contains(name, "read"), strings.Contains(name, "grep"),
		strings.Contains(name, "glob"), strings.Contains(name, "search"),
		strings.HasPrefix(t.Action, "Read"), strings.HasPrefix(t.Action, "读"),
		strings.HasPrefix(t.Action, "Grep"), strings.HasPrefix(t.Action, "Glob"):
		return true
	}
	return false
}

// groupReadOnly collapses a run of consecutive completed read-only tools
// into one summary activity ("读取了 5 个文件") that expands to each call —
// the same降噪 ZCode's ConsecutiveSummaryGroup does. A run shorter than
// three stays individual, and any non-completed or diff-bearing call
// always renders on its own.
func (s *appState) groupReadOnly(tools []toolRow) []chat.Activity {
	var out []chat.Activity
	i := 0
	for i < len(tools) {
		t := tools[i]
		if !isReadOnlyTool(t) || t.Status != "completed" || t.Diff != "" {
			out = append(out, s.toolActivity(t))
			i++
			continue
		}
		j := i
		for j < len(tools) && isReadOnlyTool(tools[j]) && tools[j].Status == "completed" && tools[j].Diff == "" {
			j++
		}
		if j-i < 3 {
			for k := i; k < j; k++ {
				out = append(out, s.toolActivity(tools[k]))
			}
			i = j
			continue
		}
		group := append([]toolRow(nil), tools[i:j]...)
		act := chat.Activity{
			ID:     "group-" + group[0].ID,
			Action: toolVerb(group[0]) + fmt.Sprintf("了 %d 个", len(group)),
			Detail: toolSubject(group[0]),
			Icon:   toolIcon(group[0]),
			Status: chat.Completed,
			Mono:   false,
			Content: func(cx *core.Scope) core.Node {
				rows := make([]core.Node, 0, len(group))
				for _, g := range group {
					rows = append(rows, core.Text{
						Content: toolCardSubject(g), Selectable: core.SelectableOn,
						Style: core.Style{FontSize: 11.5, FontFamily: core.FontMonospace,
							Color: ui.ColorsOf(cx).Muted()},
					})
				}
				return core.View{Style: core.Style{Gap: 2}, Children: rows}
			},
		}
		out = append(out, act)
		i = j
	}
	return out
}

// toolContent is the expanded detail of a non-diff tool: the output when
// it returned, the formatted arguments while it runs.
func toolContent(t toolRow) chat.View {
	return func(cx *core.Scope) core.Node {
		body := t.Partial
		if strings.TrimSpace(body) == "" {
			body = t.Detail
		}
		if strings.TrimSpace(body) == "" && t.Args != nil {
			if action, detail := toolfields.Format(t.Name, t.Args); detail != "" {
				body = action + "\n" + detail
			}
		}
		if strings.TrimSpace(body) == "" {
			return nil
		}
		return core.Scroll{Style: core.Style{FlexGrow: 0, FlexShrink: 1, MaxHeight: core.Pt(280)}, Children: []core.Node{
			core.Text{Content: body, Selectable: core.SelectableOn,
				Style: core.Style{FontSize: 11.5, LineHeight: 17, FontFamily: core.FontMonospace, Color: ui.ColorsOf(cx).Muted()}},
		}}
	}
}
