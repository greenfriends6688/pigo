package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"piwebnative/internal/models"
)

// modelDialog manages one provider's models: the list pi-web's Models panel
// shows on the left, and the detail form it shows on the right. pi's
// models.json is kept as is otherwise — keys the form does not edit survive
// every save.
func (s *appState) modelDialog() core.Node {
	return ui.Dialog{
		Open:         s.modelOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.modelOpen.Set(open) },
		Title:        "模型",
		Description:  "写入 ~/.pi/agent/models.json，保存前自动备份。",
		Style:        core.Style{Width: core.Pt(620)},
		Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
			provider := s.modelProvider()
			if provider == nil {
				return core.Text{Content: "提供商不存在", Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}
			}
			if s.modelFormOpen.Get() {
				return s.modelForm(provider)
			}
			return s.modelList(provider)
		})},
		Footer: []core.Node{
			ui.Button{Text: "关闭", TestID: "model-close", OnPress: func() { s.modelOpen.Set(false) }},
		},
		TestID: "model-dialog",
	}
}

// modelProvider is the provider the dialog is editing, nil once deleted.
func (s *appState) modelProvider() *models.Provider {
	id := s.modelProviderID.Get()
	for i := range s.providers.GetUntracked() {
		if s.providers.GetUntracked()[i].ID == id {
			p := s.providers.GetUntracked()[i]
			return &p
		}
	}
	return nil
}

// openModels shows the provider's models.
func (s *appState) openModels(providerID string) {
	s.modelProviderID.Set(providerID)
	s.modelFormOpen.Set(false)
	s.modelOpen.Set(true)
}

// modelList is the provider's models with their add, edit and delete
// actions, as pi-web's model list has.
func (s *appState) modelList(provider *models.Provider) core.Node {
	children := []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			core.Text{Content: provider.Name, MaxLines: 1,
				Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
			core.Text{Content: fmt.Sprintf("%d 个模型", len(provider.Models)), MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.Button{Text: "添加模型", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "model-add",
				OnPress: func() { s.beginModel(nil) }},
		}},
	}
	if len(provider.Models) == 0 {
		children = append(children, core.Text{Content: "还没有模型，点「添加模型」填入 id、上下文窗口与成本。",
			TestID: "model-empty", Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}})
	} else {
		rows := make([]core.Node, 0, len(provider.Models))
		for _, m := range provider.Models {
			model := m
			meta := []string{}
			if model.ContextWindow > 0 {
				meta = append(meta, "上下文 "+shortCount(int64(model.ContextWindow)))
			}
			if model.Reasoning {
				meta = append(meta, "推理")
			}
			if model.ImageInput {
				meta = append(meta, "图片")
			}
			if model.API != "" {
				meta = append(meta, model.API)
			}
			rows = append(rows, s.settingsRow(lucide.Cpu, model.Name+" · "+model.ID, strings.Join(meta, " · "),
				ui.Button{Text: "编辑", Variant: ui.ButtonSecondary, Size: ui.SizeSmall, TestID: "model-edit-" + model.ID,
					OnPress: func() { s.beginModel(&model) }},
				ui.Button{Text: "删除", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "model-delete-" + model.ID,
					OnPress: func() { s.deleteModel(model.ID) }},
			))
		}
		children = append(children, core.Scroll{Style: core.Style{MaxHeight: core.Pt(320), FlexShrink: 1},
			Children: []core.Node{core.View{Style: core.Style{Gap: 2}, Children: rows}}})
	}
	return core.View{Style: core.Style{Gap: 10, MaxHeight: core.Pt(420)}, Children: children}
}

// modelField is one labelled text field of the form.
func modelField(label, placeholder string, sig *core.Signal[string], testID string) core.Node {
	return ui.Input{Value: sig.Get, OnChange: sig.Set, Label: label, Placeholder: placeholder,
		TestID: testID, Style: core.Style{Width: core.Pct(100)}}
}

