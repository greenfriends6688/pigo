package chatview

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui/chat"
	"pigo/agio/agent"
	"pigo/agio/chatview/locales"
	"pigo/agio/transcript"
)

// activityKind maps tool names, which drivers align with pi's built-in
// tools, to the kinds activities are grouped and counted by.
func activityKind(s *transcript.Segment) string {
	if s.Kind == transcript.Thinking {
		return "think"
	}
	switch strings.ToLower(s.Tool) {
	case "bash":
		return "run"
	case "read":
		return "read"
	case "edit", "write":
		return "edit"
	case "grep", "find", "web_search":
		return "search"
	case "ls":
		return "list"
	}
	return "tool"
}

func actionName(m locales.Messages, kind string) string {
	switch kind {
	case "think":
		return m.ActionThink()
	case "run":
		return m.ActionRun()
	case "read":
		return m.ActionRead()
	case "edit":
		return m.ActionEdit()
	case "search":
		return m.ActionSearch()
	case "list":
		return m.ActionList()
	}
	return m.ActionTool()
}

func activityCount(m locales.Messages, kind string, count int) string {
	switch kind {
	case "run":
		return m.CountRun(count)
	case "read":
		return m.CountRead(count)
	case "edit":
		return m.CountEdit(count)
	case "search":
		return m.CountSearch(count)
	case "list":
		return m.CountList(count)
	}
	return m.CountTool(count)
}

// groupTitle summarizes a group, such as "Ran 2 commands · 1 file read".
func groupTitle(m locales.Messages, group []*transcript.Segment, running bool) string {
	counts := map[string]int{}
	var order []string
	pending := false
	for _, s := range group {
		kind := activityKind(s)
		if kind == "think" {
			continue // reasoning is not something the agent ran
		}
		if counts[kind] == 0 {
			order = append(order, kind)
		}
		counts[kind]++
		pending = pending || s.Status == transcript.Running
	}
	if len(order) == 0 {
		return m.Thought()
	}
	var parts []string
	for _, kind := range order {
		parts = append(parts, activityCount(m, kind, counts[kind]))
	}
	activities := strings.Join(parts, " · ")
	if running && pending {
		return m.Running(activities)
	}
	return m.Ran(activities)
}

// steps groups consecutive thinking and tool segments into activity
// groups; intermediate assistant text stays prose between them.
func steps(m locales.Messages, t *transcript.Turn, markdown func(text string) chat.View) []chat.Step {
	final := t.Final()
	running := t.Status == transcript.Running
	var out []chat.Step
	var group []*transcript.Segment
	flush := func() {
		if len(group) == 0 {
			return
		}
		step := chat.Step{ID: "g-" + group[0].ID, Title: groupTitle(m, group, running)}
		for _, s := range group {
			step.Activities = append(step.Activities, activity(m, s))
		}
		out = append(out, step)
		group = nil
	}
	for _, s := range t.Segments {
		if s == final {
			continue
		}
		if s.Kind == transcript.Text {
			flush()
			out = append(out, chat.Step{ID: s.ID, Message: markdown(s.Text)})
			continue
		}
		group = append(group, s)
	}
	flush()
	if n := len(out); n > 0 && running && len(out[n-1].Activities) > 0 {
		// A live group shows the latest activity as its title.
		if last := t.Segments[len(t.Segments)-1]; last.Kind != transcript.Text {
			out[n-1].Title = liveTitle(m, last)
		}
	}
	return out
}

func activity(m locales.Messages, s *transcript.Segment) chat.Activity {
	kind := activityKind(s)
	act := chat.Activity{ID: s.ID, Action: actionName(m, kind), Status: chatStatus(s.Status)}
	body := s.Text
	switch kind {
	case "think":
		act.Icon, act.Detail = lucide.Brain, agent.FirstLine(s.Text)
		act.Content = func(cx *core.Scope) core.Node { return segmentBody(cx, body, false) }
		return act
	case "run":
		act.Icon, act.Detail = lucide.Terminal, s.Preview
	case "read":
		act.Icon, act.Detail = lucide.FileText, pathBase(s.Preview)
	case "edit":
		act.Icon, act.Detail = lucide.FilePen, pathBase(s.Preview)
	case "search":
		act.Icon, act.Detail = lucide.Search, s.Preview
	case "list":
		act.Icon, act.Detail = lucide.FolderOpen, s.Preview
	default:
		act.Icon, act.Detail = lucide.Wrench, strings.TrimSpace(s.Tool+" "+s.Preview)
	}
	if strings.TrimSpace(s.Text) != "" {
		act.Mono, act.MaxHeight = true, 320
		act.Content = func(cx *core.Scope) core.Node { return segmentBody(cx, body, true) }
	}
	return act
}

func liveTitle(m locales.Messages, s *transcript.Segment) string {
	if s.Kind == transcript.Thinking {
		return m.ThinkingStatus()
	}
	act := activity(m, s)
	return strings.TrimSpace(act.Action + " " + act.Detail)
}

// chatStatus is a turn's or segment's status in chat's terms.
func chatStatus(s transcript.Status) chat.Status {
	switch s {
	case transcript.Completed:
		return chat.Completed
	case transcript.Failed:
		return chat.Failed
	case transcript.Cancelled:
		return chat.Cancelled
	}
	return chat.Running
}

func duration(m locales.Messages, d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return m.DurationSeconds(int(d / time.Second))
	case d < time.Hour:
		return m.DurationMinutes(int(d/time.Minute), int(d%time.Minute)/int(time.Second))
	case d < 48*time.Hour:
		return m.DurationHours(int(d/time.Hour), int(d%time.Hour)/int(time.Minute))
	}
	return m.DurationDays(int(d / (24 * time.Hour)))
}

func thinkingLabel(m locales.Messages, level string) string {
	switch level {
	case "":
		return m.Thinking()
	case "minimal":
		return m.ThinkingMinimal()
	case "low":
		return m.ThinkingLow()
	case "medium":
		return m.ThinkingMedium()
	case "high":
		return m.ThinkingHigh()
	case "xhigh":
		return m.ThinkingXhigh()
	case "max":
		return m.ThinkingMax()
	case "ultra":
		return m.ThinkingUltra()
	}
	return level
}

// tierLabel translates the tiers whose meaning is known.
func tierLabel(m locales.Messages, t agent.Tier) string {
	switch t.ID {
	case "priority", "fast":
		return m.TierFast()
	}
	return t.Name
}

// modelGroup heads a model's tool, with the provider for pi, which routes
// to several, as in "pi · anthropic".
func modelGroup(m agent.Model) string {
	if m.Tool == agent.Pi && m.Provider != "" {
		return m.Tool.Name() + " · " + m.Provider
	}
	return m.Tool.Name()
}

func formatTokens(n int64) string {
	switch {
	case n >= 999_500:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// pathBase is a path's last element, on any system's paths.
func pathBase(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, `\`, "/"), "/")
	if p == "" {
		return ""
	}
	return path.Base(p)
}
