package chatview

import (
	"runtime"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/desktop"
	menus "github.com/oneo-me/nsgo/desktop/locales"
	"github.com/oneo-me/nsgo/i18n"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/updater"
	agio "pigo/agio"
	"pigo/agio/chatview/locales"
)

// App is a window of Model's conversations as tasks in a sidebar beside
// the selected one: those running, then the others by when they were last
// active. The selected one shows on a card, inset from the window while
// the sidebar shows. New task leads the sidebar and shows a page of its
// own, whose first message starts a task where the one shown before
// worked, else in the first of the projects, else in Dir, a directory on
// the backend's computer, or where the backend chooses when it is empty.
// The settings end the sidebar.
//
// Name, when set, heads the menu bar on macOS: the app's menu with the
// settings, a File menu starting a conversation, and the platform's Edit
// and Window menus. Updates adds Check for Updates… to the app's menu, for
// an app whose nsgo.yaml configures updates. Elsewhere, and on macOS
// without the menu bar, the window binds their shortcuts itself.
type App struct {
	Model *Model
	Dir   string
	// Projects, when set, are folders the app remembers, most recently
	// used first: New task offers them before its tasks' own and starts in
	// the first.
	Projects func() []string
	Name     string
	Updates  bool
	// Icon heads New task's page, such as the app's logo, 64 points
	// square.
	Icon core.Node
	// Header ends the window's header with the app's own actions for the
	// directory a new conversation would work in, built again when it or
	// the language changes.
	Header func(dir string) []core.Node
	// Settings lists the settings dialog's categories, placing chatview's
	// own, the agents and then, with Updates, the updates, among the app's;
	// without it the dialog shows chatview's alone. SettingsKey says what
	// else Settings reads, so the dialog rebuilds when it changes;
	// SettingsSectionActions end a section's heading, and SettingsCategory,
	// when set, holds the page shown, so the app can go to one.
	Settings               func(own []ui.SettingsCategory) []ui.SettingsCategory
	SettingsKey            func() string
	SettingsSectionActions func(cx *core.Scope, section string) core.Node
	SettingsCategory       *core.Signal[string]
	// ChromeStyle, when set, paints the window's chrome around the sidebar
	// and the content's card, such as with a gradient; it is a StyleFunc.
	ChromeStyle func() core.Style
	// Backdrop is drawn over the chrome, behind the sidebar and the
	// content, such as a texture.
	Backdrop []core.Node
	// OnAppearanceChange, when set, ends the sidebar with a menu choosing
	// the appearance, which it reports so the app keeps it.
	OnAppearanceChange func(core.Appearance)
	// AltShortcuts, when it reports true, moves the shortcuts outside macOS
	// from Ctrl to Alt.
	AltShortcuts func() bool
	// SidebarOpen and SidebarWidth, when set, hold whether the sidebar
	// shows and how wide, so the app can keep them.
	SidebarOpen          *core.Signal[bool]
	SidebarWidth         *core.Signal[core.Points]
	SidebarOnWidthChange func(core.Points)
	SidebarOnOpenChange  func(bool)
	// Selected, when set, holds the conversation shown, "" for New task,
	// so the app can show one.
	Selected *core.Signal[string]
	// Sidebar, when set, replaces the list chatview builds from the tasks.
	Sidebar func(cx *core.Scope, selected *core.Signal[string]) core.Node
	// Chat, when set, shows conversation id instead of the built-in
	// Conversation, so an app can keep its own transcript.
	Chat func(cx *core.Scope, id string) core.Node
	// NewTask, when set, shows the draft's page instead of the built-in one.
	NewTask func(cx *core.Scope, draft func() string, open func(string)) core.Node
	// NewSession, when set, is what the sidebar's New task button does.
	NewSession func()
	// KeepSelection, when true, does not clear Selected when the backend's
	// snapshot does not hold it yet, as an app's own engine may still be
	// reading the conversation.
	KeepSelection bool
	// NewTaskChat, when set, is the draft's conversation on the built-in
	// New task page, so it keeps the app's transcript and composer.
	NewTaskChat func(cx *core.Scope, id string, onSend func()) core.Node
	// Title, when set, heads the window instead of the conversation's title.
	Title func(cx *core.Scope, id string) core.Node
	// Right is a panel at the content's end under RightHeader, shown while
	// RightOpen reports true.
	Right       []core.Node
	RightHeader []core.Node
	RightOpen   *core.Signal[bool]
	// RightWidth is the panel's width, RightDefaultWidth when 0.
	RightWidth         *core.Signal[core.Points]
	RightDefaultWidth  core.Points
	RightOnWidthChange func(core.Points)
	// SidebarFooter ends the sidebar below the app's toolbar, and
	// SidebarActions follow the New task button.
	SidebarFooter  []core.Node
	SidebarActions []core.Node
	// SettingsOpen, when set, holds whether the settings dialog shows, so
	// the app's own shortcuts can open it.
	SettingsOpen *core.Signal[bool]
	TestID       string
}