// modelForm is one model's detail, mirroring pi-web's fields: id, name,
// capabilities, context and output limits, cost per million, and the
// provider overrides. Advanced pi-only keys (headers, compat, thinking map)
// stay untouched in the file.
func (s *appState) modelForm(provider *models.Provider) core.Node {
	row := func(children ...core.Node) core.Node {
		return core.View{Style: core.Style{Direction: core.Row, Gap: 10}, Children: children}
	}
	cell := func(n core.Node) core.Node {
		return core.View{Style: core.Style{FlexGrow: 1, FlexBasis: core.Pct(0), MinWidth: core.Pt(0)}, Children: []core.Node{n}}
	}
	return core.View{Style: core.Style{Gap: 10}, Children: []core.Node{
		core.Text{Content: provider.Name + " · 模型", Style: core.Style{FontSize: fontLabel, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
		row(cell(modelField("ID *", "claude-sonnet-4", s.modelEditID, "model-id")),
			cell(modelField("名称", "显示名称", s.modelFormName, "model-name"))),
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8}, Children: []core.Node{
			ui.Button{Text: "从模型目录填充", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "model-catalog-fill",
				OnPress: s.fillModelFromCatalog},
			core.Text{Content: "按 ID 匹配 pi 的 models-store.json，填入上下文、成本与能力", MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: s.colors.Faint()}},
		}},
		core.Text{Content: "能力", Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: s.colors.Muted()}},
		row(
			core.Text{Content: "推理", Style: core.Style{FontSize: fontLabel, Color: s.colors.Text()}},
			ui.Switch{Checked: s.modelFormReasoning.Get, OnChange: s.modelFormReasoning.Set, TestID: "model-reasoning"},
			core.View{Style: core.Style{Width: core.Pt(16)}},
			core.Text{Content: "图片输入", Style: core.Style{FontSize: fontLabel, Color: s.colors.Text()}},
			ui.Switch{Checked: s.modelFormImage.Get, OnChange: s.modelFormImage.Set, TestID: "model-image"},
		),
		core.Text{Content: "规格", Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: s.colors.Muted()}},
		row(cell(modelField("上下文窗口", "200000", s.modelFormContext, "model-context")),
			cell(modelField("最大输出 tokens", "16384", s.modelFormMax, "model-max-tokens"))),
		core.Text{Content: "每百万 tokens 成本（$）", Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: s.colors.Muted()}},
		row(
			cell(modelField("输入", "3", s.modelFormCostInput, "model-cost-input")),
			cell(modelField("输出", "15", s.modelFormCostOutput, "model-cost-output")),
			cell(modelField("缓存读", "0.3", s.modelFormCostCacheRead, "model-cost-cache-read")),
			cell(modelField("缓存写", "3.75", s.modelFormCostCacheWrite, "model-cost-cache-write")),
		),
		core.Text{Content: "高级（留空则跟随提供商）", Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: s.colors.Muted()}},
		row(cell(modelField("Base URL 覆盖", provider.BaseURL, s.modelFormBase, "model-base-url")),
			cell(modelField("API 覆盖", provider.API, s.modelFormAPI, "model-api"))),
		core.View{Style: core.Style{Direction: core.Row, Gap: 6}, Children: []core.Node{
			ui.Button{Text: "取消", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "model-cancel",
				OnPress: func() { s.modelFormOpen.Set(false) }},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.Button{Text: "保存", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "model-save",
				OnPress: s.saveModelForm},
		}},
	}}
}

// beginModel fills the form, empty for a new model.
func (s *appState) beginModel(m *models.Model) {
	s.modelEditID.Set("")
	s.modelOriginalID.Set("")
	s.modelFormName.Set("")
	s.modelFormBase.Set("")
	s.modelFormAPI.Set("")
	s.modelFormContext.Set("")
	s.modelFormMax.Set("")
	s.modelFormReasoning.Set(false)
	s.modelFormImage.Set(false)
	s.modelFormCostInput.Set("")
	s.modelFormCostOutput.Set("")
	s.modelFormCostCacheRead.Set("")
	s.modelFormCostCacheWrite.Set("")
	s.modelOriginalID.Set("")
	if m != nil {
		s.modelEditID.Set(m.ID)
		s.modelOriginalID.Set(m.ID)
		s.modelFormName.Set(m.Name)
		s.modelFormBase.Set(m.BaseURL)
		s.modelFormAPI.Set(m.API)
		if m.ContextWindow > 0 {
			s.modelFormContext.Set(strconv.Itoa(m.ContextWindow))
		}
		if m.MaxTokens > 0 {
			s.modelFormMax.Set(strconv.Itoa(m.MaxTokens))
		}
		s.modelFormReasoning.Set(m.Reasoning)
		s.modelFormImage.Set(m.ImageInput)
		if v, ok := m.Cost["input"]; ok {
			s.modelFormCostInput.Set(trimFloat(v))
		}
		if v, ok := m.Cost["output"]; ok {
			s.modelFormCostOutput.Set(trimFloat(v))
		}
		if v, ok := m.Cost["cacheRead"]; ok {
			s.modelFormCostCacheRead.Set(trimFloat(v))
		}
		if v, ok := m.Cost["cacheWrite"]; ok {
			s.modelFormCostCacheWrite.Set(trimFloat(v))
		}
	}
	s.modelFormOpen.Set(true)
}

