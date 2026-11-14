package main

// Agio's looks, copied into native: a soft gradient over the window's chrome,
// a page and an accent per appearance. The settings choose them apart for
// light and dark; the first of each is the default.

import (
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/desktop"
	"github.com/oneo-me/nsgo/ui"
)

type colorScheme struct {
	ID   string
	Dark bool
	// Plain leaves a window material showing as it is, untinted.
	Plain bool
	// Chrome are the gradient's colors, from the window's top left to its
	// bottom right.
	Chrome             []core.Color
	Page, Text, Accent core.Color
}

var (
	lightSchemes = []colorScheme{
		{ID: "mist", Plain: true, Chrome: rgb(0xe6ebf3, 0xeceef3, 0xf1eee8), Page: core.RGB(0xffffff), Text: core.RGB(0x1f2329), Accent: core.RGB(0x2f6bff)},
		{ID: "blossom", Chrome: rgb(0xf1dcef, 0xf5e6e6, 0xf4ead2), Page: core.RGB(0xfffcfb), Text: core.RGB(0x2b2327), Accent: core.RGB(0xc8417c)},
		{ID: "lagoon", Chrome: rgb(0xd5edf1, 0xdfeafb, 0xebe5fa), Page: core.RGB(0xfcfeff), Text: core.RGB(0x1b2a33), Accent: core.RGB(0x0b8bad)},
		{ID: "meadow", Chrome: rgb(0xdcefda, 0xe9f1d8, 0xf6edd5), Page: core.RGB(0xfefffb), Text: core.RGB(0x1f2a1f), Accent: core.RGB(0x2b9653)},
		{ID: "sunset", Chrome: rgb(0xfcdcc6, 0xf9d6de, 0xe6d7f4), Page: core.RGB(0xfffbf9), Text: core.RGB(0x2e2220), Accent: core.RGB(0xe8622a)},
		{ID: "citrus", Chrome: rgb(0xfbefb8, 0xeef3bf, 0xd5efcf), Page: core.RGB(0xfffef7), Text: core.RGB(0x2a2814), Accent: core.RGB(0xd08300)},
		{ID: "ocean", Chrome: rgb(0xc9e2fb, 0xd0eef4, 0xd9f4e6), Page: core.RGB(0xfbfdff), Text: core.RGB(0x182634), Accent: core.RGB(0x1677ff)},
		{ID: "lilac", Chrome: rgb(0xdcd3fa, 0xe8d5f6, 0xf5daec), Page: core.RGB(0xfdfbff), Text: core.RGB(0x261f38), Accent: core.RGB(0x7c4dff)},
	}
	darkSchemes = []colorScheme{
		{ID: "graphite", Dark: true, Plain: true, Chrome: rgb(0x17181c, 0x1c1d22, 0x202127), Page: core.RGB(0x26272d), Text: core.RGB(0xe8e8eb), Accent: core.RGB(0x5b95ff)},
		{ID: "aurora", Dark: true, Chrome: rgb(0x0d2629, 0x111c33, 0x221737), Page: core.RGB(0x1a2130), Text: core.RGB(0xe2ebf5), Accent: core.RGB(0x4fd1c5)},
		{ID: "dusk", Dark: true, Chrome: rgb(0x261533, 0x33182f, 0x36201c), Page: core.RGB(0x271f2c), Text: core.RGB(0xf1e6f2), Accent: core.RGB(0xe879b9)},
		{ID: "ember", Dark: true, Chrome: rgb(0x26130d, 0x2e170d, 0x251710), Page: core.RGB(0x2a1e19), Text: core.RGB(0xf6e5da), Accent: core.RGB(0xff8a4c)},
		{ID: "nebula", Dark: true, Chrome: rgb(0x191342, 0x29134f, 0x3a1241), Page: core.RGB(0x1e1a35), Text: core.RGB(0xece6ff), Accent: core.RGB(0x8b7bff)},
		{ID: "moss", Dark: true, Chrome: rgb(0x12251a, 0x1b2c15, 0x292b16), Page: core.RGB(0x1c2620), Text: core.RGB(0xe3eedf), Accent: core.RGB(0x8fce5c)},
		{ID: "crimson", Dark: true, Chrome: rgb(0x2c0d17, 0x360f1d, 0x2e1124), Page: core.RGB(0x2a1a20), Text: core.RGB(0xf6e3e8), Accent: core.RGB(0xff5c7a)},
		{ID: "ink", Dark: true, Chrome: rgb(0x0d1a26, 0x10232e, 0x0d2728), Page: core.RGB(0x18222b), Text: core.RGB(0xdceaf0), Accent: core.RGB(0x38bdf8)},
	}
)

