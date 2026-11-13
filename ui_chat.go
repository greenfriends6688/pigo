package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/base/textview"
	"github.com/oneo-me/nsgo/base/textview/highlight"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"

	"pigo/internal/agent"
)

// chatView is ui/chat's floating conversation over the reducer's turns: the
// transcript scrolls behind a glass composer that measures itself, so the
// transcript keeps its full height (PLAN §7 component mapping).
func (s *appState) chatView() core.Node {
	turns := s.turns.Get
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), Position: core.Relative},
		Children: []core.Node{
			s.bindFindKey(),
			chat.FloatingConversation{
				State:     s.conversation,
				JumpLabel: "跳到最新",
				TestID:    "conversation",
				Children: []core.Node{
					core.View{Selectable: true, Style: core.Style{Gap: 20, MinWidth: core.Pt(0), MaxWidth: core.Pt(s.chatWidth.Get()), AlignSelf: core.AlignCenter, Width: core.Pct(100)}, Children: []core.Node{
						core.Each(
							turns,
							func(t turnRow) string { return t.ID },
							func(cx *core.Scope, at func() turnRow, _ func() int) core.Node {
								// Build the turn once: its memos and fold state
								// outlive each delta, which is what made long
								// streaming rebuilds crawl.
								turn := at()
								return core.View{Style: core.Style{MinWidth: core.Pt(0)},
									OnLayout: func(b core.Bounds) {
										if s.turnHeights == nil {
											s.turnHeights = make(map[string]core.Points)
										}
										s.turnHeights[turn.ID] = b.Size.Height
									},
									Children: []core.Node{s.turnNode(cx, at)}}
							},
						),
					}},
					core.Show{When: func() bool { return len(turns()) == 0 && s.transcriptLoading.Get() }, Then: func(*core.Scope) core.Node {
						return ui.Skeleton{Lines: 6, TestID: "transcript-loading", Style: core.Style{Padding: core.All(12), MaxWidth: core.Pt(chat.ContentWidth)}}
					}},
					core.Dynamic(func(*core.Scope) core.Node {
						// A window with no usable pi says what to install
						// instead of offering a composer that cannot send.
						if setup := s.piSetup.Get(); setup != "" {
							return s.piSetupCard()
						}
						if len(turns()) == 0 && !s.transcriptLoading.Get() {
							return s.emptyConversation(nil)
						}
						return nil
					}),
					s.autoLoadEarlier(),
					s.loadEarlier(),
				},
				Composer: []core.Node{core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
					s.bashCard(),
					s.disconnectCard(),
					s.approvalCard(),
					core.Show{When: func() bool { return s.queuePanel.Get() && s.queuesActive() }, Then: s.queuePanelView},
					core.Show{When: func() bool { return !s.readOnly.Get() }, Then: func(*core.Scope) core.Node { return s.inlinePalette() }},
					core.Show{When: s.readOnly.Get, Then: s.readOnlyComposer, Else: func(*core.Scope) core.Node { return s.composer() }},
				}}},
			},
			s.findBar(),
			s.minimap(),
			s.composerHistoryKeys(),
		},
	}
}

// bindFindKey owns ⌘F for the transcript. keymap.go's binding only reaches
// the terminal's search, and the newest binding of a key wins, so this one
// keeps that behavior and opens the transcript's find bar otherwise.
func (s *appState) bindFindKey() core.Node {
	return core.Show{When: func() bool { return true }, Then: func(cx *core.Scope) core.Node {
		cx.OnKey("mod+f", s.toggleFind)
		return nil
	}}
}

// minimap is the transcript's turn rail, drawn as a weighted stack: a turn
// with more words gets more length, the current turn is a wider bar, and a
// press scrolls that turn's exact share. The old rail gave every turn an
// equal slot from the top of the window, so a short chat showed eight bars
// bunched under the header and the highlight rarely matched what was on
// screen.
func (s *appState) minimap() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if cx.Env().Window().Width < 1100 {
			return nil
		}
		turns := s.turns.Get()
		if len(turns) < 2 {
			return nil
		}
		weights := make([]float32, len(turns))
		var total float32
		measured := float32(0)
		for _, t := range turns {
			if h, ok := s.turnHeights[t.ID]; ok && h > 0 {
				measured += float32(h)
			}
		}
		for i, t := range turns {
			var w float32
			if measured > 0 {
				if h, ok := s.turnHeights[t.ID]; ok && h > 0 {
					w = float32(h)
				} else {
					// An unmeasured turn borrows the average until its layout.
					w = measured / float32(len(turns))
				}
			} else {
				w = float32(len([]rune(t.Prompt)) + len([]rune(t.Final)) + len(t.Tools)*60)
				for _, step := range t.Steps {
					w += float32(len([]rune(step)))
				}
				w = max(w, 60)
			}
			weights[i] = w
			total += w
		}
		fraction := float32(0)
		if max := s.conversation.MaxOffset(); max > 0 {
			fraction = float32(s.conversation.OffsetNow().Y) / float32(max)
		}
		active := len(turns) - 1
		var walked float32
		for i, w := range weights {
			walked += w
			if fraction*total <= walked {
				active = i
				break
			}
		}
		nodes := make([]core.Node, 0, len(turns))
		var before float32
		for i, t := range turns {
			index := i
			turn := t
			share := weights[i]
			at := (before + share/2) / total
			before += share
			bar := s.colors.Muted()
			height := core.Points(3)
			width := core.Points(3)
			if index == active {
				bar = s.colors.Accent()
				height = 9
				width = 4
			}
			// The slot keeps its weighted flex and its press; the bar inside
			// is both the press target and the hover target of a card that
			// carries the whole prompt, which a one-line tooltip could not.
			nodes = append(nodes, core.View{
				Role: core.RoleButton, Label: "跳到提问", TestID: fmt.Sprintf("minimap-bar-%d", index),
				OnPress: func(core.PressEvent) { s.jumpToFraction(at) },
				Style:   core.Style{FlexGrow: share, FlexBasis: core.Pt(0), MinHeight: core.Pt(6), Width: core.Pt(12), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
				Children: []core.Node{ui.HoverCard{
					// Uncontrolled: the pointer resting on the bar opens it and
					// the card itself keeps it open while the pointer is on it.
					OpenDelay: 300 * time.Millisecond,
					Placement: core.PlaceLeft,
					Align:     core.AlignCenter,
					TestID:    fmt.Sprintf("minimap-hover-%d", index),
					Style:     core.Style{Width: core.Pt(280), Gap: 5},
					Trigger:   core.View{Style: core.Style{Width: core.Pt(width), Height: core.Pt(height), CornerRadius: 2, Background: bar}},
					// Built only while the card shows: a rail of twenty bars
					// does not lay out twenty previews on every scroll.
					Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node { return s.minimapHoverCard(turn) })},
				}},
			})
		}
		return core.View{Style: core.Style{Position: core.Absolute, Right: core.Pt(3), Top: core.Pt(64), Bottom: core.Pt(132),
			Width: core.Pt(12), Direction: core.Column, Gap: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
			Children: nodes}
	})
}

// jumpToFraction scrolls to a turn's weighted share of the transcript.
func (s *appState) jumpToFraction(fraction float32) {
	target := core.Points(float32(s.conversation.MaxOffset()) * fraction)
	s.conversation.ScrollToPoint(core.Point{Y: target})
}

