package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"pigo/internal/agent"
	"pigo/internal/reducer"
	"pigo/internal/sessions"
)

// sideTab is one side chat open in the right panel, as PI NEXT opens it: a
// tab of the right dock with its own RPC child, reducer and transcript. It
// used to take over the main transcript, which is why "侧聊" replaced the
// conversation the user was reading instead of sitting beside it.
type sideTab struct {
	ID     string // session file, the tab key's payload
	Title  string
	Parent string // the parent session the side chat was forked from
	// Subagent marks a saved child session shown read-only in the dock.
	Subagent bool

	// handle is written by the connect goroutine and read by the panel, so
	// it stays an atomic pointer; every other field is UI-thread only.
	handle atomic.Pointer[agent.SessionHandle]
	// openPath is the file pi actually opened (a repaired copy when the
	// recorded cwd is gone) and poolKey the pool entry that holds it. ID
	// stays the row the user pressed, so lookups and the tab's name keep
	// working whatever the recovery did.
	openPath string
	poolKey  string

	Store   *reducer.Store
	Turns   []turnRow
	Running bool
	Draft   string
	Err     string
	Busy    bool

	// refreshPending coalesces the frames of one delta burst into one
	// rebuild: a streaming answer sends a frame per token fragment.
	refreshPending bool
}

// sideTabKey is the dock key of a side chat.
func sideTabKey(path string) string { return "side:" + path }

// sideTabPath reads a side tab key back.
func sideTabPath(key string) string { return strings.TrimPrefix(key, "side:") }

// sideTab returns the open tab for path, nil when it is not open.
func (s *appState) sideTab(path string) *sideTab {
	for _, tab := range s.sideTabs.GetUntracked() {
		if tab.ID == path {
			return tab
		}
	}
	return nil
}

// bumpSide repaints the panel: sideTab fields change under pointers, so the
// signal is the repaint itself.
func (s *appState) bumpSide() {
	s.sideRev.Set(s.sideRev.GetUntracked() + 1)
}

// sideTabTitle names a tab: the fork inherits the parent's title, so the
// label says 侧聊 and the parent rather than repeating the conversation.
func (s *appState) sideTabTitle(parentPath string) string {
	name := ""
	if info, err := sessions.Read(parentPath); err == nil {
		name = info.Name
	}
	if name == "" {
		name = baseName(parentPath)
	}
	if len([]rune(name)) > 20 {
		name = string([]rune(name)[:20]) + "…"
	}
	return "侧聊 · " + name
}

// openSideTab puts a side chat in the right dock and connects it. The main
// conversation keeps its process and its transcript.
func (s *appState) openSideTab(path, parent string) *sideTab {
	if tab := s.sideTab(path); tab != nil {
		s.openDock(sideTabKey(path))
		s.rightOpen.Set(true)
		return tab
	}
	tab := &sideTab{ID: path, Title: s.sideTabTitle(parent), Parent: parent, Store: reducer.New()}
	s.sideTabs.Set(append(append([]*sideTab(nil), s.sideTabs.GetUntracked()...), tab))
	s.dockOpenKinds.Set(append(append([]string(nil), s.dockOpenKinds.GetUntracked()...), sideTabKey(path)))
	s.openDock(sideTabKey(path))
	s.rightOpen.Set(true)
	s.bumpSide()
	go s.connectSideTab(tab)
	return tab
}

// connectSideTab spawns the side chat's own child with the parent's
// read-mostly tools. Its session file already carries the parent's history up
// to the boundary note, so the tab opens with the inherited context and only
// the prompts sent from this tab are new.
func (s *appState) connectSideTab(tab *sideTab) {
	// Test mode starts no child at all, for the app tests that only need the
	// panel's layout.
	if os.Getenv("PIGO_NO_AGENT") == "1" {
		s.cx.Post(func() {
			tab.Err = "测试模式：未启动侧聊子进程"
			s.bumpSide()
		})
		return
	}
	if s.bundle == "" || s.nodePath == "" {
		s.cx.Post(func() {
			tab.Err = "未找到 node 或 rpc-entry.js，请在设置中检查"
			s.bumpSide()
		})
		return
	}
	cwd, missing := s.sessionCwd(tab.ID)
	openPath := tab.ID
	if missing != "" {
		if repaired, err := sessions.RepairCwd(tab.ID, cwd, filepath.Join(s.agentDir, "pigo", "repaired")); err == nil {
			openPath = repaired
		}
	}
	tab.openPath = openPath
	opts := s.agentOptions(cwd, openPath)
	opts.Tools = sessions.SideTools
	key := s.sessionKey(openPath) + "|side"
	tab.poolKey = key

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h, err := s.pool.Acquire(key, opts, func(f agent.Frame) { s.handleSideFrame(tab, f) }, func(err error) {
			s.cx.Post(func() {
				if tab.handle.Load() != nil && tab.Err == "" {
					tab.Err = "侧聊连接已断开"
					if err != nil {
						tab.Err += "：" + err.Error()
					}
					tab.Running = false
					s.bumpSide()
				}
			})
		})
		if err != nil {
			s.cx.Post(func() {
				tab.Err = "启动失败：" + err.Error()
				s.bumpSide()
			})
			return
		}
		if tab.handle.Load() == nil {
			tab.handle.Store(h)
		}
		msgs, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetMessages})
		if err != nil {
			s.cx.Post(func() {
				tab.Err = "读取侧聊失败：" + err.Error()
				s.bumpSide()
			})
			return
		}
		var payload struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if msgs != nil && len(msgs.Data) > 0 {
			_ = json.Unmarshal(msgs.Data, &payload)
		}
		state := messagesFromRPC(payload.Messages)
		s.cx.Post(func() {
			tab.Store.SetState(state)
			tab.Err = ""
			s.rebuildSide(tab)
		})
	}()
}

