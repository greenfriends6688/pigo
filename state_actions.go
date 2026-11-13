package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/commitgraph"
	"github.com/oneo-me/nsgo/ui/filepicker"

	"piwebnative/internal/agent"
	"piwebnative/internal/files"
	"piwebnative/internal/sessions"
	"piwebnative/internal/worktree"
)

// notify shows a toast. It runs on the graph goroutine, as every caller posts
// before calling it.
func (s *appState) notify(kind ui.ToastType, title, description string) {
	s.toaster.Show(ui.ToastContent{Type: kind, Title: title, Description: description})
}

// responseError turns a failed Call into the message shown to the user.
func responseError(err error, resp *agent.Frame) string {
	if err != nil {
		return err.Error()
	}
	if resp == nil {
		return "没有响应"
	}
	if resp.Error != "" {
		return resp.Error
	}
	return "命令失败"
}

// newSession starts a fresh conversation. With a live process the RPC's
// new_session creates and switches to a new session file in the same
// process; without one a new process is acquired for the current project.
func (s *appState) newSession() {
	s.readOnly.Set(false)
	if s.projectRoot() == "" {
		s.openProjectPicker()
		return
	}
	if !s.ensureTrust() {
		return
	}
	h := s.current()
	s.selected.Set("")
	s.redux.Reset()
	s.refresh()
	if h == nil || !s.connected.GetUntracked() {
		s.connect("")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdNewSession})
		if err != nil || resp == nil || !resp.Success {
			msg := responseError(err, resp)
			s.cx.Post(func() {
				s.status.Set("新建会话失败：" + msg)
				s.notify(ui.ToastError, "新建会话失败", msg)
			})
			return
		}
		s.adoptSession(h, "新会话")
	}()
}

// adoptSession follows the process to the session file it just switched to
// (new_session, fork, clone), rekeys the pool so the same process is found
// under the real path, and rebuilds the transcript from get_messages.
func (s *appState) adoptSession(h *agent.SessionHandle, label string) {
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()

		path := ""
		if resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetState}); err == nil {
			path = sessionFileFromState(resp.Data)
		}
		if path != "" {
			s.mu.Lock()
			oldKey := s.connKey
			s.mu.Unlock()
			if oldKey != "" && oldKey != path {
				s.pool.Rekey(oldKey, path)
			}
			s.mu.Lock()
			if s.handle == h {
				s.connKey = path
			}
			s.mu.Unlock()
		}

		if resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetMessages}); err == nil &&
			resp != nil && len(resp.Data) > 0 {
			var payload struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if json.Unmarshal(resp.Data, &payload) == nil {
				msgs := messagesFromRPC(payload.Messages)
				s.cx.Post(func() {
					s.redux.SetState(msgs)
					s.refresh()
				})
			}
		}

		go func() {
			_ = s.store.Refresh()
			s.cx.Post(s.publishSessions)
		}()
		s.cx.Post(func() {
			if path != "" {
				s.selected.Set(path)
			}
			s.publishSessions()
			s.status.Set("已切换到" + label)
			s.notify(ui.ToastSuccess, label, "对话已切换到新的会话文件")
		})
	}()
}

