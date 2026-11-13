package main

import (
	"encoding/json"
	"strings"
	"testing"

	"pigo/internal/reducer"
)

func TestSessionFileFromState(t *testing.T) {
	var raw json.RawMessage = []byte(`{"sessionFile":"/tmp/s/1.jsonl","sessionId":"x"}`)
	if got := sessionFileFromState(raw); got != "/tmp/s/1.jsonl" {
		t.Fatalf("sessionFileFromState = %q", got)
	}
	if got := sessionFileFromState(nil); got != "" {
		t.Fatalf("empty data = %q", got)
	}
	if got := sessionFileFromState([]byte(`{}`)); got != "" {
		t.Fatalf("missing field = %q", got)
	}
	if got := sessionFileFromState([]byte(`not json`)); got != "" {
		t.Fatalf("invalid JSON = %q", got)
	}
}

func TestItemsFromRowsGroupsAndOrdinals(t *testing.T) {
	rows := []sessionRow{
		{ID: "a", Title: "A", Group: "p1"},
		{ID: "b", Title: "B", Group: "p2"},
		{ID: "c", Title: "C", Group: "p1"},
	}
	items := itemsFromRows(rows, nil)
	if len(items) != 5 {
		t.Fatalf("items = %d, want 5", len(items))
	}
	if !items[0].Header || items[0].Group != "p1" {
		t.Fatalf("first item is not the p1 header: %+v", items[0])
	}
	if items[1].Row.ID != "a" || items[2].Row.ID != "c" {
		t.Fatalf("p1 rows are not grouped in order: %+v", items[1:3])
	}
	if items[3].Header != true || items[3].Group != "p2" {
		t.Fatalf("p2 header missing: %+v", items[3])
	}
	wantOrdinals := map[string]int{"a": 0, "c": 1, "b": 2}
	for _, it := range items {
		if it.Header {
			continue
		}
		if it.Ordinal != wantOrdinals[it.Row.ID] {
			t.Fatalf("%s ordinal = %d, want %d", it.Row.ID, it.Ordinal, wantOrdinals[it.Row.ID])
		}
	}
}

func TestItemsFromRowsPinsFirst(t *testing.T) {
	rows := []sessionRow{
		{ID: "a", Title: "A", Group: "g"},
		{ID: "b", Title: "B", Group: "g", Pinned: true},
		{ID: "c", Title: "C", Group: "g"},
	}
	items := itemsFromRows(rows, nil)
	var order []string
	for _, it := range items {
		if !it.Header {
			order = append(order, it.Row.ID)
		}
	}
	if len(order) != 3 || order[0] != "b" || order[1] != "a" || order[2] != "c" {
		t.Fatalf("order = %v, want pinned first then store order", order)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("  第一行\n第二行  ", 10); got != "第一行" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine("1234567890abc", 5); got != "12345…" {
		t.Fatalf("truncated = %q", got)
	}
}

func TestBashCommandOf(t *testing.T) {
	if cmd, ok := bashCommandOf("!ls -la"); !ok || cmd != "ls -la" {
		t.Fatalf("bash = %q %v", cmd, ok)
	}
	if _, ok := bashCommandOf("!!literal"); ok {
		t.Fatal("!! must stay a prompt")
	}
	if _, ok := bashCommandOf("普通消息"); ok {
		t.Fatal("plain text must not run a shell")
	}
}

func TestFlattenTree(t *testing.T) {
	data := []byte(`{"tree":[{"entry":{"type":"message","id":"u1","message":{"role":"user","content":"第一问"}},"children":[
		{"entry":{"type":"message","id":"a1","message":{"role":"assistant","content":[{"type":"text","text":"答案一"}]}},"children":[
			{"entry":{"type":"message","id":"u2","message":{"role":"user","content":"第二问"}},"children":[]}]}]},
		{"entry":{"type":"model_change","id":"m1"},"children":[]}]}`)
	items := flattenTree(data)
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].ID != "u1" || items[0].Depth != 0 || items[1].ID != "a1" || items[1].Depth != 1 || items[2].ID != "u2" {
		t.Fatalf("items = %+v", items)
	}
	if !strings.HasPrefix(items[1].Label, "回答：答案一") {
		t.Fatalf("assistant label = %q", items[1].Label)
	}
}

func TestRecalledQueueText(t *testing.T) {
	data := []byte(`{"steering":["先改这个"],"followUp":["再跑测试"]}`)
	if got := recalledQueueText(data); got != "先改这个\n再跑测试" {
		t.Fatalf("text = %q", got)
	}
	if got := recalledQueueText([]byte(`not json`)); got != "" {
		t.Fatalf("bad json = %q", got)
	}
}

func TestLastUserText(t *testing.T) {
	snap := reducer.Snapshot{Messages: []reducer.Message{{Role: "assistant", Text: "回答"}, {Role: "user", Text: "  最后一个问题  "}}}
	if got := lastUserText(snap); got != "最后一个问题" {
		t.Fatalf("prompt = %q", got)
	}
}
