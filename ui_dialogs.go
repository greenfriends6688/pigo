package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/base/editor"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/filepicker"

	"piwebnative/internal/agent"
	"piwebnative/internal/reducer"
)

// palette is the ⌘K command surface. The app binds the shortcuts; NSGo's
// CommandDialog owns focus, filtering and the modal presentation.
func (s *appState) palette() core.Node {
	return ui.CommandDialog{
		Open:         s.paletteOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.paletteOpen.Set(open) },
		Command: ui.Command{
			Items:       s.commandItems,
			Placeholder: "输入命令…",
			SearchLabel: "搜索命令",
			Empty:       "没有匹配的命令",
			OnSelect:    s.runCommand,
			TestID:      "palette-command",
		},
		TestID: "palette",
	}
}

func (s *appState) commandItems() []base.CommandItem {
	items := []base.CommandItem{
		{ID: "new-session", Label: "新建会话", Shortcut: "mod+n", Group: "会话", Keywords: []string{"new", "session"}},
		{ID: "reconnect", Label: "重连当前会话", Group: "会话"},
		{ID: "rename", Label: "重命名当前会话", Group: "会话"},
		{ID: "fork", Label: "Fork 到最后一条消息", Group: "会话"},
		{ID: "clone", Label: "克隆会话", Group: "会话"},
		{ID: "side-chat", Label: "侧聊（从当前进度分出一条只读工具的对话）", Group: "会话", Keywords: []string{"side", "侧聊"}},
		{ID: "export-html", Label: "导出会话 HTML", Group: "会话"},
		{ID: "view-chat", Label: "回到对话", Group: "视图"},
		{ID: "view-files", Label: "右侧打开文件与 Git", Shortcut: "mod+1", Group: "视图"},
		{ID: "view-git", Label: "右侧打开 Git 历史", Shortcut: "mod+3", Group: "视图"},
		{ID: "view-terminal", Label: "右侧打开终端", Shortcut: "mod+2", Group: "视图"},
		{ID: "open-project", Label: "打开项目…", Shortcut: "mod+o", Group: "视图"},
		{ID: "toggle-right", Label: "收起 / 展开右侧面板", Group: "视图"},
		{ID: "toggle-sidebar", Label: "切换侧栏", Shortcut: "mod+b", Group: "视图", Keywords: []string{"sidebar", "侧栏"}},
		{ID: "toggle-appearance", Label: "切换明暗外观", Shortcut: "mod+shift+l", Group: "视图", Keywords: []string{"appearance", "theme", "外观", "主题"}},
		{ID: "view-settings", Label: "打开设置", Shortcut: "mod+,", Group: "视图"},
		{ID: "debug", Label: "诊断信息…", Group: "视图", Keywords: []string{"debug", "诊断", "diagnostics"}},
		{ID: "abort", Label: "停止当前运行", Group: "Agent"},
		{ID: "clear-queue", Label: "清空 steering / followUp 队列", Group: "Agent"},
		{ID: "compact", Label: "压缩上下文", Group: "Agent"},
		{ID: "get-state", Label: "读取 Agent 状态", Group: "Agent"},
		{ID: "get-messages", Label: "重新对齐消息（get_messages）", Group: "Agent"},
		{ID: "get-available-models", Label: "取回可用模型", Group: "Agent"},
		{ID: "get-commands", Label: "取回 / 命令列表", Group: "Agent"},
		{ID: "new-terminal-tab", Label: "新终端标签", Group: "工具"},
		// The tool policy lost its title-bar menu, so the palette carries it.
		{ID: "tools-default", Label: "工具：默认（读写）", Group: "工具", Keywords: []string{"tools", "工具"}},
		{ID: "tools-read", Label: "工具：只读", Group: "工具", Keywords: []string{"read", "只读"}},
		{ID: "tools-none", Label: "工具：无", Group: "工具", Keywords: []string{"none", "无工具"}},
		{ID: "trust-cwd", Label: "信任当前项目", Group: "工具"},
		{ID: "worktrees", Label: "Git 工作树…", Group: "工具"},
		{ID: "subagents", Label: "子代理会话…", Group: "会话"},
		{ID: "stats", Label: "会话用量…", Group: "会话"},
		{ID: "branches", Label: "对话分支…", Group: "会话"},
		{ID: "settings-models", Label: "设置：模型与提供商", Group: "设置"},
		{ID: "settings-mcp", Label: "设置：MCP 服务器", Group: "设置"},
		{ID: "settings-skills", Label: "设置：Skills", Group: "设置"},
		{ID: "settings-agents", Label: "设置：Agents", Group: "设置"},
		{ID: "settings-plugins", Label: "设置：插件", Group: "设置", Keywords: []string{"plugins", "插件", "扩展"}},
		{ID: "view-settings-general", Label: "设置：通用", Group: "设置", Keywords: []string{"general", "通用"}},
		{ID: "view-settings-appearance", Label: "设置：外观", Group: "设置", Keywords: []string{"appearance", "theme", "外观"}},
		{ID: "set-default-model", Label: "将当前模型设为默认", Group: "设置", Keywords: []string{"default", "默认", "model"}},
	}
	// Recent projects: the palette jumps straight to a project the sidebar has
	// already seen, so switching costs no folder picker. Group is a lower-cased
	// grouping key, not a usable path; the row's Subtitle keeps the real cwd.
	// Sentinel groups the sidebar invents ("侧聊", "归档", "其它项目") are not
	// paths and are left out.
	seen := make(map[string]bool, 5)
	for _, r := range s.rows.Get() {
		path := r.Subtitle
		if !filepath.IsAbs(path) {
			continue
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		label := r.Project
		if label == "" {
			label = filepath.Base(path)
		}
		items = append(items, base.CommandItem{
			ID:       "project:" + path,
			Label:    "打开项目：" + label,
			Group:    "项目",
			Keywords: []string{"project", "项目"},
		})
		if len(seen) == 5 {
			break
		}
	}
	return base.FilterCommands(items, "")
}

func (s *appState) runCommand(id string) {
	s.paletteOpen.Set(false)
	switch id {
	case "new-session":
		s.newSession()
	case "reconnect":
		s.reconnect()
	case "rename":
		s.beginRename("", s.sessionTitle(s.selected.GetUntracked()))
	case "fork":
		s.forkSession()
	case "clone":
		s.cloneSession()
	case "side-chat":
		s.startSideChat()
	case "export-html":
		s.call(agent.Command{"type": agent.CmdExportHTML})
	case "view-chat":
		s.rightOpen.Set(false)
	case "view-files":
		s.openDock("files")
	case "view-git":
		s.openDock("review")
	case "view-terminal":
		s.openDock("terminal")
	case "open-project":
		s.openProjectPicker()
	case "toggle-right":
		s.rightOpen.Set(!s.rightOpen.GetUntracked())
	case "toggle-sidebar":
		s.toggleSidebar()
	case "toggle-appearance":
		s.toggleAppearance()
	case "view-settings":
		s.settingsOpen.Set(true)
	case "debug":
		s.debugOpen.Set(true)
	case "view-settings-general":
		s.category.Set("general")
		s.settingsOpen.Set(true)
	case "view-settings-appearance":
		s.category.Set("general")
		s.settingsOpen.Set(true)
	case "view-settings-usage":
		s.category.Set("models")
		s.settingsOpen.Set(true)
	case "abort":
		s.stop()
	case "clear-queue":
		s.call(s.newCommand(agent.CmdClearQueue))
	case "compact":
		s.call(s.newCommand(agent.CmdCompact))
	case "set-default-model":
		s.setCurrentAsDefaultModel()
	case "get-state":
		s.call(s.newCommand(agent.CmdGetState))
	case "get-messages":
		s.call(s.newCommand(agent.CmdGetMessages))
	case "get-available-models":
		s.call(s.newCommand(agent.CmdGetAvailableModels))
	case "get-commands":
		s.call(s.newCommand(agent.CmdGetCommands))
	case "new-terminal-tab":
		s.newTerminalTab()
	case "tools-default":
		s.setToolMode("default")
	case "tools-read":
		s.setToolMode("read")
	case "tools-none":
		s.setToolMode("none")
	case "trust-cwd":
		s.trustCurrentProject()
	case "worktrees":
		s.openWorktreeDialog()
	case "subagents":
		s.openSubagents()
	case "stats":
		s.category.Set("models")
		s.settingsOpen.Set(true)
	case "branches":
		s.openBranches()
	case "settings-models":
		s.category.Set("models")
		s.settingsOpen.Set(true)
	case "settings-mcp":
		s.category.Set("mcp")
		s.settingsOpen.Set(true)
	case "settings-skills":
		s.category.Set("skills")
		s.settingsOpen.Set(true)
	case "settings-agents":
		s.category.Set("agents")
		s.settingsOpen.Set(true)
	case "settings-plugins":
		s.category.Set("extensions")
		s.settingsOpen.Set(true)
	}
	// Recent-project rows carry their project key in the ID; the palette has
	// already closed by now, so the switch is only a jump.
	if strings.HasPrefix(id, "project:") {
		s.openProject(strings.TrimPrefix(id, "project:"))
	}
}

// setCurrentAsDefaultModel makes the model the session is on the default for
// new sessions. The picker's list is the only source of "provider/model", so
// a name pi reports without a matching entry is a warning, not a guess.
func (s *appState) setCurrentAsDefaultModel() {
	name := s.modelName.Get()
	for _, m := range s.models.Get() {
		if m.Name == name {
			s.setDefaultModel(m.Provider + "/" + m.ID)
			return
		}
	}
	s.notify(ui.ToastWarning, "无法设为默认", "当前模型不在可用列表中")
}

// sessionTitle is the row's title, for prefilling a dialog.
func (s *appState) sessionTitle(id string) string {
	if id == "" {
		return ""
	}
	for _, r := range s.searchRows.GetUntracked() {
		if r.ID == id {
			return r.Title
		}
	}
	return baseName(id)
}

func (s *appState) renameDialog() core.Node {
	project := func() bool { return s.renameGroup.Get() != "" }
	return ui.Dialog{
		Open:         s.renameOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.renameOpen.Set(open) },
		Title:        "重命名",
		Description:  "会话名写入会话文件；项目名只改侧栏显示。",
		Children: []core.Node{
			core.Show{When: project, Then: func(*core.Scope) core.Node {
				return core.Text{Content: "只改侧栏显示的项目名，不动文件夹。清空则恢复原名。", TestID: "rename-project-note",
					MaxLines: 2, Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
			}},
			ui.Input{
				Value: s.renameName.Get, OnChange: s.renameName.Set,
				Placeholder: "名称", AutoFocus: true,
				TestID: "rename-input", Style: core.Style{Width: core.Pct(100)},
				OnSubmit: func(string) { s.commitRename() },
			},
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", OnPress: func() { s.renameOpen.Set(false) }},
			ui.Button{Text: "保存", Variant: ui.ButtonPrimary, TestID: "rename-submit", OnPress: s.commitRename},
		},
		TestID: "rename-dialog",
	}
}

// deleteDialog confirms removing a session file from disk.
func (s *appState) deleteDialog() core.Node {
	return ui.Dialog{
		Open:         s.deleteOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.deleteOpen.Set(open) },
		Title:        "删除会话",
		Description:  "会话文件会从磁盘删除，无法通过本应用恢复。",
		Children: []core.Node{
			core.Text{ContentFunc: func() string { return s.deleteName() }, TestID: "delete-name", MaxLines: 1,
				Style: core.Style{FontSize: 12.5, FontFamily: core.FontMonospace, Color: s.colors.Text()}},
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", OnPress: func() { s.deleteOpen.Set(false) }},
			ui.Button{Text: "删除", Variant: ui.ButtonPrimary, TestID: "delete-submit", OnPress: s.commitDelete},
		},
		TestID: "delete-dialog",
	}
}

