package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
)

// The transcript used to draw every tool call with one generic row: the
// formatted arguments on top, the output below. That reads fine for bash and
// edits, but a read, a grep, a todo list and an MCP call all look the same,
// and the one fact the user wants (which file, how many hits, what is left to
// do, which server) is buried in the output. Each family below owns its row
// and its expanded body; a call whose arguments cannot answer that falls back
// to the generic card instead of inventing a summary.

// toolView is a family's card: the collapsed row and the expanded body.
type toolView struct {
	action, detail string
	icon           func() *core.SVG
	mono           bool
	content        chat.View
}

func (v toolView) activity(t toolRow) chat.Activity {
	return chat.Activity{
		ID: t.ID, Action: v.action, Detail: v.detail, Icon: v.icon,
		Status: toolStatus(t), Mono: v.mono, Content: v.content,
	}
}

// toolViewOf picks a tool family by its registered name, as pi names its
// tools: read/write/edit, grep/glob, todo, web_fetch/web_search, task/agent
// and the MCP server namespace. ok is false when the family has nothing
// factual to say about this call.
func (s *appState) toolViewOf(t toolRow) (toolView, bool) {
	name := strings.ToLower(t.Name)
	switch {
	case strings.HasPrefix(name, "mcp__"):
		return mcpToolView(t)
	case strings.Contains(name, "todo"):
		return todoToolView(t)
	case strings.Contains(name, "web_fetch"), strings.Contains(name, "webfetch"):
		return fetchToolView(t)
	case strings.Contains(name, "web_search"), strings.Contains(name, "websearch"):
		return searchToolView(t)
	case strings.Contains(name, "task"), strings.Contains(name, "agent"), strings.Contains(t.Action, "子代理"):
		return agentToolView(t)
	case strings.Contains(name, "read"):
		return readToolView(t)
	case strings.Contains(name, "grep"):
		return grepToolView(t)
	case strings.Contains(name, "glob"):
		return globToolView(t)
	}
	return toolView{}, false
}

// readToolView names the file and, when the call carried them, its line
// range: "src/app.ts:120-180" is what the reader wants, not the whole path
// plus a wall of output.
func readToolView(t toolRow) (toolView, bool) {
	path := argText(t.Args, "path", "file_path")
	if path == "" {
		return toolView{}, false
	}
	return toolView{action: "读取", detail: readRangeLabel(t, path), icon: lucide.FileText,
		mono: true, content: toolContent(t)}, true
}

// readRangeLabel is a path with the offset/limit the call asked for. An
// offset of 0 carries no line number, so only the line count is shown then
// rather than inventing "line 0".
func readRangeLabel(t toolRow, path string) string {
	from, hasFrom := argInt(t.Args, "offset", "start_line", "startLine")
	limit, hasLimit := argInt(t.Args, "limit", "lines")
	switch {
	case hasFrom && from > 0 && hasLimit && limit > 1:
		return fmt.Sprintf("%s:%d-%d", path, from, from+limit-1)
	case hasFrom && from > 0:
		return fmt.Sprintf("%s:%d", path, from)
	case hasLimit:
		return fmt.Sprintf("%s · %d 行", path, limit)
	}
	return path
}

// grepToolView leads with the pattern and the hit count the output carries.
func grepToolView(t toolRow) (toolView, bool) {
	pattern := argText(t.Args, "pattern", "query")
	if pattern == "" {
		return toolView{}, false
	}
	detail := `"` + truncateRunes(pattern, 60) + `"`
	if n := countNonEmptyLines(t.Partial); n > 0 {
		detail += fmt.Sprintf(" · %d 处", n)
	}
	return toolView{action: "搜索", detail: detail, icon: lucide.Search, mono: true, content: toolContent(t)}, true
}

// globToolView leads with the pattern and how many paths it returned.
func globToolView(t toolRow) (toolView, bool) {
	pattern := argText(t.Args, "pattern", "glob")
	if pattern == "" {
		return toolView{}, false
	}
	detail := `"` + truncateRunes(pattern, 60) + `"`
	if n := countNonEmptyLines(t.Partial); n > 0 {
		detail += fmt.Sprintf(" · %d 个文件", n)
	}
	return toolView{action: "查找", detail: detail, icon: lucide.FolderSearch, mono: true, content: toolContent(t)}, true
}

// todoEntry is one item of a todo list, normalized from whatever shape the
// tool's arguments use.
type todoEntry struct {
	text         string
	done, active bool
}

