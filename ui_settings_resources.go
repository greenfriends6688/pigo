package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// 「外观」「数据与用量」「关于」三个板块新增的文案集中在这里，待 i18n。
// locales/catalog_gen.go 由 nsgo i18n 从 locales/*.yaml 生成（文件头写明
// DO NOT EDIT），手工加条目会被下一次生成覆盖，所以本轮不动 locales；其余
// 设置页的既有中文仍是就地硬编码，格式保持一致，等 i18n 全量接入时一起搬。
const (
	appearanceCategoryDesc = "主题、终端字号与终端主题即时生效；其余为只读展示"

	uiFontTitle   = "界面字号"
	uiFontDesc    = "当前 13（设定项 12 / 13 / 14）；本版本没有全局字号入口，暂为只读"
	codeFontTitle = "代码字号"
	codeFontDesc  = "当前 12（设定项 11 / 12 / 13）；代码块跟随界面字体，暂无独立入口"
	densityTitle  = "密度"
	densityDesc   = "当前舒适；本版本没有全局间距入口，紧凑密度暂未接入"
	motionTitle   = "减少动效"
	motionDesc    = "只读：跟随 macOS「减弱动态效果」，应用内切换暂未接入"

	usageCategoryTitle   = "数据与用量"
	usageCategoryDesc    = "本次连接读取的 token、费用估算与本地数据"
	usageTokenGroup      = "Token 总量"
	usageDataGroup       = "数据"
	usageInputTitle      = "输入"
	usageOutputTitle     = "输出"
	usageCacheReadTitle  = "缓存读"
	usageCacheWriteTitle = "缓存写"
	usageCostTitle       = "费用估算"
	usageStatsHint       = "来自 get_session_stats；本次连接还没有数据时显示「暂无」"
	usageNoStats         = "暂无"
	usageNoCacheWrite    = "暂无（RPC 未返回）"
	usageDataDirTitle    = "数据目录"
	usageIndexTitle      = "会话索引"
	usageIndexDesc       = "sessions.json 只是解析缓存，删除后按需重建；置顶、归档等 meta.json 不受影响"
	openDataDirText      = "打开数据目录"
	clearCacheText       = "清理会话缓存"

	aboutCategoryDesc = "版本、运行时与本地文件位置"
	aboutVersionTitle = "版本"
	aboutPiTitle      = "pi 版本"
	aboutNodeTitle    = "Node 路径"
	aboutBundleTitle  = "RPC bundle"
	aboutLogTitle     = "日志"
	aboutDataTitle    = "数据目录"
	unknownText       = "未知"
	devBuildText      = "开发版（未打包）"
)

// settingsRow is a custom settings row: a title, a subtitle and trailing
// controls.
func (s *appState) settingsRow(icon func() *core.SVG, title, subtitle string, trailing ...core.Node) core.Node {
	children := []core.Node{}
	if icon != nil {
		children = append(children, ui.Icon(icon, 15, s.colors.Muted))
	}
	children = append(children, core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
		core.Text{Content: title, MaxLines: 1, Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
		core.Text{Content: subtitle, MaxLines: 2, Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
	}})
	children = append(children, trailing...)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(6, 2)}, Children: children}
}

func (s *appState) modelSettingsCategory() ui.SettingsCategory {
	groups := []ui.SettingsGroup{
		{
			ID: "models-default", Title: "默认模型",
			Items: []ui.SettingsItem{{
				ID: "default-model", Title: "新会话使用的模型", Description: "写入 settings.json 的 defaultProvider / defaultModel",
				Content: s.defaultModelSelect(),
			}},
		},
		{
			ID: "models-providers", Title: "提供商",
			Items: []ui.SettingsItem{{
				ID: "providers-detail", Title: "提供商与模型",
				Description: "左边选一个提供商，右边填 API Key、开关模型。模型本身在 models.json 里，可用模型写入 settings.json 的 enabledModels。",
				Stacked:     true,
				Content:     s.modelsMasterDetail(),
			}},
		},
	}
	// The data actions (open the folder, clear the session cache) close the
	// page; the token totals that used to sit here were the session's running
	// spend, which the transcript's own per-turn footer now says better.
	groups = append(groups, s.usageDataGroups()...)
	return ui.SettingsCategory{
		ID: "models", Title: "模型", Section: "偏好", Icon: lucide.Cpu, Description: "提供商、API Key、默认模型",
		Groups: groups,
	}
}