// projectPicker is ui/filepicker over the local file system: the sidebar
// opens a folder and the dialog makes it the window's project.
func (s *appState) projectPicker() core.Node {
	return filepicker.Dialog{
		State:  s.picker,
		Open:   s.projectOpen.Get,
		OnOpen: s.projectOpen.Set,
		Labels: filepicker.Labels{
			Title: "打开项目", Address: "路径",
			Icons: "图标", List: "列表", Hidden: "显示隐藏文件",
			Name: "名称", Modified: "修改时间", Size: "大小",
			Empty: "空文件夹", Cancel: "取消", Choose: "打开此文件夹", Close: "关闭",
		},
		Device: "本机",
		Hint:   "打开后，文件树、终端与新会话都使用这个目录",
		Sections: []filepicker.Section{
			{Label: "位置", Places: []filepicker.Place{
				{Name: "主目录", Path: homeDir(), Icon: lucide.House},
				{Name: "当前项目", Path: s.projectPath(), Icon: lucide.FolderOpen},
			}},
		},
		Choose: s.openProject,
		TestID: "project-picker",
	}
}

// syncExtensions keeps exactly one extension request in front of the user.
// Every request must be answered with extension_ui_response or the extension
// blocks forever (PLAN §8 P2).
func (s *appState) syncExtensions() {
	snap := s.redux.Snapshot()
	for _, req := range snap.ExtensionRequests {
		if s.extDone[req.ID] {
			continue
		}
		s.extCurrent.Set(req)
		s.extInput.Set(req.Prefill)
		resetApproval(req.ID)
		s.extOpen.Set(true)
		return
	}
	s.extOpen.Set(false)
}

