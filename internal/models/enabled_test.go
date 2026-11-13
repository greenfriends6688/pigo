package models

import (
	"errors"
	"reflect"
	"testing"
)

// enabledSettings builds a settings.json map the way encoding/json decodes it.
func enabledSettings(patterns ...string) map[string]any {
	list := make([]any, len(patterns))
	for i, pattern := range patterns {
		list[i] = pattern
	}
	return map[string]any{enabledKey: list}
}

func requireEnabled(t *testing.T, settings map[string]any, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if got := ReadEnabled(settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledModels = %v, want %v", got, want)
	}
}

func TestReadEnabledDefaultsToEverything(t *testing.T) {
	for _, settings := range []map[string]any{
		nil,
		{},
		{enabledKey: nil},
		{enabledKey: []any{}},
		{enabledKey: []any{"", "  "}},
	} {
		patterns := ReadEnabled(settings)
		if patterns != nil {
			t.Fatalf("ReadEnabled(%v) = %v, want nil", settings, patterns)
		}
		if !EnabledFor(patterns, "commandcode", "sakana/fugu-ultra") {
			t.Fatal("no patterns must enable every model, nested ids included")
		}
		if got := ProviderCovered(patterns, "openai", []string{"gpt-5", "gpt-5-mini"}); got != 2 {
			t.Fatalf("ProviderCovered = %d, want 2", got)
		}
	}
}

func TestReadEnabledAcceptsBothListForms(t *testing.T) {
	if got := ReadEnabled(enabledSettings("anthropic/*", "openai/gpt-5")); !reflect.DeepEqual(got, []string{"anthropic/*", "openai/gpt-5"}) {
		t.Fatalf("json form = %v", got)
	}
	settings := map[string]any{enabledKey: []string{"a/*", "b/c"}}
	if got := ReadEnabled(settings); !reflect.DeepEqual(got, []string{"a/*", "b/c"}) {
		t.Fatalf("go form = %v", got)
	}
	settings[enabledKey] = []any{"a/*", 7, "b/c"}
	if got := ReadEnabled(settings); !reflect.DeepEqual(got, []string{"a/*", "b/c"}) {
		t.Fatalf("non-strings must be skipped, got %v", got)
	}
}

func TestEnabledForPatterns(t *testing.T) {
	nested := "sakana/fugu-ultra"
	cases := []struct {
		patterns []string
		provider string
		id       string
		want     bool
	}{
		{nil, "commandcode", nested, true},
		{[]string{"*"}, "commandcode", nested, true},
		{[]string{"commandcode/*"}, "commandcode", nested, true},
		{[]string{"commandcode/**"}, "commandcode", nested, true},
		{[]string{"commandcode/*"}, "openai", nested, false},
		{[]string{"openai/gpt-5"}, "openai", "gpt-5", true},
		{[]string{"openai/gpt-5"}, "openai", "gpt-5-mini", false},
		{[]string{"openai/gpt-5*"}, "openai", "gpt-5-mini", true},
		{[]string{"openai/gpt-5:high"}, "openai", "gpt-5", true},
		{[]string{"openai/gpt-5:high"}, "openai", "gpt-5-mini", false},
		{[]string{"zen/x:exacto"}, "zen", "x:exacto", true},
		{[]string{"zen/*:exacto"}, "zen", "x", false},
		{[]string{"gpt-5"}, "openai", "gpt-5", true},
		{[]string{"gone/*"}, "openai", "gpt-5", false},
		{[]string{"anthropic/*", "openai/gpt-5"}, "openai", "gpt-5", true},
	}
	for _, tc := range cases {
		if got := EnabledFor(tc.patterns, tc.provider, tc.id); got != tc.want {
			t.Fatalf("EnabledFor(%v, %q, %q) = %v, want %v", tc.patterns, tc.provider, tc.id, got, tc.want)
		}
	}
}

