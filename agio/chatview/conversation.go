package chatview

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/oneo-me/nsgo/base/textview"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
	agio "pigo/agio"
	"pigo/agio/agent"
	"pigo/agio/chatview/locales"
	"pigo/agio/transcript"
)

// Conversation shows one conversation of Model: its turns scroll behind a
// floating composer with the model, reasoning level and service tier
// choices. Give its parent a bounded height, and key it by ID, as its
// draft and scrolling are the conversation's.
type Conversation struct {
	Model *Model
	ID    string
	// Turn, when set, renders one turn instead of the built-in card.
	Turn func(cx *core.Scope, turn *transcript.Turn, last bool) core.Node
	// Composer, when set, adjusts the composer chatview builds.
	Composer func(cx *core.Scope, id string, base chat.Composer) chat.Composer
	// Overlay, when set, is shown above the composer, for a find bar, a
	// queue or a disconnect card.
	Overlay func(cx *core.Scope, id string) core.Node
	// TurnLimit, when it reports a positive number, shows only the last
	// that many turns.
	TurnLimit func() int
	// State, when set, is the transcript's scroll and fold state, so a turn
	// the app builds shares it.
	State *chat.ConversationState
	// TopInset clears a bar the host overlays, which the transcript
	// scrolls under.
	TopInset core.Points
	TestID   string
	// OnSend runs as a message is sent, as New task shows its task.
	OnSend func()
}

// Render implements core.Node.
func (v Conversation) Render(cx *core.Scope) core.Node {
	m := v.Model
	conv := func() *agio.Conversation { return m.Conversation(v.ID) }
	state := v.State
	if state == nil {
		state = chat.NewConversationState(cx)
	}
	draft := core.NewSignal(cx, "")
	return chat.FloatingConversation{State: state, JumpLabel: locales.For(cx).JumpLatest(), TopInset: v.TopInset, TestID: v.TestID,
		Children: []core.Node{
			core.Show{When: live(m, func() bool { return conv().Loading }), Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{AlignSelf: core.AlignCenter}, Children: []core.Node{ui.Spinner{Size: 20, Label: locales.For(cx).LoadingHistory()}}}
			}},
			v.turns(cx, state),
		},
		Composer: []core.Node{column(6, v.overlay(cx), v.banner(cx), v.composer(cx, state, draft))}}
}

// turns shows the conversation's turns, each kept by its ID.
func (v Conversation) turns(cx *core.Scope, state *chat.ConversationState) core.Node {
	m := v.Model
	ids := core.NewMemoFunc(cx, live(m, func() []string {
		var ids []string
		for _, t := range m.Conversation(v.ID).Turns {
			ids = append(ids, t.ID)
		}
		if v.TurnLimit != nil {
			if n := v.TurnLimit(); n > 0 && len(ids) > n {
				ids = ids[len(ids)-n:]
			}
		}
		return ids
	}), slices.Equal).Get
	last := func() string {
		list := ids()
		if len(list) == 0 {
			return ""
		}
		return list[len(list)-1]
	}
	return core.Each(ids, func(id string) string { return id }, func(cx *core.Scope, id func() string, _ func() int) core.Node {
		idv := core.Untracked(cx, id)
		return v.turn(cx, state, idv, idv == core.Untracked(cx, last))
	})
}

