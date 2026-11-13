package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"

	"piwebnative/internal/agent"
	"piwebnative/internal/models"
	"piwebnative/internal/resources"
)

func boolSetting(m map[string]any, key string, fallback bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return fallback
}

func (s *appState) newCommand(typ string) agent.Command {
	return agent.Command{"type": typ}
}

// refreshSettings reads every file inside the §3.5 boundary and publishes a
// text summary for each settings category. It is always called off the UI
// thread; signals are updated through cx.Post.
func (s *appState) refreshSettings() {
	info := map[string]string{
		"agentDir": s.agentDir,
		"node":     s.nodePath,
		"bundle":   s.bundle,
	}
	if settings, err := s.cfg.Settings(); err == nil {
		b, _ := json.MarshalIndent(settings, "", "  ")
		info["settings"] = string(b)
		var packages []string
		if raw, ok := settings["packages"].([]any); ok {
			for _, v := range raw {
				if name, ok := v.(string); ok && name != "" {
					packages = append(packages, name)
				}
			}
		}
		info["packages"] = strings.Join(packages, "\n")
		patterns := models.ReadEnabled(settings)
		s.cx.Post(func() { s.enabledPatterns.Set(patterns) })
	} else {
		info["settings"] = err.Error()
	}
	if models, err := s.cfg.Models(); err == nil {
		b, _ := json.MarshalIndent(models, "", "  ")
		text := string(b)
		if len(text) > 1200 {
			text = text[:1200] + "…"
		}
		info["models"] = text
	} else {
		info["models"] = err.Error()
	}
	if trust, err := s.cfg.Trust(); err == nil {
		b, _ := json.MarshalIndent(trust, "", "  ")
		info["trust"] = string(b)
	}
	info["skills"] = strings.Join(s.cfg.Skills(), "\n")
	info["extensions"] = strings.Join(s.cfg.Extensions(), "\n")

	// Headless tests (and offline machines) must not spawn the pi CLI just
	// to paint the settings page.
	if os.Getenv("PIWEB_NATIVE_NO_AGENT") == "1" {
		info["auth"] = "（跳过：测试模式）"
		info["mcp"] = "（跳过：测试模式）"
		s.cx.Post(func() { s.settingsInfo.Set(info) })
		s.refreshResources()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if auth, err := s.cfg.AuthCheck(ctx); err != nil {
		info["auth"] = "pi auth check: " + err.Error()
	} else {
		b, _ := json.MarshalIndent(auth, "", "  ")
		info["auth"] = string(b)
	}
	if raw, err := s.cfg.MCPListJSON(ctx); err != nil {
		info["mcp"] = "pi mcp list --json: " + err.Error()
	} else {
		info["mcp"] = string(raw)
	}

	s.cx.Post(func() { s.settingsInfo.Set(info) })
	s.refreshResources()
}

// setSettingBool writes settings.json in the background and keeps the switch
// responsive by updating its signal immediately.
func (s *appState) setSettingBool(key string, v bool, sig *core.Signal[bool]) {
	sig.Set(v)
	go func() {
		err := s.cfg.SetSetting(key, v)
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("保存 " + key + " 失败：" + err.Error())
				return
			}
			s.status.Set("已保存 " + key)
		})
		go s.refreshSettings()
	}()
}

// setExpandThinking remembers whether a finished turn keeps its thinking
// and tools open.
func (s *appState) setExpandThinking(v bool) {
	s.expandThinking.Set(v)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// setHideThinking drops thinking text from the transcript without touching
// the session.
func (s *appState) setHideThinking(v bool) {
	s.hideThinking.Set(v)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	s.refreshNow()
}

func (s *appState) trustCurrent() {
	cwd := s.currentCwd()
	go func() {
		err := s.cfg.SetTrusted(cwd, true)
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("信任失败：" + err.Error())
			} else {
				s.status.Set(fmt.Sprintf("已信任 %s", cwd))
			}
		})
		go s.refreshSettings()
	}()
}