func TestProviderCovered(t *testing.T) {
	patterns := []string{"anthropic/*", "openai/gpt-5"}
	if got := ProviderCovered(patterns, "anthropic", []string{"claude-a", "claude-b", "claude-c"}); got != 3 {
		t.Fatalf("anthropic covered = %d, want 3", got)
	}
	if got := ProviderCovered(patterns, "openai", []string{"gpt-5", "gpt-5-mini"}); got != 1 {
		t.Fatalf("openai covered = %d, want 1", got)
	}
}

func TestSetEnabledExpandsOnlyTheCoveringPattern(t *testing.T) {
	available := []string{"anthropic/claude-a", "anthropic/claude-b", "openai/gpt-5"}
	settings := enabledSettings("anthropic/*", "openai/gpt-5", "gone/*")
	if err := SetEnabled(settings, "anthropic", "claude-b", false, available...); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "anthropic/claude-a", "openai/gpt-5", "gone/*")
}

func TestSetEnabledExpandsEveryCoveringPattern(t *testing.T) {
	settings := enabledSettings("anthropic/*", "anthropic/claude-a")
	available := []string{"anthropic/claude-a", "anthropic/claude-b"}
	if err := SetEnabled(settings, "anthropic", "claude-a", false, available...); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "anthropic/claude-b")
}

func TestSetEnabledKeepsThinkingPin(t *testing.T) {
	settings := enabledSettings("anthropic/*:high", "openai/gpt-5")
	available := []string{"anthropic/claude-a", "anthropic/claude-b", "openai/gpt-5"}
	if err := SetEnabled(settings, "anthropic", "claude-a", false, available...); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "anthropic/claude-b:high", "openai/gpt-5")

	// A model id containing a colon is not a pin.
	settings = enabledSettings("*:low")
	if err := SetEnabled(settings, "anthropic", "claude-a", false, "anthropic/claude-a", "zen/x:exacto"); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "zen/x:exacto:low")
}

func TestSetEnabledFromDefaultScopeMaterializes(t *testing.T) {
	settings := map[string]any{}
	available := []string{"openai/gpt-5", "openai/gpt-5-mini", "anthropic/claude-a"}
	if err := SetEnabled(settings, "openai", "gpt-5", false, available...); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5-mini", "anthropic/claude-a")
}

func TestSetEnabledRefusesLastModel(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]any
		provider string
		id       string
		avail    []string
	}{
		{"only model", enabledSettings("openai/gpt-5"), "openai", "gpt-5", []string{"openai/gpt-5"}},
		{"stale siblings do not count", enabledSettings("openai/gpt-5", "gone/*"), "openai", "gpt-5", []string{"openai/gpt-5"}},
		{"default scope", map[string]any{}, "openai", "gpt-5", []string{"openai/gpt-5"}},
		{"sole provider", enabledSettings("*"), "openai", "gpt-5", []string{"openai/gpt-5"}},
	}
	for _, tc := range cases {
		before := ReadEnabled(tc.settings)
		if err := SetEnabled(tc.settings, tc.provider, tc.id, false, tc.avail...); !errors.Is(err, ErrLastModel) {
			t.Fatalf("%s: err = %v, want ErrLastModel", tc.name, err)
		}
		if got := ReadEnabled(tc.settings); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s: settings changed to %v, want %v", tc.name, got, before)
		}
	}
}

func TestSetEnabledWithoutModelListKeepsGlobs(t *testing.T) {
	settings := enabledSettings("anthropic/*")
	if err := SetEnabled(settings, "anthropic", "claude-a", false); !errors.Is(err, ErrNeedModels) {
		t.Fatalf("glob without ids: err = %v, want ErrNeedModels", err)
	}
	requireEnabled(t, settings, "anthropic/*")

	settings = map[string]any{}
	if err := SetEnabled(settings, "anthropic", "claude-a", false); !errors.Is(err, ErrNeedModels) {
		t.Fatalf("default scope without ids: err = %v, want ErrNeedModels", err)
	}
	requireEnabled(t, settings)

	// An exact pattern needs no model list: only the removed entry goes.
	settings = enabledSettings("openai/gpt-5", "anthropic/*", "not a pattern")
	if err := SetEnabled(settings, "openai", "gpt-5", false); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "anthropic/*", "not a pattern")
}