// turn shows the turn id.
func (v Conversation) turn(cx *core.Scope, state *chat.ConversationState, id string, last bool) core.Node {
	m := v.Model
	if v.Turn != nil {
		if t := m.Conversation(v.ID); t != nil {
			for _, turn := range t.Turns {
				if turn.ID == id {
					return v.Turn(cx, turn, last)
				}
			}
		}
		return v.Turn(cx, &transcript.Turn{ID: id}, last)
	}
	msgs := locales.For(cx)
	colors := ui.ColorsOf(cx)
	find := func() *transcript.Turn {
		for _, t := range m.Conversation(v.ID).Turns {
			if t.ID == id {
				return t
			}
		}
		return &transcript.Turn{ID: id}
	}
	current := live(m, find)
	final := func() *transcript.Segment { return current().Final() }
	markdown := func(text string) chat.View {
		return func(cx *core.Scope) core.Node { return markdownView(cx, func() string { return text }) }
	}
	return chat.Turn{Conversation: state, TestID: "turn-" + id,
		Status: func() chat.Status { return chatStatus(current().Status) },
		Summary: func() string {
			t := current()
			if !t.Started.IsZero() && t.Ended.After(t.Started) {
				return msgs.WorkedFor(duration(msgs, t.Ended.Sub(t.Started)))
			}
			return msgs.Worked()
		},
		Working: func() string {
			if c := m.Conversation(v.ID); c.Starting {
				return msgs.StartingStatus()
			}
			return msgs.WorkingFor(duration(msgs, m.now().Sub(current().Started)))
		},
		Prompt: func(cx *core.Scope) core.Node {
			t := core.Untracked(cx, find)
			text := wrapped(t.Prompt, 13.5, colors.Text)
			if len(t.Files) == 0 {
				return text
			}
			chips := make([]core.Node, len(t.Files))
			for i, f := range t.Files {
				chips[i] = chat.Attachment{Label: pathBase(f)}
			}
			return column(6, core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6}, Children: chips}, text)
		},
		Steps:    func() []chat.Step { return steps(msgs, current(), markdown) },
		HasFinal: func() bool { return final() != nil },
		Final: func(cx *core.Scope) core.Node {
			return markdownView(cx, func() string {
				if s := final(); s != nil {
					return s.Text
				}
				return ""
			})
		},
		Footer: func(cx *core.Scope) core.Node {
			return core.Dynamic(func(cx *core.Scope) core.Node {
				t := current()
				switch {
				case t.Err != "":
					return row(8, ui.Icon(lucide.CircleAlert, 14, colors.Danger), wrapped(t.Err, 13, colors.Danger))
				case t.Status == transcript.Cancelled && !t.Started.IsZero():
					return ui.Text(msgs.StoppedAfter(duration(msgs, t.Ended.Sub(t.Started))), 13, ui.Regular, colors.Muted)
				}
				return core.Group(nil)
			})
		}}
}

// overlay is the app's own bar above the composer.
func (v Conversation) overlay(cx *core.Scope) core.Node {
	if v.Overlay == nil {
		return core.Group(nil)
	}
	return v.Overlay(cx, v.ID)
}

// banner shows above the composer what keeps it from working: no agents,
// a lost connection or a failed request.
func (v Conversation) banner(cx *core.Scope) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	colors := ui.ColorsOf(cx)
	problems := live(m, func() []string {
		s := m.State()
		var out []string
		if s.Err != "" {
			out = append(out, s.Err)
		}
		if c := s.Conversation(v.ID); c != nil && c.Err != "" {
			out = append(out, c.Err)
		}
		if n := m.Notice(); n != "" {
			out = append(out, n)
		}
		return out
	})
	missing := live(m, func() bool { s := m.State(); return !s.Discovering && !s.Ready() && s.Err == "" })
	key := func() string { return strings.Join(problems(), "\x00") + fmt.Sprint(missing()) }
	return region(m, key, func(cx *core.Scope) core.Node {
		var rows []core.Node
		for _, p := range problems() {
			rows = append(rows, row(8, ui.Icon(lucide.CircleAlert, 14, colors.Danger), wrapped(p, 12.5, colors.Danger)))
		}
		if missing() {
			rows = append(rows, row(8, ui.Icon(lucide.CircleAlert, 14, colors.Muted), wrapped(msgs.NoAgents(), 12.5, colors.Muted),
				ui.Button{Text: msgs.LookAgain(), Size: ui.SizeSmall, Icon: lucide.RefreshCw, TestID: "look-again",
					OnPress: func() { m.Do(func(b agio.Backend) error { b.Discover(); return nil }, nil) }}))
		}
		if len(rows) == 0 {
			return core.Group(nil)
		}
		return chat.GlassPanel{Children: []core.Node{column(6, rows...)}}
	})
}

// composer is the conversation's editor with its choices.
func (v Conversation) composer(cx *core.Scope, state *chat.ConversationState, draft *core.Signal[string]) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	conv := func() *agio.Conversation { return m.Conversation(v.ID) }
	send := func() {
		text := strings.TrimSpace(draft.Get())
		if text == "" {
			return
		}
		draft.Set("")
		state.JumpToLatest()
		if v.OnSend != nil {
			v.OnSend()
		}
		m.Do(func(b agio.Backend) error { return b.Send(v.ID, agio.Message{Text: text}) }, func(err error) {
			// A message that was not sent returns to an empty composer.
			if err != nil && draft.GetUntracked() == "" {
				draft.Set(text)
			}
		})
	}
	base := chat.Composer{Label: msgs.MessageInput(), SendLabel: msgs.Send(), StopLabel: msgs.Stop(), Glass: true, State: state, TestID: "composer-card",
		PlaceholderFunc: live(m, func() string {
			s := m.State()
			c := conv()
			switch {
			case s.Discovering && !s.Ready():
				return msgs.LookingForAgents()
			case c.Running:
				return msgs.InterruptPlaceholder()
			case len(c.Turns) == 0:
				return msgs.NewPlaceholder()
			}
			return msgs.ReplyPlaceholder()
		}),
		Draft:         draft.Get,
		OnDraftChange: draft.Set,
		Running:       live(m, func() bool { c := conv(); return c.Running || c.Starting }),
		SendDisabled:  live(m, func() bool { s := m.State(); return s.Err != "" || !s.Ready() && !conv().Bound }),
		OnSend:        send,
		OnStop:        func() { m.Do(func(b agio.Backend) error { return b.Stop(v.ID) }, nil) },
		Accessory:     func(cx *core.Scope) core.Node { return v.choices(cx) },
		Trailing:      func(cx *core.Scope) core.Node { return v.contextMeter(cx) },
		Footer: func(cx *core.Scope) core.Node {
			return core.Group{v.project(cx), core.View{Style: core.Style{FlexGrow: 1}}, v.quotaFooter(cx)}
		}}
	if v.Composer != nil {
		return v.Composer(cx, v.ID, base)
	}
	return base
}

