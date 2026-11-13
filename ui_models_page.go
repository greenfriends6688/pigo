package main

import (
	"fmt"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"piwebnative/internal/models"
)

// modelsMasterDetail is the models page's provider browser: the providers on
// the left with their add button, the chosen one's key and models on the
// right, as PI NEXT's model page is laid out. A flat list of provider rows
// made every provider's key and switches a dialog away.
func (s *appState) modelsMasterDetail() core.Node {
	return core.View{Style: core.Style{Gap: 6}, Children: []core.Node{
		s.modelsUsageLine(),
		core.View{TestID: "providers", Style: core.Style{Direction: core.Row, Height: core.Pt(430),
			MinWidth: core.Pt(0), Gap: 0, BorderWidth: 1, CornerRadius: radiusCard}, StyleFunc: func() core.Style {
			return core.Style{BorderColor: s.colors.Border()}
		}, Children: []core.Node{
			s.providerListPane(),
			core.View{Style: core.Style{Width: core.Pt(1), Background: s.colors.Border()}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
				Children: []core.Node{s.providerDetailPane()}},
		}},
	}}
}

// modelsUsageLine is what this session has spent: get_session_stats' tokens
// and cost. A provider's own balance lives behind its login (pi has no RPC for
// it), so the line says where the number comes from instead of inventing one.
func (s *appState) modelsUsageLine() core.Node {
	return core.Text{TestID: "models-usage", ContentFunc: func() string {
		line := s.statsLine.Get()
		if line == "" {
			return "本会话用量：暂无（连接会话后由 get_session_stats 报告）"
		}
		return "本会话用量：" + line
	}, MaxLines: 1, Style: core.Style{FontSize: fontMeta, FontFamily: core.FontMonospace, Color: s.colors.Muted()}}
}

// providerListPane is the left column: one row per configured provider and
// the button that opens the catalog.
func (s *appState) providerListPane() core.Node {
	return core.View{Style: core.Style{Width: core.Pt(200), FlexShrink: 0, MinHeight: core.Pt(0), Padding: core.All(6), Gap: 4},
		Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					providers := s.providers.Get()
					selected := s.providerSel.Get()
					if len(providers) == 0 {
						return core.Text{Content: "models.json 里还没有提供商", TestID: "providers-empty", MaxLines: 3,
							Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted(), Padding: core.All(6)}}
					}
					rows := make([]core.Node, 0, len(providers))
					for _, p := range providers {
						provider := p
						on := provider.ID == selected
						subtitle := fmt.Sprintf("%d 个模型", providerModelCount(provider.ID, len(provider.Models)))
						if provider.HasOAuth || hasBuiltin(provider.ID) {
							subtitle += " · pi 内置"
						}
						textStyle := core.Style{FontSize: fontLabel, Color: s.colors.Text()}
						rowStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
							Padding: core.Symmetric(6, 8), CornerRadius: radiusInline, MinWidth: core.Pt(0)}
						if on {
							textStyle.FontWeight = core.FontWeightSemibold
							rowStyle.Background = ui.Alpha(s.colors.Accent(), 0.12)
						}
						rows = append(rows, core.View{
							Role: core.RoleButton, Label: provider.Name, TestID: "provider-row-" + provider.ID,
							OnPress: func(core.PressEvent) { s.selectProvider(provider.ID) },
							Style:   rowStyle,
							StyleFunc: func() core.Style {
								return core.Style{Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.06)}}
							},
							Children: []core.Node{
								ui.Icon(lucide.Cpu, 13, s.colors.Muted),
								core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
									core.Text{Content: provider.Name, MaxLines: 1, Style: textStyle},
									core.Text{Content: subtitle, MaxLines: 1,
										Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
								}},
							},
						})
					}
					return core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)},
						Children: []core.Node{core.View{Style: core.Style{Gap: 1}, Children: rows}}}
				}),
			}},
			ui.Button{Text: "添加 Provider", Icon: lucide.Plus, Variant: ui.ButtonGhost, Size: ui.SizeSmall,
				TestID: "provider-add", OnPress: func() {
					s.catalogQuery.Set("")
					s.catalogOpen.Set(true)
				}},
		}}
}