// forkSession forks at the newest user message. pi's fork takes a user
// entry id and defaults to forking before it, which is what "Fork 到最后一条
// 消息" means; get_fork_messages is the supported way to find that id.
func (s *appState) forkSession() {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先打开一个会话")
		s.notify(ui.ToastWarning, "没有可 Fork 的会话", "先打开一个已连接的会话")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetForkMessages})
		if err != nil || resp == nil || !resp.Success {
			msg := responseError(err, resp)
			s.cx.Post(func() { s.notify(ui.ToastError, "Fork 失败", msg) })
			return
		}
		var payload struct {
			Messages []struct {
				EntryID string `json:"entryId"`
				Text    string `json:"text"`
			} `json:"messages"`
		}
		if json.Unmarshal(resp.Data, &payload) != nil || len(payload.Messages) == 0 {
			s.cx.Post(func() { s.notify(ui.ToastWarning, "Fork 不可用", "当前会话还没有用户消息") })
			return
		}
		last := payload.Messages[len(payload.Messages)-1]
		fresp, ferr := h.Client.Call(ctx, agent.Command{"type": agent.CmdFork, "entryId": last.EntryID})
		if ferr != nil || fresp == nil || !fresp.Success {
			msg := responseError(ferr, fresp)
			s.cx.Post(func() { s.notify(ui.ToastError, "Fork 失败", msg) })
			return
		}
		var out struct {
			Cancelled bool `json:"cancelled"`
		}
		_ = json.Unmarshal(fresp.Data, &out)
		if out.Cancelled {
			s.cx.Post(func() { s.status.Set("Fork 已取消") })
			return
		}
		s.adoptSession(h, "Fork")
	}()
}

// forkFromPrompt branches a new session at one user message, keeping that
// prompt and its earlier turns.
func (s *appState) forkFromPrompt(entryID string) { s.forkAt(entryID) }

// forkAt continues the conversation from one entry as a new branch.
func (s *appState) forkAt(entryID string) {
	h := s.current()
	if h == nil || entryID == "" {
		s.status.Set("未连接：先打开一个会话")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdFork, "entryId": entryID, "position": "at"})
		if err != nil || resp == nil || !resp.Success {
			msg := responseError(err, resp)
			s.cx.Post(func() { s.notify(ui.ToastError, "分支失败", msg) })
			return
		}
		var out struct {
			Cancelled bool `json:"cancelled"`
		}
		_ = json.Unmarshal(resp.Data, &out)
		if out.Cancelled {
			s.cx.Post(func() { s.status.Set("分支已取消") })
			return
		}
		s.adoptSession(h, "分支")
	}()
}

// cloneSession forks at the current leaf, keeping the whole conversation.
func (s *appState) cloneSession() {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先打开一个会话")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdClone})
		if err != nil || resp == nil || !resp.Success {
			msg := responseError(err, resp)
			s.cx.Post(func() { s.notify(ui.ToastError, "克隆失败", msg) })
			return
		}
		var out struct {
			Cancelled bool `json:"cancelled"`
		}
		_ = json.Unmarshal(resp.Data, &out)
		if out.Cancelled {
			s.cx.Post(func() { s.status.Set("克隆已取消") })
			return
		}
		s.adoptSession(h, "克隆")
	}()
}

// beginRename opens the rename dialog prefilled with the row's title.
func (s *appState) beginRename(path, title string) {
	if path == "" {
		path = s.selected.GetUntracked()
	}
	if path == "" {
		s.notify(ui.ToastWarning, "没有选中的会话", "先打开一个会话再重命名")
		return
	}
	s.renameTarget.Set(path)
	s.renameName.Set(title)
	s.renameOpen.Set(true)
}

// commitRename writes the name through the RPC for the live session (so pi's
// own state sees it) and through the store for any other row.
func (s *appState) commitRename() {
	if group := s.renameGroup.GetUntracked(); group != "" {
		s.renameOpen.Set(false)
		s.commitProjectRename(group)
		return
	}
	name := strings.TrimSpace(s.renameName.GetUntracked())
	path := s.renameTarget.GetUntracked()
	if name == "" {
		s.status.Set("会话名称不能为空")
		return
	}
	if path == "" {
		path = s.selected.GetUntracked()
	}
	s.renameOpen.Set(false)
	if path == "" {
		return
	}
	h := s.current()
	viaRPC := h != nil && s.connected.GetUntracked() && path == s.selected.GetUntracked()
	go func() {
		var err error
		if viaRPC {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			resp, cerr := h.Client.Call(ctx, agent.Command{"type": agent.CmdSetSessionName, "name": name})
			if cerr != nil {
				err = cerr
			} else if resp == nil || !resp.Success {
				err = fmt.Errorf("%s", responseError(nil, resp))
			}
		} else {
			err = s.store.SetName(path, name)
		}
		if err == nil {
			_ = s.store.Refresh()
		}
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("重命名失败：" + err.Error())
				s.notify(ui.ToastError, "重命名失败", err.Error())
				return
			}
			s.publishSessions()
			s.status.Set("已重命名：" + name)
			s.notify(ui.ToastSuccess, "已重命名", name)
		})
	}()
}

