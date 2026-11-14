package chatview

import (
	"fmt"
	"slices"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/i18n"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/updater"
	agio "pigo/agio"
	"pigo/agio/agent"
	"pigo/agio/chatview/locales"
)

// AppVersion is the app's version, which native sets.
var AppVersion string

// settings is App's settings dialog: the backend's agents and their
// models and its updates when it has them, among the app's own categories.
func (a App) settings(cx *core.Scope, open *core.Signal[bool]) core.Node {
	m := a.Model
	category, query := a.SettingsCategory, core.NewSignal(cx, "")
	if category == nil {
		category = core.NewSignal(cx, "")
	}
	// Search matches the agents' statuses, so the page is rebuilt when they
	// change, but not as models are turned on or off, which keeps their
	// dropdown open; the page and search are held here.
	key := func() string {
		var b strings.Builder
		msgs := locales.For(cx)
		s := m.State()
		for _, t := range s.Tools {
			b.WriteString(t.Tool.Name() + toolStatus(msgs, t, s.Discovering) + fmt.Sprint(t.Ready(), len(t.Catalog.Models)) + "\n")
		}
		b.WriteString(i18n.Language(cx))
		if a.SettingsKey != nil {
			b.WriteString("\n" + a.SettingsKey())
		}
		return b.String()
	}
	return ui.SettingsDialog{Open: open.Get, OnOpenChange: func(o bool, _ base.Reason) { open.Set(o) },
		Label: locales.For(cx).Settings(), TestID: "settings-dialog",
		Content: func(narrow func() bool) core.Node {
			return region(m, key, func(cx *core.Scope) core.Node {
				msgs := locales.For(cx)
				var categories []ui.SettingsCategory
				if a.Updates {
					categories = append(categories, updateSettings(msgs, func() { updater.Check(cx) }))
				}
				if a.Settings != nil {
					categories = a.Settings(categories)
				}
				return ui.Settings{Categories: categories, Category: category, Query: query, Narrow: narrow, TestID: "settings",
					Title: msgs.Settings(), SearchLabel: msgs.SearchSettings(), SearchPlaceholder: msgs.SearchSettings(),
					ResultsTitle: msgs.SettingsResults(), EmptyText: msgs.NoMatchingSettings(),
					SectionActions: a.SettingsSectionActions,
					OnClose:        func() { open.Set(false) }, CloseLabel: msgs.CloseSettings()}
			})
		}}
}

// updateSettings shows the app's version with a button checking for a
// newer one, the only way to on Windows and Linux, which have no menu bar.
func updateSettings(msgs locales.Messages, check func()) ui.SettingsCategory {
	version := AppVersion
	if version == "" {
		version = "—"
	}
	return ui.SettingsCategory{ID: "updates", Title: msgs.Updates(), Icon: lucide.CircleArrowUp, Groups: []ui.SettingsGroup{{
		ID: "updates", Title: msgs.Updates(), Items: []ui.SettingsItem{{
			ID: "check", Title: msgs.CurrentVersion(version), Description: msgs.UpdatesDetail(),
			Keywords: []string{"update", "version", "更新", "版本"},
			Control:  ui.Button{Text: msgs.CheckForUpdates(), Variant: ui.ButtonOutline, TestID: "check-for-updates", OnPress: check},
		}}}}}
}

// agentSettings shows each agent as a card: what looking for it found,
// its models and a switch turning it on or off. Agents turned off and
// those not found come last.
func agentSettings(m *Model, msgs locales.Messages) ui.SettingsCategory {
	tools := slices.Clone(m.State().Tools)
	slices.SortStableFunc(tools, func(x, y agio.Tool) int { return toolRank(x) - toolRank(y) })
	cards := make([]core.Node, len(tools))
	for i, t := range tools {
		cards[i] = toolCard(m, msgs, t.Tool)
	}
	refresh := ui.IconButton{Icon: lucide.RefreshCw, Size: 20, IconSize: 11.375, TestID: "refresh-agents",
		LabelFunc: live(m, func() string {
			if m.State().Discovering {
				return msgs.CheckingAgents()
			}
			return msgs.RefreshAgents()
		}),
		Disabled: live(m, func() bool { return m.State().Discovering }),
		OnPress:  func() { m.Do(func(b agio.Backend) error { b.Discover(); return nil }, nil) }}
	// One item holds every card, so they sit closer than rows; its
	// description gathers their statuses for search.
	return ui.SettingsCategory{ID: "coding-agents", Title: "编码智能体", Icon: lucide.Bot, Groups: []ui.SettingsGroup{{
		ID: "agents", Title: msgs.CodingAgents(), Actions: []core.Node{refresh}, Items: []ui.SettingsItem{{
			ID: "tools", Title: msgs.CodingAgents(), Description: strings.Join(agentWords(m, msgs), " "),
			Keywords: []string{"agent", "model", "enable", "disable", "智能体", "模型", "启用", "禁用"},
			Content:  column(8, cards...),
		}}}}}
}