// selectProvider shows a provider's detail and puts its key in the field's
// head space: the draft starts empty, as a replacement would.
func (s *appState) selectProvider(id string) {
	s.providerSel.Set(id)
	s.providerKeyDraft.Set("")
}

// pickProvider is the master-detail's provider: the chosen one, or the first
// when nothing is chosen or the choice is gone.
func pickProvider(providers []models.Provider, want string) *models.Provider {
	if len(providers) == 0 {
		return nil
	}
	for i := range providers {
		if providers[i].ID == want {
			p := providers[i]
			return &p
		}
	}
	p := providers[0]
	return &p
}

// providerDetailPane is the right column: the key, the switches and the
// provider's own actions.
func (s *appState) providerDetailPane() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		// Tracked reads: the pane must follow a provider that arrives after
		// the first render, as the one just added from the catalog does.
		provider := pickProvider(s.providers.Get(), s.providerSel.Get())
		if provider == nil {
			return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter,
				Padding: core.All(20), Gap: 8}, Children: []core.Node{
				core.Text{Content: "还没有提供商", Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
				core.Text{Content: "点「添加 Provider」从 pi 的目录里选一个，或填一个自定义端点。", MaxLines: 2,
					Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted(), TextAlign: core.TextAlignCenter}},
				ui.Button{Text: "添加 Provider", Variant: ui.ButtonSecondary, TestID: "provider-empty-add",
					OnPress: func() { s.catalogOpen.Set(true) }},
			}}
		}
		p := *provider
		return core.View{TestID: "provider-detail", Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0),
			MinHeight: core.Pt(0), Padding: core.All(12), Gap: 10}, Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
					core.Text{Content: p.Name, MaxLines: 1,
						Style: core.Style{FontSize: fontTitle, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
					core.Text{Content: providerSubtitle(p), MaxLines: 2,
						Style: core.Style{FontSize: fontMeta, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
				}},
				ui.Button{Text: "编辑", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "provider-edit-" + p.ID,
					OnPress: func() { s.beginProvider(&p) }},
				ui.Button{Text: "删除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "provider-delete-" + p.ID,
					OnPress: func() { s.removeProvider(p.ID) }},
			}},
			s.providerKeyRow(p),
			s.providerModelsSection(p),
		}}
	})
}

// providerSubtitle is the provider's endpoint and API type, or what it lacks.
func providerSubtitle(p models.Provider) string {
	parts := make([]string, 0, 2)
	if p.BaseURL != "" {
		parts = append(parts, p.BaseURL)
	}
	if p.API != "" {
		parts = append(parts, p.API)
	}
	if len(parts) == 0 {
		return "没有 baseUrl"
	}
	return strings.Join(parts, " · ")
}

// providerKeyRow is the API key: its state, a replacement field and the two
// actions. The stored key is never shown, only whether one is set.
func (s *appState) providerKeyRow(p models.Provider) core.Node {
	configured := strings.TrimSpace(p.APIKey) != "" || p.HasOAuth
	state := "未配置"
	color := s.colors.Faint()
	if configured {
		state = "已配置"
		color = s.colors.Up()
	}
	return core.View{Style: core.Style{Gap: 4}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
			core.Text{Content: "API KEY", MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.3, Color: s.colors.Muted()}},
			core.View{Style: core.Style{Width: core.Pt(6), Height: core.Pt(6), CornerRadius: 3, Background: color}},
			core.Text{Content: state, TestID: "provider-key-state", MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: color}},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.Button{Text: "清除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "provider-key-clear",
				OnPress: func() { s.clearProviderKey(p.ID) }},
		}},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				ui.Input{Value: s.providerKeyDraft.Get, OnChange: s.providerKeyDraft.Set, Masked: true,
					Placeholder: "输入新的 API Key 以替换…", TestID: "provider-key",
					Style: core.Style{Width: core.Pct(100), Height: core.Pt(28)}},
			}},
			ui.Button{Text: "保存", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "provider-key-save",
				OnPress: func() { s.saveProviderKey(p.ID) }},
		}},
	}}
}