// minimapHoverCard is what a rail bar stands for: the whole prompt, the
// first lines of the answer and what the turn cost. It reads only what the
// turn recorded, so an old bar never speaks for the live run.
func (s *appState) minimapHoverCard(t turnRow) core.Node {
	answer := "尚未回答"
	answerColor := s.colors.Faint()
	if line := firstLine(t.Final, 140); line != "" {
		answer, answerColor = line, s.colors.Muted()
	}
	meta := []string{fmt.Sprintf("%d 个工具", len(t.Tools))}
	if !t.StartedAt.IsZero() {
		meta = append(meta, t.StartedAt.Format("15:04"))
	}
	elapsed := ""
	if t.Elapsed > 0 {
		elapsed = durationLabel(t.Elapsed)
	}
	if line := turnMetaLine(elapsed, "", 0, 0, 0); line != "" {
		meta = append(meta, line)
	}
	return core.View{TestID: "minimap-card-" + t.ID, Style: core.Style{Gap: 4}, Children: []core.Node{
		core.Text{Content: t.Prompt, MaxLines: 3,
			Style: core.Style{FontSize: fontLabel, LineHeight: 18, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
		core.Text{Content: answer, MaxLines: 2,
			Style: core.Style{FontSize: fontLabel, LineHeight: 17, Color: answerColor}},
		core.Text{Content: strings.Join(meta, " · "), MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
	}}
}

// turnNode is one chat.Turn: its prompt, its tool work as one step and the
// assistant's prose as the steps and the final response. Keying the turn by
// its message id keeps the user's fold choice across updates.
func (s *appState) turnNode(cx *core.Scope, at func() turnRow) core.Node {
	status := core.NewMemo(cx, func() chat.Status {
		t := at()
		switch {
		case t.Running:
			return chat.Running
		case t.Failed:
			return chat.Failed
		case t.Final == "" && len(t.Steps) == 0 && len(t.Tools) == 0:
			return chat.Waiting
		default:
			return chat.Completed
		}
	})
	// more is this turn's answer to "展开更早 N 步": each press widens the
	// window by another page. A long run stays cheap to draw without a fake
	// row claiming steps were folded away.
	more := core.NewSignal(cx, 0)
	steps := core.NewMemoFunc(cx, func() []chat.Step {
		t := at()
		if len(t.Blocks) > 0 {
			return s.blocksToSteps(at, more)
		}
		var out []chat.Step
		var work []chat.Activity
		if !s.hideThinking.Get() {
			for i, thought := range t.Thinks {
				index, text := i, thought
				work = append(work, s.thinkActivity(t, fmt.Sprintf("think-%d", i), func() string {
					if t := at(); index < len(t.Thinks) {
						return t.Thinks[index]
					}
					return text
				}, t.Running && i == len(t.Thinks)-1))
			}
		}
		work = append(work, s.groupReadOnly(t.Tools)...)
		if total := len(work); total > 0 {
			out = append(out, chat.Step{ID: "work", Title: fmt.Sprintf("运行 %d 步", total),
				Activities: windowActivities(work, more)})
		}
		for i, text := range t.Steps {
			streaming := t.Running && i == len(t.Steps)-1
			out = append(out, chat.Step{ID: fmt.Sprintf("say-%d", i), Message: markdownStep(text, streaming)})
		}
		return out
	}, nil).Get
	hasFinal := func() bool { return at().Final != "" && !at().Running }

	return chat.Turn{
		TestID:       "turn-" + at().ID,
		Status:       status.Get,
		Conversation: s.conversation,
		Summary:      func() string { return fmt.Sprintf("已折叠 %d 步 · 点开查看", len(steps())) },
		Working: func() string {
			if e := s.runElapsedLabel(); e != "" {
				return "正在处理… " + e
			}
			return "正在处理…"
		},
		Prompt: func(*core.Scope) core.Node {
			return core.Text{Content: at().Prompt, Selectable: core.SelectableOn,
				Style: core.Style{FontSize: fontLabel, LineHeight: 19}}
		},
		Steps: steps,
		DefaultOpen: func() bool {
			return s.expandThinking.Get() && !s.hideThinking.Get() && len(at().Thinks) > 0
		},
		Final:    markdownViewAt(func() string { return at().Final }),
		HasFinal: hasFinal,
		Footer: func(*core.Scope) core.Node {
			t := at()
			all := s.turns.Get()
			last := len(all) == 0 || all[len(all)-1].ID == t.ID
			return s.turnFooter(t, last)
		},
		ShowActions: func() bool { return hasFinal() || at().Failed },
		Actions: func(*core.Scope) core.Node {
			t := at()
			nodes := []core.Node{
				chat.IconAction{Label: "从这条提问分支", TestID: "fork-turn", Icon: lucide.GitBranch,
					OnPress: func() { s.forkFromTurn(t) }},
			}
			if hasFinal() {
				nodes = append(nodes, chat.IconAction{Label: "复制回答", TestID: "copy-turn",
					IconFunc: func() *core.SVG {
						if s.copied.Get() == t.ID {
							return lucide.Check()
						}
						return lucide.Copy()
					},
					Active:  func() bool { return s.copied.Get() == t.ID },
					OnPress: func() { s.copyTurn(t) }})
			}
			if t.Failed {
				nodes = append(nodes, chat.IconAction{Label: "重试", TestID: "retry-turn", Icon: lucide.RefreshCw,
					OnPress: func() { s.send(t.Prompt) }})
			}
			return core.Group(nodes)
		},
	}
}

// turnFooter is one turn's footer: a muted meta line (elapsed, tokens,
// model), the round's diff summary when it changed files, and the failure
// card. The copy/fork/retry actions stay in chat.Turn's own action row.
// last lets the newest turn speak for the live run, whose clock and stats
// only the window knows.
func (s *appState) turnFooter(t turnRow, last bool) core.Node {
	var nodes []core.Node
	if meta := s.turnMeta(t, last); meta != "" {
		nodes = append(nodes, core.Text{Content: meta, TestID: "turn-meta", MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}})
	}
	if files, add, del := turnDiffStats(t); files > 0 {
		nodes = append(nodes, ui.Button{
			Text:    fmt.Sprintf("本轮改动：%d 个文件 +%d −%d", files, add, del),
			Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "turn-diff-summary",
			OnPress: func() { s.openDock("review") },
		})
	}
	if written := s.turnWrittenFiles(t); len(written) > 0 {
		// chat.ResultSummary is ui/chat's bounded artifact list: three rows,
		// the rest one press away, each opening the file it names.
		items := make([]chat.SummaryItem, 0, len(written))
		for _, path := range written {
			file := path
			items = append(items, chat.SummaryItem{
				ID: file, Label: filepath.Base(file), Detail: s.relativePath(file),
			})
		}
		nodes = append(nodes, chat.ResultSummary{
			Title: "本轮文件", Description: fmt.Sprintf("%d 个文件", len(written)),
			ActionLabel: "审查", MoreLabel: fmt.Sprintf("显示全部 %d 个", len(written)), LessLabel: "收起",
			Items:        items,
			Conversation: s.conversation,
			OnAction:     func() { s.openDock("review") },
			OnSelect:     func(id string) { s.openFile(id) },
		})
	}
	switch {
	case t.Error != "":
		nodes = append(nodes, s.turnErrorCard(t))
	case t.Failed:
		nodes = append(nodes, core.Text{Content: "本轮失败，可点「重连」后重试",
			Style: core.Style{FontSize: fontLabel, Color: s.colors.Danger()}})
	}
	if len(nodes) == 0 {
		return nil
	}
	return core.View{Style: core.Style{Gap: 6, MinWidth: core.Pt(0)}, Children: nodes}
}

// turnMeta is the footer's line: what the turn cost. It prefers what the
// turn recorded (Elapsed, Usage, Model); only the newest turn falls back to
// the live run's clock, token stats and the window's model, because that is
// all the app knows before pi reports them per turn.
func (s *appState) turnMeta(t turnRow, last bool) string {
	elapsed := ""
	if t.Elapsed > 0 {
		elapsed = durationLabel(t.Elapsed)
	} else if last {
		elapsed = s.runElapsedLabel()
	}
	model := t.Model
	if model == "" && last {
		model = s.modelName.Get()
	}
	return turnMetaLine(elapsed, model, t.Usage.In, t.Usage.Out, t.Usage.Cache)
}

// turnMetaLine joins the meta parts, leaving out the ones unknown.
func turnMetaLine(elapsed, model string, in, out, cache int) string {
	var parts []string
	if elapsed != "" {
		parts = append(parts, "耗时 "+elapsed)
	}
	if in+out+cache > 0 {
		tokens := fmt.Sprintf("输入 %s · 输出 %s", shortCount(int64(in)), shortCount(int64(out)))
		if cache > 0 {
			tokens += " · 缓存 " + shortCount(int64(cache))
		}
		parts = append(parts, tokens)
	}
	if model != "" {
		parts = append(parts, model)
	}
	return strings.Join(parts, " · ")
}

// turnDiffStats counts the files and lines one turn's edit/write calls
// changed.
func turnDiffStats(t turnRow) (files, add, del int) {
	for _, tool := range t.Tools {
		if tool.Diff == "" {
			continue
		}
		files++
		add += tool.Add
		del += tool.Del
	}
	return files, add, del
}

// turnWrittenFiles lists the files one turn wrote, in the order it touched
// them: the write-like calls that carried a diff, resolved against the
// project so a chip opens the same path the file tree does. A file touched
// twice is listed once; the caller caps what it draws and counts the rest.
func (s *appState) turnWrittenFiles(t turnRow) []string {
	root := s.projectRoot()
	var out []string
	seen := make(map[string]bool, 4)
	for _, tool := range t.Tools {
		if tool.Diff == "" {
			continue
		}
		name := strings.ToLower(tool.Name)
		if !strings.Contains(name, "edit") && !strings.Contains(name, "write") && !strings.Contains(name, "apply_patch") {
			continue
		}
		path, _ := tool.Args["path"].(string)
		if path == "" {
			path, _ = tool.Args["file_path"].(string)
		}
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// blocksToSteps draws a turn's blocks in the order they happened: reasoning
// and tool runs gather into one collapsible work step, and each message the
// assistant wrote flushes it, so commentary between tools stays where it
// belongs instead of all tools piling above all prose.
func (s *appState) blocksToSteps(at func() turnRow, more *core.Signal[int]) []chat.Step {
	t := at()
	var out []chat.Step
	var work []chat.Activity
	var total int
	flush := func(id string) {
		if len(work) == 0 {
			return
		}
		out = append(out, chat.Step{ID: id, Title: fmt.Sprintf("运行 %d 步", total),
			Activities: windowActivities(work, more)})
		work = nil
		total = 0
	}
	for i, b := range t.Blocks {
		switch b.Kind {
		case "think":
			if s.hideThinking.Get() {
				continue
			}
			index := i
			work = append(work, s.thinkActivity(t, fmt.Sprintf("think-%d", i), func() string {
				if t := at(); index < len(t.Blocks) && t.Blocks[index].Kind == "think" {
					return t.Blocks[index].Text
				}
				return ""
			}, t.Running && i == len(t.Blocks)-1))
			total++
		case "tools":
			cards := s.groupReadOnly(b.Tools)
			work = append(work, cards...)
			total += len(cards)
		case "say":
			flush(fmt.Sprintf("work-%d", i))
			streaming := t.Running && i == len(t.Blocks)-1
			// The text is read again on every render: a delta that lands after
			// this step was mounted must reach the row it streams into, which
			// is what made an answer appear only after reopening the session.
			index := i
			out = append(out, chat.Step{ID: fmt.Sprintf("say-%d", i), Message: markdownStepFunc(func() string {
				if t := at(); index < len(t.Blocks) && t.Blocks[index].Kind == "say" {
					return t.Blocks[index].Text
				}
				return ""
			}, streaming)})
		}
	}
	flush("work-last")
	return out
}

// thinkActivity is one collapsible reasoning card. "思考了 N 秒" appears
// only when the turn recorded how long it ran; a running turn shows a
// shimmer in the body instead of a made-up clock, and a finished one starts
// folded unless the user asked for expanded thinking (its own fold choice,
// once made, stays with ActivityRow).
func (s *appState) thinkActivity(t turnRow, id string, thought func() string, live bool) chat.Activity {
	action := "思考"
	if t.Elapsed > 0 {
		action = "思考了 " + durationLabel(t.Elapsed)
	}
	act := chat.Activity{
		ID: id, Action: action, Detail: firstLine(thought(), 80), Icon: lucide.Brain,
		Status: chat.Completed, MaxHeight: 260, Mono: false,
		Expanded: s.expandThinking.Get(),
	}
	if live {
		act.Status = chat.Running
		act.Detail = "正在思考…"
		act.Content = func(*core.Scope) core.Node {
			return chat.Shimmer{Text: func() string { return firstLine(thought(), 70) }, Size: 12,
				Color: s.colors.Muted, Highlight: s.colors.Text}
		}
		return act
	}
	act.Content = func(*core.Scope) core.Node {
		return core.Text{Content: thought(), Selectable: core.SelectableOn,
			Style: core.Style{FontSize: fontLabel, LineHeight: 18, Color: s.colors.Muted()}}
	}
	return act
}

// activityPage is how many steps a window shows before the user asks for
// more; each press of the "expand older" row adds another page.
const activityPage = 60

// windowActivities keeps the newest page (plus what the user has asked to
// reveal) and puts one row above it that reveals another page. Nothing
// claims earlier steps were folded away: they are one press from view.
func windowActivities(work []chat.Activity, more *core.Signal[int]) []chat.Activity {
	limit := activityPage + more.Get()*activityPage
	if len(work) <= limit {
		return work
	}
	hidden := len(work) - limit
	page := min(hidden, activityPage)
	row := chat.Activity{
		ID: "older-more", Action: fmt.Sprintf("展开更早 %d 步", page),
		Detail: fmt.Sprintf("共 %d 步", len(work)), Icon: lucide.ChevronsUp,
		Status: chat.Completed, Mono: false, Expanded: true, MaxHeight: 60,
		Content: func(*core.Scope) core.Node {
			return ui.Button{Text: fmt.Sprintf("展开更早 %d 步", page), Variant: ui.ButtonGhost,
				Size: ui.SizeSmall, TestID: "expand-older",
				OnPress: func() { more.Set(more.GetUntracked() + 1) }}
		},
	}
	return append([]chat.Activity{row}, work[hidden:]...)
}

// turnErrorCard is a hard failure the transcript itself explains: what the
// agent reported, what to try, and the buttons that do it.
func (s *appState) turnErrorCard(t turnRow) core.Node {
	return core.View{
		TestID: "turn-error",
		Style:  core.Style{CornerRadius: radiusControl, BorderWidth: 1, Padding: core.Symmetric(8, 10), Gap: 6, Background: ui.Alpha(s.colors.Danger(), 0.07)},
		StyleFunc: func() core.Style {
			return core.Style{BorderColor: ui.Alpha(s.colors.Danger(), 0.4)}
		},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				ui.Icon(lucide.CircleAlert, 13, s.colors.Danger),
				core.Text{Content: "本轮失败", Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Danger()}},
			}},
			core.Text{Content: t.Error, Selectable: core.SelectableOn, MaxLines: 4,
				Style: core.Style{FontSize: fontLabel, LineHeight: 17, Color: s.colors.Text()}},
			core.View{Style: core.Style{Direction: core.Row, Gap: 6}, Children: []core.Node{
				ui.Button{Text: "重试", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "turn-error-retry",
					OnPress: func() { s.send(t.Prompt) }},
				ui.Button{Text: "复制错误", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "turn-error-copy",
					OnPress: func() { s.cx.SetClipboard(t.Error); s.notify(ui.ToastInfo, "已复制错误信息", "") }},
			}},
		},
	}
}