// handleSideFrame routes a frame into the tab's own reducer. Every mutation of
// the tab happens on the UI thread, through cx.Post, like the main session.
func (s *appState) handleSideFrame(tab *sideTab, f agent.Frame) {
	h := tab.handle.Load()
	if h == nil {
		return
	}
	if f.Type == "response" {
		return // prompts answer through their Call
	}
	if len(f.Raw) == 0 {
		return
	}
	b, err := json.Marshal(f.Raw)
	if err != nil {
		return
	}
	s.cx.Post(func() {
		if tab.handle.Load() != h {
			return
		}
		if !tab.Store.Apply(b) {
			return
		}
		if !tab.refreshPending {
			tab.refreshPending = true
			s.cx.AfterFunc(32*time.Millisecond, func() {
				tab.refreshPending = false
				s.rebuildSide(tab)
			})
		}
	})
}

// rebuildSide re-derives the tab's turns from its reducer, throttled by the
// caller during a stream.
func (s *appState) rebuildSide(tab *sideTab) {
	if s.sideTab(tab.ID) == nil {
		return
	}
	snap := tab.Store.Snapshot()
	tab.Turns = buildTurns(snap)
	tab.Running = snap.Running
	s.bumpSide()
}

// sidePanel is the dock body of a side chat: a header, the transcript and a
// composer of its own. The tab is read-only in tool terms — 侧聊 keeps
// read/bash/grep and never rings or notifies (PI NEXT side sessions).
func (s *appState) sidePanel(path string) core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		s.sideRev.Get() // repaint dependency: tab fields change under pointers
		tab := s.sideTab(path)
		if tab == nil {
			return core.Text{Content: "这个侧聊已关闭", Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
		}
		return s.sideBody(cx, tab)
	})
}

func (s *appState) sideBody(cx *core.Scope, tab *sideTab) core.Node {
	colors := ui.ColorsOf(cx)
	icon := lucide.MessageSquare
	status := "只读工具 · 不通知"
	if tab.Subagent {
		icon = lucide.Bot
		status = "只读 · 子代理"
	} else {
		cx.OnKey("mod+enter", func() { s.sendSide(cx, tab) })
	}
	switch {
	case tab.Err != "":
		status = "读取失败"
	case tab.Running:
		status = "运行中"
	case tab.Busy:
		status = "发送中"
	}
	return core.View{
		TestID: "side-panel",
		Style:  core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
				Padding: core.Edges{Top: 8, Right: 10, Bottom: 6, Left: 12}}, Children: []core.Node{
				ui.Icon(icon, 13, colors.Accent),
				core.Text{Content: tab.Title, MaxLines: 1, TestID: "side-title",
					Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
				core.Text{Content: status, MaxLines: 1, TestID: "side-status",
					Style: core.Style{FontSize: fontMeta, Color: colors.Muted()}},
				core.Show{When: func() bool { return tab.Running }, Then: func(*core.Scope) core.Node {
					return ui.Button{Text: "停止", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "side-stop",
						OnPress: func() { s.abortSide(tab) }}
				}},
			}},
			core.Show{When: func() bool { return tab.Err != "" }, Then: func(*core.Scope) core.Node {
				return core.View{TestID: "side-error", Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
					FlexShrink: 0, Padding: core.Symmetric(4, 12)}, Children: []core.Node{
					ui.Icon(lucide.CircleAlert, 12, colors.Danger),
					core.Text{Content: tab.Err, MaxLines: 2, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), FontSize: fontMeta, Color: colors.Danger()}},
					ui.Button{Text: "重连", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "side-reconnect",
						OnPress: func() { s.reconnectSide(tab) }},
				}}
			}},
			core.Scroll{TestID: "side-transcript", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0),
				Padding: core.Symmetric(12, 12)}, Children: []core.Node{s.sideTranscript(tab)}},
			core.Show{When: func() bool { return !tab.Subagent }, Then: func(cx *core.Scope) core.Node {
				return s.sideComposer(cx, tab)
			}},
		},
	}
}

