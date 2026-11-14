package chatview

import (
	_ "embed"
	"sync"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"pigo/agio/agent"
	"pigo/agio/quota"
)

// Marks are SVGs drawn in their own brand colors; pi's and xAI's
// monochrome marks use currentColor, which follows the text.
var (
	//go:embed icons/pi.svg
	piSVG []byte
	//go:embed icons/codex.svg
	codexSVG []byte
	//go:embed icons/claude.svg
	claudeSVG []byte
	//go:embed icons/codebuddy.svg
	codeBuddySVG []byte
	//go:embed icons/xai.svg
	xaiSVG []byte

	piIcon        = lazySVG(piSVG)
	codexIcon     = lazySVG(codexSVG)
	claudeIcon    = lazySVG(claudeSVG)
	codeBuddyIcon = lazySVG(codeBuddySVG)
	xaiIcon       = lazySVG(xaiSVG)
)

// toolIcon is a tool's mark, or a generic one for tools without one.
func toolIcon(tool agent.Tool) func() *core.SVG {
	switch tool {
	case agent.Pi:
		return piIcon
	case agent.Codex:
		return codexIcon
	case agent.Claude:
		return claudeIcon
	case agent.CodeBuddy:
		return codeBuddyIcon
	}
	return lucide.Bot
}

// providerIcon is the mark of the company behind a pi provider, or pi's
// own for the providers without one.
func providerIcon(provider string) func() *core.SVG {
	switch provider {
	case "anthropic":
		return claudeIcon
	case "openai", "openai-codex", "azure-openai-responses":
		return codexIcon
	case "xai":
		return xaiIcon
	}
	return piIcon
}

// modelIcon is a model's mark: its provider's under pi, which routes to
// several, else its tool's.
func modelIcon(m agent.Model) func() *core.SVG {
	if m.Tool == agent.Pi && m.Provider != "" {
		return providerIcon(m.Provider)
	}
	return toolIcon(m.Tool)
}

// quotaIcon is the mark of an account.
func quotaIcon(p quota.Provider) func() *core.SVG {
	switch p {
	case quota.Claude:
		return claudeIcon
	case quota.Codex, quota.PiCodex:
		return codexIcon
	case quota.PiXAI:
		return xaiIcon
	}
	return lucide.Wallet
}

// mark draws a mark at size, its currentColor the text's.
func mark(cx *core.Scope, icon func() *core.SVG, size core.Points) core.Node {
	return core.Icon{SVGFunc: icon, Style: core.Style{Width: core.Pt(size), Height: core.Pt(size), FlexShrink: 0},
		StyleFunc: ui.Colored(ui.ColorsOf(cx).Text)}
}

// leading is a mark as a chip's or option's leading content.
func leading(icon func() *core.SVG) func(cx *core.Scope, size core.Points) core.Node {
	return func(cx *core.Scope, size core.Points) core.Node { return mark(cx, icon, size-1) }
}

// lazySVG parses once on first use, outside package initialization.
func lazySVG(data []byte) func() *core.SVG {
	return sync.OnceValue(func() *core.SVG {
		svg, err := core.ParseSVG(data)
		if err != nil {
			panic(err)
		}
		return svg
	})
}