// forkFromTurn branches a new session at one prompt's entry. History
// replays whose get_messages carried no entry id cannot fork directly —
// passing the synthetic "history-N" id used to fail silently — so those
// open the branch picker, which always has real ids.
func (s *appState) forkFromTurn(t turnRow) {
	id := t.EntryID
	if id == "" && !strings.HasPrefix(t.ID, "history-") {
		id = t.ID
	}
	if id == "" || strings.HasPrefix(id, "history-") {
		s.notify(ui.ToastInfo, "该会话没有可用的消息 ID", "已打开分支列表，从那里选择起点")
		s.openBranches()
		return
	}
	s.forkAt(id)
}

// emptyConversation shows over the transcript while there is nothing to read.
func (s *appState) emptyConversation(*core.Scope) core.Node {
	hero := core.View{
		Style: core.Style{AlignItems: core.AlignCenter, Gap: 10},
		Children: []core.Node{
			ui.Icon(lucide.Sparkles, 28, s.colors.Accent),
			core.Text{Content: "PIGO", Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.8, Color: s.colors.Muted()}},
			core.Text{Content: emptyChatHint, Style: core.Style{FontSize: fontLabel, LineHeight: 21, TextAlign: core.TextAlignCenter, Color: s.colors.Muted()}},
		},
	}
	column := core.View{
		Style:    core.Style{FlexGrow: 1, JustifyContent: core.JustifyCenter, Padding: core.Symmetric(24, 20), Gap: 10, MinHeight: core.Pt(0)},
		Children: []core.Node{hero},
	}
	frame := core.View{
		Style:    core.Style{FlexGrow: 1, Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth), MinHeight: core.Pt(0)},
		Children: []core.Node{column},
	}
	return core.View{
		Style:     core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pt(0), Right: core.Pt(0), AlignItems: core.AlignCenter},
		StyleFunc: func() core.Style { return core.Style{Bottom: core.Pt(s.conversation.ComposerHeight())} },
		Children:  []core.Node{frame},
	}
}

// mentionPicker is ui/chat's Picker over the project's files: choosing one
// inserts an @-mention into the draft instead of uploading anything.
func (s *appState) mentionPicker() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		index := s.mentions.Get()
		options := make([]chat.Option, 0, len(index))
		for _, rel := range index {
			options = append(options, chat.Option{ID: rel, Label: rel})
		}
		return chat.Picker{
			Label:       func() string { return "引用文件" },
			Icon:        lucide.AtSign,
			Options:     options,
			Selected:    func() string { return "" },
			OnChange:    s.insertMention,
			SearchLabel: "搜索文件",
			EmptyText:   "没有匹配的文件",
			Width:       300,
			TestID:      "mention-picker",
		}
	})
}

// slashPicker offers pi's slash commands while the draft starts with "/".
func (s *appState) slashPicker() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		draft := strings.TrimSpace(s.draft.Get())
		if !strings.HasPrefix(draft, "/") || strings.ContainsAny(draft, " ") {
			return nil
		}
		prefix := strings.ToLower(strings.TrimPrefix(draft, "/"))
		options := s.slashOptions(prefix)
		if len(options) == 0 {
			return nil
		}
		return chat.Picker{
			Label:       func() string { return "斜杠命令" },
			Icon:        lucide.Command,
			Options:     options,
			Selected:    func() string { return "" },
			OnChange:    s.chooseSlash,
			SearchLabel: "搜索命令",
			EmptyText:   "没有匹配的命令",
			Width:       340,
			TestID:      "slash-picker",
		}
	})
}