func (s *appState) replyExtension(req reducer.ExtensionRequest, value string, confirmed, cancelled bool) {
	if req.ID == "" {
		return
	}
	s.extDone[req.ID] = true
	var cmd agent.Command
	switch {
	case cancelled:
		cmd = agent.CancelResponse(req.ID)
	case req.Method == "confirm":
		cmd = agent.ConfirmResponse(req.ID, confirmed)
	default:
		cmd = agent.ValueResponse(req.ID, value)
	}
	s.fire(cmd)
	s.extOpen.Set(false)
	s.syncExtensions()
}

// extensionDialog renders confirm as an alert and the value-taking methods
// as a single input dialog. A `select` with options belongs to the composer's
// approval card (ui_approval.go), which keeps the transcript and the message
// in view; a select without options still gets the input as a fallback.
// Non-interactive methods (setStatus/setWidget/setTitle/notify) are consumed
// by the reducer and never reach here.
func (s *appState) extensionDialog() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		req := s.extCurrent.Get()
		if req.ID == "" || !s.extOpen.Get() {
			return core.View{}
		}
		if req.Method == "select" && len(req.Options) > 0 {
			return core.View{}
		}
		title := req.Title
		if title == "" {
			title = "扩展请求 · " + req.Method
		}
		if req.Method == "confirm" {
			return ui.AlertDialog{
				Open:         s.extOpen.Get,
				OnOpenChange: func(open bool, _ base.Reason) { s.extOpen.Set(open) },
				Title:        title,
				Description:  req.Message,
				Actions: []ui.AlertAction{
					{Text: "取消", OnPress: func() { s.replyExtension(req, "", false, false) }},
					{Text: "确定", Variant: ui.ButtonPrimary, OnPress: func() { s.replyExtension(req, "", true, false) }},
				},
				TestID: "extension-confirm",
			}
		}
		body := req.Message
		if body == "" && len(req.Options) > 0 {
			body = strings.Join(req.Options, "\n")
		}
		return ui.Dialog{
			Open:         s.extOpen.Get,
			OnOpenChange: func(open bool, _ base.Reason) { s.extOpen.Set(open) },
			Title:        title,
			Description:  body,
			Children: []core.Node{
				ui.Input{
					Value: s.extInput.Get, OnChange: s.extInput.Set,
					Placeholder: req.Placeholder, TestID: "extension-input",
					Style: core.Style{Width: core.Pct(100)},
				},
			},
			Footer: []core.Node{
				ui.Button{Text: "取消", OnPress: func() { s.replyExtension(req, "", false, true) }},
				ui.Button{Text: "提交", Variant: ui.ButtonPrimary, TestID: "extension-submit",
					OnPress: func() { s.replyExtension(req, s.extInput.GetUntracked(), false, false) }},
			},
			TestID: "extension-dialog",
		}
	})
}