// hasBuiltin reports a provider pi itself ships.
func hasBuiltin(id string) bool {
	_, ok := models.Builtin(id)
	return ok
}

// builtinModels is pi's own model list for a provider.
func builtinModels(id string) []models.BuiltinModel { return models.BuiltinModels(id) }

// providerModelCount says how many models a provider shows: the models.json
// entries when it has any, else what pi's own catalog ships for it.
func providerModelCount(id string, own int) int {
	if own > 0 {
		return own
	}
	return len(builtinModels(id))
}

// providerModelList is the provider's models for the row list: models.json's
// when it has entries, else pi's built-in ones as modelItem values.
func providerModelList(p models.Provider) []modelItem {
	if len(p.Models) > 0 {
		out := make([]modelItem, 0, len(p.Models))
		for _, m := range p.Models {
			out = append(out, modelItem{ID: m.ID, Name: m.Name, Provider: p.ID, Reasoning: m.Reasoning})
		}
		return out
	}
	out := make([]modelItem, 0, len(builtinModels(p.ID)))
	for _, m := range builtinModels(p.ID) {
		out = append(out, modelItem{ID: m.ID, Name: m.Name, Provider: p.ID, Reasoning: m.Reasoning})
	}
	return out
}

// providerModelsSection is the model list: the enable switches pi reads from
// settings.json, and the actions that add or edit one in models.json.
func (s *appState) providerModelsSection(p models.Provider) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Gap: 4},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
				core.Text{Content: "可用模型", MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.3, Color: s.colors.Muted()}},
				core.Text{ContentFunc: func() string {
					patterns := s.enabledPatterns.Get()
					list := providerModelList(p)
					ids := make([]string, 0, len(list))
					for _, m := range list {
						ids = append(ids, m.ID)
					}
					cover := len(ids)
					if len(patterns) > 0 {
						cover = models.ProviderCovered(patterns, p.ID, ids)
					}
					return fmt.Sprintf("%d / %d", cover, len(list))
				}, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
				core.View{Style: core.Style{FlexGrow: 1}},
				ui.Button{Text: "刷新模型目录", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "models-refresh",
					OnPress: s.refreshModelCatalog},
				ui.Button{Text: "全部开启", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "provider-models-on",
					OnPress: func() { s.setProviderEnabled(p.ID, providerModelIDs(p), true, s.availableModelRefs()) }},
				ui.Button{Text: "全部关闭", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "provider-models-off",
					OnPress: func() { s.setProviderEnabled(p.ID, providerModelIDs(p), false, s.availableModelRefs()) }},
				ui.Button{Text: "添加模型", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "model-add",
					OnPress: func() { s.openModels(p.ID); s.beginModel(nil) }},
			}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					list := providerModelList(p)
					if len(list) == 0 {
						return core.Text{Content: "还没有模型，点「添加模型」填入 id、上下文窗口与成本。",
							TestID: "model-empty", MaxLines: 2,
							Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
					}
					patterns := s.enabledPatterns.Get()
					refs := s.availableModelRefs()
					rows := make([]core.Node, 0, len(list))
					for _, m := range list {
						model := m
						provider := p.ID
						meta := []string{model.ID}
						if model.Reasoning {
							meta = append(meta, "推理")
						}
						rows = append(rows, core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
							Gap: 8, Padding: core.Edges{Top: 5, Right: 4, Bottom: 5, Left: 4}, MinWidth: core.Pt(0)},
							Children: []core.Node{
								core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
									core.Text{Content: model.Name, MaxLines: 1,
										Style: core.Style{FontSize: fontLabel, Color: s.colors.Text()}},
									core.Text{Content: strings.Join(meta, " · "), MaxLines: 1,
										Style: core.Style{FontSize: fontMeta, FontFamily: core.FontMonospace, Color: s.colors.Faint()}},
								}},
								ui.Button{Text: "编辑", Variant: ui.ButtonGhost, Size: ui.SizeSmall,
									TestID: "model-edit-" + model.ID,
									OnPress: func() {
										s.openModels(provider)
										s.beginModel(editableModel(p, model.ID))
									}},
								ui.Switch{Checked: func() bool { return models.EnabledFor(patterns, provider, model.ID) },
									TestID: "enabled-model-" + provider + "-" + model.ID,
									OnChange: func(on bool) {
										s.setModelEnabled(provider, model.ID, on, refs)
									}},
							}})
					}
					return core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)},
						Children: []core.Node{core.View{Style: core.Style{Gap: 1}, Children: rows}}}
				}),
			}},
		}}
}