func (s *appState) defaultModelSelect() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		providers := s.providers.Get()
		options := []ui.Option[string]{{Value: "", Label: "跟随 pi 默认"}}
		seen := map[string]bool{}
		for _, p := range providers {
			for _, m := range p.Models {
				key := p.ID + "/" + m.ID
				seen[key] = true
				options = append(options, ui.Option[string]{Value: key, Label: m.Name + " · " + p.ID})
			}
		}
		// Presets from pi's catalog let the user choose a default before the
		// provider is written to models.json.
		for _, p := range s.catalog.Get() {
			for _, m := range p.Models {
				key := p.ID + "/" + m.ID
				if seen[key] {
					continue
				}
				seen[key] = true
				options = append(options, ui.Option[string]{Value: key, Label: m.Name + " · " + p.ID + "（预置）"})
			}
		}
		return ui.Select[string]{
			Options: options, Value: s.modelDefault.Get, OnChange: s.setDefaultModel,
			Label: "默认模型", Searchable: true, SearchPlaceholder: "搜索模型", Empty: "没有匹配的模型",
			TestID: "default-model", Style: core.Style{Width: core.Pt(260)},
		}
	})
}

func (s *appState) mcpSettingsCategory() ui.SettingsCategory {
	groups := []ui.SettingsGroup{{
		ID: "mcp-servers", Title: "服务器",
		Actions: []core.Node{ui.Button{Text: "添加服务器", TestID: "mcp-add", OnPress: s.beginMCP}},
		Items:   []ui.SettingsItem{{ID: "mcp-list", Title: "已配置", Content: s.mcpList()}},
	}}
	// A project's .pi settings run only once its folder is trusted, so trust
	// is decided here, beside the servers it gates.
	groups = append(groups, s.trustSettingsCategory().Groups...)
	return ui.SettingsCategory{
		ID: "mcp", Title: "MCP", Section: "偏好", Icon: lucide.Plug, Description: "全局与项目 mcp.json",
		Groups: groups,
	}
}

func (s *appState) mcpList() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		items := s.mcpItems.Get()
		if len(items) == 0 {
			return core.Text{Content: "还没有 MCP 服务器", TestID: "mcp-empty",
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
		}
		rows := make([]core.Node, 0, len(items))
		for _, item := range items {
			server := item
			badge := server.Scope + " · " + server.Transport
			rows = append(rows, s.settingsRow(lucide.Plug, server.Name, badge+" · "+server.Target,
				ui.Switch{Checked: func() bool { return server.Enabled },
					OnChange: func(v bool) { s.toggleMCP(server, v) }, TestID: "mcp-enabled-" + server.Name},
				ui.Button{Text: "登录", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "mcp-login-" + server.Name,
					OnPress: func() { s.mcpAuth(server.Name, false) }},
				ui.Button{Text: "登出", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "mcp-logout-" + server.Name,
					OnPress: func() { s.mcpAuth(server.Name, true) }},
				ui.Button{Text: "移除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "mcp-remove-" + server.Name,
					OnPress: func() { s.removeMCP(server) }},
			))
		}
		return core.View{Style: core.Style{Gap: 2}, Children: rows}
	})
}

func (s *appState) skillsSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "skills", Title: "Skills", Section: "偏好", Icon: lucide.Sparkles, Description: "技能说明与休眠",
		Groups: []ui.SettingsGroup{{
			ID: "skills-list", Title: "已安装",
			Actions: []core.Node{
				ui.Button{Text: "搜索并安装…", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "skills-search", OnPress: s.openSkillsSearch},
				ui.Button{Text: "打开目录", Variant: ui.ButtonSecondary, TestID: "skills-open-dir", OnPress: func() {
					s.cx.OpenFile(core.SharedFile{Path: filepath.Join(s.agentDir, "skills"), Name: "skills", MIME: "inode/directory"}, nil)
				}},
			},
			Items: []ui.SettingsItem{{ID: "skills-items", Title: "技能", Content: s.skillsListView()}},
		}},
	}
}