func rgb(hex ...uint32) []core.Color {
	out := make([]core.Color, len(hex))
	for i, h := range hex {
		out[i] = core.RGB(h)
	}
	return out
}

func schemeOf(schemes []colorScheme, id string) colorScheme {
	for _, s := range schemes {
		if s.ID == id {
			return s
		}
	}
	return schemes[0]
}

// nativeTheme is the window's palette from the schemes chosen.
func (s *appState) nativeTheme() *core.Theme {
	t := core.DefaultTheme()
	t.Light = schemeOf(lightSchemes, s.lightScheme.GetUntracked()).palette()
	t.Dark = schemeOf(darkSchemes, s.darkScheme.GetUntracked()).palette()
	return t
}

func (s *appState) applyTheme() {
	s.cx.SetTheme(s.nativeTheme())
}

// chromeStyle paints the window's chrome with the gradient of the scheme
// shown, for ui.SidebarShell's ChromeStyle.
func (s *appState) chromeStyle() core.Style {
	sc := schemeOf(lightSchemes, s.lightScheme.Get())
	if s.cx.Dark() {
		sc = schemeOf(darkSchemes, s.darkScheme.Get())
	}
	tint, accent, fill := float32(1), float32(0), sc.Chrome[0]
	if desktop.ShowsMaterial(s.cx) {
		if sc.Plain {
			return core.Style{}
		}
		tint, accent, fill = 0.6, 0.16, 0
		if sc.Dark {
			tint, accent = 0.7, 0.12
		}
	}
	stops := make([]core.ColorStop, len(sc.Chrome))
	for i, c := range sc.Chrome {
		stops[i] = core.ColorStop{Color: ui.Alpha(ui.Mix(c, sc.Accent, accent), tint), At: core.Pct(float32(100 * i / max(len(sc.Chrome)-1, 1)))}
	}
	return core.Style{Background: fill,
		Gradients: []core.Gradient{{Kind: core.GradientLinear, ToCorner: core.ToBottomRight, Stops: stops}}}
}

func (sc colorScheme) palette() core.Palette {
	text, page, accent := sc.Text, sc.Page, sc.Accent
	mid := sc.Chrome[len(sc.Chrome)/2]
	black, white := core.RGB(0x000000), core.RGB(0xffffff)
	p := core.DefaultTheme().Light
	surface, raised, field := page, page, page
	inset := ui.Mix(page, mid, 0.55)
	hover, pressed := ui.Mix(accent, black, 0.1), ui.Mix(accent, black, 0.2)
	onAccent, scrollbar := white, float32(0.30)
	if sc.Dark {
		p = core.DefaultTheme().Dark
		surface, raised, field = ui.Mix(page, text, 0.04), ui.Mix(page, text, 0.08), ui.Mix(page, black, 0.15)
		inset = ui.Mix(page, mid, 0.5)
		hover, pressed = ui.Mix(accent, white, 0.12), ui.Mix(accent, black, 0.12)
		onAccent, scrollbar = ui.Mix(page, black, 0.5), 0.35
	}
	p.Background, p.Surface = page, surface
	p.SurfaceHover, p.SurfacePressed = text.Faded(0.07), text.Faded(0.12)
	p.Border, p.Text, p.TextMuted = text.Faded(0.14), text, text.Faded(0.58)
	p.Accent, p.AccentHover, p.AccentPressed, p.AccentText = accent, hover, pressed, onAccent
	p.Selection, p.Focus, p.Scrollbar = ui.Alpha(accent, 0.24), ui.Alpha(accent, 0.6), text.Faded(scrollbar)
	for name, c := range map[string]core.Color{
		"inset": inset, "raised": raised, "field": field, "fieldBorder": text.Faded(0.14),
		"line": text.Faded(0.10), "faint": text.Faded(0.38), "accentFill": accent,
		"light": text, "onLight": page, "code": text,
	} {
		p.SetColor(name, c)
	}
	return p
}

// schemeNames names the schemes in the language shown.
func schemeNames() map[string]string {
	return map[string]string{
		"mist": "Mist", "blossom": "Blossom", "lagoon": "Lagoon", "meadow": "Meadow",
		"sunset": "Sunset", "citrus": "Citrus", "ocean": "Ocean", "lilac": "Lilac",
		"graphite": "Graphite", "aurora": "Aurora", "dusk": "Dusk", "ember": "Ember",
		"nebula": "Nebula", "moss": "Moss", "crimson": "Crimson", "ink": "Ink",
	}
}

func schemeName(id string) string {
	if name, ok := schemeNames()[id]; ok {
		return name
	}
	return id
}