// slashBuiltins are the commands the window itself runs, as pi's TUI lists
// them beside extension commands.
var slashBuiltins = []struct{ ID, Name, Label, Description string }{
	{"compact", "compact", "压缩上下文", "把较早的对话压缩成摘要"},
	{"clone", "clone", "克隆会话", "在当前位置复制出新会话"},
	{"fork", "fork", "Fork", "从最后一条提问新建分支"},
	{"new-session", "new", "新建会话", "开始一段新对话"},
	{"rename", "name", "重命名会话", "给当前会话起名"},
	{"export-html", "export", "导出 HTML", "导出当前会话的网页"},
	{"reconnect", "reload", "重连", "重新连接 pi 子进程"},
	{"abort", "stop", "停止", "停止当前运行"},
	{"open-project", "project", "打开项目", "切换文件树与终端所在目录"},
	{"view-settings", "settings", "设置", "打开设置弹窗"},
	{"trust-cwd", "trust", "信任项目", "信任当前目录再运行工具"},
}

// slashOptions merges the window's builtins with the commands pi reports.
func (s *appState) slashOptions(prefix string) []chat.Option {
	options := make([]chat.Option, 0, 32)
	match := func(name string) bool {
		return prefix == "" || strings.HasPrefix(strings.ToLower(name), prefix)
	}
	for _, b := range slashBuiltins {
		if !match(b.Name) {
			continue
		}
		options = append(options, chat.Option{ID: "cmd:" + b.ID, Label: "/" + b.Name, Detail: "内置", Description: b.Description})
	}
	for _, c := range s.slashCommands.Get() {
		if !match(c.Name) || len(options) >= 60 {
			continue
		}
		detail := c.Source
		if detail == "builtin" {
			detail = "内置"
		}
		options = append(options, chat.Option{ID: "pi:" + c.Name, Label: "/" + c.Name, Detail: detail, Description: c.Description})
	}
	return options
}

// chooseSlash runs a builtin or writes an extension's command into the draft.
func (s *appState) chooseSlash(id string) {
	if strings.HasPrefix(id, "cmd:") {
		s.runCommand(strings.TrimPrefix(id, "cmd:"))
		return
	}
	s.insertSlash(strings.TrimPrefix(id, "pi:"))
}

// thinkingLevels is pi's reasoning ladder, weakest first.
var thinkingLevels = []chat.Option{
	{ID: "off", Label: "关闭"}, {ID: "minimal", Label: "极低"}, {ID: "low", Label: "低"},
	{ID: "medium", Label: "中"}, {ID: "high", Label: "高"}, {ID: "xhigh", Label: "极高"},
}

// modelPickerChip is the composer's model control: the live model's name on a
// chip, a searchable list behind it. The thinking level is its own chip
// (thinkingPicker): ui/chat's EffortPicker draws the panel with the strength
// bar, which a merged panel could not.
func (s *appState) modelPickerChip() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		// A model list that arrives after the first render (the session's
		// catalog, the configured providers) must still show the control.
		if len(s.models.Get()) == 0 && len(s.providers.Get()) == 0 {
			return nil
		}
		return modelPickerPopover{s: s}
	})
}

// modelPickerPopover is that control. It is a node of its own so its signals
// outlive a render, as the panel's open state and search must.
type modelPickerPopover struct{ s *appState }

// Render implements core.Node.
func (p modelPickerPopover) Render(cx *core.Scope) core.Node {
	s := p.s
	open := core.NewSignal(cx, false)
	query := core.NewSignal(cx, "")
	return base.Popover{
		Open: open.Get,
		OnOpenChange: func(v bool, _ base.Reason) {
			open.Set(v)
			if v {
				query.Set("")
			}
		},
		Placement: core.PlaceTop,
		Align:     core.AlignEnd,
		Offset:    6,
		Margin:    8,
		Label:     "切换模型",
		TestID:    "model-picker",
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 5, Height: core.Pt(26),
			Padding: core.Symmetric(0, 8), CornerRadius: radiusCard, MinWidth: core.Pt(0),
			Background: ui.Alpha(s.colors.Text(), 0.04), BorderWidth: 1, BorderColor: s.colors.Border()},
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.08)},
				Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.12)}}
		},
		PopupStyle: core.Style{Width: core.Pt(360), Padding: core.All(10), Gap: 8, CornerRadius: radiusCard,
			Background: s.colors.Raised(), BorderWidth: 1, BorderColor: s.colors.Border(),
			Shadows: []core.BoxShadow{ui.Shadow(0, 10, 28, 0, ui.Black(0.18))}},
		Trigger: core.Group{
			ui.Icon(lucide.Cpu, 12, s.colors.Muted),
			core.Text{ContentFunc: func() string {
				if name := s.modelName.Get(); name != "" {
					return name
				}
				return "模型"
			}, TestID: "model-picker-label", MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted(), FlexShrink: 1, MinWidth: core.Pt(0)}},
			ui.Icon(lucide.ChevronDown, 10, s.colors.Muted),
		},
		Children: []core.Node{
			ui.Input{Value: query.Get, OnChange: query.Set, Placeholder: "搜索模型", TestID: "model-search",
				Style: core.Style{Height: core.Pt(28), FontSize: 12.5}},
			p.modelList(open, query),
		},
	}
}

// thinkingPicker is chat.EffortPicker over pi's reasoning levels: the chip
// shows the level, and the panel draws the strength bar with a stop per level
// and the keyboard stepping the picker has.
func (s *appState) thinkingPicker() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		if s.thinking.Get() == "" {
			return nil
		}
		return chat.EffortPicker{Label: "思考", Icon: lucide.Brain, Levels: thinkingLevels, Off: true,
			Selected: s.thinking.Get, OnChange: s.setThinkingLevel, Width: 260, TestID: "thinking-picker"}
	})
}

// modelList is the searchable model rows, the live model ticked.
func (p modelPickerPopover) modelList(open *core.Signal[bool], query *core.Signal[string]) core.Node {
	s := p.s
	return core.Dynamic(func(*core.Scope) core.Node {
		models := sortedModels(s.models.Get())
		if len(models) == 0 {
			for _, pv := range s.providers.Get() {
				for _, m := range pv.Models {
					models = append(models, modelItem{ID: m.ID, Name: m.Name, Provider: pv.ID})
				}
			}
		}
		needle := strings.ToLower(strings.TrimSpace(query.Get()))
		def := s.modelDefault.Get()
		current := s.modelName.Get()
		rows := make([]core.Node, 0, len(models))
		for _, m := range models {
			model := m
			if needle != "" && !strings.Contains(strings.ToLower(model.Name), needle) &&
				!strings.Contains(strings.ToLower(model.Provider), needle) {
				continue
			}
			detail := model.Provider
			if model.Provider+"/"+model.ID == def {
				detail = "默认 · " + detail
			}
			rows = append(rows, ui.MenuItem{
				Text:    model.Name,
				Checked: func() bool { return model.Name == current },
				TestID:  "model-option-" + model.Provider + "/" + model.ID,
				Trailing: core.Text{Content: detail, MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
				OnSelect: func() { s.setModel(model); open.Set(false) },
			})
		}
		if len(rows) == 0 {
			return core.Text{Content: "没有匹配的模型", MaxLines: 1,
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted(), Padding: core.All(8)}}
		}
		return core.Scroll{Style: core.Style{MaxHeight: core.Pt(300), FlexShrink: 1},
			Children: []core.Node{core.View{Style: core.Style{Gap: 1}, Children: rows}}}
	})
}