// project shows where the agent works; New task chooses it.
func (v Conversation) project(cx *core.Scope) core.Node {
	m := v.Model
	label := live(m, func() string { return pathBase(m.Conversation(v.ID).Dir) })
	return chat.Chip{TextFunc: label, Icon: lucide.Folder, TestID: "project"}
}

// projects are the directories the backend's conversations work in, most
// recent first.
func projects(s agio.State) []string {
	var dirs []string
	for _, c := range s.Conversations {
		if c.Dir != "" && !slices.Contains(dirs, c.Dir) {
			dirs = append(dirs, c.Dir)
		}
	}
	return dirs
}

// choices are the model, reasoning level and service tier pickers.
func (v Conversation) choices(cx *core.Scope) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	conv := func() *agio.Conversation { return m.Conversation(v.ID) }
	choose := func(c agio.Choice) { m.Do(func(b agio.Backend) error { return b.Choose(v.ID, c) }, nil) }
	key := func() string {
		s := m.State()
		c := conv()
		info, known := s.Model(c.Model)
		var tiers []string
		for _, t := range info.Tiers {
			tiers = append(tiers, t.ID)
		}
		var models []string
		for _, mo := range offered(s, c.Model) {
			models = append(models, mo.Key())
		}
		return fmt.Sprint(c.Model, c.Bound, known, info.Efforts, tiers, models)
	}
	return region(m, key, func(cx *core.Scope) core.Node {
		s := m.State()
		c := conv()
		if !s.Ready() {
			return core.Group(nil)
		}
		nodes := []core.Node{v.modelPopover(cx, func(key string) { choose(agio.Choice{Model: &key}) })}
		info, known := s.Model(c.Model)
		if known && len(info.Efforts) > 1 {
			var levels []chat.Option
			for _, level := range info.Efforts {
				levels = append(levels, chat.Option{ID: level, Label: thinkingLabel(msgs, level)})
			}
			effort := live(m, func() string { return conv().Effort })
			nodes = append(nodes, chat.EffortPicker{Label: msgs.Thinking(), Icon: lucide.Brain, Levels: levels, Selected: effort,
				Tooltip:  func() string { return msgs.Thinking() + " · " + thinkingLabel(msgs, effort()) },
				OnChange: func(level string) { choose(agio.Choice{Effort: &level}) }})
		}
		if known && len(info.Tiers) > 0 {
			const standard = "\x00standard"
			options := []chat.Option{{ID: standard, Label: msgs.TierStandard()}}
			for _, t := range info.Tiers {
				options = append(options, chat.Option{ID: t.ID, Label: tierLabel(msgs, t)})
			}
			selected := live(m, func() string {
				if tier := conv().Tier; tier != "" {
					return tier
				}
				return standard
			})
			nodes = append(nodes, chat.Picker{Icon: lucide.Zap, Options: options, Selected: selected, Width: 260,
				Label: func() string {
					id := selected()
					for _, o := range options {
						if o.ID == id {
							return o.Label
						}
					}
					return msgs.TierStandard()
				},
				OnChange: func(id string) {
					if id == standard {
						id = ""
					}
					choose(agio.Choice{Tier: &id})
				}})
		}
		return core.Group(nodes)
	})
}

