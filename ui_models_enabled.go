package main

import (
	"github.com/oneo-me/nsgo/ui"

	"pigo/internal/models"
)

// modelGroup is one provider's models as the switches list them.
type modelGroup struct {
	provider string
	ids      []string
	models   []modelItem
}

// availableModelGroups groups the live model list by provider, falling back
// to models.json when pi has not answered yet.
func (s *appState) availableModelGroups() []modelGroup {
	var groups []modelGroup
	index := map[string]int{}
	add := func(provider string, m modelItem) {
		at, ok := index[provider]
		if !ok {
			index[provider] = len(groups)
			groups = append(groups, modelGroup{provider: provider})
			at = index[provider]
		}
		groups[at].ids = append(groups[at].ids, m.ID)
		groups[at].models = append(groups[at].models, m)
	}
	for _, m := range s.models.Get() {
		add(m.Provider, m)
	}
	if len(groups) == 0 {
		for _, p := range s.providers.Get() {
			if len(p.Models) > 0 {
				for _, m := range p.Models {
					add(p.ID, modelItem{Provider: p.ID, ID: m.ID, Name: m.Name, Reasoning: m.Reasoning})
				}
				continue
			}
			// A provider added from pi's catalog carries no model list: pi
			// supplies its own, so the switches name those.
			for _, m := range models.BuiltinModels(p.ID) {
				add(p.ID, modelItem{Provider: p.ID, ID: m.ID, Name: m.Name, Reasoning: m.Reasoning})
			}
		}
	}
	return groups
}

// availableModelRefs is every provider/id the settings can name.
func (s *appState) availableModelRefs() []string {
	var refs []string
	for _, group := range s.availableModelGroups() {
		for _, id := range group.ids {
			refs = append(refs, group.provider+"/"+id)
		}
	}
	return refs
}

func (s *appState) setModelEnabled(provider, id string, on bool, available []string) {
	s.applyEnabled(func(settings map[string]any) error {
		return models.SetEnabled(settings, provider, id, on, available...)
	})
}

func (s *appState) setProviderEnabled(provider string, ids []string, on bool, available []string) {
	s.applyEnabled(func(settings map[string]any) error {
		return models.SetProviderEnabled(settings, provider, ids, on, available...)
	})
}

// applyEnabled writes enabledModels and refreshes the switches; pi reads the
// list when a session's process starts, so the note says when it applies.
func (s *appState) applyEnabled(edit func(settings map[string]any) error) {
	if s.enabledBusy.GetUntracked() {
		return
	}
	s.enabledBusy.Set(true)
	go func() {
		settings, err := s.cfg.Settings()
		if err == nil {
			err = edit(settings)
		}
		if err == nil {
			err = s.cfg.Write("settings.json", settings, false)
		}
		s.cx.Post(func() {
			s.enabledBusy.Set(false)
			if err != nil {
				s.notify(ui.ToastWarning, "保存可用模型失败", err.Error())
				return
			}
			s.enabledPatterns.Set(models.ReadEnabled(settings))
			s.notify(ui.ToastSuccess, "已保存可用模型", "新会话或重连后生效")
		})
	}()
}