// sortedModels orders the picker's models by provider and then by name,
// keeping pi's order for two models that share both.
func sortedModels(models []modelItem) []modelItem {
	out := append([]modelItem(nil), models...)
	slices.SortStableFunc(out, func(a, b modelItem) int {
		if a.Provider != b.Provider {
			return strings.Compare(a.Provider, b.Provider)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// setToolMode switches the tool policy and reconnects the same session with
// the new allowlist, which pi only reads when a process starts.
func (s *appState) setToolMode(mode string) {
	if mode != "read" && mode != "none" {
		mode = "default"
	}
	s.toolMode.Set(mode)
	path := s.currentSessionPath()
	if path == "" {
		return
	}
	tools := ""
	none := false
	switch mode {
	case "read":
		tools = "read,grep,glob,ls"
	case "none":
		none = true
	case "default":
		s.connect(path)
		s.notify(ui.ToastInfo, "工具模式已切换", "默认工具，历史保留")
		return
	}
	s.notify(ui.ToastInfo, "工具模式已切换", "会话已用新的工具权限重启，历史保留")
	s.connectWith(path, "|mode:"+mode, tools, none)
}

// contextMeter is the composer's context-window gauge, with a hover card
// carrying the numbers the ring can only hint at. It takes no press: the
// gauge used to open an empty popup on click, which said nothing the hover
// card does not say better.
func (s *appState) contextMeter() core.Node {
	fraction := func() float64 {
		window := s.ctxWindow.Get()
		if window <= 0 {
			return -1
		}
		f := float64(s.ctxUsed.Get()) / float64(window)
		return min(max(f, 0), 1)
	}
	value := func() string {
		window := s.ctxWindow.Get()
		if window <= 0 {
			return "未知"
		}
		return fmt.Sprintf("%s / %s", shortCount(int64(s.ctxUsed.Get())), shortCount(int64(window)))
	}
	ring := core.Canvas{Label: "上下文窗口",
		Style: core.Style{Width: core.Pt(13), Height: core.Pt(13), FlexShrink: 0},
		Draw: func(d *core.Drawing, size core.Size) {
			const stroke = 2.5
			r := (float32(size.Width) - stroke) / 2
			x, y := float32(size.Width)/2, float32(size.Height)/2
			d.SetLineWidth(stroke)
			d.BeginPath().Arc(x, y, r, 0, 2*math.Pi, false).SetStrokeColor(s.colors.Border()).Stroke()
			if f := fraction(); f > 0 {
				start := float32(-math.Pi / 2)
				d.BeginPath().Arc(x, y, r, start, start+float32(min(f, 1))*2*math.Pi, false).SetStrokeColor(s.colors.Accent()).Stroke()
			}
		}}
	trigger := core.View{TestID: "context-meter", Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: []core.Node{
		ring,
		core.Text{ContentFunc: value, TestID: "context-meter-value", MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
	}}
	return ui.HoverCard{
		// Uncontrolled like the rail's cards: the pointer opens it after a
		// short rest and the card keeps it open while it is read.
		OpenDelay:  300 * time.Millisecond,
		CloseDelay: 200 * time.Millisecond,
		Placement:  core.PlaceTop,
		Align:      core.AlignEnd,
		TestID:     "context-hover",
		Style:      core.Style{Width: core.Pt(260), Gap: 5},
		Trigger:    trigger,
		Children:   []core.Node{core.Dynamic(func(*core.Scope) core.Node { return s.contextHoverCard() })},
	}
}

// contextHoverCard is the meter's card: the window and the share used, this
// session's tokens, and the model that is spending them.
func (s *appState) contextHoverCard() core.Node {
	window, used := s.ctxWindow.Get(), s.ctxUsed.Get()
	children := make([]core.Node, 0, 4)
	if window <= 0 {
		children = append(children, core.Text{Content: "上下文窗口未知", MaxLines: 1,
			Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}})
	} else {
		share := min(max(float64(used)/float64(window), 0), 1)
		children = append(children, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
			core.Text{Content: fmt.Sprintf("已用 %s / %s", shortCount(int64(used)), shortCount(int64(window))), MaxLines: 1,
				Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
			core.Text{Content: fmt.Sprintf("%.0f%%", share*100), MaxLines: 1,
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
		}})
	}
	in, out, cache := int(s.statsIn.Get()), int(s.statsOut.Get()), int(s.statsCache.Get())
	if in+out+cache > 0 {
		parts := []string{"输入 " + shortCount(int64(in)), "输出 " + shortCount(int64(out))}
		if cache > 0 {
			parts = append(parts, "缓存 "+shortCount(int64(cache)))
		}
		children = append(children, core.Text{Content: strings.Join(parts, " · "), MaxLines: 1,
			Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}})
	}
	if model := s.modelName.Get(); model != "" {
		children = append(children, core.Text{Content: model, MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}})
	}
	return core.View{TestID: "context-hover-card", Style: core.Style{Gap: 4}, Children: children}
}

// composerKey lets Enter take the inline list's highlighted match instead
// of sending: /compact and @path are usable from the keyboard, as pi's own
// composer does. The list's own bindings already consume the key when they
// fire; this is the fallback for the editor's Return.
func (s *appState) composerKey(key string) {
	if s.paletteHandled {
		return
	}
	if key != "Enter" {
		return
	}
	_, options := s.paletteOptions(s.draft.GetUntracked())
	if len(options) == 0 {
		return
	}
	options[s.paletteSelection(len(options))].choose()
	s.paletteIndex.Set(0)
	s.paletteHandled = true
}

// toggleRightPanel shows the dock on its last tab, or hides it. It is the
// composer's one panel control, in place of three tab buttons.
func (s *appState) toggleRightPanel() {
	if s.rightOpen.GetUntracked() {
		s.rightOpen.Set(false)
		return
	}
	// The panel reopens on the tab it was left on, or on the first tab in the
	// strip when every tab was closed.
	if s.dock.GetUntracked() == "" {
		if keys := s.dockTabKeys(); len(keys) > 0 {
			s.dock.Set(keys[0])
		}
	}
	s.rightOpen.Set(true)
	switch s.dock.GetUntracked() {
	case "terminal":
		s.ensureTerminal()
	case "review":
		s.loadGit()
	case "subagents":
		s.loadSubagents()
	}
}

// onDraftChange stores the draft and returns the inline list to its first
// row: typing is what reopens a dismissed list.
func (s *appState) onDraftChange(text string) {
	s.draft.Set(text)
	s.paletteIndex.Set(0)
	s.paletteDismissed.Set(false)
	s.historyAt.Set(-1)
	s.historyDraft = ""
}

// historyMove walks the sent prompts with ↑/↓, restoring the draft the user
// had before stepping into the history when it walks past the newest.
func (s *appState) historyMove(delta int) {
	list := s.promptHistory.GetUntracked()
	if len(list) == 0 {
		return
	}
	at := s.historyAt.GetUntracked()
	if at < 0 {
		if delta > 0 {
			return
		}
		s.historyDraft = s.draft.GetUntracked()
	}
	next := at + delta
	if next < -1 {
		next = -1
	}
	if next >= len(list) {
		next = len(list) - 1
	}
	if next < 0 {
		s.draft.Set(s.historyDraft)
	} else {
		s.draft.Set(list[next])
	}
	s.historyAt.Set(next)
}

// paletteShowing reports whether the @-mention or /-command list is on
// screen and therefore owns ↑↓/Tab/Enter.
func (s *appState) paletteShowing() bool {
	if s.paletteDismissed.GetUntracked() {
		return false
	}
	_, options := s.paletteOptions(s.draft.GetUntracked())
	return len(options) > 0
}

// approvalShowing reports whether the extension select card owns ↑↓/Enter.
func (s *appState) approvalShowing() bool {
	req := s.extCurrent.GetUntracked()
	return s.extOpen.GetUntracked() && req.ID != "" && req.Method == "select" && len(req.Options) > 0
}

// composerHistoryKeys binds ↑/↓ only while the composer has the keyboard and
// no nearer list (mention/command palette, approval card, queue panel) is
// showing. Those lists bind ↑↓ themselves; NSGo runs the newest binding, so
// an unconditional history binding used to steal their arrows.
func (s *appState) composerHistoryKeys() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		if !s.composerFocused.Get() || s.paletteShowing() || s.approvalShowing() || s.queuePanel.Get() {
			return core.View{}
		}
		cx.OnKeyWith("up", core.KeyOptions{When: core.KeyAlways}, func() { s.historyMove(-1) })
		cx.OnKeyWith("down", core.KeyOptions{When: core.KeyAlways}, func() { s.historyMove(1) })
		return core.View{}
	})
}

// queuesActive is true while pi holds steering or follow-up prompts.
func (s *appState) queuesActive() bool {
	q := strings.TrimSpace(s.queues.Get())
	return q != "" && q != "steering 0 · followUp 0"
}

// readOnlyComposer replaces the input while a subagent's transcript is
// open: prompting a child session would rewrite what it recorded, so the
// panel shows a way back to the editable session instead.
func (s *appState) readOnlyComposer(*core.Scope) core.Node {
	return core.View{TestID: "readonly-composer", Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100),
		MaxWidth: core.Pt(chat.ContentWidth), Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
		Padding: core.Symmetric(8, 12), CornerRadius: radiusCard, BorderWidth: 1, Background: s.colors.Surface()},
		StyleFunc: func() core.Style { return core.Style{BorderColor: s.colors.Border()} },
		Children: []core.Node{
			ui.Icon(lucide.Bot, 14, s.colors.Muted),
			core.Text{Content: "只读：子代理会话", MaxLines: 1, Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.Button{Text: "恢复输入", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "readonly-enable",
				OnPress: func() { s.readOnly.Set(false) }},
			ui.Button{Text: "新建会话", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "readonly-new",
				OnPress: func() { s.readOnly.Set(false); s.newSession() }},
		}}
}

// composerFooterVisible keeps the composer's footer off the page when it
// has nothing to say: an empty stats row with a stray queue button was the
// loudest clutter under the input.
func (s *appState) composerFooterVisible() bool {
	return s.banner.Get() != "" || s.running.Get() || strings.TrimSpace(s.widgetText.Get()) != "" ||
		strings.TrimSpace(s.statsLine.Get()) != "" || s.queuesActive()
}

