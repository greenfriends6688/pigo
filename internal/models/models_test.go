package models

import (
	"testing"
)

func TestProvidersAndUpsertPreservesUnknownFields(t *testing.T) {
	root := map[string]any{"providers": map[string]any{
		"acme": map[string]any{
			"name": "Acme", "baseUrl": "https://old", "api": "openai-completions",
			"apiKey": "sk-old", "headers": map[string]any{"X": "y"},
			"models": []any{
				map[string]any{"id": "a-1", "name": "A 1", "cost": map[string]any{"input": 1.0}},
				map[string]any{"id": "a-2", "name": "A 2"},
			},
		},
	}}
	providers := Providers(root)
	if len(providers) != 1 || providers[0].ID != "acme" || len(providers[0].Models) != 2 {
		t.Fatalf("providers = %+v", providers)
	}

	if err := Upsert(root, Input{ID: "acme", BaseURL: "https://new", Models: []string{"a-1", "a-3"}}); err != nil {
		t.Fatal(err)
	}
	entry := root["providers"].(map[string]any)["acme"].(map[string]any)
	if entry["baseUrl"] != "https://new" || entry["name"] != "Acme" || entry["apiKey"] != "sk-old" {
		t.Fatalf("provider changed unexpectedly: %+v", entry)
	}
	if _, ok := entry["headers"]; !ok {
		t.Fatal("unknown fields were dropped")
	}
	list := entry["models"].([]any)
	if len(list) != 2 {
		t.Fatalf("models = %+v", list)
	}
	first := list[0].(map[string]any)
	if first["id"] != "a-1" || first["cost"] == nil {
		t.Fatalf("kept model lost its fields: %+v", first)
	}
	if list[1].(map[string]any)["id"] != "a-3" {
		t.Fatalf("new model missing: %+v", list[1])
	}

	if err := Upsert(root, Input{ID: "beta", BaseURL: "https://beta"}); err != nil {
		t.Fatal(err)
	}
	if len(Providers(root)) != 2 {
		t.Fatal("adding a provider failed")
	}
	Delete(root, "beta")
	if len(Providers(root)) != 1 {
		t.Fatal("deleting a provider failed")
	}
	if err := Upsert(root, Input{}); err == nil {
		t.Fatal("an empty provider id must fail")
	}
}

func TestCatalogReadsStore(t *testing.T) {
	root := map[string]any{
		"acme": map[string]any{"models": []any{
			map[string]any{"id": "a-1", "name": "A 1", "baseUrl": "https://a/v1", "api": "openai-completions"},
			map[string]any{"id": "a-2", "name": "A 2", "baseUrl": "https://b/v1", "api": "anthropic-messages"},
		}},
		"empty": map[string]any{"models": []any{}},
	}
	catalog := Catalog(root)
	if len(catalog) != 1 || catalog[0].ID != "acme" || len(catalog[0].Models) != 2 {
		t.Fatalf("catalog = %+v", catalog)
	}
	if catalog[0].BaseURL != "https://a/v1" || catalog[0].API != "openai-completions" {
		t.Fatalf("provider defaults = %+v", catalog[0])
	}
}