// refreshResources publishes the editable configuration the settings pages
// show: providers, default model, MCP servers, skills, agents and trusted
// directories. It only reads files and the filesystem.
func (s *appState) refreshResources() {
	root := s.projectRoot()
	providers := []models.Provider{}
	if raw, err := s.cfg.Models(); err == nil {
		providers = models.Providers(raw)
	}
	def := ""
	if settings, err := s.cfg.Settings(); err == nil {
		provider, _ := settings["defaultProvider"].(string)
		model, _ := settings["defaultModel"].(string)
		if provider != "" && model != "" {
			def = provider + "/" + model
		}
	}
	catalog := []models.CatalogProvider{}
	if raw, err := s.cfg.Read("models-store.json"); err == nil {
		catalog = models.Catalog(raw)
	}
	mcps := s.mcpRows(root)
	skills := resources.Skills(s.agentDir, homeDir(), root)
	agents := resources.Agents(s.agentDir, root, disabledBuiltins(s.agentDir))
	trusted := s.cfg.TrustedDirs()
	s.cx.Post(func() {
		s.providers.Set(providers)
		s.catalog.Set(catalog)
		s.modelDefault.Set(def)
		s.mcpItems.Set(mcps)
		s.skillsList.Set(skills)
		s.agentsList.Set(agents)
		s.trustDirs.Set(trusted)
	})
}

// mcpRows reads global and project server entries into one list.
func (s *appState) mcpRows(root string) []mcpItem {
	var out []mcpItem
	add := func(raw map[string]any, scope string) {
		servers, _ := raw["mcpServers"].(map[string]any)
		for name, v := range servers {
			entry, _ := v.(map[string]any)
			item := mcpItem{Name: name, Scope: scope, Transport: "stdio", Enabled: true}
			if enabled, ok := entry["enabled"].(bool); ok && !enabled {
				item.Enabled = false
			}
			if url, _ := entry["url"].(string); url != "" {
				item.Transport, item.Target = "http", url
			} else if cmd, _ := entry["command"].(string); cmd != "" {
				target := cmd
				if args, ok := entry["args"].([]any); ok {
					for _, a := range args {
						if s, ok := a.(string); ok {
							target += " " + s
						}
					}
				}
				item.Target = target
			}
			out = append(out, item)
		}
	}
	if raw, err := s.cfg.MCP(); err == nil {
		add(raw, "全局")
	}
	if root != "" {
		if raw, err := s.cfg.ProjectMCP(root); err == nil {
			add(raw, "项目")
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// beginProvider opens the provider form, empty for a new one.
func (s *appState) beginProvider(p *models.Provider) {
	s.providerPreset.Set("")
	s.providerEditID.Set("")
	s.providerName.Set("")
	s.providerBase.Set("")
	s.providerAPI.Set("")
	s.providerKey.Set("")
	s.providerModels.Set("")
	if p != nil {
		s.providerEditID.Set(p.ID)
		s.providerName.Set(p.Name)
		s.providerBase.Set(p.BaseURL)
		s.providerAPI.Set(p.API)
		s.providerKey.Set(p.APIKey)
		s.providerModels.Set(strings.Join(p.ModelIDs(), "\n"))
	}
	s.providerOpen.Set(true)
}

// applyProviderPreset fills the form from a catalog provider.
func (s *appState) applyProviderPreset(id string) {
	s.providerPreset.Set(id)
	for _, p := range s.catalog.GetUntracked() {
		if p.ID != id {
			continue
		}
		s.providerEditID.Set(p.ID)
		s.providerName.Set(p.ID)
		s.providerBase.Set(p.BaseURL)
		s.providerAPI.Set(p.API)
		ids := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			ids = append(ids, m.ID)
		}
		s.providerModels.Set(strings.Join(ids, "\n"))
		return
	}
}

// addCatalogProvider adds one of pi's providers to models.json. Only the
// endpoint is written, never a model list: pi's own catalog already ships the
// models for a provider it knows, and a sparse override list would replace
// them with entries that have no context window or price. A provider that is
// only in pi's store (a community one) carries its models with it.
func (s *appState) addCatalogProvider(id string) {
	in := models.Input{ID: id, Name: id}
	hint := "填入 API Key 后可用"
	if p, ok := models.Builtin(id); ok {
		in.Name, in.BaseURL, in.API = p.Name, p.BaseURL, p.API
		if p.OAuth {
			hint = "订阅类：在终端里运行 pi login 登录"
		}
	} else {
		found := false
		for _, p := range s.catalog.GetUntracked() {
			if p.ID != id {
				continue
			}
			in.BaseURL, in.API = p.BaseURL, p.API
			for _, m := range p.Models {
				in.ModelNames = map[string]string{}
				in.Models = append(in.Models, m.ID)
				in.ModelNames[m.ID] = m.Name
			}
			found = true
			break
		}
		if !found {
			return
		}
	}
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			if err := models.Upsert(raw, in); err != nil {
				return err
			}
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "添加提供商失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已添加提供商", in.Name+" · "+hint)
			s.catalogOpen.Set(false)
			s.selectProvider(id)
			s.refreshResources()
		})
	}()
}