// composer is chat.Composer on glass: the accessory row carries the working
// directory and the right dock's tabs, the footer the runtime state.
func (s *appState) composer() core.Node {
	wide := func(cx *core.Scope) bool { return cx.Env().Window().Width >= 900 }
	return chat.Composer{
		Draft:          s.draft.Get,
		OnDraftChange:  s.onDraftChange,
		Label:          "消息",
		Placeholder:    "输入消息，@ 引用文件，/ 唤起命令，Enter 发送",
		SendLabel:      "发送",
		StopLabel:      "停止",
		Running:        s.running.Get,
		OnSend:         func() { s.send(s.draft.GetUntracked()) },
		OnStop:         s.stop,
		OnKeyPress:     s.composerKey,
		OnFocus:        s.composerFocused.Set,
		SubmitOnEnter:  s.sendOnEnter.Get,
		Glass:          true,
		State:          s.conversation,
		OnDropFiles:    s.attachFiles,
		OnPaste:        s.pasteImages,
		Attachments:    s.attachmentsView,
		HasAttachments: func() bool { return len(s.attachments.Get()) > 0 },
		TestID:         "composer-card",
		Accessory: func(cx *core.Scope) core.Node {
			// Three controls, not seven: attach, the right panel, and the
			// model controls on the trailing edge. The dock's own header
			// carries Files/Review/Terminal, so repeating them under the
			// input was the bulk of the clutter.
			return core.Group{
				chat.IconAction{Label: "附件", Icon: lucide.Paperclip, TestID: "attach-files",
					OnPress: s.pickAttachments},
				// 侧聊 opens beside this conversation in the right panel, the
				// way PI NEXT's composer offers it: a scratchpad on the same
				// context that never replaces the transcript on screen.
				chat.IconAction{Label: "新建侧聊", Icon: lucide.MessageSquarePlus, TestID: "start-side-chat",
					OnPress: s.startSideChat},
				s.dictationAction(),
			}
		},
		Trailing: func(cx *core.Scope) core.Node {
			// Two controls, both always there: a narrow window that hid the
			// model switcher could not change models at all. The context
			// meter is read, not acted on, so it folds away first.
			if !wide(cx) {
				return core.Group{s.modelPickerChip(), s.thinkingPicker()}
			}
			return core.Group{s.contextMeter(), s.modelPickerChip(), s.thinkingPicker()}
		},
		Footer: func(cx *core.Scope) core.Node {
			if !wide(cx) || !s.composerFooterVisible() {
				return nil
			}
			return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				core.Show{When: func() bool { return s.banner.Get() != "" }, Then: func(*core.Scope) core.Node {
					return core.Text{ContentFunc: s.banner.Get, TestID: "banner", MaxLines: 1,
						Style: core.Style{FontSize: fontLabel, Color: s.colors.Warn()}}
				}},
				core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}},
				core.Show{When: s.running.Get, Then: func(*core.Scope) core.Node {
					return chat.Shimmer{Text: func() string { return "生成中" }, Size: 12.5,
						Color: s.colors.Muted, Highlight: s.colors.Text}
				}},
				core.Show{When: func() bool { return strings.TrimSpace(s.widgetText.Get()) != "" }, Then: func(*core.Scope) core.Node {
					// One truncated line says a widget is reporting; the card
					// carries the whole text, which a footer row cannot.
					return ui.HoverCard{
						OpenDelay: 250 * time.Millisecond,
						Placement: core.PlaceTop,
						Align:     core.AlignEnd,
						TestID:    "extension-widgets-hover",
						Style:     core.Style{Width: core.Pt(320), Gap: 5},
						Trigger: core.Text{ContentFunc: s.widgetText.Get, TestID: "extension-widgets", MaxLines: 1,
							Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
						Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node { return s.extensionWidgetCard() })},
					}
				}},
				core.Show{When: s.queuesActive, Then: func(*core.Scope) core.Node {
					return core.Group{
						ui.Button{Text: "查看队列", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "open-queue",
							OnPress: func() { s.queuePanel.Set(true) }},
						ui.Button{Text: "撤回队列", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "clear-queue",
							OnPress: func() { s.runCommand("clear-queue") }},
					}
				}},
			}}
		},
	}
}

// extensionWidgetCard is the widget text the footer has to clip:
// setWidget usually reports a status line or two, and a hover is the only
// place under the input where all of it fits.
func (s *appState) extensionWidgetCard() core.Node {
	if strings.TrimSpace(s.widgetText.Get()) == "" {
		return core.View{}
	}
	return core.View{Style: core.Style{Gap: 5}, Children: []core.Node{
		core.Text{Content: "扩展状态", TestID: "extension-widgets-title", MaxLines: 1,
			Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
		core.Text{ContentFunc: s.widgetText.Get, TestID: "extension-widgets-full", MaxLines: 6,
			Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
	}}
}

// queuePanelView lists the prompts pi is holding, so a queued message can
// be pulled back into the composer instead of only being discarded whole.
func (s *appState) queuePanelView(*core.Scope) core.Node {
	colors := ui.ColorsOf(s.cx)
	rows := []core.Node{core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
		core.Text{Content: "队列", MaxLines: 1, Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: colors.Muted()}},
		core.View{Style: core.Style{FlexGrow: 1}},
		ui.Button{Text: "撤回全部", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "queue-clear",
			OnPress: func() { s.runCommand("clear-queue"); s.queuePanel.Set(false) }},
		ui.IconButton{Icon: lucide.X, Label: "关闭队列", Size: 22, TestID: "queue-close",
			OnPress: func() { s.queuePanel.Set(false) }},
	}}}
	add := func(kind string, list []string) {
		for i, text := range list {
			item := text
			idx := i
			rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
				Padding: core.Symmetric(3, 4), CornerRadius: radiusInline}, Children: []core.Node{
				core.Text{Content: kind, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: colors.Faint()}},
				core.Text{Content: truncateRunes(item, 90), MaxLines: 1, Style: core.Style{FontSize: fontLabel, Color: colors.Text()}},
				core.View{Style: core.Style{FlexGrow: 1}},
				ui.Button{Text: "放入输入框", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: fmt.Sprintf("queue-to-draft-%d", idx),
					OnPress: func() { s.draft.Set(item); s.queuePanel.Set(false) }},
				ui.Button{Text: "撤回", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: fmt.Sprintf("queue-drop-%d", idx),
					OnPress: func() { s.recallQueueItem(kind == "引导", idx) }},
			}})
		}
	}
	add("引导", s.steeringItems.Get())
	add("追加", s.followUpItems.Get())
	return core.View{TestID: "queue-panel", Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100),
		MaxWidth: core.Pt(chat.ContentWidth), Gap: 2, Padding: core.All(8), CornerRadius: radiusCard, BorderWidth: 1,
		Background: colors.Surface()}, StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
		Children: rows}
}

// recallQueueItem removes one queued prompt the only way the RPC allows:
// clear the queue, then re-queue every other entry in its own lane. The
// order of each lane is kept; the two lanes are re-added in their original
// grouping. The entry is picked by index: two identical prompts in a queue
// are distinct items, and matching by text deleted the wrong one.
func (s *appState) recallQueueItem(steering bool, index int) {
	h := s.current()
	if h == nil || s.running.GetUntracked() == false {
		// The queue only exists mid-run; out of a run clear it locally.
		s.call(agent.Command{"type": agent.CmdClearQueue})
		return
	}
	steer, follow := append([]string(nil), s.steeringItems.GetUntracked()...), append([]string(nil), s.followUpItems.GetUntracked()...)
	if steering {
		if index >= 0 && index < len(steer) {
			steer = append(steer[:index], steer[index+1:]...)
		}
	} else {
		if index >= 0 && index < len(follow) {
			follow = append(follow[:index], follow[index+1:]...)
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdClearQueue}); err != nil {
			s.cx.Post(func() { s.notify(ui.ToastWarning, "撤回失败", err.Error()) })
			return
		}
		for _, text := range steer {
			_, _ = h.Client.Call(ctx, agent.Command{"type": agent.CmdSteer, "message": text})
		}
		for _, text := range follow {
			_, _ = h.Client.Call(ctx, agent.Command{"type": agent.CmdFollowUp, "message": text})
		}
		s.cx.Post(func() { s.notify(ui.ToastInfo, "已撤回该条", "") })
	}()
}

// markdownView renders the answer's Markdown with ui.TextView, which
// carries the design system's headings, lists, tables, code blocks and
// links; the app only tunes the reading rhythm here. A base textview with
// no styles was why answers read as one flat run of text.
func markdownView(md string, streaming bool) chat.View {
	return markdownWith(func() string { return md }, streaming, false)
}