// modelPopover chooses a model among those the conversation may choose,
// with a search across them, under headings of their tool and, for pi,
// provider, each showing its account's limit.
func (v Conversation) modelPopover(cx *core.Scope, choose func(key string)) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	open, query := core.NewSignal(cx, false), core.NewSignal(cx, "")
	selected := live(m, func() string { return m.Conversation(v.ID).Model })
	label := func() string {
		key := selected()
		if info, ok := m.State().Model(key); ok {
			return info.Name
		}
		if key != "" {
			_, id := agent.ParseKey(key)
			return id
		}
		return msgs.ChooseModel()
	}
	icon := func() *core.SVG {
		key := selected()
		if info, ok := m.State().Model(key); ok {
			return modelIcon(info)()
		}
		tool, _ := agent.ParseKey(key)
		return toolIcon(tool)()
	}
	chip := chat.Chip{TextFunc: label, Caret: true, Yield: true, Leading: leading(icon)}
	return chat.ChipPopover{Open: open.Get, OnOpenChange: func(o bool) { open.Set(o); query.Set("") }, Chip: chip, Label: msgs.ChooseModel(),
		Width: 300, TestID: "model-picker",
		Children: []core.Node{
			core.View{Style: core.Style{Padding: core.Edges{Top: 8, Right: 8, Left: 8}}, Children: []core.Node{
				ui.Input{Value: query.Get, OnChange: query.Set, Label: msgs.SearchModels(), Placeholder: msgs.SearchModels(), AutoFocus: true}}},
			core.Scroll{Style: core.Style{MaxHeight: core.Pt(260)}, Children: []core.Node{
				core.View{Style: core.Style{Padding: core.Edges{Top: 4, Right: 8, Bottom: 8, Left: 8}, Gap: 1}, Children: []core.Node{
					core.Dynamic(func(cx *core.Scope) core.Node {
						q := strings.ToLower(strings.TrimSpace(query.Get()))
						return region(m, func() string { return q + "\x00" + selected() + "\x00" + v.modelsKey() }, func(cx *core.Scope) core.Node {
							return v.modelRows(cx, q, core.Untracked(cx, selected), func(key string) { open.Set(false); choose(key) })
						})
					})}}}},
		}}
}

// modelsKey says which models the popover offers.
func (v Conversation) modelsKey() string {
	c := v.Model.Conversation(v.ID)
	var b strings.Builder
	for _, mo := range offered(v.Model.State(), c.Model) {
		b.WriteString(mo.Key() + " ")
	}
	return fmt.Sprint(c.Bound, b.String())
}

// modelRows lists the offered models matching the search under their
// headings.
func (v Conversation) modelRows(cx *core.Scope, query, selected string, choose func(key string)) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	colors := ui.ColorsOf(cx)
	s := m.State()
	c := m.Conversation(v.ID)
	locked, _ := agent.ParseKey(c.Model)
	var rows []core.Node
	group, count := "", 0
	for _, mo := range offered(s, c.Model) {
		if c.Bound && mo.Tool != locked {
			continue // a conversation keeps its agent
		}
		if query != "" && !matchesModel(mo, query) {
			continue
		}
		key := mo.Key()
		if heading := modelGroup(mo); count == 0 || heading != group {
			rows = append(rows, modelGroupHeading(cx, m, heading, key))
			group = heading
		}
		count++
		content := []core.Node{mark(cx, modelIcon(mo), 14), fillLine(mo.Name, 13, colors.Text)}
		if mo.ContextWindow > 0 {
			content = append(content, ui.Text(formatTokens(mo.ContextWindow), 11.5, ui.Regular, colors.Muted))
		}
		rows = append(rows, optionRow(cx, mo.Name, "model-"+key, func() bool { return key == selected }, func() { choose(key) }, content...))
	}
	if count == 0 {
		empty := msgs.NoResults()
		switch {
		case query != "":
		case s.Discovering:
			empty = msgs.LoadingModels()
		default:
			empty = msgs.NoEnabledModels()
		}
		t := ui.Text(empty, 12.5, ui.Regular, colors.Muted)
		t.Style.Padding = core.All(8)
		return t
	}
	return core.View{Style: core.Style{Gap: 1}, Children: rows}
}

// modelGroupHeading shows, beside a group's name, the limit its first
// model shows in the composer. It only informs; the composer opens the
// account's limits.
func modelGroupHeading(cx *core.Scope, m *Model, heading, key string) core.Node {
	msgs := locales.For(cx)
	colors := ui.ColorsOf(cx)
	children := []core.Node{fillLine(heading, 11.5, colors.Muted)}
	if r, tooltip, ok := quotaSummary(msgs, m.State(), key, m.now()); ok {
		var glance []core.Node
		if r.Fraction >= 0 && r.Detail != "" {
			glance = append(glance, ui.Text(r.Detail, 11.5, ui.Regular, colors.Muted))
		}
		if r.Fraction >= 0 {
			glance = append(glance, ui.Progress{Value: func() float32 { return float32(100 * r.Fraction) }, Label: r.Label,
				Style: core.Style{Width: core.Pt(64), Height: core.Pt(4), FlexShrink: 0}})
		} else {
			glance = append(glance, ui.Text(r.Value, 11.5, ui.Regular, colors.Muted))
		}
		children = append(children, ui.Tooltip{Text: tooltip, Trigger: row(6, glance...)})
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, MinWidth: core.Pt(0),
		Padding: core.Edges{Top: 8, Right: 8, Bottom: 3, Left: 8}}, Children: children}
}