// saveProviderForm writes one provider into models.json.
func (s *appState) saveProviderForm() {
	in := models.Input{
		ID:         strings.TrimSpace(s.providerEditID.GetUntracked()),
		Name:       s.providerName.GetUntracked(),
		BaseURL:    s.providerBase.GetUntracked(),
		API:        s.providerAPI.GetUntracked(),
		APIKey:     s.providerKey.GetUntracked(),
		ModelNames: map[string]string{},
	}
	for _, line := range strings.Split(s.providerModels.GetUntracked(), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			in.Models = append(in.Models, id)
		}
	}
	s.providerOpen.Set(false)
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			if err := models.Upsert(raw, in); err != nil {
				return err
			}
			if err := s.cfg.SetModels(raw); err != nil {
				return err
			}
			return nil
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "保存提供商失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已保存提供商", in.ID)
			s.refreshResources()
			go s.refreshSettings()
		})
	}()
}

func (s *appState) removeProvider(id string) {
	if id == "" {
		return
	}
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			models.Delete(raw, id)
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "删除提供商失败", err.Error())
				return
			}
			s.notify(ui.ToastInfo, "已删除提供商", id)
			s.refreshResources()
		})
	}()
}

// setDefaultModel saves "provider/model" as the default for new sessions.
func (s *appState) setDefaultModel(key string) {
	s.modelDefault.Set(key)
	provider, model, ok := strings.Cut(key, "/")
	go func() {
		if !ok || key == "" {
			_ = s.cfg.RemoveSetting("defaultProvider")
			_ = s.cfg.RemoveSetting("defaultModel")
			s.cx.Post(func() { s.notify(ui.ToastInfo, "已跟随 pi 的默认模型", "") })
			return
		}
		_ = s.cfg.SetSetting("defaultProvider", provider)
		_ = s.cfg.SetSetting("defaultModel", model)
		s.cx.Post(func() { s.notify(ui.ToastInfo, "默认模型已更新", key) })
	}()
}

// beginMCP opens the add-server form.
func (s *appState) beginMCP() {
	s.mcpName.Set("")
	s.mcpTransport.Set("stdio")
	s.mcpCommand.Set("")
	s.mcpArgs.Set("")
	s.mcpURL.Set("")
	s.mcpHeaders.Set("")
	s.mcpProject.Set(false)
	s.mcpOpen.Set(true)
}

// saveMCPForm adds a server to the global or the project's mcp.json.
func (s *appState) saveMCPForm() {
	name := strings.TrimSpace(s.mcpName.GetUntracked())
	transport := s.mcpTransport.GetUntracked()
	project := s.mcpProject.GetUntracked()
	root := s.projectRoot()
	s.mcpOpen.Set(false)
	if name == "" || (transport == "stdio" && strings.TrimSpace(s.mcpCommand.GetUntracked()) == "") ||
		(transport == "http" && strings.TrimSpace(s.mcpURL.GetUntracked()) == "") {
		s.notify(ui.ToastWarning, "MCP 服务器信息不完整", "需要名称和命令或 URL")
		return
	}
	entry := map[string]any{}
	if transport == "http" {
		entry["url"] = strings.TrimSpace(s.mcpURL.GetUntracked())
		if headers := parseHeaderLines(s.mcpHeaders.GetUntracked()); len(headers) > 0 {
			entry["headers"] = headers
		}
	} else {
		entry["command"] = strings.TrimSpace(s.mcpCommand.GetUntracked())
		if args := strings.Fields(s.mcpArgs.GetUntracked()); len(args) > 0 {
			list := make([]any, 0, len(args))
			for _, a := range args {
				list = append(list, a)
			}
			entry["args"] = list
		}
	}
	go func() {
		err := func() error {
			var raw map[string]any
			var err error
			if project && root != "" {
				raw, err = s.cfg.ProjectMCP(root)
			} else {
				raw, err = s.cfg.MCP()
			}
			if err != nil {
				return err
			}
			servers, _ := raw["mcpServers"].(map[string]any)
			if servers == nil {
				servers = map[string]any{}
				raw["mcpServers"] = servers
			}
			servers[name] = entry
			if project && root != "" {
				return s.cfg.SetProjectMCP(root, raw)
			}
			return s.cfg.SetMCP(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "添加 MCP 服务器失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已添加 MCP 服务器", name)
			s.refreshResources()
		})
	}()
}