func (s *appState) togglePin(row sessionRow) {
	go func() {
		err := s.store.SetPinned(row.ID, !row.Pinned)
		if err == nil {
			_ = s.store.Refresh()
		}
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("更新固定状态失败：" + err.Error())
				return
			}
			s.publishSessions()
			if row.Pinned {
				s.notify(ui.ToastInfo, "已取消固定", row.Title)
			} else {
				s.notify(ui.ToastInfo, "已固定", row.Title)
			}
		})
	}()
}

func (s *appState) archiveSession(id string) {
	if id == "" {
		return
	}
	wasSelected := s.selected.GetUntracked() == id
	go func() {
		err := s.store.SetArchived(id, true)
		if err == nil {
			_ = s.store.Refresh()
		}
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("归档失败：" + err.Error())
				return
			}
			s.publishSessions()
			s.notify(ui.ToastInfo, "已归档", baseName(id))
			if wasSelected {
				s.newSession()
			}
		})
	}()
}

// openBranches reads the conversation tree and opens the branch picker.
func (s *appState) openBranches() {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先打开一个会话")
		return
	}
	s.branchOpen.Set(true)
	s.branchItems.Set(nil)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetTree})
		if err != nil || resp == nil || !resp.Success {
			msg := responseError(err, resp)
			s.cx.Post(func() { s.notify(ui.ToastError, "读取对话树失败", msg) })
			return
		}
		items := flattenTree(resp.Data)
		s.cx.Post(func() { s.branchItems.Set(items) })
	}()
}

