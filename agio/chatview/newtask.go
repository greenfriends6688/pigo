package chatview

import (
	"slices"
	"strings"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
	agio "pigo/agio"
	"pigo/agio/chatview/locales"
)

// newTaskPane is New task's page: the draft's composer in the middle of
// the pane, with the hero and the project to choose above it, scrolling
// when the pane is too short. The first message sent shows the task with
// open.
func (a App) newTaskPane(cx *core.Scope, draft func() string, open func(id string)) core.Node {
	m := a.Model
	height := core.NewSignal(cx, core.Points(0))
	composer := region(m, draft, func(cx *core.Scope) core.Node {
		id := draft()
		if id == "" {
			return core.View{Style: core.Style{AlignSelf: core.AlignCenter}, Children: []core.Node{ui.Spinner{Size: 20}}}
		}
		if a.NewTaskChat != nil {
			return a.NewTaskChat(cx, id, func() { open(id) })
		}
		v := Conversation{Model: m, ID: id, TestID: "conversation", OnSend: func() { open(id) }}
		return core.View{TestID: "conversation", Children: []core.Node{column(6, v.overlay(cx), v.banner(cx), v.composer(cx, chat.NewConversationState(cx), core.NewSignal(cx, "")))}}
	})
	// The halves above and below grow alike, which keeps the composer in
	// the middle; the hero sits at the bottom of the upper one.
	half := func(children ...core.Node) core.Node {
		return core.View{Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), Width: core.Pct(100), AlignItems: core.AlignCenter, JustifyContent: core.JustifyEnd},
			Children: children}
	}
	return core.Scroll{Style: ui.FillRest, TestID: "new-task-page", OnLayout: func(b core.Bounds) { height.Set(b.Size.Height) }, Children: []core.Node{
		core.View{Style: core.Style{AlignItems: core.AlignCenter, Padding: core.Symmetric(32, 20)},
			StyleFunc: func() core.Style { return core.Style{MinHeight: core.Pt(height.Get())} },
			Children: []core.Node{
				half(a.hero(cx, draft)),
				core.View{Style: core.Style{Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth), MinWidth: core.Pt(0), FlexShrink: 0}, Children: []core.Node{composer}},
				half(),
			}},
	}}
}

// hero greets New task: the app's icon and a question naming the project
// the task works in, which the name, in the accent color, chooses.
func (a App) hero(cx *core.Scope, draft func() string) core.Node {
	colors := ui.ColorsOf(cx)
	msgs := locales.For(cx)
	var children []core.Node
	if a.Icon != nil {
		children = append(children, core.View{Style: core.Style{Width: core.Pt(64), Height: core.Pt(64), FlexShrink: 0, Margin: core.Edges{Bottom: 6}},
			Children: []core.Node{a.Icon}})
	}
	// A NUL placeholder marks where the translated question names the
	// project.
	before, after, _ := strings.Cut(msgs.HeroTitle("\x00"), "\x00")
	part := func(text string) core.Node {
		if text == "" {
			return core.Group(nil)
		}
		return ui.Text(text, 24, ui.Semibold, colors.Text)
	}
	heading := core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, MinWidth: core.Pt(0)},
		Children: []core.Node{part(before), a.projectPicker(cx, draft), part(after)}}
	detail := ui.Text(msgs.HeroDetail(), 13.5, ui.Regular, colors.Muted)
	detail.Style = detail.Style.Merge(core.Style{TextAlign: core.TextAlignCenter, MaxWidth: core.Pt(420)})
	children = append(children, heading, detail)
	return core.View{Style: core.Style{AlignItems: core.AlignCenter, Gap: 8, MinWidth: core.Pt(0), Padding: core.Edges{Bottom: 28}}, Children: children}
}

// projectPicker chooses the folder New task's draft works in: one of the
// projects, or another from the system's folder picker. Its
// face is the folder's name in the heading, its path in a tooltip.
func (a App) projectPicker(cx *core.Scope, draft func() string) core.Node {
	m := a.Model
	msgs := locales.For(cx)
	colors := ui.ColorsOf(cx)
	dir := live(m, func() string { return m.Conversation(draft()).Dir })
	choose := func(d string) {
		if id := core.Untracked(cx, draft); id != "" {
			m.Do(func(b agio.Backend) error { return b.Choose(id, agio.Choice{Dir: &d}) }, nil)
		}
	}
	name := ui.Textf(func() string {
		if d := dir(); d != "" {
			return pathBase(d)
		}
		return msgs.ChooseProject()
	}, 24, ui.Semibold, colors.Accent)
	name.MaxLines = 1
	name.Style = name.Style.Merge(core.Style{FlexShrink: 1, MinWidth: core.Pt(0)})
	face := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, MinWidth: core.Pt(0), MaxWidth: core.Pt(360),
		Padding: core.Symmetric(0, 6), Margin: core.Symmetric(0, 2), CornerRadius: 8},
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Accent(), .1)}}
		},
		Children: []core.Node{name}}
	items := region(m, func() string { return strings.Join(a.projects(m.State()), "\x00") }, func(cx *core.Scope) core.Node {
		var items []core.Node
		for _, d := range a.projects(m.State()) {
			items = append(items, ui.MenuItem{Text: pathBase(d), Icon: lucide.Folder, TestID: "project-" + pathBase(d),
				Checked: func() bool { return dir() == d }, OnSelect: func() { choose(d) }})
		}
		if len(items) > 0 {
			items = append(items, ui.MenuSeparator{})
		}
		items = append(items, ui.MenuItem{Text: msgs.ChooseFolder(), Icon: lucide.FolderOpen, TestID: "choose-folder", OnSelect: func() {
			cx.PickDirectories(core.PickDirectoriesOptions{}, func(dirs []core.Directory, err error) {
				switch {
				case err != nil:
					m.notice.Set(err.Error())
				case len(dirs) > 0 && dirs[0].Path != "":
					choose(dirs[0].Path)
				}
			})
		}})
		return core.Group(items)
	})
	menu := ui.Menu{Face: face, Label: msgs.ChooseProject(), Placement: core.PlaceBottom, Children: []core.Node{items}, MaxHeight: 320, TestID: "new-task-project"}
	// The tooltip follows the folder alone, so other changes keep the menu.
	shown := core.NewMemo(cx, dir)
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if d := shown.Get(); d != "" {
			return ui.Tooltip{Text: d, Trigger: menu}
		}
		return menu
	})
}

// projects are the app's remembered folders, then those the backend's
// tasks work in.
func (a App) projects(s agio.State) []string {
	var dirs []string
	if a.Projects != nil {
		dirs = slices.Clone(a.Projects())
	}
	for _, d := range projects(s) {
		if !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}
