package main

import (
	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/desktop"

	// Registering the generated catalog makes it the app's: NSGo's own
	// packages (textview, terminal, desktop) then speak the language it
	// chooses instead of falling back to English (PLAN §9.11).
	_ "piwebnative/locales"
)

func main() {
	// Before anything else: a crash launched from Finder has no terminal to
	// print to, and this keeps its report.
	keepCrashLog()
	// --remote (v2) will replace the StdioTransport with a WebSocket one;
	// internal/agent's Transport interface already isolates that boundary.
	nsgo.Run(Root,
		nsgo.WindowSize(1280, 820),
		nsgo.WindowMaterial(desktop.WindowMaterialSidebar),
	)
}