// flattenTree turns pi's nested tree into rows the dialog lists.
func flattenTree(data json.RawMessage) []branchItem {
	var payload struct {
		Tree []treeNode `json:"tree"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil
	}
	var out []branchItem
	var walk func(nodes []treeNode, depth int)
	walk = func(nodes []treeNode, depth int) {
		for _, node := range nodes {
			if item, ok := treeItemOf(node, depth); ok {
				out = append(out, item)
			}
			walk(node.Children, depth+1)
		}
	}
	walk(payload.Tree, 0)
	return out
}

type treeNode struct {
	Entry struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
	} `json:"entry"`
	Children []treeNode `json:"children"`
}

// treeItemOf names a user or assistant entry; other plumbing stays out of
// the picker.
func treeItemOf(node treeNode, depth int) (branchItem, bool) {
	if node.Entry.Type != "message" || len(node.Entry.Message) == 0 {
		return branchItem{}, false
	}
	var msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(node.Entry.Message, &msg) != nil {
		return branchItem{}, false
	}
	text := blocksText(msg.Content)
	switch msg.Role {
	case "user":
		return branchItem{ID: node.Entry.ID, Label: "你：" + truncateRunes(text, 70), Depth: depth, Kind: "user"}, true
	case "assistant":
		if text == "" {
			return branchItem{}, false
		}
		return branchItem{ID: node.Entry.ID, Label: "回答：" + truncateRunes(text, 70), Depth: depth, Kind: "assistant"}, true
	}
	return branchItem{}, false
}

// blocksText joins the text blocks of a message's content.
func blocksText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// recallQueue clears the queue and puts its prompts back in the draft.
func (s *appState) recallQueue() {
	h := s.current()
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdClearQueue})
		if err != nil || resp == nil || !resp.Success {
			return
		}
		text := recalledQueueText(resp.Data)
		s.cx.Post(func() {
			if text == "" {
				return
			}
			draft := strings.TrimSpace(s.draft.GetUntracked())
			if draft != "" {
				draft += "\n"
			}
			s.draft.Set(draft + text)
			s.notify(ui.ToastInfo, "已撤回队列", "内容回到输入框")
		})
	}()
}

// recalledQueueText joins the steering and follow-up prompts clear_queue
// hands back.
func recalledQueueText(data json.RawMessage) string {
	var payload struct {
		Steering []string `json:"steering"`
		FollowUp []string `json:"followUp"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	return strings.Join(append(payload.Steering, payload.FollowUp...), "\n")
}

// openSubagents shows the subagent list as a dialog and loads it.
func (s *appState) openSubagents() {
	s.subOpen.Set(true)
	s.loadSubagents()
}

// loadSubagents reads the sessions subagents spawned, for the dock's
// subagents tab and the dialog alike.
func (s *appState) loadSubagents() {
	parent := s.currentSessionPath()
	go func() {
		children := s.store.ChildrenOf(parent)
		rows := make([]sessionRow, 0, len(children))
		for _, info := range children {
			title := info.Name
			if title == "" {
				title = info.FirstMessage
			}
			if title == "" {
				title = info.ID
			}
			rows = append(rows, sessionRow{ID: info.Path, Title: title, Subtitle: info.Cwd,
				Project: projectTitleOf(info), Age: relativeAge(info.Modified)})
		}
		s.cx.Post(func() { s.subItems.Set(rows) })
	}()
}

// toggleArchiveView switches the sidebar between live and archived sessions.
func (s *appState) toggleArchiveView() {
	on := !s.archiveView.GetUntracked()
	s.archiveView.Set(on)
	s.publishSessions()
}

// unarchiveSession puts a session back in the main list.
func (s *appState) unarchiveSession(id string) {
	if id == "" {
		return
	}
	go func() {
		err := s.store.SetArchived(id, false)
		if err == nil {
			_ = s.store.Refresh()
		}
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("取消归档失败：" + err.Error())
				return
			}
			s.publishSessions()
			s.notify(ui.ToastInfo, "已取消归档", baseName(id))
		})
	}()
}

// beginDelete asks before removing a session file: the transcript cannot be
// recovered through the app.
func (s *appState) beginDelete(row sessionRow) {
	s.deleteTarget.Set(row.ID)
	s.deleteOpen.Set(true)
}

func (s *appState) commitDelete() {
	id := s.deleteTarget.GetUntracked()
	s.deleteOpen.Set(false)
	s.deleteTarget.Set("")
	if id == "" {
		return
	}
	wasSelected := s.selected.GetUntracked() == id
	go func() {
		err := s.store.Delete(id)
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("删除失败：" + err.Error())
				s.notify(ui.ToastError, "删除失败", err.Error())
				return
			}
			if wasSelected {
				s.detachSession()
				s.selected.Set("")
				s.redux.Reset()
				s.refresh()
			}
			s.publishSessions()
			s.notify(ui.ToastSuccess, "已删除会话", baseName(id))
		})
	}()
}

// deleteName is the file name shown in the delete confirmation.
func (s *appState) deleteName() string {
	id := s.deleteTarget.Get()
	if id == "" {
		return ""
	}
	return baseName(id)
}

// applyStateInfo keeps the composer's model and thinking chips in step with
// the session get_state reports.
func (s *appState) applyStateInfo(data json.RawMessage) {
	if len(data) == 0 {
		return
	}
	var state struct {
		Model struct {
			ID, Name, Provider string
			ContextWindow      int `json:"contextWindow"`
		} `json:"model"`
		ThinkingLevel string `json:"thinkingLevel"`
	}
	if json.Unmarshal(data, &state) != nil {
		return
	}
	name := state.Model.Name
	if name == "" {
		name = state.Model.ID
	}
	s.cx.Post(func() {
		if name != "" && name != "unknown" {
			s.modelName.Set(name)
		}
		if state.ThinkingLevel != "" {
			s.thinking.Set(state.ThinkingLevel)
		}
		if state.Model.ContextWindow > 0 {
			s.ctxWindow.Set(state.Model.ContextWindow)
		}
	})
}