// markdownViewAt is a final answer whose text is read again on every render:
// the last prose arrives after the view is mounted, and a captured string
// would freeze at whatever it was then.
func markdownViewAt(md func() string) chat.View {
	return markdownWith(md, false, false)
}

// markdownStep renders the commentary between tool calls: the same
// Markdown at a quieter size, so the final answer keeps the emphasis.
func markdownStep(md string, streaming bool) chat.View {
	return markdownWith(func() string { return md }, streaming, true)
}

// markdownStepFunc is a commentary step that streams: its text is read from
// the turn on every render, so a delta repaints the row instead of waiting
// for the session to be opened again.
func markdownStepFunc(md func() string, streaming bool) chat.View {
	return markdownWith(md, streaming, true)
}

func markdownWith(md func() string, streaming bool, secondary bool) chat.View {
	return func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		var caret core.Node
		if streaming {
			caret = core.View{Style: core.Style{Width: core.Pt(6), Height: core.Pt(14), Background: colors.Accent()}}
		}
		body := core.Style{FontSize: fontLabel, LineHeight: 22}
		if secondary {
			body = core.Style{FontSize: fontLabel, LineHeight: 21, Color: colors.Muted()}
		}
		return ui.TextView{
			MarkdownFunc:     md,
			Options:          textview.Options{Streaming: streaming},
			StreamingCaret:   caret,
			Selectable:       true,
			OpenLinks:        true,
			Highlighter:      highlight.New(cx),
			CodeBlockActions: []textview.CodeAction{{ID: textview.CopyAction}},
			// Only the reading text changes; headings, markers and
			// surfaces keep ui's defaults.
			Styles: textview.Styles{
				textview.PartRoot:            body.Merge(core.Style{Gap: 14}),
				textview.PartParagraph:       {},
				textview.PartHeading:         core.Style{Margin: core.Edges{Top: 10}},
				textview.PartList:            core.Style{Gap: 5},
				textview.PartListItem:        core.Style{Direction: core.Row, AlignItems: core.AlignStart, Gap: 8},
				textview.PartCodeBlockScroll: core.Style{Padding: core.Symmetric(10, 12)},
				textview.PartCodeBlockCode: core.Style{FontFamily: core.FontMonospace, FontSize: 12,
					LineHeight: 18, Color: colors.Text()},
				textview.PartTableCell: core.Style{Padding: core.Symmetric(7, 10)},
			},
		}
	}
}

func toolStatus(t toolRow) chat.Status {
	switch t.Status {
	case "running", "pending":
		return chat.Running
	case "failed":
		return chat.Failed
	case "cancelled":
		return chat.Cancelled
	}
	return chat.Completed
}

// toolIcon maps a tool name onto the icon its family reads as.
func toolIcon(t toolRow) func() *core.SVG {
	switch {
	case strings.HasPrefix(t.Action, "Read"), strings.HasPrefix(t.Action, "读"):
		return lucide.FileText
	case strings.HasPrefix(t.Action, "Write"), strings.HasPrefix(t.Action, "Edit"):
		return lucide.Pencil
	case strings.HasPrefix(t.Action, "Bash"), strings.Contains(strings.ToLower(t.Action), "shell"):
		return lucide.Terminal
	case strings.HasPrefix(t.Action, "Grep"), strings.HasPrefix(t.Action, "Glob"):
		return lucide.Search
	case strings.HasPrefix(t.Action, "Web"):
		return lucide.Globe
	case strings.Contains(strings.ToLower(t.Name), "task"), strings.Contains(strings.ToLower(t.Name), "agent"),
		strings.Contains(t.Action, "子代理"):
		return lucide.Bot
	}
	return lucide.Wrench
}

func (s *appState) copyTurn(t turnRow) {
	s.cx.SetClipboard(t.Final)
	s.copied.Set(t.ID)
	s.cx.AfterFunc(1600*time.Millisecond, func() {
		if s.copied.GetUntracked() == t.ID {
			s.copied.Set("")
		}
	})
}

// pickAttachments opens the system's file picker for pictures. Picking was
// otherwise possible only by dropping or pasting; a button is how the
// composer is expected to offer it.
func (s *appState) pickAttachments() {
	s.cx.PickFiles(core.PickFilesOptions{Multiple: true, Limit: 8, Types: []string{"image/*"}}, func(files []core.File, err error) {
		if err != nil {
			s.notify(ui.ToastWarning, "选择文件失败", err.Error())
			return
		}
		for _, f := range files {
			reader, err := f.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(reader, 12<<20))
			_ = reader.Close()
			if err != nil {
				continue
			}
			mime := f.MIME
			if mime == "" || mime == "application/octet-stream" {
				mime = mimeOf(f.Name)
			}
			s.addAttachment(f.Name, mime, data)
		}
	})
}

// attachFiles takes dropped files: pictures become attachments, everything
// else an @-mention in the draft.
func (s *appState) attachFiles(uris []string) {
	if len(uris) == 0 {
		return
	}
	draft := s.draft.GetUntracked()
	for _, u := range uris {
		path := strings.TrimPrefix(u, "file://")
		path = strings.ReplaceAll(path, "%20", " ")
		if imageExts[strings.ToLower(filepath.Ext(path))] {
			if data, err := os.ReadFile(path); err == nil {
				s.addAttachment(baseName(path), mimeOf(path), data)
				continue
			}
		}
		draft += " @" + s.mentionPath(u)
	}
	s.draft.Set(draft)
}

// pasteImages keeps pictures pasted from another app and turns pasted files
// into the same mentions a drop makes.
func (s *appState) pasteImages(e core.PasteEvent) {
	for _, f := range e.Files {
		if !strings.HasPrefix(f.MIME, "image/") {
			continue
		}
		name := f.Name
		if name == "" {
			name = "pasted-image"
		}
		s.addAttachment(name, f.MIME, f.Data)
	}
	if len(e.URIs) > 0 {
		s.attachFiles(e.URIs)
	}
}

// addAttachment keeps at most eight pictures, as pi's own composer does.
func (s *appState) addAttachment(name, mime string, data []byte) {
	if len(data) == 0 {
		return
	}
	current := s.attachments.GetUntracked()
	if len(current) >= 8 {
		s.status.Set("最多附加 8 张图片")
		return
	}
	s.attachments.Set(append(append([]imageAttachment{}, current...), imageAttachment{Name: name, MIME: mime, Data: data}))
}

func (s *appState) removeAttachment(i int) {
	current := s.attachments.GetUntracked()
	if i < 0 || i >= len(current) {
		return
	}
	out := append([]imageAttachment{}, current[:i]...)
	out = append(out, current[i+1:]...)
	s.attachments.Set(out)
}

// attachmentsView shows the pasted pictures above the editor with
// ui/chat's own attachment chips.
func (s *appState) attachmentsView(cx *core.Scope) core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		items := s.attachments.Get()
		if len(items) == 0 {
			return nil
		}
		nodes := make([]core.Node, 0, len(items))
		for i, a := range items {
			idx, item := i, a
			nodes = append(nodes, chat.Attachment{Label: item.Name, Detail: shortBytes(item.Data), RemoveLabel: "移除 " + item.Name,
				Image: core.ImageSource{Data: item.Data}, OnRemove: func() { s.removeAttachment(idx) }})
		}
		return core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 6, Padding: core.Symmetric(4, 8)}, Children: nodes}
	})
}

// shortBytes names an attachment's size.
func shortBytes(data []byte) string {
	n := len(data)
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// mimeOf names a picture's type from its extension.
func mimeOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic":
		return "image/heic"
	case ".bmp":
		return "image/bmp"
	}
	return "image/jpeg"
}

// mentionPath turns a dropped file URI into the @-mention path pi resolves:
// relative to the project when the file lives inside it, its base name
// otherwise.
func (s *appState) mentionPath(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	path = strings.ReplaceAll(path, "%20", " ")
	if root := s.projectRoot(); root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return baseName(path)
}

// bashCard shows the output of the composer's `!` shell mode.
func (s *appState) bashCard() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		output := s.bashOutput.Get()
		command := s.bashCommand.Get()
		if output == "" && command == "" {
			return nil
		}
		colors := ui.ColorsOf(cx)
		header := "Shell"
		if command != "" {
			header = "$ " + command
		}
		return core.View{Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth),
			CornerRadius: radiusCard, BorderWidth: 1, Background: colors.Surface(), Padding: core.All(8), Gap: 6},
			StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
					ui.Icon(lucide.Terminal, 13, colors.Muted),
					core.Text{Content: header, MaxLines: 1, Style: core.Style{FontSize: 11.5, FontFamily: core.FontMonospace, Color: colors.Text()}},
					core.View{Style: core.Style{FlexGrow: 1}},
					ui.Button{Text: "停止", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "bash-stop",
						OnPress: func() { s.call(agentCommand("abort_bash")) }},
					ui.Button{Text: "清空", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "bash-clear",
						OnPress: func() { s.bashOutput.Set(""); s.bashCommand.Set("") }},
				}},
				core.Scroll{Style: core.Style{MaxHeight: core.Pt(200), FlexShrink: 1}, Children: []core.Node{
					core.Text{Content: output, Selectable: core.SelectableOn,
						Style: core.Style{FontFamily: core.FontMonospace, FontSize: 11.5, Color: colors.Muted()}},
				}},
			}}
	})
}