func (s *appState) skillsListNodes() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		items := s.skillsList.Get()
		if len(items) == 0 {
			return core.Text{Content: "还没有安装技能", TestID: "skills-empty",
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
		}
		rows := make([]core.Node, 0, len(items))
		for _, skill := range items {
			sk := skill
			subtitle := sk.Source + " · " + sk.Description
			if sk.Dormant {
				subtitle = sk.Source + " · 已休眠 · " + sk.Description
			}
			rows = append(rows, s.settingsRow(lucide.Sparkles, "/"+sk.Name, subtitle,
				ui.Switch{Checked: func() bool { return !sk.Dormant },
					OnChange: func(v bool) { s.toggleSkillDormant(sk, !v) }, TestID: "skill-enabled-" + sk.Name},
				ui.Button{Text: "查看", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "skill-open-" + sk.Name,
					OnPress: func() { s.openSkill(sk) }},
			))
		}
		return core.View{Style: core.Style{Gap: 2}, Children: rows}
	})
}

// skillsListView is the item's node; kept separate so the category stays
// small.
func (s *appState) skillsListView() core.Node { return s.skillsListNodes() }

func (s *appState) agentsSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "agents", Title: "Agents", Section: "偏好", Icon: lucide.Bot, Description: "子代理配置（markdown + frontmatter）",
		Groups: []ui.SettingsGroup{{
			ID: "agents-builtin", Title: "内置子代理",
			Items: []ui.SettingsItem{
				{ID: "agents-builtin-enabled", Title: "启用内置子代理", Description: "写入 ~/.pi/agent/agents/settings.json 的 builtInEnabled",
					Leading: ui.Icon(lucide.Bot, 15, s.colors.Muted),
					Control: core.Dynamic(func(*core.Scope) core.Node {
						_ = s.settingsRev.Get()
						enabled, _ := s.subagentSettings()
						return ui.Switch{Checked: func() bool { return enabled }, TestID: "agents-builtin-enabled",
							OnChange: func(v bool) {
								s.writeSubagentSettings(func(raw map[string]any) { raw["builtInEnabled"] = v })
							}}
					})},
				{ID: "agents-max", Title: "并发上限", Description: "同时运行的子代理数量，1–32",
					Leading: ui.Icon(lucide.Gauge, 15, s.colors.Muted),
					Control: core.Dynamic(func(*core.Scope) core.Node {
						_ = s.settingsRev.Get()
						_, current := s.subagentSettings()
						return ui.Select[int]{Options: []ui.Option[int]{{Value: 1, Label: "1"}, {Value: 2, Label: "2"}, {Value: 4, Label: "4"}, {Value: 6, Label: "6"}, {Value: 8, Label: "8"}, {Value: 10, Label: "10"}, {Value: 16, Label: "16"}, {Value: 32, Label: "32"}},
							Value: func() int { return current }, Label: "并发上限", TestID: "agents-max",
							OnChange: func(v int) {
								s.writeSubagentSettings(func(raw map[string]any) { raw["maxConcurrent"] = v })
							}, Style: core.Style{Width: core.Pt(100)}}
					})},
			},
		}, {
			ID: "agents-list", Title: "子代理",
			Actions: []core.Node{
				ui.Button{Text: "子代理会话", Variant: ui.ButtonSecondary, Icon: lucide.Bot, TestID: "agents-sessions", OnPress: s.openSubagents},
				ui.Button{Text: "打开配置目录", Variant: ui.ButtonSecondary, TestID: "agents-open-dir", OnPress: func() {
					s.cx.OpenFile(core.SharedFile{Path: filepath.Join(s.agentDir, "agents"), Name: "agents", MIME: "inode/directory"}, nil)
				}},
				ui.Button{Text: "新建 Agent", Icon: lucide.Plus, TestID: "agent-add", OnPress: func() { s.openAgent(nil) }},
			},
			Items: []ui.SettingsItem{{ID: "agents-items", Title: "已配置", Content: s.agentsListNodes()}},
		}},
	}
}