// Render implements core.Node.
func (a App) Render(cx *core.Scope) core.Node {
	m := a.Model
	colors := ui.ColorsOf(cx)
	// selected is the conversation shown, "" for New task.
	selected := a.Selected
	if selected == nil {
		selected = core.NewSignal(cx, "")
	}
	open, width := a.SidebarOpen, a.SidebarWidth
	if open == nil {
		open = core.NewSignal(cx, true)
	}
	if width == nil {
		width = core.NewSignal(cx, core.Points(220))
	}
	settingsOpen := a.SettingsOpen
	if settingsOpen == nil {
		settingsOpen = core.NewSignal(cx, false)
	}
	draft := live(m, func() string { return draftID(m.State()) })
	// dir is where the shown conversation works: New task's draft's, else
	// Dir.
	dir := func() string {
		id := selected.Get()
		if id == "" {
			id = draft()
		}
		if c := m.State().Conversation(id); c != nil && c.Dir != "" {
			return c.Dir
		}
		return a.Dir
	}
	// next is where New task's next draft works: where the conversation
	// shown when it opened did, else the first project, else Dir.
	next := ""
	creating := false
	// failed is the directory starting a draft in failed, which is not
	// retried on its own.
	failed := ""
	create := func() {
		d := next
		if d == "" {
			if list := a.projects(core.Untracked(cx, m.State)); len(list) > 0 {
				d = list[0]
			} else {
				d = a.Dir
			}
		}
		if creating || d == failed {
			return
		}
		creating = true
		m.Do(func(b agio.Backend) error {
			_, err := b.Create(agio.Create{Dir: d})
			return err
		}, func(err error) {
			creating = false
			if err != nil {
				failed = d
				return
			}
			failed, next = "", ""
			m.refresh()
		})
	}
	// New task always has a draft to compose, and a conversation that went
	// leaves New task shown.
	cx.Effect(func() {
		s := m.State()
		id := selected.Get()
		if id != "" {
			if s.Conversation(id) == nil && !a.KeepSelection {
				cx.Untrack(func() { selected.Set("") })
			}
			return
		}
		if draftID(s) == "" && s.Err == "" {
			cx.Untrack(create)
		}
	})
	newTask := func() {
		if id := selected.GetUntracked(); id != "" {
			if c := core.Untracked(cx, m.State).Conversation(id); c != nil && c.Dir != "" {
				next = c.Dir
			}
		}
		failed = ""
		selected.Set("")
		// A draft kept from before moves to where the shown conversation works.
		if id, d := core.Untracked(cx, draft), next; id != "" && d != "" && core.Untracked(cx, m.State).Conversation(id).Dir != d {
			next = ""
			m.Do(func(b agio.Backend) error { return b.Choose(id, agio.Choice{Dir: &d}) }, nil)
		}
	}
	title := live(m, func() string {
		msgs := locales.For(cx)
		id := selected.Get()
		if id == "" {
			return msgs.NewTask()
		}
		if c := m.State().Conversation(id); c != nil && c.Title != "" {
			return c.Title
		}
		return msgs.Untitled()
	})
	sidebar := a.sidebar(cx, selected)
	if a.Sidebar != nil {
		sidebar = a.Sidebar(cx, selected)
	}
	titleNode := func() core.Node {
		if a.Title != nil {
			return a.Title(cx, selected.Get())
		}
		return core.Text{ContentFunc: title, MaxLines: 1,
			Style:     core.Style{FontSize: 13, FontWeight: ui.Semibold, FlexShrink: 1, MinWidth: core.Pt(0)},
			StyleFunc: func() core.Style { return core.Style{Color: colors.Text()} }}
	}
	if a.Name != "" && runtime.GOOS == "darwin" {
		cx.Effect(func() {
			var checkUpdates func()
			if a.Updates {
				checkUpdates = func() { updater.Check(cx) }
			}
			desktop.SetApplicationMenu(cx, appMenu(a.Name, locales.For(cx), menus.For(cx), newTask,
				func() { settingsOpen.Set(true) }, checkUpdates))
		})
	}
	openSettings := func() { settingsOpen.Set(true) }
	a.bindShortcuts(cx, func() {
		if settingsOpen.GetUntracked() {
			return
		}
		if a.NewSession != nil {
			a.NewSession()
			return
		}
		newTask()
	}, openSettings)
	var actions []core.Node
	if a.Header != nil {
		key := func() string { return dir() + "\x00" + i18n.Language(cx) }
		actions = []core.Node{region(m, key, func(cx *core.Scope) core.Node { return core.Group(a.Header(dir())) })}
	}
	// The words shown follow the language; what the window holds stays.
	shell := core.Dynamic(func(cx *core.Scope) core.Node {
		msgs := locales.For(cx)
		// Read outside Untrack so the shell follows these.
		open.Get()
		width.Get()
		if a.RightOpen != nil {
			a.RightOpen.Get()
		}
		var node core.Node
		cx.Untrack(func() {
			sidebarActions := []core.Node{ui.SidebarButton{Text: "新建会话", Icon: lucide.SquarePen, TestID: "new-session", Shortcut: a.shortcutKey("n"),
				Selected: func() bool { return selected.Get() == "" }, OnPress: func() {
					if a.NewSession != nil {
						a.NewSession()
						return
					}
					newTask()
				}}}
			sidebarActions = append(sidebarActions, a.SidebarActions...)
			footer := a.toolbar(cx, msgs, openSettings)
			footer = append(footer, a.SidebarFooter...)
			rightOpen := func() bool { return a.RightOpen != nil && a.RightOpen.Get() }
			rightWidth := func() core.Points {
				if a.RightWidth != nil {
					return a.RightWidth.Get()
				}
				return a.RightDefaultWidth
			}
			node = ui.SidebarShell{TestID: a.TestID, Variant: ui.SidebarShellInset, ChromeStyle: a.ChromeStyle, Backdrop: a.Backdrop,
				Open: open.Get, OnOpenChange: func(o bool) {
					open.Set(o)
					if a.SidebarOnOpenChange != nil {
						a.SidebarOnOpenChange(o)
					}
				}, ToggleLabel: msgs.ToggleSidebar(),
				Width: width.Get, OnWidthChange: func(w core.Points) {
					width.Set(w)
					if a.SidebarOnWidthChange != nil {
						a.SidebarOnWidthChange(w)
					}
				}, ResizeLabel: msgs.ResizeSidebar(),
				DefaultWidth: 220, MinWidth: 220, MaxWidth: 420,
				Sidebar: []core.Node{core.View{Style: ui.FillRest.Merge(core.Style{Gap: 2}), Children: []core.Node{
					core.View{Style: core.Style{Padding: core.Symmetric(0, 12), FlexShrink: 0}, Children: sidebarActions},
					sidebar,
				}}},
				SidebarFooter:      footer,
				Header:             []core.Node{titleNode()},
				Actions:            actions,
				Right:              a.Right,
				RightHeader:        a.RightHeader,
				RightOpen:          rightOpen,
				RightWidth:         rightWidth,
				RightDefaultWidth:  a.RightDefaultWidth,
				OnRightWidthChange: a.RightOnWidthChange,
				RightResizeLabel:   "调整右侧面板宽度",
				RightMinWidth:      320,
				RightMaxWidth:      1200,
				Content: []core.Node{core.Dynamic(func(cx *core.Scope) core.Node {
					id := selected.Get()
					if id == "" {
						if a.NewTask != nil {
							return a.NewTask(cx, draft, selected.Set)
						}
						return a.newTaskPane(cx, draft, selected.Set)
					}
					if a.Chat != nil {
						return a.Chat(cx, id)
					}
					return Conversation{Model: m, ID: id, TestID: "conversation"}
				})},
			}
		})
		return node
	})
	return core.Group([]core.Node{shell, a.settings(cx, settingsOpen)})
}