// agentWords are each agent's name and status, with its models' count once
// usable.
func agentWords(m *Model, msgs locales.Messages) []string {
	s := m.State()
	var words []string
	for _, t := range s.Tools {
		status := toolStatus(msgs, t, s.Discovering)
		if t.Ready() {
			status = strings.TrimPrefix(status+" · "+modelsSummary(msgs, t), " · ")
		}
		words = append(words, t.Tool.Name()+" "+status)
	}
	return words
}

// found reports whether looking for a tool found it.
func found(t agio.Tool) bool { return t.Path != "" || len(t.Catalog.Models) > 0 }

// toolRank orders a tool in the settings: in use or being looked for, then
// turned off, then not found.
func toolRank(t agio.Tool) int {
	switch {
	case t.Err != "" && !found(t):
		return 2
	case t.Disabled:
		return 1
	}
	return 0
}

// toolStatus describes what looking for a tool found, as in "0.50.0 ·
// Off"; the card shows its path in a tooltip.
func toolStatus(msgs locales.Messages, t agio.Tool, discovering bool) string {
	switch {
	case !found(t) && t.Err == "" && !t.Disabled && discovering:
		return msgs.CheckingAgent()
	case !found(t) && (t.Err != "" || !t.Disabled):
		return msgs.AgentNotFound()
	}
	var parts []string
	if t.Version != "" {
		parts = append(parts, t.Version)
	}
	switch {
	case t.Disabled:
		parts = append(parts, msgs.AgentDisabled())
	case t.Err != "":
		parts = append(parts, t.Err)
	}
	return strings.Join(parts, " · ")
}

// modelsSummary counts a tool's models turned on, as in "10/12 models".
func modelsSummary(msgs locales.Messages, t agio.Tool) string {
	enabled := 0
	for _, mo := range t.Catalog.Models {
		if !slices.Contains(t.DisabledModels, mo.ID) {
			enabled++
		}
	}
	return msgs.ModelsEnabled(len(t.Catalog.Models), enabled)
}

// tool is the latest state of a tool.
func tool(m *Model, id agent.Tool) agio.Tool {
	for _, t := range m.State().Tools {
		if t.Tool == id {
			return t
		}
	}
	return agio.Tool{Tool: id}
}

// toolCard is a tool's row on a rounded face: its mark, name and status, a
// tooltip with its path, and once found or turned off its switch. A usable
// tool's status ends with a link counting its models turned on, which
// opens their dropdown.
func toolCard(m *Model, msgs locales.Messages, id agent.Tool) core.Node {
	return core.Func(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		s := m.State()
		t := tool(m, id)
		status := ui.Text(toolStatus(msgs, t, s.Discovering), 11.375, ui.Regular, colors.Muted)
		line := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Wrap: core.Wrap, MinWidth: core.Pt(0)},
			Children: []core.Node{status}}
		if t.Ready() {
			line.Children = append(line.Children, modelsPopover(cx, m, msgs, id))
		}
		info := core.View{Style: core.Style{Direction: core.Row, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), AlignItems: core.AlignCenter, Gap: 12},
			Children: []core.Node{
				mark(cx, toolIcon(id), 20),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Gap: 4},
					Children: []core.Node{ui.Text(id.Name(), 13, ui.Regular, colors.Text), line}},
			}}
		children := []core.Node{info}
		if t.Path != "" {
			children[0] = ui.Tooltip{Text: t.Path, Trigger: info, Style: ui.FillRest}
		}
		if found(t) || t.Disabled {
			children = append(children, ui.Switch{Label: msgs.EnableAgent(id.Name()), TestID: "agent-" + string(id),
				Checked:  live(m, func() bool { return !tool(m, id).Disabled }),
				OnChange: func(on bool) { m.Do(func(b agio.Backend) error { return b.Enable(id, on) }, nil) }})
		}
		return core.View{Style: core.Style{Direction: core.Row, MinWidth: core.Pt(0), Padding: core.All(12), Gap: 12, AlignItems: core.AlignCenter, CornerRadius: 12},
			StyleFunc: func() core.Style { return core.Style{Background: ui.Alpha(colors.Text(), .04)} }, Children: children}
	})
}