func (s *appState) agentsListNodes() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		items := s.agentsList.Get()
		if len(items) == 0 {
			return core.Text{Content: "还没有子代理配置", TestID: "agents-empty",
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
		}
		rows := make([]core.Node, 0, len(items))
		for _, agent := range items {
			a := agent
			if a.Builtin {
				// pi's own profile: the switch is the only thing the app owns,
				// and the view shows the prompt it runs with.
				rows = append(rows, s.settingsRow(lucide.Bot, a.Display, "内置 · "+a.Description,
					ui.Button{Text: "查看", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "agent-view-" + a.Name,
						OnPress: func() { s.viewBuiltinAgent(a) }},
					ui.Switch{Checked: func() bool { return a.Enabled }, TestID: "agent-enabled-" + a.Name,
						OnChange: func(on bool) { s.toggleBuiltinAgent(a.Name, on) }},
				))
				continue
			}
			subtitle := a.Source
			if a.Model != "" {
				subtitle += " · " + a.Model
			}
			if a.Description != "" {
				subtitle += " · " + a.Description
			}
			rows = append(rows, s.settingsRow(lucide.Bot, a.Name, subtitle,
				ui.Button{Text: "编辑", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "agent-edit-" + a.Name,
					OnPress: func() { s.openAgent(&a) }},
				ui.Button{Text: "删除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "agent-delete-" + a.Name,
					OnPress: func() { s.removeAgent(a) }},
			))
		}
		return core.View{Style: core.Style{Gap: 2}, Children: rows}
	})
}

// trustSettingsCategory is the project-trust group MCP ends with: a folder
// with its own .pi settings runs them only once trusted.
func (s *appState) trustSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "trust", Title: "信任", Section: "偏好", Icon: lucide.ShieldCheck, Description: "带 .pi 配置的项目先确认再运行",
		Groups: []ui.SettingsGroup{{
			ID: "trust-dirs", Title: "项目信任",
			Actions: []core.Node{
				ui.Button{Text: "信任当前项目", TestID: "trust-cwd", OnPress: func() { s.trustCurrentProject() }},
			},
			Items: []ui.SettingsItem{{ID: "trusted-list", Title: "目录", Content: s.trustedList()}},
		}},
	}
}

func (s *appState) trustedList() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		dirs := s.trustDirs.Get()
		if len(dirs) == 0 {
			return core.Text{Content: "还没有信任任何目录", TestID: "trust-empty",
				Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
		}
		rows := make([]core.Node, 0, len(dirs))
		for _, dir := range dirs {
			path := dir
			rows = append(rows, s.settingsRow(lucide.ShieldCheck, baseName(path), path,
				ui.Button{Text: "移除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "trust-remove-" + baseName(path),
					OnPress: func() { s.untrustDir(path) }},
			))
		}
		return core.View{Style: core.Style{Gap: 2}, Children: rows}
	})
}

// chatSettingsCategory holds the transcript's reading options.
func (s *appState) chatSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "chat", Title: "对话", Section: "应用", Icon: lucide.MessageSquare, Description: "思考与工具过程的显示",
		Groups: []ui.SettingsGroup{{
			ID: "chat-thinking", Title: "思考过程",
			Items: []ui.SettingsItem{
				{ID: "expand-thinking", Title: "默认展开过程", Description: "一轮结束后仍展开思考与工具；关闭后折叠成一行",
					Leading: ui.Icon(lucide.Brain, 15, s.colors.Muted),
					Control: ui.Switch{Checked: s.expandThinking.Get, OnChange: s.setExpandThinking, TestID: "expand-thinking"}},
				{ID: "hide-thinking", Title: "隐藏思考", Description: "只显示工具与回答，不渲染模型的推理文字",
					Leading: ui.Icon(lucide.EyeOff, 15, s.colors.Muted),
					Control: ui.Switch{Checked: s.hideThinking.Get, OnChange: s.setHideThinking, TestID: "hide-thinking"}},
			},
		}},
	}
}