// agentCommand is a command without arguments.
func agentCommand(typ string) agent.Command { return agent.Command{"type": typ} }

// paletteOption is one row of the composer's inline list.
type paletteOption struct {
	icon          func() *core.SVG
	label, detail string
	testID        string
	choose        func()
}

// paletteOptions returns the header and the rows the draft asks for: the
// files an @-fragment matches, or pi's commands under "/".
func (s *appState) paletteOptions(draft string) (string, []paletteOption) {
	if frag, ok := mentionFragment(draft); ok {
		needle := strings.ToLower(frag)
		var out []paletteOption
		for i, rel := range s.mentions.Get() {
			if needle != "" && !strings.Contains(strings.ToLower(rel), needle) {
				continue
			}
			path := rel
			out = append(out, paletteOption{
				icon: lucide.FileText, label: path, detail: "提及", testID: "mention-option-" + fmt.Sprint(i),
				choose: func() { s.chooseMention(frag, path) },
			})
			if len(out) >= 20 {
				break
			}
		}
		return "提及文件", out
	}
	trimmed := strings.TrimSpace(draft)
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \n") {
		return "", nil
	}
	prefix := strings.ToLower(strings.TrimPrefix(trimmed, "/"))
	var out []paletteOption
	for i, opt := range s.slashOptions(prefix) {
		option := opt
		out = append(out, paletteOption{
			icon: lucide.Command, label: option.Label, detail: option.Description,
			testID: "slash-option-" + fmt.Sprint(i),
			choose: func() { s.chooseSlash(option.ID) },
		})
	}
	return "命令", out
}

// paletteSelection clamps the keyboard index to the rows shown.
func (s *appState) paletteSelection(count int) int {
	if count <= 0 {
		return 0
	}
	index := s.paletteIndex.Get()
	if index < 0 || index >= count {
		index = 0
	}
	return index
}

// inlinePalette lists slash commands or file mentions while the draft asks
// for one, above the composer. ↑↓ / Tab / Enter choose a row and Esc hides
// the list until the draft changes.
func (s *appState) inlinePalette() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		draft := s.draft.Get()
		if s.paletteDismissed.Get() {
			return nil
		}
		label, options := s.paletteOptions(draft)
		if len(options) == 0 {
			return nil
		}
		colors := ui.ColorsOf(cx)
		body := core.View{Style: core.Style{AlignSelf: core.AlignCenter, Width: core.Pct(100), MaxWidth: core.Pt(chat.ContentWidth),
			CornerRadius: radiusCard, BorderWidth: 1, Background: colors.Surface(), Padding: core.All(4), Gap: 2},
			StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
			Children: []core.Node{
				core.Text{Content: label, Style: core.Style{FontSize: fontMeta, Color: colors.Faint(), Padding: core.Symmetric(2, 6)}},
				// Only the rows follow the highlighted index, so moving it
				// does not re-register the list's key bindings.
				core.Dynamic(func(cx *core.Scope) core.Node {
					index := s.paletteSelection(len(options))
					rows := make([]core.Node, 0, len(options))
					lastDir := ""
					for i, option := range options {
						opt := option
						if label == "提及文件" {
							dir := filepath.Dir(opt.label)
							if dir == "." {
								dir = ""
							} else {
								dir += "/"
							}
							if dir != lastDir {
								rows = append(rows, core.Text{Content: dir, MaxLines: 1,
									Style: core.Style{FontSize: fontMeta, Color: colors.Faint(), Padding: core.Symmetric(2, 6)}})
								lastDir = dir
							}
						}
						rows = append(rows, paletteRow(cx, opt.icon, opt.label, opt.detail, opt.testID, i == index, func() {
							opt.choose()
							s.paletteIndex.Set(0)
						}))
					}
					return core.Scroll{Style: core.Style{MaxHeight: core.Pt(240), FlexShrink: 1}, Children: []core.Node{
						core.View{Style: core.Style{Gap: 1}, Children: rows},
					}}
				}),
			}}
		// The bindings exist only while the list shows, so ↑↓/Esc belong to
		// the caret whenever it is closed. The composer keeps the focus,
		// which is why they must bind with KeyAlways.
		return core.Show{When: func() bool { return true }, Then: func(cx *core.Scope) core.Node {
			cx.OnKeyWith("up", core.KeyOptions{When: core.KeyAlways, Repeat: true}, func() {
				s.paletteIndex.Set((s.paletteSelection(len(options)) - 1 + len(options)) % len(options))
			})
			cx.OnKeyWith("down", core.KeyOptions{When: core.KeyAlways, Repeat: true}, func() {
				s.paletteIndex.Set((s.paletteSelection(len(options)) + 1) % len(options))
			})
			cx.OnKeyWith("tab", core.KeyOptions{When: core.KeyAlways}, func() {
				options[s.paletteSelection(len(options))].choose()
				s.paletteIndex.Set(0)
			})
			cx.OnKeyWith("enter", core.KeyOptions{When: core.KeyAlways}, func() {
				options[s.paletteSelection(len(options))].choose()
				s.paletteIndex.Set(0)
				s.paletteHandled = true
			})
			cx.OnKeyWith("escape", core.KeyOptions{When: core.KeyAlways}, func() {
				s.paletteDismissed.Set(true)
				s.paletteIndex.Set(0)
			})
			return body
		}}
	})
}

// paletteRow is one inline option.
func paletteRow(cx *core.Scope, icon func() *core.SVG, label, detail, testID string, selected bool, press func()) core.Node {
	colors := ui.ColorsOf(cx)
	rowStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(4, 6), CornerRadius: radiusControl}
	if selected {
		rowStyle.Background = ui.Alpha(colors.Accent(), 0.14)
	}
	return core.View{Role: core.RoleButton, Label: label, TestID: testID, OnPress: func(core.PressEvent) { press() },
		Style: rowStyle,
		StyleFunc: func() core.Style {
			if selected {
				return core.Style{}
			}
			return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.06)}, Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.1)}}
		},
		Children: []core.Node{
			ui.Icon(icon, 13, colors.Muted),
			core.Text{Content: label, MaxLines: 1, Style: core.Style{FontSize: fontLabel, Color: colors.Text()}},
			core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}},
			core.Text{Content: truncateRunes(detail, 70), MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: colors.Faint()}},
		}}
}

// mentionFragment returns the text after an @ being typed at the draft's end.
func mentionFragment(draft string) (string, bool) {
	at := strings.LastIndex(draft, "@")
	if at < 0 || at > 0 && !strings.ContainsAny(draft[at-1:at], " \n") {
		return "", false
	}
	frag := draft[at+1:]
	if strings.ContainsAny(frag, " \n") {
		return "", false
	}
	return frag, true
}

// chooseMention replaces the @fragment being typed with the chosen path.
func (s *appState) chooseMention(frag, rel string) {
	draft := s.draft.GetUntracked()
	token := "@" + frag
	if idx := strings.LastIndex(draft, token); idx >= 0 {
		s.draft.Set(draft[:idx] + "@" + rel + " ")
		return
	}
	s.insertMention(rel)
}

// autoLoadEarlier widens the window when the transcript is scrolled to its
// top, the way a long history should page itself instead of ending at the
// 20-turn window.
func (s *appState) autoLoadEarlier() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		total, shown := s.turnTotal.Get(), len(s.turns.Get())
		if total <= shown || s.autoLoading {
			return nil
		}
		if s.conversation.OffsetNow().Y > 24 {
			return nil
		}
		s.autoLoading = true
		s.cx.Post(func() {
			s.turnLimit.Set(s.turnLimit.GetUntracked() + 20)
			s.refresh()
			s.cx.AfterFunc(400*time.Millisecond, func() { s.autoLoading = false })
		})
		return nil
	})
}

// loadEarlier restores another page of the transcript.
func (s *appState) loadEarlier() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		total, shown := s.turnTotal.Get(), len(s.turns.Get())
		if total <= shown {
			return nil
		}
		return core.View{Style: core.Style{AlignItems: core.AlignCenter, Padding: core.All(8)}, Children: []core.Node{
			ui.Button{Text: fmt.Sprintf("加载更早的 %d 轮", total-shown), Variant: ui.ButtonGhost, Size: ui.SizeSmall,
				TestID: "load-earlier", OnPress: func() { s.turnLimit.Set(s.turnLimit.GetUntracked() + 40); s.refresh() }},
		}}
	})
}

func firstLine(text string, n int) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return truncateRunes(text, n)
}

func truncateRunes(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n]) + "…"
}

func baseName(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