func parseHeaderLines(text string) map[string]any {
	out := map[string]any{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

// mutateMCP applies change to the server's file, global or project.
func (s *appState) mutateMCP(item mcpItem, change func(servers map[string]any)) {
	root := s.projectRoot()
	project := item.Scope == "项目" && root != ""
	go func() {
		err := func() error {
			var raw map[string]any
			var err error
			if project {
				raw, err = s.cfg.ProjectMCP(root)
			} else {
				raw, err = s.cfg.MCP()
			}
			if err != nil {
				return err
			}
			servers, _ := raw["mcpServers"].(map[string]any)
			if servers == nil {
				servers = map[string]any{}
				raw["mcpServers"] = servers
			}
			change(servers)
			if project {
				return s.cfg.SetProjectMCP(root, raw)
			}
			return s.cfg.SetMCP(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "更新 MCP 失败", err.Error())
				return
			}
			s.refreshResources()
		})
	}()
}

func (s *appState) toggleMCP(item mcpItem, enabled bool) {
	s.mutateMCP(item, func(servers map[string]any) {
		entry, _ := servers[item.Name].(map[string]any)
		if entry == nil {
			return
		}
		if enabled {
			delete(entry, "enabled")
		} else {
			entry["enabled"] = false
		}
	})
}

func (s *appState) removeMCP(item mcpItem) {
	s.mutateMCP(item, func(servers map[string]any) { delete(servers, item.Name) })
}

// mcpAuth signs a server in or out through pi's own OAuth store.
func (s *appState) mcpAuth(name string, logout bool) {
	verb := "login"
	if logout {
		verb = "logout"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		defer cancel()
		out, err := s.cfg.RunPi(ctx, "mcp", verb, name)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastWarning, "MCP "+verb+" 失败", err.Error())
				return
			}
			if text := strings.TrimSpace(string(out)); text != "" {
				s.notify(ui.ToastSuccess, "MCP "+verb, firstLine(text, 120))
			}
			go s.refreshSettings()
		})
	}()
}

// openSkill shows a skill's instructions read-only.
func (s *appState) openSkill(skill resources.Skill) {
	go func() {
		body, err := resources.ReadSkill(skill.Path)
		s.cx.Post(func() {
			if err != nil {
				s.status.Set("读取技能失败：" + err.Error())
				return
			}
			s.skillName.Set(skill.Name)
			s.skillPath.Set(skill.Path)
			s.skillBody.Set(body)
			s.skillOpen.Set(true)
		})
	}()
}

// saveSkillForm writes the edited SKILL.md back. The frontmatter is the
// user's: the switch changes disable-model-invocation on its own, and this
// saved whatever else was typed around it.
func (s *appState) saveSkillForm() {
	path := s.skillPath.GetUntracked()
	body := s.skillBody.GetUntracked()
	if path == "" {
		s.skillOpen.Set(false)
		return
	}
	go func() {
		err := resources.SaveSkill(path, body)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "保存技能失败", err.Error())
				return
			}
			s.skillOpen.Set(false)
			s.notify(ui.ToastSuccess, "已保存技能", s.skillName.GetUntracked())
			s.refreshResources()
		})
	}()
}

func (s *appState) toggleSkillDormant(skill resources.Skill, dormant bool) {
	go func() {
		err := resources.SetDormant(skill.Path, dormant)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "更新技能失败", err.Error())
				return
			}
			s.refreshResources()
		})
	}()
}