// editableModel is the model the form opens on: the models.json entry when
// there is one, else pi's built-in entry prefilled, so "编辑" on a built-in
// starts from its real context window and capabilities and saves an override.
func editableModel(provider models.Provider, id string) *models.Model {
	for _, m := range provider.Models {
		if m.ID == id {
			model := m
			return &model
		}
	}
	for _, m := range models.BuiltinModels(provider.ID) {
		if m.ID == id {
			return &models.Model{ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow,
				MaxTokens: m.MaxTokens, Reasoning: m.Reasoning}
		}
	}
	return nil
}

// refreshModelCatalog re-reads pi's model store and, when a session is live,
// asks pi for its list again, so a model added or renamed upstream shows
// without restarting the app.
func (s *appState) refreshModelCatalog() {
	go s.refreshSettings()
	if h := s.current(); h != nil {
		go s.fetchCatalog(h)
	}
	s.notify(ui.ToastInfo, "正在刷新模型目录", "重新读取 models-store.json 与会话的模型列表")
}

// providerModelIDs is the ids the switches act on: models.json's when it lists
// any, else pi's built-in list for that provider.
func providerModelIDs(p models.Provider) []string {
	list := providerModelList(p)
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, m.ID)
	}
	return out
}

// saveProviderKey writes the typed key into models.json with the provider's
// model list left alone.
func (s *appState) saveProviderKey(provider string) {
	key := strings.TrimSpace(s.providerKeyDraft.GetUntracked())
	if key == "" {
		s.notify(ui.ToastWarning, "没有新的 API Key", "先填入一个 Key 再保存")
		return
	}
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			if err := models.Upsert(raw, models.Input{ID: provider, APIKey: key}); err != nil {
				return err
			}
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "保存 API Key 失败", err.Error())
				return
			}
			s.providerKeyDraft.Set("")
			s.notify(ui.ToastSuccess, "已保存 API Key", provider)
			s.refreshResources()
		})
	}()
}

// clearProviderKey removes the stored key, leaving the provider and its
// models in place.
func (s *appState) clearProviderKey(provider string) {
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			models.DeleteAPIKey(raw, provider)
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "清除 API Key 失败", err.Error())
				return
			}
			s.notify(ui.ToastInfo, "已清除 API Key", provider)
			s.refreshResources()
		})
	}()
}