// fillModelFromCatalog copies what pi's catalog knows about the model the
// form names, as pi-web's catalog fill does. The provider's own entry wins,
// then any provider offering the same id.
func (s *appState) fillModelFromCatalog() {
	id := strings.TrimSpace(s.modelEditID.GetUntracked())
	if id == "" {
		s.notify(ui.ToastWarning, "先填模型 ID", "")
		return
	}
	providerID := s.modelProviderID.GetUntracked()
	var found *models.Model
	for _, p := range s.catalog.GetUntracked() {
		if p.ID != providerID {
			continue
		}
		for i := range p.Models {
			if strings.EqualFold(p.Models[i].ID, id) {
				found = &p.Models[i]
			}
		}
	}
	if found == nil {
		for _, p := range s.catalog.GetUntracked() {
			for i := range p.Models {
				if strings.EqualFold(p.Models[i].ID, id) {
					found = &p.Models[i]
					break
				}
			}
			if found != nil {
				break
			}
		}
	}
	if found == nil {
		s.notify(ui.ToastInfo, "目录里没有这个模型", id)
		return
	}
	s.modelFormName.Set(found.Name)
	s.modelFormBase.Set(found.BaseURL)
	s.modelFormAPI.Set(found.API)
	s.modelFormReasoning.Set(found.Reasoning)
	s.modelFormImage.Set(found.ImageInput)
	s.modelFormContext.Set(itoa(found.ContextWindow))
	s.modelFormMax.Set(itoa(found.MaxTokens))
	s.modelFormCostInput.Set(trimFloat(found.Cost["input"]))
	s.modelFormCostOutput.Set(trimFloat(found.Cost["output"]))
	s.modelFormCostCacheRead.Set(trimFloat(found.Cost["cacheRead"]))
	s.modelFormCostCacheWrite.Set(trimFloat(found.Cost["cacheWrite"]))
	s.notify(ui.ToastSuccess, "已从目录填充", found.ID)
}

// atoi reads a positive integer field, zero when empty or invalid.
func atoi(text string) int {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// trimFloat prints a cost without a trailing .0, as a text field wants.
func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// costValue parses one cost field, ignoring an empty or invalid one.
func costValue(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v, true
}

// saveModelForm writes the form's model; a new id adds a model, the old one
// is replaced in place.
func (s *appState) saveModelForm() {
	providerID := s.modelProviderID.GetUntracked()
	original := s.modelOriginalID.GetUntracked()
	model := models.Model{
		ID:            strings.TrimSpace(s.modelEditID.GetUntracked()),
		Name:          strings.TrimSpace(s.modelFormName.GetUntracked()),
		BaseURL:       strings.TrimSpace(s.modelFormBase.GetUntracked()),
		API:           strings.TrimSpace(s.modelFormAPI.GetUntracked()),
		Reasoning:     s.modelFormReasoning.GetUntracked(),
		ImageInput:    s.modelFormImage.GetUntracked(),
		ContextWindow: atoi(s.modelFormContext.GetUntracked()),
		MaxTokens:     atoi(s.modelFormMax.GetUntracked()),
		Cost:          map[string]float64{},
	}
	if model.Name == "" {
		model.Name = model.ID
	}
	for key, text := range map[string]string{
		"input":      s.modelFormCostInput.GetUntracked(),
		"output":     s.modelFormCostOutput.GetUntracked(),
		"cacheRead":  s.modelFormCostCacheRead.GetUntracked(),
		"cacheWrite": s.modelFormCostCacheWrite.GetUntracked(),
	} {
		if v, ok := costValue(text); ok {
			model.Cost[key] = v
		}
	}
	if model.ID == "" {
		s.notify(ui.ToastWarning, "需要模型 ID", "")
		return
	}
	s.modelFormOpen.Set(false)
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			if err := models.SaveModel(raw, providerID, model, original); err != nil {
				return err
			}
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "保存模型失败", err.Error())
				return
			}
			s.notify(ui.ToastSuccess, "已保存模型", model.ID)
			s.refreshResources()
			go s.refreshSettings()
		})
	}()
}

// deleteModel removes one model from the provider.
func (s *appState) deleteModel(id string) {
	providerID := s.modelProviderID.GetUntracked()
	go func() {
		err := func() error {
			raw, err := s.cfg.Models()
			if err != nil {
				return err
			}
			models.DeleteModel(raw, providerID, id)
			return s.cfg.SetModels(raw)
		}()
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "删除模型失败", err.Error())
				return
			}
			s.notify(ui.ToastInfo, "已删除模型", id)
			s.refreshResources()
			go s.refreshSettings()
		})
	}()
}