// The size of the sidebar's toolbar buttons and their icons.
const (
	toolSize  = 32
	toolGlyph = 16.25
)

// toolbar ends the sidebar with Settings, whose tooltip names its shortcut,
// then the appearance menu when the app keeps the appearance.
func (a App) toolbar(cx *core.Scope, msgs locales.Messages, openSettings func()) []core.Node {
	gear := ui.Tooltip{Text: msgs.Settings() + " (" + a.shortcutHint(",") + ")",
		Trigger: ui.IconButton{Icon: lucide.Settings, Label: msgs.Settings(), TestID: "nav-settings", Size: toolSize, IconSize: toolGlyph, OnPress: openSettings}}
	if a.OnAppearanceChange == nil {
		return []core.Node{gear}
	}
	env := cx.Env()
	var items []core.Node
	for _, choice := range []core.Appearance{core.AppearanceSystem, core.AppearanceLight, core.AppearanceDark} {
		items = append(items, ui.MenuItem{Text: appearanceName(msgs, choice), Checked: func() bool { return env.Appearance() == choice },
			OnSelect: func() {
				cx.SetAppearance(choice)
				a.OnAppearanceChange(choice)
			}})
	}
	menu := core.Dynamic(func(cx *core.Scope) core.Node {
		icon := lucide.Monitor
		switch env.Appearance() {
		case core.AppearanceLight:
			icon = lucide.Sun
		case core.AppearanceDark:
			icon = lucide.Moon
		}
		return ui.Menu{Trigger: ui.Button{Variant: ui.ButtonGhost, Icon: icon, Label: msgs.Appearance(), Side: toolSize, IconSize: toolGlyph, Tint: ui.ColorsOf(cx).Muted},
			Label: msgs.Appearance(), Placement: core.PlaceTop, Children: items, TestID: "appearance"}
	})
	return []core.Node{gear, ui.Spacer{}, menu}
}