// optionRow is a row styled as chat.Picker's, with the choice's content
// and a check mark while selected.
func optionRow(cx *core.Scope, label, testID string, selected func() bool, press func(), content ...core.Node) core.Node {
	colors := ui.ColorsOf(cx)
	check := core.Show{When: selected, Then: func(*core.Scope) core.Node { return ui.Icon(lucide.Check, 12, colors.Accent) },
		Else: func(*core.Scope) core.Node {
			return core.View{Style: core.Style{Width: core.Pt(12), Height: core.Pt(12), FlexShrink: 0}}
		}}
	return core.View{Role: core.RoleButton, Label: label, Selected: selected, TestID: testID,
		OnPress: func(core.PressEvent) { press() },
		Style:   core.Style{Direction: core.Row, MinWidth: core.Pt(0), Padding: core.Symmetric(6, 8), CornerRadius: 6, AlignItems: core.AlignCenter, Gap: 8, FlexShrink: 0},
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Text(), .05)}, Focus: ui.FocusRing(cx, -2)}
		}, Children: append(content, check)}
}

// offered are the models a conversation may choose: those turned on, and
// its own.
func offered(s agio.State, current string) []agent.Model {
	models := s.Models()
	if m, ok := s.Model(current); ok && !slices.ContainsFunc(models, func(o agent.Model) bool { return o.Key() == current }) {
		i := slices.IndexFunc(models, func(o agent.Model) bool { return o.Tool == m.Tool })
		if i < 0 {
			i = len(models)
		}
		models = slices.Insert(models, i, m)
	}
	return models
}

// contextMeter shows how much of the model's context window is in use.
func (v Conversation) contextMeter(cx *core.Scope) core.Node {
	m := v.Model
	msgs := locales.For(cx)
	usage := func() (window, tokens int64) {
		c := m.Conversation(v.ID)
		return c.ContextWindow, c.ContextTokens
	}
	fraction := live(m, func() float64 {
		if window, tokens := usage(); window > 0 {
			return math.Min(1, float64(tokens)/float64(window))
		}
		return -1
	})
	return chat.ContextMeter{Label: msgs.ContextWindow(), Title: msgs.ContextWindow(), Fraction: fraction, TestID: "context-meter",
		Value: live(m, func() string {
			window, tokens := usage()
			if window <= 0 {
				return formatTokens(tokens)
			}
			return fmt.Sprintf("%s / %s (%d%%)", formatTokens(tokens), formatTokens(window), int(math.Round(fraction()*100)))
		}),
		Tooltip: live(m, func() string {
			if window, _ := usage(); window > 0 {
				return msgs.ContextUsed(int(math.Round(fraction() * 100)))
			}
			return ""
		})}
}

// markdownView shows assistant text, selectable across its blocks.
func markdownView(cx *core.Scope, text func() string) core.Node {
	return ui.TextView{MarkdownFunc: text, Selectable: true, OnLinkPress: func(e textview.LinkPressEvent) { cx.OpenURL(e.URL, nil) }}
}

// segmentBody is the thinking or tool body: plain, selectable and bounded.
func segmentBody(cx *core.Scope, text string, mono bool) core.Node {
	t := wrapped(strings.TrimRight(text, "\n"), 10.4, ui.ColorsOf(cx).Muted)
	if mono {
		t.Style.FontFamily = core.FontMonospace
	}
	return t
}

// wrapped is selectable text that wraps and gives way to what is beside
// it.
func wrapped(text string, size core.Points, color func() core.Color) core.Text {
	t := ui.Text(text, size, ui.Regular, color)
	t.Style = t.Style.Merge(core.Style{FlexShrink: 1, MinWidth: core.Pt(0)})
	t.Selectable = core.SelectableOn
	return t
}

func row(gap core.Points, children ...core.Node) core.View {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: gap, MinWidth: core.Pt(0)}, Children: children}
}

func column(gap core.Points, children ...core.Node) core.View {
	return core.View{Style: core.Style{Gap: gap, MinWidth: core.Pt(0)}, Children: children}
}