// providerDialog edits one models.json provider.
func (s *appState) providerDialog() core.Node {
	return ui.Dialog{
		Open:         s.providerOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.providerOpen.Set(open) },
		Title:        "提供商",
		Description:  "写入 ~/.pi/agent/models.json，保存前自动备份；空的 API Key 不覆盖原值。",
		Style:        core.Style{Width: core.Pt(560)},
		Children: []core.Node{
			core.Dynamic(func(*core.Scope) core.Node {
				catalog := s.catalog.Get()
				if len(catalog) == 0 {
					return nil
				}
				options := []ui.Option[string]{{Value: "", Label: "自定义"}}
				for _, p := range catalog {
					options = append(options, ui.Option[string]{Value: p.ID, Label: p.ID + fmt.Sprintf("（%d 个模型）", len(p.Models))})
				}
				return ui.Select[string]{Options: options, Value: s.providerPreset.Get, OnChange: s.applyProviderPreset,
					Label: "从 pi 预置填写", TestID: "provider-preset", Style: core.Style{Width: core.Pct(100)}}
			}),
			providerField("ID", "openai-compatible", s.providerEditID, s.providerEditID.Get() != "", "provider-id"),
			providerField("名称", "Acme", s.providerName, false, "provider-name"),
			providerField("Base URL", "https://api.example.com/v1", s.providerBase, false, "provider-base"),
			providerField("API 类型", "openai-completions", s.providerAPI, false, "provider-api"),
			providerFieldMasked("API Key", "sk-…", s.providerKey, "provider-key"),
			core.View{Style: core.Style{Gap: 4}, Children: []core.Node{
				core.Text{Content: "模型 ID（每行一个）", Style: core.Style{FontSize: 11, Color: s.colors.Muted()}},
				ui.Input{Value: s.providerModels.Get, OnChange: s.providerModels.Set, Multiline: true,
					Placeholder: "gpt-5\ngpt-5-mini", TestID: "provider-models",
					Style: core.Style{Width: core.Pct(100), MinHeight: core.Pt(90)}},
			}},
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", TestID: "provider-cancel", OnPress: func() { s.providerOpen.Set(false) }},
			ui.Button{Text: "保存", Variant: ui.ButtonPrimary, TestID: "provider-submit", OnPress: s.saveProviderForm},
		},
		TestID: "provider-dialog",
	}
}