// providerCatalogDialog is the "添加 Provider" picker: pi's own provider
// catalog as a searchable grid, grouped the way PI NEXT groups it — the ones
// that sign in with a subscription first, then the ones that take an API key.
// It used to list only the providers already in models-store.json, so a
// machine with three of them offered three.
func (s *appState) providerCatalogDialog() core.Node {
	return ui.Dialog{
		Open:         s.catalogOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.catalogOpen.Set(open) },
		Title:        "添加 Provider",
		Description:  "pi 内置的提供商目录。添加后填入 API Key；订阅类用终端里的 pi login 登录。",
		Style:        core.Style{Width: core.Pt(880), Height: core.Pt(620)},
		Children: []core.Node{
			ui.Input{Value: s.catalogQuery.Get, OnChange: s.catalogQuery.Set, Placeholder: "搜索 Provider…",
				TestID: "catalog-search", Style: core.Style{Width: core.Pct(100), Height: core.Pt(30)}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					query := strings.ToLower(strings.TrimSpace(s.catalogQuery.Get()))
					s.catalog.Get() // reps paint when pi's store lands
					configured := map[string]bool{}
					for _, p := range s.providers.Get() {
						configured[p.ID] = true
					}
					let := func(p models.BuiltinProvider) bool {
						return query == "" || strings.Contains(strings.ToLower(p.ID), query) ||
							strings.Contains(strings.ToLower(p.Name), query)
					}
					var children []core.Node
					add := func(title string, list []models.BuiltinProvider) {
						shown := make([]core.Node, 0, len(list))
						for _, p := range list {
							if !let(p) {
								continue
							}
							shown = append(shown, s.catalogCard(p, configured[p.ID]))
						}
						if len(shown) == 0 {
							return
						}
						children = append(children, core.Text{Content: title, MaxLines: 1,
							Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, LetterSpacing: 0.3,
								Color: s.colors.Muted(), Padding: core.Edges{Top: 8, Bottom: 2}}})
						children = append(children, core.View{Style: core.Style{Display: core.DisplayGrid,
							GridTemplateColumns: "repeat(3, minmax(0, 1fr))", Gap: 8}, Children: shown})
					}
					var oauth, apiKey []models.BuiltinProvider
					for _, p := range models.BuiltinProviders {
						if p.OAuth {
							oauth = append(oauth, p)
							continue
						}
						apiKey = append(apiKey, p)
					}
					add("订阅服务", oauth)
					add("API KEY", apiKey)
					// Providers pi's store knows and its bundled catalog does
					// not (a community provider) keep their models with them,
					// so they are added from there.
					var extras []models.BuiltinProvider
					for _, p := range s.catalog.Get() {
						if hasBuiltin(p.ID) {
							continue
						}
						builtin := models.BuiltinProvider{ID: p.ID, Name: p.ID, BaseURL: p.BaseURL, API: p.API}
						for _, m := range p.Models {
							builtin.Models = append(builtin.Models, models.BuiltinModel{ID: m.ID, Name: m.Name,
								ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, Reasoning: m.Reasoning})
						}
						extras = append(extras, builtin)
					}
					add("其它（pi 目录）", extras)
					if len(children) == 0 {
						return core.Text{Content: "目录里没有匹配的 Provider", TestID: "catalog-empty",
							Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted(), Padding: core.All(10)}}
					}
					return core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)},
						Children: []core.Node{core.View{Style: core.Style{Gap: 6, Padding: core.All(2)}, Children: children}}}
				}),
			}},
		},
		Footer: []core.Node{
			ui.Button{Text: "自定义端点…", Variant: ui.ButtonGhost, TestID: "catalog-custom", OnPress: func() {
				s.catalogOpen.Set(false)
				s.beginProvider(nil)
			}},
			ui.Button{Text: "关闭", TestID: "catalog-close", OnPress: func() { s.catalogOpen.Set(false) }},
		},
		TestID: "provider-catalog",
	}
}

// catalogCard is one provider of the grid: its name, its model count and a
// tick once it is configured.
func (s *appState) catalogCard(p models.BuiltinProvider, configured bool) core.Node {
	detail := fmt.Sprintf("%d 个模型", len(p.Models))
	if configured {
		detail = "已添加 · " + detail
	} else if p.OAuth {
		detail += " · 订阅"
	}
	children := []core.Node{
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Text{Content: p.Name, MaxLines: 1,
				Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
			core.Text{Content: detail, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
		}},
	}
	if configured {
		children = append(children, ui.Icon(lucide.Check, 14, s.colors.Up))
	} else {
		children = append(children, ui.Icon(lucide.Plus, 14, s.colors.Muted))
	}
	return core.View{
		Role: core.RoleButton, Label: p.ID, TestID: "catalog-" + p.ID,
		OnPress: func(core.PressEvent) {
			if configured {
				s.selectProvider(p.ID)
				s.catalogOpen.Set(false)
				return
			}
			s.addCatalogProvider(p.ID)
		},
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Height: core.Pt(52),
			Padding: core.Symmetric(0, 10), CornerRadius: radiusCard, Background: s.colors.Field(),
			BorderWidth: 1, BorderColor: s.colors.FieldBorder()},
		StyleFunc: func() core.Style {
			return core.Style{Hover: &core.Style{Background: s.colors.Hover(), BorderColor: s.colors.Border()}}
		},
		Children: children,
	}
}