// extensionsSettingsCategory lists the pi extensions the agent loads.
func (s *appState) extensionsSettingsCategory() ui.SettingsCategory {
	return ui.SettingsCategory{
		ID: "extensions", Title: "插件", Section: "偏好", Icon: lucide.Package, Description: "pi 包与 ~/.pi/agent/extensions",
		Groups: []ui.SettingsGroup{
			{
				ID: "plugins-packages", Title: "pi 包",
				Actions: []core.Node{
					ui.Button{Text: "安装插件…", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "plugins-install",
						OnPress: func() { s.openPluginDialog(false) }},
					ui.Button{Text: "移除插件…", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "plugins-remove",
						OnPress: func() { s.openPluginDialog(true) }},
					ui.Button{Text: "打开 settings.json", Variant: ui.ButtonSecondary, Icon: lucide.FileText, TestID: "plugins-settings", OnPress: func() {
						s.cx.OpenFile(core.SharedFile{Path: filepath.Join(s.agentDir, "settings.json"), Name: "settings.json", MIME: "application/json"}, nil)
					}},
				},
				Items: []ui.SettingsItem{
					{ID: "plugins-packages-list", Title: "packages", Description: "settings.json 的 packages；增删由 pi install / pi remove 完成",
						Leading: ui.Icon(lucide.Package, 15, s.colors.Muted), Content: s.infoText("packages", "settings.json 里没有 packages")},
				},
			},
			{
				ID: "extensions-list", Title: "扩展",
				Actions: []core.Node{ui.Button{Text: "打开目录", Variant: ui.ButtonSecondary, Icon: lucide.FolderOpen, TestID: "extensions-open-dir", OnPress: func() {
					s.cx.OpenFile(core.SharedFile{Path: filepath.Join(s.agentDir, "extensions"), Name: "extensions", MIME: "inode/directory"}, nil)
				}}},
				Items: []ui.SettingsItem{
					{ID: "extensions-items", Title: "已安装", Leading: ui.Icon(lucide.Blocks, 15, s.colors.Muted),
						Content: s.infoText("extensions", "还没有安装扩展")},
					{ID: "extensions-project", Title: "项目扩展", Description: "项目内的扩展放在 .pi/extensions，由 pi 直接加载",
						Leading: ui.Icon(lucide.FolderOpen, 15, s.colors.Muted)},
				},
			},
		},
	}
}

// pathText is a path row: monospace, selectable in effect through the copy
// button beside it, and never cut in the middle.
func (s *appState) pathText(path, testID string) core.Node {
	return core.Text{Content: path, TestID: testID, MaxLines: 2,
		Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}}
}

// usageDataGroups are the local data actions that used to live under the
// usage charts: where the sessions and settings are, and how to drop the
// cache index. The charts are gone; these still have a home.
func (s *appState) usageDataGroups() []ui.SettingsGroup {
	index := s.store.IndexPath()
	return []ui.SettingsGroup{
		{
			ID: "usage-data", Title: usageDataGroup,
			Actions: []core.Node{
				ui.Button{Text: openDataDirText, Icon: lucide.FolderOpen, Variant: ui.ButtonSecondary, TestID: "usage-open-data", OnPress: s.openDataDir},
				ui.Button{Text: clearCacheText, Icon: lucide.Eraser, Variant: ui.ButtonGhost, TestID: "usage-clear-cache", OnPress: s.clearSessionCache},
			},
			Items: []ui.SettingsItem{
				{ID: "usage-data-dir", Title: usageDataDirTitle, Description: "会话、settings.json 与原生缓存都在这里",
					Leading: ui.Icon(lucide.FolderOpen, 15, s.colors.Muted), Content: s.pathText(s.agentDir, "usage-data-path")},
				{ID: "usage-index", Title: usageIndexTitle, Description: usageIndexDesc,
					Leading: ui.Icon(lucide.Files, 15, s.colors.Muted),
					Content: core.Text{TestID: "usage-index-path", ContentFunc: func() string {
						s.settingsRev.Get() // repaint after 清理会话缓存
						size := "尚未生成"
						if st, err := os.Stat(index); err == nil {
							size = humanBytes(st.Size())
						}
						return index + " · " + size
					}, Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}, MaxLines: 2}},
			},
		},
	}
}

// humanBytes formats a file size for the index row.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// openDataDir reveals the pi agent directory in Finder.
func (s *appState) openDataDir() {
	s.cx.OpenFile(core.SharedFile{Path: s.agentDir, Name: "agent", MIME: "inode/directory"}, nil)
}

// clearSessionCache deletes sessions.json, a pure parse cache. Pins and
// archives live in meta.json and are left alone; the index rebuilds on the
// next scan.
func (s *appState) clearSessionCache() {
	path := s.store.IndexPath()
	go func() {
		err := os.Remove(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.cx.Post(func() { s.status.Set("清理会话缓存失败：" + err.Error()) })
			return
		}
		_ = s.store.Refresh()
		s.cx.Post(func() {
			s.publishSessions()
			s.settingsRev.Set(s.settingsRev.Get() + 1)
			s.status.Set("已清理会话缓存索引，按需重建")
		})
	}()
}
