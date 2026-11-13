// Package models edits pi's models.json without losing fields it does not
// know about: the file is a provider map the SDK keeps adding keys to.
package models

import (
	"fmt"
	"sort"
	"strings"
)

// Model is one model of a provider: the fields the Models settings page
// edits, as pi-web's ModelsConfig does. Unknown keys stay in the file.
type Model struct {
	ID, Name      string
	API, BaseURL  string
	Reasoning     bool
	ImageInput    bool
	ContextWindow int
	MaxTokens     int
	Cost          map[string]float64
}

// Provider is a provider row as the settings page shows it.
type Provider struct {
	ID, Name, BaseURL, API, APIKey string
	Models                         []Model
	HasOAuth                       bool
}

// Providers reads {"providers": {...}} into rows ordered by id.
func Providers(root map[string]any) []Provider {
	raw, _ := root["providers"].(map[string]any)
	out := make([]Provider, 0, len(raw))
	for id, v := range raw {
		p := Provider{ID: id, Name: id}
		entry, _ := v.(map[string]any)
		if s, ok := entry["name"].(string); ok && s != "" {
			p.Name = s
		}
		p.BaseURL, _ = entry["baseUrl"].(string)
		p.API, _ = entry["api"].(string)
		p.APIKey, _ = entry["apiKey"].(string)
		_, p.HasOAuth = entry["oauth"]
		if list, ok := entry["models"].([]any); ok {
			for _, m := range list {
				mm, _ := m.(map[string]any)
				if model, ok := parseModel(mm); ok {
					p.Models = append(p.Models, model)
				}
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Input is the provider form's fields. Empty strings leave the stored value
// alone; Models is the complete list of model ids after the edit.
type Input struct {
	ID, Name, BaseURL, API, APIKey string
	Models                         []string
	ModelNames                     map[string]string
}

// Upsert writes a provider, preserving keys the form does not edit (headers,
// compat, oauth, modelOverrides, per-model cost and limits). A renamed id is
// not supported: the id is the map key.
func Upsert(root map[string]any, in Input) error {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return fmt.Errorf("models: provider id is required")
	}
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		providers = map[string]any{}
		root["providers"] = providers
	}
	entry, _ := providers[id].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			entry[key] = strings.TrimSpace(value)
		}
	}
	set("name", in.Name)
	set("baseUrl", in.BaseURL)
	set("api", in.API)
	set("apiKey", in.APIKey)

	// Keep an existing model entry (cost, limits, thinking map) when its id
	// survives, and give new ids a minimal entry.
	existing := map[string]map[string]any{}
	if list, ok := entry["models"].([]any); ok {
		for _, m := range list {
			mm, _ := m.(map[string]any)
			if mid, _ := mm["id"].(string); mid != "" {
				existing[mid] = mm
			}
		}
	}
	if len(in.Models) > 0 {
		list := make([]any, 0, len(in.Models))
		for _, mid := range in.Models {
			mid = strings.TrimSpace(mid)
			if mid == "" {
				continue
			}
			if m, ok := existing[mid]; ok {
				list = append(list, m)
				continue
			}
			name := mid
			if n := in.ModelNames[mid]; n != "" {
				name = n
			}
			list = append(list, map[string]any{"id": mid, "name": name})
		}
		entry["models"] = list
	}
	providers[id] = entry
	return nil
}

// parseModel reads one models.json model entry.
func parseModel(mm map[string]any) (Model, bool) {
	id, _ := mm["id"].(string)
	if id == "" {
		return Model{}, false
	}
	model := Model{ID: id, Name: id}
	if s, ok := mm["name"].(string); ok && s != "" {
		model.Name = s
	}
	model.API, _ = mm["api"].(string)
	model.BaseURL, _ = mm["baseUrl"].(string)
	model.Reasoning, _ = mm["reasoning"].(bool)
	model.ContextWindow = number(mm["contextWindow"])
	model.MaxTokens = number(mm["maxTokens"])
	if list, ok := mm["input"].([]any); ok {
		for _, v := range list {
			if s, _ := v.(string); s == "image" {
				model.ImageInput = true
			}
		}
	}
	if raw, ok := mm["cost"].(map[string]any); ok {
		for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
			if v, ok := raw[key].(float64); ok {
				if model.Cost == nil {
					model.Cost = map[string]float64{}
				}
				model.Cost[key] = v
			}
		}
	}
	return model, true
}