// openAgent edits an agent profile; state.agentPath empty means a new one.
func (s *appState) openAgent(agent *resources.Agent) {
	s.agentPath.Set("")
	s.agentTitle.Set("")
	s.agentBody.Set("")
	if agent != nil {
		body, err := resources.ReadAgent(agent.Path)
		if err != nil {
			s.status.Set("读取 Agent 失败：" + err.Error())
			return
		}
		s.agentPath.Set(agent.Path)
		s.agentTitle.Set(agent.Name)
		s.agentBody.Set(body)
	} else {
		s.agentTitle.Set("")
		s.agentBody.Set("---\nname: 新 Agent\ndescription: \n---\n\n")
	}
	s.agentOpen.Set(true)
}

// toggleBuiltinAgent switches one of pi's own subagent profiles off or on.
// pi reads the names from agents/settings.json's disabledBuiltIns and needs a
// session reload for the Agent tool to follow.
func (s *appState) toggleBuiltinAgent(name string, on bool) {
	s.writeSubagentSettings(func(raw map[string]any) {
		keep := make([]any, 0, 4)
		if list, ok := raw["disabledBuiltIns"].([]any); ok {
			for _, v := range list {
				if existing, ok := v.(string); ok && strings.EqualFold(existing, name) {
					continue
				}
				keep = append(keep, v)
			}
		}
		if !on {
			keep = append(keep, name)
		}
		raw["disabledBuiltIns"] = keep
	})
	s.notify(ui.ToastInfo, "内置子代理已更新", "重连会话后生效")
}

// viewBuiltinAgent shows one of pi's profiles read-only: the system prompt it
// runs with and the tools it may call. There is no file to edit.
func (s *appState) viewBuiltinAgent(a resources.Agent) {
	s.agentBuiltin.Set(true)
	s.agentPath.Set("")
	s.agentTitle.Set(a.Display)
	s.agentBody.Set("---\nname: " + a.Name + "\ntools: " + strings.Join(a.Tools, ", ") + "\ndescription: " + a.Description +
		"\n---\n\n" + a.Prompt + "\n")
	s.agentOpen.Set(true)
}

// saveAgentForm writes the edited profile, naming a new file after its title.
func (s *appState) saveAgentForm() {
	path := s.agentPath.GetUntracked()
	title := strings.TrimSpace(s.agentTitle.GetUntracked())
	body := s.agentBody.GetUntracked()
	s.agentOpen.Set(false)
	if path == "" {
		if title == "" {
			s.notify(ui.ToastWarning, "Agent 需要名字", "")
			return
		}
		path = filepath.Join(s.agentDir, "agents", safeFileName(title)+".md")
		body = setAgentFrontmatterName(body, title)
	}
	go func() {
		err := resources.SaveAgent(path, body)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "保存 Agent 失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已保存 Agent", title)
			s.refreshResources()
			go s.refreshSettings()
		})
	}()
}

func (s *appState) removeAgent(agent resources.Agent) {
	if agent.Path == "" {
		return
	}
	go func() {
		err := resources.DeleteAgent(agent.Path)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "删除 Agent 失败", err.Error())
				return
			}
			s.notify(ui.ToastInfo, "已删除 Agent", agent.Name)
			s.refreshResources()
		})
	}()
}

// setAgentFrontmatterName makes a new profile's frontmatter match the name
// the user typed.
func setAgentFrontmatterName(body, name string) string {
	lines := strings.Split(body, "\n")
	inHead := false
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if !inHead {
				inHead = true
				continue
			}
			break
		}
		if inHead && strings.HasPrefix(strings.TrimSpace(line), "name:") {
			lines[i] = "name: " + name
			found = true
			break
		}
	}
	if !inHead {
		return "---\nname: " + name + "\n---\n\n" + body
	}
	if !found {
		lines = append(lines[:1], append([]string{"name: " + name}, lines[1:]...)...)
	}
	return strings.Join(lines, "\n")
}

func safeFileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "agent"
	}
	return out
}

func (s *appState) untrustDir(dir string) {
	go func() {
		err := s.cfg.SetTrusted(dir, false)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "移除信任失败", err.Error())
				return
			}
			s.refreshResources()
			s.notify(ui.ToastInfo, "已取消信任", dir)
		})
	}()
}