func TestSetEnabledOnNeverDuplicates(t *testing.T) {
	settings := enabledSettings("anthropic/*")
	if err := SetEnabled(settings, "anthropic", "claude-a", true); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "anthropic/*")

	settings = enabledSettings("openai/gpt-5")
	if err := SetEnabled(settings, "anthropic", "claude-a", true); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(settings, "anthropic", "claude-a", true); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5", "anthropic/claude-a")

	// Nothing narrows the list, so there is nothing to add.
	settings = map[string]any{}
	if err := SetEnabled(settings, "anthropic", "claude-a", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := settings[enabledKey]; ok {
		t.Fatalf("settings gained a scope: %v", settings)
	}
	requireEnabled(t, settings)

	// Already off: no write.
	settings = enabledSettings("openai/gpt-5")
	if err := SetEnabled(settings, "anthropic", "claude-a", false); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5")
}

func TestSetProviderEnabled(t *testing.T) {
	// Turning a whole provider off removes its glob and touches nothing else.
	settings := enabledSettings("anthropic/*", "openai/gpt-5", "gone/*")
	if err := SetProviderEnabled(settings, "anthropic", []string{"claude-a", "claude-b"}, false); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5", "gone/*")

	// Turning one on appends only what is missing, in ids order.
	settings = enabledSettings("openai/gpt-5", "anthropic/claude-b")
	ids := []string{"claude-a", "claude-b", "claude-c"}
	if err := SetProviderEnabled(settings, "anthropic", ids, true); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5", "anthropic/claude-b", "anthropic/claude-a", "anthropic/claude-c")
	if got := ProviderCovered(ReadEnabled(settings), "anthropic", ids); got != 3 {
		t.Fatalf("covered = %d, want 3", got)
	}
	if err := SetProviderEnabled(settings, "anthropic", []string{"claude-a"}, true); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5", "anthropic/claude-b", "anthropic/claude-a", "anthropic/claude-c")

	// A bare "*" needs the rest of the catalog to be narrowed.
	settings = enabledSettings("*")
	if err := SetProviderEnabled(settings, "anthropic", []string{"claude-a", "claude-b"}, false); !errors.Is(err, ErrNeedModels) {
		t.Fatalf("star scope: err = %v, want ErrNeedModels", err)
	}
	requireEnabled(t, settings, "*")
	if err := SetProviderEnabled(settings, "anthropic", []string{"claude-a", "claude-b"}, false, "openai/gpt-5"); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "openai/gpt-5")

	// Off for the only provider is the last model.
	settings = enabledSettings("*")
	if err := SetProviderEnabled(settings, "anthropic", []string{"claude-a"}, false, "anthropic/claude-a"); !errors.Is(err, ErrLastModel) {
		t.Fatalf("sole provider: err = %v, want ErrLastModel", err)
	}
	requireEnabled(t, settings, "*")
}

func TestSetProviderEnabledKeepsOrderOfOtherEntries(t *testing.T) {
	settings := enabledSettings("zen/*:medium", "commandcode/*", "openai/gpt-5", "gone/*")
	if err := SetProviderEnabled(settings, "openai", []string{"gpt-5"}, false); err != nil {
		t.Fatal(err)
	}
	// openai/gpt-5 was the only match of its pattern, so the entry goes.
	requireEnabled(t, settings, "zen/*:medium", "commandcode/*", "gone/*")

	if err := SetProviderEnabled(settings, "openai", []string{"gpt-5", "gpt-6"}, true); err != nil {
		t.Fatal(err)
	}
	requireEnabled(t, settings, "zen/*:medium", "commandcode/*", "gone/*", "openai/gpt-5", "openai/gpt-6")
}