// number reads a JSON number, which encoding/json decodes as float64.
func number(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// SaveModel writes one model into a provider, preserving the keys the form
// does not edit (headers, compat, thinkingLevelMap, limits). originalID is
// the id the entry had before the edit; empty adds a new model at the end.
func SaveModel(root map[string]any, providerID string, model Model, originalID string) error {
	providerID = strings.TrimSpace(providerID)
	model.ID = strings.TrimSpace(model.ID)
	if providerID == "" || model.ID == "" {
		return fmt.Errorf("models: provider and model id are required")
	}
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		return fmt.Errorf("models: no providers")
	}
	entry, _ := providers[providerID].(map[string]any)
	if entry == nil {
		return fmt.Errorf("models: provider %q is not configured", providerID)
	}
	list, _ := entry["models"].([]any)
	at := -1
	if originalID == "" {
		originalID = model.ID
	}
	for i, raw := range list {
		mm, _ := raw.(map[string]any)
		if id, _ := mm["id"].(string); id == originalID {
			at = i
			break
		}
	}
	var mm map[string]any
	if at >= 0 {
		mm, _ = list[at].(map[string]any)
	}
	if mm == nil {
		mm = map[string]any{}
	}
	mm["id"] = model.ID
	if model.Name != "" && model.Name != model.ID {
		mm["name"] = model.Name
	} else {
		delete(mm, "name")
	}
	setString := func(key, value string) {
		if value != "" {
			mm[key] = value
		} else {
			delete(mm, key)
		}
	}
	setString("api", model.API)
	setString("baseUrl", model.BaseURL)
	if model.Reasoning {
		mm["reasoning"] = true
	} else {
		delete(mm, "reasoning")
	}
	if model.ImageInput {
		mm["input"] = []any{"text", "image"}
	} else {
		delete(mm, "input")
	}
	if model.ContextWindow > 0 {
		mm["contextWindow"] = model.ContextWindow
	} else {
		delete(mm, "contextWindow")
	}
	if model.MaxTokens > 0 {
		mm["maxTokens"] = model.MaxTokens
	} else {
		delete(mm, "maxTokens")
	}
	if len(model.Cost) > 0 {
		cost, _ := mm["cost"].(map[string]any)
		if cost == nil {
			cost = map[string]any{}
		}
		for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
			if v, ok := model.Cost[key]; ok {
				cost[key] = v
			}
		}
		mm["cost"] = cost
	}
	if at >= 0 {
		list[at] = mm
	} else {
		list = append(list, mm)
	}
	entry["models"] = list
	providers[providerID] = entry
	return nil
}

// DeleteModel removes one model from a provider.
func DeleteModel(root map[string]any, providerID, id string) {
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		return
	}
	entry, _ := providers[providerID].(map[string]any)
	if entry == nil {
		return
	}
	list, _ := entry["models"].([]any)
	out := make([]any, 0, len(list))
	for _, raw := range list {
		mm, _ := raw.(map[string]any)
		if mid, _ := mm["id"].(string); mid == id {
			continue
		}
		out = append(out, raw)
	}
	entry["models"] = out
	providers[providerID] = entry
}

// Delete removes a provider and its models.
func Delete(root map[string]any, id string) {
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		return
	}
	delete(providers, id)
}

// ModelIDs lists one provider's ids.
func (p Provider) ModelIDs() []string {
	ids := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

// CatalogProvider is a provider pi's model catalog already knows, used to
// fill the add form and to list what can be added.
type CatalogProvider struct {
	ID, Name, BaseURL, API string
	Models                 []Model
}

// Catalog reads models-store.json, pi's cached model catalog, into provider
// presets. The catalog's model entries carry their own baseUrl and api, so
// the first model supplies the provider's defaults.
func Catalog(root map[string]any) []CatalogProvider {
	out := make([]CatalogProvider, 0, len(root))
	for id, v := range root {
		entry, _ := v.(map[string]any)
		list, _ := entry["models"].([]any)
		p := CatalogProvider{ID: id, Name: id}
		for _, raw := range list {
			m, _ := raw.(map[string]any)
			model, ok := parseModel(m)
			if !ok {
				continue
			}
			if len(p.Models) == 0 {
				p.BaseURL, _ = m["baseUrl"].(string)
				p.API, _ = m["api"].(string)
			}
			p.Models = append(p.Models, model)
		}
		if len(p.Models) == 0 {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

// DeleteAPIKey removes one provider's stored key and leaves everything else
// about it alone: the provider, its base URL and its models stay configured,
// so only the credential has to be entered again.
func DeleteAPIKey(root map[string]any, providerID string) {
	providers, ok := root["providers"].(map[string]any)
	if !ok {
		return
	}
	entry, _ := providers[providerID].(map[string]any)
	if entry == nil {
		return
	}
	delete(entry, "apiKey")
}