// fetchCatalog asks pi for the models and slash commands once per connection.
func (s *appState) fetchCatalog(h *agent.SessionHandle) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetAvailableModels}); err == nil && resp != nil && resp.Success {
		var data struct {
			Models []struct {
				ID, Name, Provider string
				Reasoning          bool
			} `json:"models"`
		}
		if json.Unmarshal(resp.Data, &data) == nil {
			models := make([]modelItem, 0, len(data.Models))
			for _, m := range data.Models {
				name := m.Name
				if name == "" {
					name = m.ID
				}
				models = append(models, modelItem{ID: m.ID, Name: name, Provider: m.Provider, Reasoning: m.Reasoning})
			}
			s.cx.Post(func() { s.models.Set(models) })
		}
	}
	if resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetCommands}); err == nil && resp != nil && resp.Success {
		var data struct {
			Commands []struct {
				Name, Description, Source string
			} `json:"commands"`
		}
		if json.Unmarshal(resp.Data, &data) == nil {
			commands := make([]commandItem, 0, len(data.Commands))
			for _, c := range data.Commands {
				commands = append(commands, commandItem{Name: c.Name, Description: c.Description, Source: c.Source})
			}
			s.cx.Post(func() { s.slashCommands.Set(commands) })
		}
	}
}

// insertSlash replaces the draft with the chosen slash command.
func (s *appState) insertSlash(name string) {
	if name == "" {
		return
	}
	s.draft.Set("/" + name + " ")
}

// setModel switches the live session's model.
func (s *appState) setModel(m modelItem) {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先打开一个会话")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdSetModel, "provider": m.Provider, "modelId": m.ID})
		s.cx.Post(func() {
			if err != nil || resp == nil || !resp.Success {
				msg := responseError(err, resp)
				s.status.Set("切换模型失败：" + msg)
				return
			}
			s.modelName.Set(m.Name)
			s.status.Set("模型：" + m.Name)
		})
	}()
}

// setThinkingLevel changes how much reasoning the model may do. The level
// shows at once and the call follows: a slider whose thumb waited for the
// round trip felt stuck on the old level and could not be dragged past it.
func (s *appState) setThinkingLevel(level string) {
	h := s.current()
	if h == nil || level == "" {
		return
	}
	prev := s.thinking.GetUntracked()
	if prev == level {
		return
	}
	s.thinking.Set(level)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdSetThinkingLevel, "level": level})
		s.cx.Post(func() {
			if err != nil || resp == nil || !resp.Success {
				s.thinking.Set(prev) // the model kept the old level
				s.notify(ui.ToastWarning, "设置思考级别失败", responseError(err, resp))
				return
			}
			s.status.Set("思考级别：" + thinkingLabel(level))
		})
	}()
}

// thinkingLabel names pi's thinking levels in Chinese.
func thinkingLabel(level string) string {
	switch level {
	case "off":
		return "关闭"
	case "minimal":
		return "极低"
	case "low":
		return "低"
	case "medium":
		return "中"
	case "high":
		return "高"
	case "xhigh":
		return "极高"
	case "max":
		return "最大"
	}
	return level
}