// trustCurrentProject trusts the project and closes the gate.
func (s *appState) trustCurrentProject() {
	dir := s.projectRoot()
	if s.pendingTrust != "" {
		dir = s.pendingTrust
	}
	s.pendingTrust = ""
	s.trustOpen.Set(false)
	if dir == "" {
		return
	}
	go func() {
		err := s.cfg.SetTrusted(dir, true)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "信任失败", err.Error())
				return
			}
			s.refreshResources()
			s.notify(ui.ToastSuccess, "已信任项目", dir)
			if path := s.pendingTrustAction; path != "" {
				s.pendingTrustAction = ""
				s.openSession(path)
				return
			}
		})
	}()
}

// ensureTrust shows the trust gate when a project ships its own pi config and
// the user has not trusted it yet.
func (s *appState) ensureTrust() bool {
	// Headless tests run no project code; the gate can be forced on there to
	// exercise the dialog.
	if os.Getenv("PIWEB_NATIVE_NO_AGENT") == "1" && os.Getenv("PIWEB_NATIVE_TRUST_GATE") != "1" {
		return true
	}
	dir := s.projectRoot()
	if dir == "" || !requiresTrust(dir) || s.cfg.IsTrusted(dir) {
		return true
	}
	s.trustOpen.Set(true)
	return false
}

// ensureTrustFor gates one directory (the session being opened may record a
// different project than the window's). When it opens the gate it remembers
// the session to open after the user trusts it.
func (s *appState) ensureTrustFor(dir, openAfter string) bool {
	if dir == "" || !requiresTrust(dir) || s.cfg.IsTrusted(dir) {
		return true
	}
	s.pendingTrust = dir
	s.pendingTrustAction = openAfter
	s.trustOpen.Set(true)
	return false
}

// requiresTrust mirrors pi's rule: a project .pi directory holding any of
// these files or folders runs project-controlled code and prompts.
func requiresTrust(dir string) bool {
	for _, name := range []string{"settings.json", "mcp.json", "extensions", "skills", "prompts", "themes", "SYSTEM.md", "APPEND_SYSTEM.md"} {
		if _, err := os.Stat(filepath.Join(dir, ".pi", name)); err == nil {
			return true
		}
	}
	return false
}

// applyAppearance shows the whole window light, dark or as the system is.
func (s *appState) applyAppearance(value string) {
	switch value {
	case "light":
		s.cx.SetSubtreeAppearance(core.AppearanceLight)
	case "dark":
		s.cx.SetSubtreeAppearance(core.AppearanceDark)
	default:
		value = "system"
		s.cx.SetSubtreeAppearance(core.AppearanceSystem)
	}
	s.appearance.Set(value)
}

// setAppearance applies and remembers an appearance choice.
func (s *appState) setAppearance(value string) {
	s.applyAppearance(value)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// disabledBuiltins is the list of pi's own subagent profiles switched off in
// agents/settings.json, as the panel shows them.
func disabledBuiltins(agentDir string) []string {
	data, err := os.ReadFile(filepath.Join(agentDir, "agents", "settings.json"))
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	list, _ := raw["disabledBuiltIns"].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		if name, ok := v.(string); ok && strings.TrimSpace(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// subagentSettings reads ~/.pi/agent/agents/settings.json, pi-web's
// built-in subagent switch and concurrency cap.
func (s *appState) subagentSettings() (enabled bool, maxConcurrent int) {
	maxConcurrent = 10
	path := filepath.Join(s.agentDir, "agents", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return true, maxConcurrent
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return true, maxConcurrent
	}
	if v, ok := raw["builtInEnabled"].(bool); ok {
		enabled = v
	} else {
		enabled = true
	}
	if v, ok := raw["maxConcurrent"].(float64); ok && v >= 1 && v <= 32 {
		maxConcurrent = int(v)
	}
	return enabled, maxConcurrent
}

// writeSubagentSettings merges one change into the file, keeping the keys it
// does not edit (disabledBuiltIns, version).
func (s *appState) writeSubagentSettings(mutate func(map[string]any)) {
	path := filepath.Join(s.agentDir, "agents", "settings.json")
	raw := map[string]any{"version": 1, "builtInEnabled": true}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	mutate(raw)
	if _, ok := raw["version"]; !ok {
		raw["version"] = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.notify(ui.ToastWarning, "保存子代理设置失败", err.Error())
		return
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		s.notify(ui.ToastWarning, "保存子代理设置失败", err.Error())
		return
	}
	s.settingsRev.Set(s.settingsRev.GetUntracked() + 1)
}
