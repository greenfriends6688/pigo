package chatview

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/i18n"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	agio "pigo/agio"
	"pigo/agio/chatview/locales"
	"pigo/agio/transcript"
)

// draft reports whether c is a task not started yet, which New task
// composes and the sidebar leaves out.
func draft(c *agio.Conversation) bool {
	return !c.Bound && len(c.Turns) == 0 && !c.Running && !c.Starting && !c.Loading && c.Cursor == ""
}

// draftID is New task's draft, or "".
func draftID(s agio.State) string {
	for _, c := range s.Conversations {
		if draft(c) {
			return c.ID
		}
	}
	return ""
}

// running reports whether c's agent starts or works.
func running(c *agio.Conversation) bool { return c.Running || c.Starting }

// lastActive is when c was last active.
func lastActive(c *agio.Conversation) time.Time {
	if c.Updated.IsZero() {
		return c.Created
	}
	return c.Updated
}

// period groups the tasks that stopped working by local calendar day: a
// task falls into the first period it is old enough for, the last one
// unbounded.
type period struct {
	key  string
	name func(locales.Messages) string
	days int
}

var periods = []period{
	{"today", locales.Messages.Today, 0},
	{"yesterday", locales.Messages.Yesterday, 1},
	{"week", locales.Messages.PreviousWeek, 7},
	{"month", locales.Messages.PreviousMonth, 30},
	{"older", locales.Messages.Older, -1},
}

// periodOf is the index of the period of a task last active at t.
func periodOf(now, t time.Time) int {
	y, mo, d := now.Date()
	midnight := time.Date(y, mo, d, 0, 0, 0, 0, now.Location())
	for i, p := range periods {
		if p.days < 0 || !t.Before(midnight.AddDate(0, 0, -p.days)) {
			return i
		}
	}
	return len(periods) - 1
}

// taskGroup is a sidebar group's key, title and tasks.
type taskGroup struct {
	key, title string
	tasks      []*agio.Conversation
}

// taskGroups are the running tasks, then the others by period, most
// recently active first; drafts are left out and empty groups too.
func taskGroups(msgs locales.Messages, s agio.State, now time.Time) []taskGroup {
	var list []*agio.Conversation
	for _, c := range s.Conversations {
		if !draft(c) {
			list = append(list, c)
		}
	}
	slices.SortStableFunc(list, func(x, y *agio.Conversation) int { return lastActive(y).Compare(lastActive(x)) })
	groups := []taskGroup{{key: "running", title: msgs.RunningTasks()}}
	for _, p := range periods {
		groups = append(groups, taskGroup{key: p.key, title: p.name(msgs)})
	}
	for _, c := range list {
		i := 0
		if !running(c) {
			i = 1 + periodOf(now, lastActive(c))
		}
		groups[i].tasks = append(groups[i].tasks, c)
	}
	return slices.DeleteFunc(groups, func(g taskGroup) bool { return len(g.tasks) == 0 })
}

// sidebar lists the tasks in collapsible groups with their counts. Its
// rows are built again only when what they show changes.
func (a App) sidebar(cx *core.Scope, selected *core.Signal[string]) core.Node {
	m := a.Model
	// closed are the groups folded away; folding bumps folds.
	closed, folds := map[string]bool{}, core.NewSignal(cx, 0)
	key := core.NewMemo(cx, live(m, func() string {
		msgs := locales.For(cx)
		var b strings.Builder
		fmt.Fprint(&b, i18n.Language(cx), ";")
		for _, g := range taskGroups(msgs, m.State(), time.Now()) {
			fmt.Fprint(&b, g.key, ":")
			for _, c := range g.tasks {
				fmt.Fprint(&b, c.ID, c.Title, c.Dir, ",")
			}
		}
		return b.String()
	}))
	sections := func() []ui.SidebarSection {
		key.Get()
		var sections []ui.SidebarSection
		cx.Untrack(func() {
			msgs := locales.For(cx)
			colors := ui.ColorsOf(cx)
			groups := taskGroups(msgs, m.State(), time.Now())
			if len(groups) == 0 {
				note := ui.Text(msgs.NoTasks(), 13, ui.Regular, colors.Muted)
				sections = []ui.SidebarSection{{Note: note}}
				return
			}
			for _, g := range groups {
				var items []ui.SidebarItem
				for _, c := range g.tasks {
					items = append(items, a.taskItem(msgs, c))
				}
				sections = append(sections, ui.SidebarSection{Title: g.title, Collapsible: true, Items: items, TestID: "tasks-" + g.key,
					Trailing: ui.Text(strconv.Itoa(len(g.tasks)), 11, ui.Regular, colors.Muted),
					Open:     func() bool { folds.Get(); return !closed[g.key] },
					OnOpenChange: func(open bool) {
						closed[g.key] = !open
						folds.Update(func(n int) int { return n + 1 })
					}})
			}
		})
		return sections
	}
	return ui.Sidebar{SectionsFunc: sections, Selected: selected.Get, OnSelect: selected.Set, TestID: "tasks"}
}

// taskItem is a task's row: its title, and below it the name of the folder
// it works in and its working time, with a context menu removing it.
func (a App) taskItem(msgs locales.Messages, c *agio.Conversation) ui.SidebarItem {
	m := a.Model
	id, title := c.ID, c.Title
	if title == "" {
		title = msgs.Untitled()
	}
	return ui.SidebarItem{ID: id, Text: title, TestID: "task-" + id, Content: taskRow{m: m, id: id, title: title, dir: pathBase(c.Dir)},
		Menu: []core.Node{ui.MenuItem{Text: msgs.RemoveConversation(), Icon: lucide.Trash, Variant: ui.MenuItemDanger,
			OnSelect: func() { m.Do(func(b agio.Backend) error { return b.Remove(id) }, nil) }}}}
}

// taskRow is a sidebar row's content. Its time is the running turn's, in
// the accent color, else the turns' together.
type taskRow struct {
	m          *Model
	id         string
	title, dir string
}

// Render implements core.Node.
func (r taskRow) Render(cx *core.Scope) core.Node {
	colors := ui.ColorsOf(cx)
	msgs := locales.For(cx)
	conv := func() *agio.Conversation { return r.m.Conversation(r.id) }
	title := ui.Text(r.title, 13.5, ui.Regular, colors.Text)
	title.MaxLines = 1
	title.Style = title.Style.Merge(core.Style{FlexShrink: 1, MinWidth: core.Pt(0)})
	dir := ui.Text(r.dir, 12, ui.Regular, colors.Muted)
	dir.MaxLines = 1
	dir.Style = dir.Style.Merge(core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)})
	worked := live(r.m, func() string {
		c := conv()
		var d time.Duration
		for _, t := range c.Turns {
			switch {
			case t.Started.IsZero():
			case t.Status == transcript.Running:
				d += r.m.now().Sub(t.Started)
			case t.Ended.After(t.Started):
				d += t.Ended.Sub(t.Started)
			}
		}
		if d <= 0 {
			return ""
		}
		return duration(msgs, d)
	})
	clock := ui.Textf(worked, 12, ui.Regular, colors.Muted)
	clock.Style.FlexShrink = 0
	clock.StyleFunc = live(r.m, func() core.Style {
		if running(conv()) {
			return core.Style{Color: colors.Accent()}
		}
		return core.Style{Color: colors.Muted()}
	})
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Padding: core.Symmetric(7, 0), Gap: 3}, Children: []core.Node{
		title,
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0)}, Children: []core.Node{dir, clock}},
	}}
}