// todoToolView shows what is being worked on and the progress, and expands
// to the whole list: the plan is the content, not a side effect.
func todoToolView(t toolRow) (toolView, bool) {
	items := todoEntries(t.Args)
	if len(items) == 0 {
		return toolView{}, false
	}
	done := 0
	current := ""
	for _, item := range items {
		if item.done {
			done++
		}
		if item.active && current == "" {
			current = item.text
		}
	}
	detail := fmt.Sprintf("%d/%d", done, len(items))
	if current != "" {
		detail += " · " + truncateRunes(current, 60)
	}
	return toolView{action: "计划", detail: detail, icon: lucide.ListTodo,
		mono: false, content: todoContent(items)}, true
}

func todoEntries(args map[string]any) []todoEntry {
	if args == nil {
		return nil
	}
	raw, ok := args["todos"]
	if !ok {
		raw = args["items"]
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]todoEntry, 0, len(list))
	for _, v := range list {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}
		text := stringField(item, "content", "text", "title", "task")
		if text == "" {
			continue
		}
		status := strings.ToLower(stringField(item, "status", "state"))
		done := status == "completed" || status == "done" || status == "checked"
		active := status == "in_progress" || status == "in-progress" || status == "active" || status == "running"
		if v, ok := item["completed"].(bool); ok && v {
			done = true
		}
		out = append(out, todoEntry{text: text, done: done, active: active})
	}
	return out
}

// todoContent is the expanded checklist.
func todoContent(items []todoEntry) chat.View {
	return func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		rows := make([]core.Node, 0, len(items))
		for _, item := range items {
			icon, tint := lucide.Circle, colors.Muted
			text := core.Style{FontSize: 11.5, Color: colors.Text()}
			switch {
			case item.done:
				icon, text = lucide.CircleCheck, core.Style{FontSize: 11.5, Color: colors.Faint()}
			case item.active:
				icon, tint = lucide.CircleDot, colors.Accent
			}
			rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 6}, Children: []core.Node{
				ui.Icon(icon, 12, tint),
				core.Text{Content: item.text, Selectable: core.SelectableOn, Style: text},
			}})
		}
		return core.View{TestID: "tool-todo", Style: core.Style{Gap: 4}, Children: rows}
	}
}

// fetchToolView shows the URL it fetched (its host leads the row).
func fetchToolView(t toolRow) (toolView, bool) {
	url := argText(t.Args, "url", "uri")
	if url == "" {
		return toolView{}, false
	}
	return toolView{action: "抓取", detail: truncateRunes(url, 90), icon: lucide.Globe,
		mono: true, content: toolContent(t)}, true
}