// modelsPopover is a dropdown of a usable tool's models from a link counting
// those turned on, with a check box each, a search and actions turning all
// matches on or off at once.
func modelsPopover(cx *core.Scope, m *Model, msgs locales.Messages, id agent.Tool) core.Node {
	open, query := core.NewSignal(cx, false), core.NewSignal(cx, "")
	colors := ui.ColorsOf(cx)
	models := tool(m, id).Catalog.Models
	matches := func() []agent.Model {
		q := strings.ToLower(strings.TrimSpace(query.Get()))
		var out []agent.Model
		for _, mo := range models {
			if q == "" || matchesModel(mo, q) {
				out = append(out, mo)
			}
		}
		return out
	}
	enabled := func(list []agent.Model) int {
		off := tool(m, id).DisabledModels
		on := 0
		for _, mo := range list {
			if !slices.Contains(off, mo.ID) {
				on++
			}
		}
		return on
	}
	enable := func(list []agent.Model, on bool) {
		keys := make([]string, len(list))
		for i, mo := range list {
			keys[i] = mo.Key()
		}
		m.Do(func(b agio.Backend) error { return b.EnableModels(keys, on) }, nil)
	}
	return base.Popover{Open: open.Get, OnOpenChange: func(v bool, _ base.Reason) { open.Set(v); query.Set("") }, Placement: core.PlaceBottom, Align: core.AlignStart, Offset: 4, Margin: 8,
		Label: msgs.ToolModels(id.Name()), TestID: "models-" + string(id),
		Trigger:    ui.Textf(live(m, func() string { return modelsSummary(msgs, tool(m, id)) }), 11.375, ui.Regular, colors.Accent),
		PopupStyle: core.Style{Width: core.Pt(400), MaxWidth: core.Pct(100), Gap: 0},
		PopupStyleFunc: func() core.Style {
			return ui.FinOf(cx).Menu().Over(core.Style{CornerRadius: ui.RadiusCard, BorderWidth: 1, Color: colors.Text()})
		},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, Padding: core.All(8), Gap: 8, AlignItems: core.AlignCenter}, Children: []core.Node{
				core.View{Style: ui.FillRest, Children: []core.Node{ui.Input{Value: query.Get, OnChange: query.Set, AutoFocus: true, Label: msgs.SearchModels(), Placeholder: msgs.SearchModels()}}},
				ui.Button{Text: msgs.SelectAllModels(), Variant: ui.ButtonSecondary, TestID: "select-all-models",
					Disabled: live(m, func() bool { list := matches(); return enabled(list) == len(list) }),
					OnPress:  func() { enable(core.Untracked(cx, matches), true) }},
				ui.Button{Text: msgs.SelectNoModels(), Variant: ui.ButtonSecondary, TestID: "select-no-models",
					Disabled: live(m, func() bool { return enabled(matches()) == 0 }),
					OnPress:  func() { enable(core.Untracked(cx, matches), false) }},
			}},
			core.View{Style: core.Style{Height: core.Pt(1)}, StyleFunc: ui.Filled(colors.Border)},
			core.Scroll{Style: core.Style{MaxHeight: core.Pt(340)}, Children: []core.Node{
				core.View{Style: core.Style{Padding: core.Edges{Top: 4, Right: 8, Bottom: 8, Left: 8}, Gap: 1}, Children: []core.Node{
					core.Dynamic(func(cx *core.Scope) core.Node {
						list := matches()
						if len(list) == 0 {
							t := ui.Text(msgs.NoResults(), 12.5, ui.Regular, colors.Muted)
							t.Style.Padding = core.All(8)
							return t
						}
						var rows []core.Node
						group := ""
						for i, mo := range list {
							if heading := mo.Provider; mo.Tool == agent.Pi && heading != "" && (i == 0 || heading != group) {
								t := ui.Text(heading, 11.5, ui.Regular, colors.Muted)
								t.Style.Padding = core.Edges{Top: 8, Right: 8, Bottom: 3, Left: 8}
								rows = append(rows, t)
								group = heading
							}
							rows = append(rows, modelCheckbox(cx, m, mo))
						}
						return core.View{Style: core.Style{Gap: 1}, Children: rows}
					})}}}},
		}}
}

// modelCheckbox turns one model on or off.
func modelCheckbox(cx *core.Scope, m *Model, mo agent.Model) core.Node {
	key := mo.Key()
	return core.View{Style: core.Style{Direction: core.Row, MinWidth: core.Pt(0), Padding: core.Symmetric(5, 8), Gap: 8, AlignItems: core.AlignCenter}, Children: []core.Node{
		ui.Checkbox{Text: mo.Name, Style: core.Style{MaxWidth: core.Pt(220), FlexShrink: 0}, TestID: "model-" + key,
			Checked:  live(m, func() bool { return !slices.Contains(tool(m, mo.Tool).DisabledModels, mo.ID) }),
			OnChange: func(on bool) { m.Do(func(b agio.Backend) error { return b.EnableModels([]string{key}, on) }, nil) }},
		fillLine(modelDetail(mo), 11.5, ui.ColorsOf(cx).Muted),
	}}
}

// matchesModel reports whether a lower-case query occurs in a model's
// names.
func matchesModel(m agent.Model, query string) bool {
	return strings.Contains(strings.ToLower(strings.Join([]string{m.Name, m.ID, m.Provider, m.Detail, m.Tool.Name()}, " ")), query)
}

// modelDetail names the concrete model behind a display name, with its
// context window when known, as in "claude-opus-5-5 · 200.0k".
func modelDetail(m agent.Model) string {
	var parts []string
	if m.Detail != "" && !strings.EqualFold(m.Detail, m.Name) {
		parts = append(parts, m.Detail)
	}
	if m.ContextWindow > 0 {
		parts = append(parts, formatTokens(m.ContextWindow))
	}
	return strings.Join(parts, " · ")
}

// fillLine is a line of text that takes the room left in its row.
func fillLine(text string, size core.Points, color func() core.Color) core.Text {
	t := ui.Text(text, size, ui.Regular, color)
	t.MaxLines = 1
	t.Style = t.Style.Merge(core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)})
	return t
}