func providerField(label, placeholder string, sig *core.Signal[string], readonly bool, testID string) core.Node {
	return ui.Input{Value: sig.Get, OnChange: sig.Set, Label: label, Placeholder: placeholder, Readonly: readonly,
		TestID: testID, Style: core.Style{Width: core.Pct(100)}}
}

func providerFieldMasked(label, placeholder string, sig *core.Signal[string], testID string) core.Node {
	return ui.Input{Value: sig.Get, OnChange: sig.Set, Label: label, Placeholder: placeholder, Masked: true,
		TestID: testID, Style: core.Style{Width: core.Pct(100)}}
}

// mcpDialog adds one MCP server to the global or project file.
func (s *appState) mcpDialog() core.Node {
	transport := func() string {
		if s.mcpTransport.Get() == "http" {
			return "http"
		}
		return "stdio"
	}
	scope := func() string {
		if s.mcpProject.Get() {
			return "project"
		}
		return "global"
	}
	return ui.Dialog{
		Open:         s.mcpOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.mcpOpen.Set(open) },
		Title:        "添加 MCP 服务器",
		Description:  "写入 mcp.json；项目范围的服务器只在当前项目生效。",
		Style:        core.Style{Width: core.Pt(560)},
		Children: []core.Node{
			core.Dynamic(func(*core.Scope) core.Node {
				return core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
					ui.Input{Value: s.mcpName.Get, OnChange: s.mcpName.Set, Label: "名称", Placeholder: "my-server",
						TestID: "mcp-name", Style: core.Style{Width: core.Pct(100)}},
					ui.Select[string]{Options: []ui.Option[string]{{Value: "stdio", Label: "stdio（命令）"}, {Value: "http", Label: "http（URL）"}},
						Value: transport, OnChange: s.mcpTransport.Set, Label: "传输", TestID: "mcp-transport"},
					ui.Select[string]{Options: []ui.Option[string]{{Value: "global", Label: "全局"}, {Value: "project", Label: "当前项目"}},
						Value: scope, OnChange: func(v string) { s.mcpProject.Set(v == "project") }, Label: "范围", TestID: "mcp-scope"},
				}}
			}),
			core.Dynamic(func(*core.Scope) core.Node {
				if s.mcpTransport.Get() == "http" {
					return core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
						ui.Input{Value: s.mcpURL.Get, OnChange: s.mcpURL.Set, Label: "URL", Placeholder: "https://host/mcp",
							TestID: "mcp-url", Style: core.Style{Width: core.Pct(100)}},
						ui.Input{Value: s.mcpHeaders.Get, OnChange: s.mcpHeaders.Set, Label: "Headers（每行 Key: Value）",
							Multiline: true, TestID: "mcp-headers", Style: core.Style{Width: core.Pct(100), MinHeight: core.Pt(60)}},
					}}
				}
				return core.View{Style: core.Style{Gap: 8}, Children: []core.Node{
					ui.Input{Value: s.mcpCommand.Get, OnChange: s.mcpCommand.Set, Label: "命令", Placeholder: "npx",
						TestID: "mcp-command", Style: core.Style{Width: core.Pct(100)}},
					ui.Input{Value: s.mcpArgs.Get, OnChange: s.mcpArgs.Set, Label: "参数（空格分隔）", Placeholder: "-y @modelcontextprotocol/server-filesystem /tmp",
						TestID: "mcp-args", Style: core.Style{Width: core.Pct(100)}},
				}}
			}),
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", OnPress: func() { s.mcpOpen.Set(false) }},
			ui.Button{Text: "添加", Variant: ui.ButtonPrimary, TestID: "mcp-submit", OnPress: s.saveMCPForm},
		},
		TestID: "mcp-dialog",
	}
}