// searchToolView shows the query and, when the results carried one, the
// first link they point at.
func searchToolView(t toolRow) (toolView, bool) {
	query := argText(t.Args, "query", "q")
	if query == "" {
		if queries, ok := t.Args["queries"].([]any); ok && len(queries) > 0 {
			parts := make([]string, 0, len(queries))
			for _, q := range queries {
				if s, ok := q.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			query = strings.Join(parts, ", ")
		}
	}
	if query == "" {
		return toolView{}, false
	}
	detail := truncateRunes(query, 60)
	if link := firstLink(t.Partial); link != "" {
		detail += " · " + truncateRunes(link, 60)
	}
	return toolView{action: "搜索网页", detail: detail, icon: lucide.Globe, mono: false, content: toolContent(t)}, true
}

// agentToolView names the subagent and its state.
func agentToolView(t toolRow) (toolView, bool) {
	name := argText(t.Args, "agent", "subagent", "description", "name", "prompt")
	if name == "" {
		return toolView{}, false
	}
	return toolView{action: "子代理", detail: truncateRunes(name, 60) + " · " + toolStateLabel(t.Status),
		icon: lucide.Bot, mono: false, content: toolContent(t)}, true
}

// mcpToolView splits the registered `mcp__server__tool` name into the pair
// pi's own TUI labels the call with.
func mcpToolView(t toolRow) (toolView, bool) {
	rest := strings.TrimPrefix(t.Name, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return toolView{}, false
	}
	detail := parts[0] + " / " + parts[1]
	if summary := toolSummary(t); summary != "" && !strings.Contains(strings.ToLower(summary), strings.ToLower(t.Name)) {
		detail += " · " + truncateRunes(summary, 50)
	}
	return toolView{action: "MCP", detail: detail, icon: lucide.Plug, mono: true, content: toolContent(t)}, true
}

// toolStateLabel names a tool row's status in the words the cards use.
func toolStateLabel(status string) string {
	switch status {
	case "running", "pending":
		return "运行中"
	case "failed":
		return "失败"
	case "cancelled":
		return "已取消"
	}
	return "完成"
}

// toolCardSubject is what a grouped read-only row shows for one call: a
// read's line range when it has one, its subject otherwise.
func toolCardSubject(t toolRow) string {
	if strings.Contains(strings.ToLower(t.Name), "read") {
		if path := argText(t.Args, "path", "file_path"); path != "" {
			return readRangeLabel(t, path)
		}
	}
	return toolSubject(t)
}

// argText returns the first non-empty string argument under one of keys.
func argText(args map[string]any, keys ...string) string {
	if args == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// argInt returns the first numeric argument under one of keys.
func argInt(args map[string]any, keys ...string) (int, bool) {
	if args == nil {
		return 0, false
	}
	for _, key := range keys {
		switch v := args[key].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

// stringField is argText for a map read out of decoded JSON.
func stringField(m map[string]any, keys ...string) string { return argText(m, keys...) }

// countNonEmptyLines is the number of non-blank lines in a tool's output.
func countNonEmptyLines(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// firstLink is the first http(s) URL in a block of text, trimmed of the
// punctuation that usually follows one.
func firstLink(text string) string {
	for _, field := range strings.Fields(text) {
		if !strings.HasPrefix(field, "http://") && !strings.HasPrefix(field, "https://") {
			continue
		}
		return strings.TrimRight(field, ").,;:'\"[]<>")
	}
	return ""
}

// nestedRowLimit caps how many codemode sub-calls a card lists; anything
// beyond it collapses into one trailing "… 还有 N 条" line.
const nestedRowLimit = 30

// nestedToolsView draws a parent card's codemode sub-calls below its own
// content: one summary line each (status mark · action · detail), 11.5pt mono,
// never a button — the list is a log, not a set of controls. The scroll lives
// on the activity's bounded detail, so the rows stay plain.
func nestedToolsView(t toolRow, above chat.View) chat.View {
	return func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		var rows []core.Node
		if above != nil {
			if node := above(cx); node != nil {
				rows = append(rows, node)
			}
		}
		shown := t.Nested
		if len(shown) > nestedRowLimit {
			shown = shown[:nestedRowLimit]
		}
		list := make([]core.Node, 0, len(shown)+1)
		for _, n := range shown {
			icon, tint := nestedStatusMark(n, colors)
			label := strings.TrimSpace(n.Action)
			if d := firstLine(n.Detail, 90); d != "" {
				if label != "" {
					label += " · "
				}
				label += d
			}
			list = append(list, core.View{
				TestID: "tool-nested-" + n.ID,
				Style:  core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 6},
				Children: []core.Node{
					ui.Icon(icon, 11, tint),
					core.Text{Content: label, Selectable: core.SelectableOn,
						Style: core.Style{FontSize: 11.5, FontFamily: core.FontMonospace,
							Color: nestedRowColor(n, colors)}},
				},
			})
		}
		if more := len(t.Nested) - len(shown); more > 0 {
			list = append(list, core.Text{Content: fmt.Sprintf("… 还有 %d 条", more),
				Style: core.Style{FontSize: 11, Color: colors.Faint()}})
		}
		rows = append(rows, core.View{
			TestID:   "tool-nested",
			Style:    core.Style{Gap: 4, MinWidth: core.Pt(0)},
			Children: list,
		})
		return core.View{Style: core.Style{Gap: 8, MinWidth: core.Pt(0)}, Children: rows}
	}
}

// nestedStatusMark is the mark and its tint for one nested call: an accent
// spinner while it runs, a red cross when it failed, a dash when cancelled and
// a green check once it completed.
func nestedStatusMark(n nestedToolRow, colors ui.Colors) (func() *core.SVG, func() core.Color) {
	switch {
	case n.IsError || n.Status == "failed":
		return lucide.X, colors.Danger
	case n.Status == "cancelled":
		return lucide.Minus, colors.Faint
	case n.Status == "running", n.Status == "pending":
		return lucide.LoaderCircle, colors.Accent
	}
	return lucide.Check, colors.Up
}

// nestedRowColor is the row's text color, following the same status logic:
// red for a failure, faint for a cancel, full text while running and muted
// once settled.
func nestedRowColor(n nestedToolRow, colors ui.Colors) core.Color {
	switch {
	case n.IsError || n.Status == "failed":
		return colors.Danger()
	case n.Status == "cancelled":
		return colors.Faint()
	case n.Status == "running", n.Status == "pending":
		return colors.Text()
	}
	return colors.Muted()
}