// sideTranscript lists the side chat's turns: the prompt, the answer as
// markdown, and a one-line count of the tools it ran.
func (s *appState) sideTranscript(tab *sideTab) core.Node {
	return core.View{Style: core.Style{Gap: 16}, Children: []core.Node{
		core.Dynamic(func(cx *core.Scope) core.Node {
			s.sideRev.Get()
			if len(tab.Turns) == 0 {
				return core.Text{TestID: "side-empty", Content: "侧聊继承了父会话的上下文，从这里提问不会打扰主对话",
					Style: core.Style{FontSize: fontLabel, LineHeight: 20, Color: s.colors.Muted()}}
			}
			nodes := make([]core.Node, 0, len(tab.Turns))
			for i := range tab.Turns {
				nodes = append(nodes, s.sideTurn(cx, tab, i))
			}
			return core.View{Style: core.Style{Gap: 16}, Children: nodes}
		}),
	}}
}

// sideTurn is one turn of the side chat. The text is read through the index on
// every render, so a streaming answer keeps growing in the row it started in.
func (s *appState) sideTurn(cx *core.Scope, tab *sideTab, i int) core.Node {
	colors := ui.ColorsOf(cx)
	at := func() (turnRow, bool) {
		if i < 0 || i >= len(tab.Turns) {
			return turnRow{}, false
		}
		return tab.Turns[i], true
	}
	return core.View{TestID: fmt.Sprintf("side-turn-%d", i), Style: core.Style{Gap: 8},
		Children: []core.Node{
			core.Show{When: func() bool { t, ok := at(); return ok && t.Prompt != "" }, Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
					Padding: core.Edges{Left: 8}, Borders: core.Edges{Left: 2}},
					StyleFunc: func() core.Style { return core.Style{BorderColor: ui.Alpha(colors.Accent(), 0.6)} },
					Children: []core.Node{
						core.Text{ContentFunc: func() string {
							t, _ := at()
							return t.Prompt
						}, Selectable: core.SelectableOn, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0),
							FontSize: fontLabel, LineHeight: 20, Color: colors.Text()}},
					}}
			}},
			core.Show{When: func() bool { t, ok := at(); return ok && (t.Final != "" || t.Running || len(t.Tools) > 0) }, Then: func(*core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
					core.Show{When: func() bool { t, ok := at(); return ok && t.Final != "" }, Then: func(*core.Scope) core.Node {
						return core.Func(markdownWith(func() string {
							t, _ := at()
							return t.Final
						}, false, true))
					}},
					core.Show{When: func() bool { t, ok := at(); return ok && (t.Running || len(t.Tools) > 0) }, Then: func(*core.Scope) core.Node {
						label := "思考中…"
						if t, ok := at(); ok {
							if n := len(t.Tools); n > 0 {
								label = fmt.Sprintf("%d 个工具调用", n)
								if t.Running {
									label += " · 运行中"
								}
							}
						}
						return core.Text{Content: label, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: colors.Faint()}}
					}},
				}}
			}},
		}}
}

// sideComposer is the tab's own input: one box, ⌘↩ or the button sends. It
// carries no model picker or attachments — the side chat is a scratchpad on
// the parent's tools and its frozen background.
func (s *appState) sideComposer(cx *core.Scope, tab *sideTab) core.Node {
	colors := ui.ColorsOf(cx)
	canSend := func() bool {
		return tab.handle.Load() != nil && strings.TrimSpace(tab.Draft) != "" && !tab.Busy
	}
	return core.View{TestID: "side-composer", Style: core.Style{FlexShrink: 0, Gap: 6, Padding: core.Symmetric(8, 10),
		Borders: core.Edges{Top: 1}}, StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
		Children: []core.Node{
			ui.Input{
				Value:       func() string { return tab.Draft },
				OnChange:    func(v string) { tab.Draft = v; s.bumpSide() },
				Multiline:   true,
				Placeholder: "在侧聊里提问，主对话不受影响",
				TestID:      "side-input",
				Style:       core.Style{Width: core.Pct(100), MinHeight: core.Pt(56), FontSize: fontLabel},
			},
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				core.Text{Content: "⌘↩ 发送 · 侧聊只带读取类工具", MaxLines: 1,
					Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), FontSize: fontMeta, Color: colors.Faint()}},
				ui.Button{Text: "发送", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "side-send",
					Disabled: func() bool { return !canSend() },
					OnPress:  func() { s.sendSide(cx, tab) }},
			}},
		}}
}