// skillDialog shows a skill's instructions.
func (s *appState) skillDialog() core.Node {
	return ui.Dialog{
		Open:         s.skillOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.skillOpen.Set(open) },
		Title:        "技能",
		Description:  "SKILL.md：frontmatter 由 pi 读取，正文是技能的说明。",
		Style:        core.Style{Width: core.Pt(760), Height: core.Pt(600)},
		Children: []core.Node{
			core.Text{ContentFunc: func() string { return s.skillPath.Get() }, TestID: "skill-path", MaxLines: 2,
				Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
			// The clip is a view of its own, as a Bare dialog's is: a text view
			// that is not clipped draws its whole file over the chat behind
			// the dialog, which is what a long SKILL.md did.
			core.View{TestID: "skill-editor-box", Style: core.Style{Height: core.Pt(430), MinHeight: core.Pt(0), FlexShrink: 0,
				Overflow: core.Hidden, CornerRadius: radiusCard - 4, BorderWidth: 1}, StyleFunc: func() core.Style {
				return core.Style{BorderColor: s.colors.Border(), Background: s.colors.Inset()}
			}, Children: []core.Node{
				// No MinRows: it would make the editor as tall as its text,
				// which is what let a long file run out of the dialog.
				editor.Editor{Value: s.skillBody.Get, OnChange: s.skillBody.Set,
					Language: "markdown", SoftWrap: true, NoFolding: true,
					FontSize: 12, LineHeight: 18,
					Style: core.Style{Height: core.Pct(100), MinHeight: core.Pt(0)}, TestID: "skill-body"},
			}},
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", TestID: "skill-cancel", OnPress: func() { s.skillOpen.Set(false) }},
			ui.Button{Text: "保存", Variant: ui.ButtonPrimary, TestID: "skill-submit", OnPress: s.saveSkillForm},
		},
		TestID: "skill-dialog",
	}
}

// agentDialog edits or creates an agent profile.
func (s *appState) agentDialog() core.Node {
	return ui.Dialog{
		Open:         s.agentOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.agentOpen.Set(open) },
		Title:        "Agent",
		Description:  "markdown 正文是系统提示词，frontmatter 配置模型、工具与 thinking。",
		Style:        core.Style{Width: core.Pt(760)},
		Children: []core.Node{
			core.Show{When: func() bool { return s.agentBuiltin.Get() }, Then: func(*core.Scope) core.Node {
				return core.Text{Content: "pi 自带的子代理，只读。提示词与可用工具由 pi 决定；列表里的开关才会生效。",
					TestID: "agent-builtin-note", MaxLines: 2,
					Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
			}},
			ui.Input{Value: s.agentTitle.Get, OnChange: s.agentTitle.Set, Label: "名称", Placeholder: "reviewer",
				Readonly: s.agentPath.Get() != "" || s.agentBuiltin.Get(), TestID: "agent-title", Style: core.Style{Width: core.Pct(100)}},
			core.Dynamic(func(*core.Scope) core.Node {
				return core.View{Style: core.Style{FlexGrow: 1, Height: core.Pt(420), MinHeight: core.Pt(0)}, Children: []core.Node{
					editor.Editor{Value: s.agentBody.Get, OnChange: s.agentBody.Set, ReadOnly: s.agentBuiltin.Get(), Language: "markdown", SoftWrap: true,
						FontSize: 12, LineHeight: 18,
						Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, TestID: "agent-body"},
				}}
			}),
		},
		Footer: []core.Node{
			core.Show{When: func() bool { return !s.agentBuiltin.Get() }, Then: func(*core.Scope) core.Node {
				return core.Group{ui.Button{Text: "取消", OnPress: func() { s.agentOpen.Set(false) }},
					ui.Button{Text: "保存", Variant: ui.ButtonPrimary, TestID: "agent-submit", OnPress: s.saveAgentForm}}
			}},
			core.Show{When: func() bool { return s.agentBuiltin.Get() }, Then: func(*core.Scope) core.Node {
				return ui.Button{Text: "关闭", Variant: ui.ButtonPrimary, TestID: "agent-close", OnPress: func() { s.agentOpen.Set(false) }}
			}},
		},
		TestID: "agent-dialog",
	}
}

// trustDialog is the project trust gate.
func (s *appState) trustDialog() core.Node {
	return ui.Dialog{
		Open:         s.trustOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.trustOpen.Set(open) },
		Title:        "信任此项目？",
		Description:  "这个项目的 .pi 目录带有设置、MCP、技能或扩展，运行前请确认来源可信。",
		Children: []core.Node{
			core.Text{ContentFunc: func() string { return s.projectRoot() }, TestID: "trust-path",
				Style: core.Style{FontSize: 12, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
		},
		Footer: []core.Node{
			ui.Button{Text: "暂不", OnPress: func() { s.trustOpen.Set(false) }},
			ui.Button{Text: "信任并继续", Variant: ui.ButtonPrimary, TestID: "trust-submit", OnPress: s.trustCurrentProject},
		},
		TestID: "trust-dialog",
	}
}

// worktreeDialog lists the project's worktrees and creates another.
func (s *appState) worktreeDialog() core.Node {
	return ui.Dialog{
		Open: s.worktreeOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) {
			s.worktreeOpen.Set(open)
			if open {
				s.loadWorktrees()
			}
		},
		Title:       "Git 工作树",
		Description: "每个工作树是同一仓库的独立检出，可并行开发不同分支。",
		Style:       core.Style{Width: core.Pt(680)},
		Children: []core.Node{
			s.worktreeList(),
			core.View{Style: core.Style{Height: core.Pt(1), Background: s.colors.Border()}},
			ui.Input{Value: s.worktreeBranch.Get, OnChange: s.worktreeBranch.Set, Label: "新分支",
				Placeholder: "feature/login", TestID: "worktree-branch", Style: core.Style{Width: core.Pct(100)}},
			ui.Input{Value: s.worktreePath.Get, OnChange: s.worktreePath.Set, Label: "目录",
				TestID: "worktree-path", Style: core.Style{Width: core.Pct(100)}},
		},
		Footer: []core.Node{
			ui.Button{Text: "关闭", OnPress: func() { s.worktreeOpen.Set(false) }},
			ui.Button{Text: "创建工作树", Variant: ui.ButtonPrimary, TestID: "worktree-create", OnPress: s.createWorktree},
		},
		TestID: "worktree-dialog",
	}
}

