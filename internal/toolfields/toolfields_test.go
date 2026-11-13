package toolfields

import (
	"strings"
	"testing"
)

func TestFormatBash(t *testing.T) {
	action, detail := Format("bash", map[string]any{"command": "ls -la"})
	if action != "ls -la" {
		t.Fatalf("action = %q, want %q", action, "ls -la")
	}
	if !strings.Contains(detail, "command: ls -la") {
		t.Fatalf("detail = %q, want a flat field row for command", detail)
	}
}

func TestFormatReadFlatFields(t *testing.T) {
	action, detail := Format("read", map[string]any{
		"path": "src/app/page.tsx", "offset": 120, "limit": 80,
	})
	if action != "src/app/page.tsx" {
		t.Fatalf("action = %q, want the path", action)
	}
	for _, want := range []string{"offset: 120", "limit: 80", "path: src/app/page.tsx"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q: %q", want, detail)
		}
	}
}

func TestFormatPriorityArgvCommand(t *testing.T) {
	action, _ := Format("bash", map[string]any{
		"command": []any{"bash", "-lc", "ls"}, "workdir": "/tmp",
	})
	if action != "bash -lc ls" {
		t.Fatalf("action = %q, want %q", action, "bash -lc ls")
	}
}

func TestFormatPatternQuery(t *testing.T) {
	if action, _ := Format("grep", map[string]any{"pattern": "TODO"}); action != "TODO" {
		t.Fatalf("grep action = %q, want TODO", action)
	}
	if action, _ := Format("find", map[string]any{"query": "*.go"}); action != "*.go" {
		t.Fatalf("find action = %q, want *.go", action)
	}
}

func TestFormatNestedFallsBackToJSON(t *testing.T) {
	action, detail := Format("edit", map[string]any{
		"path":  "a.ts",
		"edits": []any{map[string]any{"oldText": "x", "newText": "y"}},
	})
	if action != "a.ts" {
		t.Fatalf("action = %q, want the path", action)
	}
	if !strings.Contains(detail, "edits") || !strings.HasPrefix(strings.TrimSpace(detail), "{") {
		t.Fatalf("nested args should fall back to a JSON blob, got %q", detail)
	}
}

func TestFormatScalarCoercion(t *testing.T) {
	_, detail := Format("grep", map[string]any{
		"pattern": "TODO", "case-sensitive": false, "limit": 0, "extra": nil, "empty": "",
	})
	for _, want := range []string{"case-sensitive: false", "limit: 0", "extra: null", "empty: (empty)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q: %q", want, detail)
		}
	}
}

func TestFormatNoPanicOnBadArgs(t *testing.T) {
	cases := []map[string]any{
		nil,
		{},
		{"command": map[string]any{"nested": true}},
		{"path": []any{1, 2, 3}},
		{"command": 12.5},
		{"query": true},
		{"a": []any{map[string]any{"x": 1}}},
	}
	for _, args := range cases {
		a, d := Format("tool", args)
		_, _ = a, d
		_ = Summary("tool", args)
	}
}

func TestSummary(t *testing.T) {
	if got := Summary("bash", map[string]any{"command": "ls -la"}); got != "ls -la" {
		t.Fatalf("Summary = %q, want ls -la", got)
	}
	if got := Summary("read", nil); got != "read" {
		t.Fatalf("Summary with no args = %q, want the tool name fallback", got)
	}
}

func TestPreviewClipAndNestedQuestion(t *testing.T) {
	if got := preview(map[string]any{"query": strings.Repeat("x", 300)}); len([]rune(got)) != previewMax {
		t.Fatalf("preview not clipped to %d runes, got %d", previewMax, len([]rune(got)))
	}
	got := preview(map[string]any{
		"questions": []any{
			map[string]any{"question": "Which database?", "header": "DB", "options": []any{}},
			map[string]any{"question": "Which features?", "header": "Feat", "options": []any{}},
		},
	})
	if got != "Which database?, Which features?" {
		t.Fatalf("nested question preview = %q", got)
	}
}

func TestFlatArraysPreferredOverFlags(t *testing.T) {
	got := preview(map[string]any{"urls": []any{"https://a.dev"}, "forceClone": true})
	if got != "https://a.dev" {
		t.Fatalf("preview = %q, want the url list", got)
	}
}
