package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/nsgotest"

	"piwebnative/internal/reducer"
)

// loadHistory reads testdata/get_messages.json, which is a real session's
// get_messages payload trimmed to its first turns. The shapes in it are what
// the subprocess actually sends: prose in "text" blocks, reasoning in
// "thinking", a call in "toolCall" whose result is a separate toolResult.
func loadHistory(t *testing.T) []json.RawMessage {
	t.Helper()
	body, err := os.ReadFile("testdata/get_messages.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("fixture decode: %v", err)
	}
	if len(payload.Messages) == 0 {
		t.Fatal("fixture is empty")
	}
	return payload.Messages
}

// TestTranscriptFromRealPayload is the regression test for the empty
// transcript: a real payload must reach the reducer as messages and the
// transcript as turns with prompts, tool cards and answers.
func TestTranscriptFromRealPayload(t *testing.T) {
	msgs := messagesFromRPC(loadHistory(t))
	if len(msgs) == 0 {
		t.Fatal("no messages converted")
	}
	store := reducer.New()
	store.SetState(msgs)
	turns := buildTurns(store.Snapshot())
	if len(turns) == 0 {
		t.Fatal("no turns built: the transcript would be empty")
	}
	first := turns[0]
	if !strings.Contains(first.Prompt, "重构") {
		t.Fatalf("first turn has no prompt: %q", first.Prompt)
	}
	if len(first.Tools) == 0 {
		t.Fatal("first turn has no tool cards")
	}
	for _, tool := range first.Tools {
		if tool.Action == "" {
			t.Fatalf("tool card %q has no action", tool.ID)
		}
	}
	// A tool card's result is attached by its call id.
	if first.Tools[0].Partial == "" {
		t.Errorf("tool card %q has no output attached", first.Tools[0].ID)
	}
	// Prose that the assistant wrote after its tools is somewhere as a step
	// or an answer, never dropped.
	var said []string
	for _, turn := range turns {
		said = append(said, turn.Steps...)
		if turn.Final != "" {
			said = append(said, turn.Final)
		}
	}
	if len(said) == 0 {
		t.Fatal("the assistant's prose was dropped")
	}
}

// TestHistoryToolStatus checks that replayed history does not paint every
// call completed: a call without a result was cancelled, a result with
// IsError failed, and only a real result is green.
func TestHistoryToolStatus(t *testing.T) {
	snap := reducer.Snapshot{Messages: []reducer.Message{
		{ID: "u1", Role: "user", Text: "跑三个工具"},
		{ID: "a1", Role: "assistant", Calls: []reducer.Call{
			{ID: "done", Name: "bash", Args: map[string]any{"command": "ls"}},
			{ID: "failed", Name: "bash", Args: map[string]any{"command": "nope"}},
			{ID: "cut", Name: "read", Args: map[string]any{"path": "a.go"}},
		}},
		{ID: "r1", Role: "toolResult", ToolCallID: "done", Text: "ok"},
		{ID: "r2", Role: "toolResult", ToolCallID: "failed", Text: "boom", IsError: true},
	}}
	turns := buildTurns(snap)
	if len(turns) != 1 || len(turns[0].Tools) != 3 {
		t.Fatalf("want one turn with three cards, got %+v", turns)
	}
	want := map[string]string{"done": "completed", "failed": "failed", "cut": "cancelled"}
	for _, tool := range turns[0].Tools {
		if tool.Status != want[tool.ID] {
			t.Errorf("tool %s: want %s, got %s", tool.ID, want[tool.ID], tool.Status)
		}
	}
}

// TestTurnsFoldHistory checks the fold rule on synthetic messages: the last
// assistant prose becomes the answer, earlier prose stays a step, and a
// running turn has no answer yet.
func TestTurnsFoldHistory(t *testing.T) {
	snap := reducer.Snapshot{Messages: []reducer.Message{
		{ID: "u1", Role: "user", Text: "第一个问题"},
		{ID: "a1", Role: "assistant", Text: "中间说明"},
		{ID: "a2", Role: "assistant", Text: "最终回答", Calls: []reducer.Call{{ID: "c1", Name: "bash", Args: map[string]any{"command": "ls"}}}},
		{ID: "u2", Role: "user", Text: "第二个问题"},
	}}
	turns := buildTurns(snap)
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].Final != "最终回答" {
		t.Fatalf("want the last prose as the answer, got %q", turns[0].Final)
	}
	if len(turns[0].Steps) != 1 || turns[0].Steps[0] != "中间说明" {
		t.Fatalf("want one step, got %v", turns[0].Steps)
	}
	if len(turns[0].Tools) != 1 || turns[0].Tools[0].Action == "" {
		t.Fatalf("want one tool card, got %v", turns[0].Tools)
	}
	// Blocks keep the order things happened in: the prose came before the
	// call, and the answer is not drawn twice.
	if len(turns[0].Blocks) != 2 || turns[0].Blocks[0].Kind != "say" || turns[0].Blocks[1].Kind != "tools" {
		t.Fatalf("blocks lost their order: %+v", turns[0].Blocks)
	}
	if turns[1].Final != "" {
		t.Fatalf("a turn without an answer must have none, got %q", turns[1].Final)
	}
}

// TestStreamingTextFollowsItsSource guards the live transcript: a step's text
// is read again on every render, so a delta that lands after the row mounted
// repaints it. A captured string froze at the first delta, which is why an
// answer only appeared after reopening the session.
func TestStreamingTextFollowsItsSource(t *testing.T) {
	var push func(string)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		live := core.NewSignal(cx, "第一段")
		push = live.Set
		return core.View{Children: []core.Node{
			core.Func(markdownStepFunc(func() string { return live.Get() }, true)),
			core.Func(markdownViewAt(func() string { return live.Get() })),
		}}
	})
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "第一段")
	})
	push("第一段 追加词")
	app.WaitUntil(5*time.Second, func() bool {
		return strings.Contains(strings.Join(app.Texts(), "\n"), "追加词")
	})
}