func appearanceName(msgs locales.Messages, a core.Appearance) string {
	switch a {
	case core.AppearanceLight:
		return msgs.AppearanceLight()
	case core.AppearanceDark:
		return msgs.AppearanceDark()
	}
	return msgs.AppearanceSystem()
}

// bindShortcuts binds the shortcuts the macOS menu bar has: with Command
// on macOS when the window has no menu bar, else with Ctrl or, as the app
// prefers, Alt.
func (a App) bindShortcuts(cx *core.Scope, newTask, openSettings func()) {
	bind := func(key string, fn func()) {
		if runtime.GOOS == "darwin" {
			if a.Name == "" {
				cx.OnKey("meta+"+key, fn)
			}
			return
		}
		cx.OnKey("ctrl+"+key, func() {
			if !a.alt() {
				fn()
			}
		})
		cx.OnKey("alt+"+key, func() {
			if a.alt() {
				fn()
			}
		})
	}
	bind(",", openSettings)
	bind("n", newTask)
}

func (a App) alt() bool { return a.AltShortcuts != nil && a.AltShortcuts() }

// shortcutKey is the shortcut of key in core's form.
func (a App) shortcutKey(key string) string {
	switch {
	case runtime.GOOS == "darwin":
		return "meta+" + key
	case a.alt():
		return "alt+" + key
	}
	return "ctrl+" + key
}

// shortcutHint names key held with the modifier of the app's shortcuts.
func (a App) shortcutHint(key string) string {
	switch {
	case runtime.GOOS == "darwin":
		return "⌘" + key
	case a.alt():
		return "Alt+" + key
	}
	return "Ctrl+" + key
}

// appMenu is the menu bar in the language shown, of chatview's words and
// NSGo's; the platform labels the role items. A nil checkUpdates leaves
// out Check for Updates….
func appMenu(name string, msgs locales.Messages, words menus.Messages, newTask, openSettings, checkUpdates func()) []desktop.MenuItem {
	app := []desktop.MenuItem{{Role: desktop.MenuAbout}}
	if checkUpdates != nil {
		app = append(app, desktop.MenuItem{Label: msgs.CheckForUpdatesMenuItem(), OnSelect: checkUpdates})
	}
	app = append(app,
		desktop.MenuItem{Separator: true},
		desktop.MenuItem{Label: msgs.SettingsMenuItem(), Shortcut: "mod+,", OnSelect: openSettings},
		desktop.MenuItem{Separator: true},
		desktop.MenuItem{Role: desktop.MenuHide}, desktop.MenuItem{Role: desktop.MenuHideOthers}, desktop.MenuItem{Role: desktop.MenuShowAll},
		desktop.MenuItem{Separator: true},
		desktop.MenuItem{Role: desktop.MenuQuit},
	)
	return []desktop.MenuItem{
		{Label: name, Items: app},
		{Label: msgs.FileMenu(), Items: []desktop.MenuItem{
			{Label: msgs.NewTask(), Shortcut: "mod+n", OnSelect: newTask},
			{Separator: true},
			{Role: desktop.MenuClose},
		}},
		{Label: words.Edit(), Items: []desktop.MenuItem{
			{Role: desktop.MenuUndo}, {Role: desktop.MenuRedo},
			{Separator: true},
			{Role: desktop.MenuCut}, {Role: desktop.MenuCopy}, {Role: desktop.MenuPaste}, {Role: desktop.MenuSelectAll},
		}},
		{Label: words.Window(), Items: []desktop.MenuItem{
			{Role: desktop.MenuMinimize}, {Role: desktop.MenuZoom}, {Role: desktop.MenuToggleFullScreen},
		}},
	}
}