func (s *appState) worktreeList() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		list := s.worktrees.Get()
		if len(list) == 0 {
			return core.Text{Content: "当前项目不是 Git 工作树", TestID: "worktrees-empty",
				Style: core.Style{FontSize: 12, Color: s.colors.Muted()}}
		}
		rows := make([]core.Node, 0, len(list))
		for _, w := range list {
			tree := w
			branch := tree.Branch
			if tree.Main {
				branch += "（主）"
			}
			var trailing []core.Node
			if !tree.Main {
				trailing = append(trailing,
					ui.Button{Text: "打开", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "worktree-open-" + baseName(tree.Path),
						OnPress: func() {
							s.worktreeOpen.Set(false)
							s.openProject(tree.Path)
						}},
					ui.Button{Text: "删除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "worktree-remove-" + baseName(tree.Path),
						OnPress: func() { s.removeWorktree(tree) }})
			}
			rows = append(rows, s.settingsRow(lucide.GitBranch, branch, tree.Path, trailing...))
		}
		return core.View{Style: core.Style{Gap: 2}, Children: rows}
	})
}

// subagentDialog lists the sessions subagents spawned.
func (s *appState) subagentDialog() core.Node {
	return ui.Dialog{
		Open:         s.subOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.subOpen.Set(false) },
		Title:        "子代理会话",
		Description:  "由当前 pi 运行派生出的子代理会话，点开即可查看其完整转录。",
		Style:        core.Style{Width: core.Pt(680)},
		Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
			items := s.subItems.Get()
			if len(items) == 0 {
				return core.Text{Content: "没有子代理会话", TestID: "subagents-empty",
					Style: core.Style{FontSize: 12, Color: s.colors.Muted()}}
			}
			rows := make([]core.Node, 0, len(items))
			for _, item := range items {
				row := item
				rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(4, 2)}, Children: []core.Node{
					ui.Icon(lucide.Bot, 14, s.colors.Muted),
					core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
						core.Text{Content: row.Title, MaxLines: 1, Style: core.Style{FontSize: 12.5, Color: s.colors.Text()}},
						core.Text{Content: row.Age, MaxLines: 1, Style: core.Style{FontSize: 10.5, Color: s.colors.Faint()}},
					}},
					ui.Button{Text: "打开", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "subagent-open-" + baseName(row.ID),
						OnPress: func() { s.openSubagent(row.ID) }},
				}})
			}
			return core.View{Style: core.Style{Gap: 2}, Children: rows}
		})},
		Footer: []core.Node{ui.Button{Text: "关闭", OnPress: func() { s.subOpen.Set(false) }}},
		TestID: "subagents-dialog",
	}
}