// fetchStats refreshes the session's token and cost totals, shown beside the
// composer.
func (s *appState) fetchStats(h *agent.SessionHandle) {
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetSessionStats})
		if err != nil || resp == nil || !resp.Success {
			return
		}
		var data struct {
			Tokens struct {
				Input, Output, CacheRead, CacheWrite, Total int64
			} `json:"tokens"`
			Cost float64 `json:"cost"`
		}
		if json.Unmarshal(resp.Data, &data) != nil {
			return
		}
		line := fmt.Sprintf("↑ %s · ↓ %s", shortCount(data.Tokens.Input), shortCount(data.Tokens.Output))
		if data.Cost > 0 {
			line += fmt.Sprintf(" · $%.2f", data.Cost)
		}
		s.cx.Post(func() {
			s.statsLine.Set(line)
			s.statsIn.Set(float64(data.Tokens.Input))
			s.statsOut.Set(float64(data.Tokens.Output))
			s.statsCache.Set(float64(data.Tokens.CacheRead))
		})
	}()
}

// shortCount formats a token count as "12.3k" or "1.2M".
func shortCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprint(n)
}

// loadGit reads the history of the current project for the Git tab.
func (s *appState) loadGit() {
	dir := s.currentCwd()
	s.gitLoading.Set(true)
	go func() {
		commits, head, err := files.GitLog(dir, 150)
		out := make([]commitgraph.Commit, 0, len(commits))
		for _, c := range commits {
			out = append(out, commitgraph.Commit{
				Hash: c.Hash, Short: c.Short, Parents: c.Parents,
				Author: c.Author, Subject: c.Subject, Date: time.Unix(c.Unix, 0),
				Head: head != "" && strings.HasPrefix(c.Hash, head),
			})
		}
		s.cx.Post(func() {
			s.gitLoading.Set(false)
			if err != nil {
				s.gitHead.Set("")
				s.gitCommits.Set(nil)
				s.status.Set("Git 历史不可用：" + err.Error())
				return
			}
			s.gitHead.Set(head)
			s.gitCommits.Set(out)
		})
	}()
}

func (s *appState) copyCommit(c commitgraph.Commit) {
	if c.Hash == "" {
		return
	}
	s.cx.SetClipboard(c.Hash)
	short := c.Short
	if short == "" {
		short = c.Hash[:min(7, len(c.Hash))]
	}
	s.notify(ui.ToastInfo, "已复制提交 "+short, c.Subject)
}

// loadMentions indexes the current project for the @ picker. It is bounded
// and skips build directories, so a huge repository cannot stall the UI.
func (s *appState) loadMentions() {
	cwd := s.currentCwd()
	go func() {
		index, err := files.MentionIndex(cwd, 3000)
		if err != nil {
			return
		}
		s.cx.Post(func() {
			if s.currentCwd() != cwd {
				return
			}
			s.mentions.Set(index)
		})
	}()
}

func (s *appState) insertMention(rel string) {
	if rel == "" {
		return
	}
	draft := strings.TrimRight(s.draft.GetUntracked(), " ")
	if draft != "" {
		draft += " "
	}
	s.draft.Set(draft + "@" + rel + " ")
}

// loadPickerFolder backs ui/filepicker's State.Load: one level at a time, on
// a goroutine, returning to the engine through cx.Post.
func (s *appState) loadPickerFolder(path string, done func(filepicker.Folder, error)) {
	go func() {
		if path == "" {
			path = s.currentCwd()
		}
		entries, err := files.List(path, 2000)
		folder := filepicker.Folder{Path: path}
		if parent := filepath.Dir(path); parent != path {
			folder.Parent = parent
		}
		for _, e := range entries {
			folder.Entries = append(folder.Entries, filepicker.Entry{
				Name: e.Name, Path: e.Path, Dir: e.IsDir, Size: e.Size,
				Modified: time.Unix(e.ModTime, 0), Hidden: strings.HasPrefix(e.Name, "."),
			})
		}
		s.cx.Post(func() { done(folder, err) })
	}()
}

func (s *appState) openProjectPicker() {
	start := s.projectRoot()
	if start == "" {
		start = s.currentCwd()
	}
	s.picker.Open(start)
	s.projectOpen.Set(true)
}