// sendSide sends the tab's draft to its own child, optimistic bubble included:
// the side transcript is not the main reducer, so the two never mix.
func (s *appState) sendSide(cx *core.Scope, tab *sideTab) {
	text := strings.TrimSpace(tab.Draft)
	if text == "" {
		return
	}
	h := tab.handle.Load()
	if h == nil {
		s.notify(ui.ToastWarning, "侧聊未连接", "点侧聊标题栏里的「重连」")
		return
	}
	tab.Busy = true
	t := text
	s.cx.Post(func() {
		tab.Draft = ""
		tab.Store.OptimisticUser(t)
		s.rebuildSide(tab)
	})
	cmd := agent.Command{"type": agent.CmdPrompt, "message": t}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, cmd)
		s.cx.Post(func() {
			tab.Busy = false
			if err != nil {
				tab.Err = "发送失败：" + err.Error()
				tab.Draft = t
				tab.Store.RollbackSend()
				s.rebuildSide(tab)
				return
			}
			if resp != nil && !resp.Success && resp.Error != "" {
				tab.Err = "侧聊：" + resp.Error
			}
			s.bumpSide()
		})
	}()
}

// abortSide stops the side chat's run without touching the main session.
func (s *appState) abortSide(tab *sideTab) {
	h := tab.handle.Load()
	if h == nil {
		return
	}
	if err := h.Client.Send(agent.Command{"type": agent.CmdAbort}); err != nil {
		s.notify(ui.ToastError, "停止失败", err.Error())
	}
}

// reconnectSide restarts a side tab whose child died.
func (s *appState) reconnectSide(tab *sideTab) {
	if h := tab.handle.Load(); h != nil {
		_ = h.Client.Close()
	}
	tab.handle.Store(nil)
	tab.Err = ""
	s.bumpSide()
	go s.connectSideTab(tab)
}

// closeSideTab drops the tab and its process: a side chat that is closed has
// no reason to keep a node child warm.
func (s *appState) closeSideTab(path string) {
	tab := s.sideTab(path)
	if tab == nil {
		return
	}
	// The panel goes first and the child shuts down behind it: Client.Close
	// waits up to 5s for a busy node to answer SIGTERM, and a close button
	// that hangs that long reads as broken.
	if h := tab.handle.Load(); h != nil {
		key := tab.poolKey
		if key == "" {
			key = s.sessionKey(path) + "|side"
		}
		go func() {
			s.pool.Release(key)
			_ = h.Client.Close()
		}()
	}
	next := make([]*sideTab, 0, len(s.sideTabs.GetUntracked()))
	for _, t := range s.sideTabs.GetUntracked() {
		if t.ID != path {
			next = append(next, t)
		}
	}
	s.sideTabs.Set(next)
	open := make([]string, 0, len(s.dockOpenKinds.GetUntracked()))
	for _, kind := range s.dockOpenKinds.GetUntracked() {
		if kind != sideTabKey(path) {
			open = append(open, kind)
		}
	}
	s.dockOpenKinds.Set(open)
	if s.dock.GetUntracked() == sideTabKey(path) {
		keys := s.dockTabKeys()
		if len(keys) > 0 {
			s.dock.Set(keys[0])
		} else {
			s.dock.Set("")
		}
	}
	s.bumpSide()
}

// loadSubagentTab reads a saved child session into the dock tab. A subagent
// keeps no live process here: its transcript is read-only, like PI NEXT's
// subagent rows.
func (s *appState) loadSubagentTab(tab *sideTab) {
	msgs, err := readSessionMessages(tab.ID)
	s.cx.Post(func() {
		if err != nil {
			tab.Err = "读取子代理失败：" + err.Error()
			s.bumpSide()
			return
		}
		tab.Store.SetState(messagesFromRPC(msgs))
		tab.Err = ""
		s.rebuildSide(tab)
	})
}

// readSessionMessages returns the message records of a session file, the
// shape reducer's messagesFromRPC takes.
func readSessionMessages(path string) ([]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		var entry struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &entry) == nil && entry.Type == "message" && len(entry.Message) > 0 {
			out = append(out, append(json.RawMessage(nil), entry.Message...))
		}
	}
	return out, sc.Err()
}