// branchTreePrefixes draws the box-drawing guide that turns the branch picker
// from a flat indented list into a tree: every ancestor that still has a
// sibling below keeps a vertical bar, and the row itself closes with a tee or a
// corner. Depth 0 rows carry no guide. Only the first 200 rows are measured, so
// a pathological tree cannot cost the dialog its frame budget; later rows fall
// back to plain labels.
func branchTreePrefixes(items []branchItem) []string {
	const maxRows = 200
	n := len(items)
	if n > maxRows {
		n = maxRows
	}
	// One forward pass over the pre-order rows recovers the parent of each row:
	// the stack holds the open ancestors, indexed by depth.
	depths := make([]int, n)
	parent := make([]int, n)
	stack := make([]int, 0, 16)
	for i := 0; i < n; i++ {
		depth := items[i].Depth
		if depth < 0 {
			depth = 0
		}
		if depth > len(stack) {
			depth = len(stack) // a skipped level; do not invent ancestors
		}
		depths[i] = depth
		parent[i] = -1
		if depth > 0 {
			parent[i] = stack[depth-1]
		}
		stack = append(stack[:depth], i)
	}
	// A row has a following sibling when the next row at the same depth hangs
	// off the same parent. Scanning backwards makes that one lookup per row.
	hasNext := make([]bool, n)
	lastAtDepth := make([]int, 0, 16)
	for i := n - 1; i >= 0; i-- {
		for len(lastAtDepth) <= depths[i] {
			lastAtDepth = append(lastAtDepth, -1)
		}
		if next := lastAtDepth[depths[i]]; next >= 0 && parent[next] == parent[i] {
			hasNext[i] = true
		}
		lastAtDepth[depths[i]] = i
	}
	out := make([]string, n)
	chain := make([]int, 0, 16)
	for i := 0; i < n; i++ {
		depth := depths[i]
		if depth == 0 {
			continue
		}
		chain = chain[:0]
		for at := parent[i]; at >= 0; at = parent[at] {
			chain = append(chain, at) // chain[0] is the parent, chain[depth-1] the root
		}
		var b strings.Builder
		// Column j belongs to the ancestor one level below its own row, so the
		// parent drives the last guide column and deeper rows draw the bars of
		// the levels above it.
		for level := 0; level < depth-1; level++ {
			ancestor := -1
			if idx := depth - 2 - level; idx >= 0 && idx < len(chain) {
				ancestor = chain[idx]
			}
			if ancestor >= 0 && hasNext[ancestor] {
				b.WriteString("│  ")
			} else {
				b.WriteString("   ")
			}
		}
		if hasNext[i] {
			b.WriteString("├─ ")
		} else {
			b.WriteString("└─ ")
		}
		out[i] = b.String()
	}
	return out
}

// branchKindDot marks who spoke the row: accent for the user, muted for the
// assistant, faint for anything else pi may add to the tree later.
func (s *appState) branchKindDot(kind string) core.Node {
	color := s.colors.Faint()
	switch kind {
	case "user":
		color = s.colors.Accent()
	case "assistant":
		color = s.colors.Muted()
	}
	return core.View{Style: core.Style{Width: core.Pt(6), Height: core.Pt(6), CornerRadius: 3, FlexShrink: 0, Background: color}}
}

// branchDialog lists the conversation tree and continues from any entry as a
// new branch.
func (s *appState) branchDialog() core.Node {
	return ui.Dialog{
		Open:         s.branchOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.branchOpen.Set(false) },
		Title:        "对话分支",
		Description:  "缩进表示分支层级；点任意一条消息，从这里继续会创建新分支（原会话不变）。",
		Style:        core.Style{Width: core.Pt(760)},
		Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
			items := s.branchItems.Get()
			if len(items) == 0 {
				return core.Text{Content: "还没有可分支的消息", TestID: "branches-empty",
					Style: core.Style{FontSize: 12, Color: s.colors.Muted()}}
			}
			prefixes := branchTreePrefixes(items)
			rows := make([]core.Node, 0, len(items))
			for i, item := range items {
				entry := item
				prefix := ""
				if i < len(prefixes) {
					prefix = prefixes[i]
				}
				guide := core.Style{FontSize: 12, FontFamily: core.FontMonospace, FlexShrink: 0, Color: s.colors.Faint()}
				line := []core.Node{s.branchKindDot(entry.Kind)}
				if prefix != "" {
					line = append(line, core.Text{Content: prefix, MaxLines: 1, Style: guide})
				}
				line = append(line, core.Text{Content: entry.Label, MaxLines: 1, Style: core.Style{
					FontSize: 12, FontFamily: core.FontMonospace, FlexShrink: 1, MinWidth: core.Pt(0), Color: s.colors.Text()}})
				rows = append(rows, core.View{Role: core.RoleButton, Label: entry.Label,
					TestID:  "branch-" + fmt.Sprint(i),
					OnPress: func(core.PressEvent) { s.branchOpen.Set(false); s.forkAt(entry.ID) },
					Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
						Padding: core.Symmetric(4, 8), CornerRadius: 5},
					StyleFunc: func() core.Style {
						return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.06)}}
					},
					Children: line,
				})
			}
			return core.Scroll{Style: core.Style{MaxHeight: core.Pt(460)}, Children: []core.Node{
				core.View{Style: core.Style{Gap: 1}, Children: rows},
			}}
		})},
		Footer: []core.Node{ui.Button{Text: "关闭", OnPress: func() { s.branchOpen.Set(false) }}},
		TestID: "branches-dialog",
	}
}