// openProject makes path the project of the window: the file tree, the shell
// and the next conversation all start there.
func (s *appState) openProject(path string) {
	s.projectOpen.Set(false)
	if path == "" {
		return
	}
	if filepath.Clean(path) == s.projectRoot() {
		return
	}
	// setProject already resets, refreshes, runs the trust gate and connects;
	// doing that again started two get_state/get_messages races per switch.
	s.setProject(path)
}

// openWorktreeDialog shows the project's worktrees and the create form.
func (s *appState) openWorktreeDialog() {
	root := s.projectRoot()
	if root == "" {
		s.openProjectPicker()
		return
	}
	s.worktreeBranch.Set("")
	s.worktreePath.Set(worktree.DefaultPath(root, "worktree"))
	s.worktreeForce.Set(false)
	s.worktreeOpen.Set(true)
	s.loadWorktrees()
}

func (s *appState) loadWorktrees() {
	root := s.projectRoot()
	if root == "" {
		s.worktrees.Set(nil)
		return
	}
	go func() {
		list, _ := worktree.List(root)
		s.cx.Post(func() {
			if s.projectRoot() == root {
				s.worktrees.Set(list)
			}
		})
	}()
}

func (s *appState) createWorktree() {
	root := s.projectRoot()
	branch := strings.TrimSpace(s.worktreeBranch.GetUntracked())
	path := strings.TrimSpace(s.worktreePath.GetUntracked())
	if root == "" || branch == "" {
		s.notify(ui.ToastWarning, "需要项目和分支名", "")
		return
	}
	if path == "" {
		path = worktree.DefaultPath(root, branch)
	}
	go func() {
		err := worktree.Create(root, path, branch, "", true)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "创建工作树失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已创建工作树", branch)
			s.worktreeBranch.Set("")
			s.loadWorktrees()
			s.worktreeOpen.Set(false)
			s.openProject(path)
		})
	}()
}

func (s *appState) removeWorktree(tree worktree.Worktree) {
	root := s.projectRoot()
	force := s.worktreeForce.GetUntracked()
	go func() {
		err := worktree.Remove(root, tree.Path, force)
		s.cx.Post(func() {
			if err != nil {
				if !force {
					s.worktreeForce.Set(true)
					s.notify(ui.ToastWarning, "工作树有改动，再点一次将强制删除", tree.Path)
					return
				}
				s.notify(ui.ToastError, "删除工作树失败", err.Error())
				return
			}
			s.worktreeForce.Set(false)
			s.notify(ui.ToastInfo, "已删除工作树", tree.Branch)
			s.loadWorktrees()
		})
	}()
}

// startSideChat forks the open conversation into a side chat: the branch is
// copied to a session file of its own, marked pi-web:side, opened in the
// right dock with the read-mostly tools. The copy never goes through pi's
// `clone`, which rebinds the parent's process to the clone: the main
// conversation must keep writing to its own file while the tab works beside
// it.
func (s *appState) startSideChat() {
	if s.current() == nil {
		s.notify(ui.ToastWarning, "侧聊需要已连接的会话", "先打开一个会话")
		return
	}
	s.mu.Lock()
	parent := s.connKey
	s.mu.Unlock()
	if parent == "" || strings.HasPrefix(parent, "new:") {
		s.notify(ui.ToastWarning, "还开不了侧聊", "先发一条消息，pi 保存会话后就能复制出侧聊")
		return
	}
	parentID := ""
	if info, err := sessions.Read(parent); err == nil {
		parentID = info.ID
	}
	go func() {
		path, err := sessions.ForkSide(parent, parentID)
		if err != nil {
			s.cx.Post(func() { s.notify(ui.ToastError, "侧聊创建失败", err.Error()) })
			return
		}
		s.cx.Post(func() {
			s.notify(ui.ToastSuccess, "侧聊已创建", "已开在右侧栏，只带读取类工具，主对话保持原样")
			s.openSideTab(path, parent)
			go func() {
				_ = s.store.Refresh()
				s.cx.Post(func() { s.publishSessions() })
			}()
		})
	}()
}


