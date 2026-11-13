package main

import (
	"github.com/oneo-me/nsgo/core"
)

// Root is the native replacement for pi-web's AppShell (PLAN §7):
// SidebarShell + native session list + streaming Markdown chat + Composer,
// with settings, files/Git, terminal and a command palette in the same
// window. There is no HTTP server, no SSE stream and no listening port:
// one session is one `pi --mode rpc` child process speaking JSONL.
func Root(cx *core.Scope) core.Node {
	return newState(cx).render()
}
