package reducer

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

func frame(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(b)
}

// --- behavior 1: streaming deltas & the ghost-bubble guard ---

func TestMessageUpdateWithoutAgentStartIsDropped(t *testing.T) {
	s := New()
	if s.Apply(frame(t, map[string]any{
		"type":                  "message_update",
		"assistantMessageEvent": map[string]any{"type": "text_delta", "text": "ghost"},
	})) {
		t.Fatal("a frame without agent_start should be ignored")
	}
	if snap := s.Snapshot(); len(snap.Messages) != 0 {
		t.Fatalf("ghost streaming bubble created: %+v", snap.Messages)
	}
}

func TestFullAssistantStream(t *testing.T) {
	s := New()
	steps := []map[string]any{
		{"type": "agent_start"},
		{"type": "message_start", "message": map[string]any{"role": "assistant", "content": []any{}}},
		{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "text": "Hello"}},
		{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "text": " "}},
		{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "text": "World"}},
		{"type": "message_end", "message": map[string]any{"role": "assistant", "content": "Hello World"}},
	}
	for _, step := range steps {
		s.Apply(frame(t, step))
	}
	snap := s.Snapshot()
	if len(snap.Messages) != 1 {
		t.Fatalf("want exactly one assistant message, got %d: %+v", len(snap.Messages), snap.Messages)
	}
	m := snap.Messages[0]
	if m.Role != "assistant" || m.Text != "Hello World" || m.Streaming {
		t.Fatalf("bad final assistant message: %+v", m)
	}
}

func TestDeltaFieldFallback(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	s.Apply(frame(t, map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}}))
	// The web wire uses "delta"; accept it too.
	s.Apply(frame(t, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "abc"}}))
	s.Apply(frame(t, map[string]any{"type": "message_end"}))
	if got := s.Snapshot().Messages[0].Text; got != "abc" {
		t.Fatalf("delta field not accumulated: %q", got)
	}
}

func TestAgentStartEndPhase(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	snap := s.Snapshot()
	if !snap.Running || snap.Phase != "waiting_model" {
		t.Fatalf("agent_start: running=%v phase=%q", snap.Running, snap.Phase)
	}
	s.Apply(frame(t, map[string]any{"type": "agent_end"}))
	snap = s.Snapshot()
	if snap.Running || snap.Phase != "idle" {
		t.Fatalf("agent_end: running=%v phase=%q", snap.Running, snap.Phase)
	}
}

// --- behavior 2: optimistic user bubbles ---

func TestOptimisticUserReplacedNotAppended(t *testing.T) {
	s := New()
	s.OptimisticUser("hello")
	s.Apply(frame(t, map[string]any{
		"type": "message_end", "message": map[string]any{"role": "user", "content": "hello"},
	}))
	snap := s.Snapshot()
	if len(snap.Messages) != 1 {
		t.Fatalf("want one user message, got %d: %+v", len(snap.Messages), snap.Messages)
	}
	if m := snap.Messages[0]; m.Role != "user" || m.Text != "hello" {
		t.Fatalf("bad delivered user message: %+v", m)
	}
	if snap.PendingUserKey != "" {
		t.Fatal("PendingUserKey was not consumed")
	}
}

func TestOptimisticUserQueuedDeletesBubble(t *testing.T) {
	s := New()
	s.OptimisticUser("hello")
	s.Apply(frame(t, map[string]any{
		"type": "message_end", "queued": true,
		"message": map[string]any{"role": "user", "content": "hello"},
	}))
	if snap := s.Snapshot(); len(snap.Messages) != 0 {
		t.Fatalf("queued prompt should delete the bubble, got %+v", snap.Messages)
	}
}

func TestRollbackSendRemovesUnacknowledgedBubble(t *testing.T) {
	s := New()
	s.OptimisticUser("hello")
	if !s.RollbackSend() {
		t.Fatal("an unacknowledged prompt should roll back")
	}
	snap := s.Snapshot()
	if len(snap.Messages) != 0 || snap.Running {
		t.Fatalf("bubble or run state survived the rollback: %+v running=%v", snap.Messages, snap.Running)
	}
}

func TestRollbackSendKeepsDeliveredBubble(t *testing.T) {
	s := New()
	s.OptimisticUser("hello")
	s.Apply(frame(t, map[string]any{
		"type": "message_end", "message": map[string]any{"role": "user", "content": "hello"},
	}))
	if s.RollbackSend() {
		t.Fatal("a delivered prompt must not be rolled back")
	}
	if snap := s.Snapshot(); len(snap.Messages) != 1 {
		t.Fatalf("delivered bubble was removed: %+v", snap.Messages)
	}
}

func TestRollbackSendKeepsBubbleOnceReplyStarted(t *testing.T) {
	s := New()
	s.OptimisticUser("hello")
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	s.Apply(frame(t, map[string]any{"type": "message_start", "message": map[string]any{"role": "assistant"}}))
	s.Apply(frame(t, map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "text": "Hi"}}))
	if s.RollbackSend() {
		t.Fatal("a prompt whose reply already streamed must not be rolled back")
	}
}

func TestUserMessageEndWithNoBubbleAppends(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	s.Apply(frame(t, map[string]any{
		"type": "message_end", "message": map[string]any{"role": "user", "content": "steered"},
	}))
	if snap := s.Snapshot(); len(snap.Messages) != 1 || snap.Messages[0].Text != "steered" {
		t.Fatalf("delivered steering user message missing: %+v", snap.Messages)
	}
}

// --- behavior 3: nested tool filtering & lifecycle ---

func TestNestedToolEventsFiltered(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_start", "toolCallId": "parent", "toolName": "code",
		"args": map[string]any{"code": "run()"},
	}))
	for _, typ := range []string{"tool_execution_start", "tool_execution_update", "tool_execution_end"} {
		s.Apply(frame(t, map[string]any{
			"type": typ, "toolCallId": "parent/1", "toolName": "read",
			"parentToolCallId": "parent", "args": map[string]any{"path": "a.ts"},
			"partialResult": map[string]any{"content": []any{}},
		}))
	}
	snap := s.Snapshot()
	if len(snap.Tools) != 1 {
		t.Fatalf("nested codemode tools leaked to the top level: %+v", snap.Tools)
	}
	parent := snap.Tools[0]
	if parent.ID != "parent" {
		t.Fatalf("the one card should be the parent, got %+v", parent)
	}
	if len(parent.Nested) != 1 {
		t.Fatalf("nested call not attached to its parent: %+v", parent.Nested)
	}
	n := parent.Nested[0]
	if n.ID != "parent/1" || n.Name != "read" || n.Status != "completed" || n.IsError {
		t.Fatalf("nested call state: %+v", n)
	}
	if n.Action == "" {
		t.Fatal("nested call has no formatted action")
	}
}

func TestNestedCallsAttachToParent(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_start", "toolCallId": "code-1", "toolName": "code",
		"args": map[string]any{},
	}))
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_start", "toolCallId": "code-1/1", "toolName": "grep",
		"parentToolCallId": "code-1", "args": map[string]any{"pattern": "todo"},
	}))
	snap := s.Snapshot()
	if len(snap.Tools) != 1 {
		t.Fatalf("nested start created a top-level card: %+v", snap.Tools)
	}
	n := snap.Tools[0].Nested
	if len(n) != 1 || n[0].Status != "running" || n[0].Name != "grep" {
		t.Fatalf("nested start: %+v", n)
	}
	// Output is never stored on a nested call; the update only keeps it live.
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_update", "toolCallId": "code-1/1",
		"parentToolCallId": "code-1", "partialResult": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "a lot of output"}},
		},
	}))
	n = s.Snapshot().Tools[0].Nested
	if len(n) != 1 || n[0].Status != "running" || n[0].Detail == "" {
		t.Fatalf("nested update: %+v", n)
	}
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_end", "toolCallId": "code-1/1",
		"parentToolCallId": "code-1", "isError": false,
	}))
	snap = s.Snapshot()
	if got := snap.Tools[0].Nested[0].Status; got != "completed" {
		t.Fatalf("nested end status = %q", got)
	}
	// A nested call neither drives the phase nor ends the parent card.
	if snap.Tools[0].Status != "running" || snap.Phase != "running_tools" {
		t.Fatalf("nested end touched the parent: status=%q phase=%q", snap.Tools[0].Status, snap.Phase)
	}
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_end", "toolCallId": "code-1", "isError": false,
	}))
	snap = s.Snapshot()
	if snap.Tools[0].Status != "completed" || len(snap.Tools[0].Nested) != 1 {
		t.Fatalf("parent end: %+v", snap.Tools[0])
	}
	// A snapshot owns its Nested slice; mutating it must not reach the store.
	snap.Tools[0].Nested[0].Status = "MUTATED"
	if got := s.Snapshot().Tools[0].Nested[0].Status; got != "completed" {
		t.Fatalf("Nested was not deep-copied: status = %q", got)
	}

	// A nested frame whose parent card never arrived is dropped, never
	// promoted into a card of its own.
	orphan := New()
	for _, typ := range []string{"tool_execution_start", "tool_execution_update", "tool_execution_end"} {
		orphan.Apply(frame(t, map[string]any{
			"type": typ, "toolCallId": "ghost/1", "toolName": "read",
			"parentToolCallId": "ghost", "args": map[string]any{"path": "x.ts"},
		}))
	}
	if snap := orphan.Snapshot(); len(snap.Tools) != 0 {
		t.Fatalf("an orphan nested frame created a top-level card: %+v", snap.Tools)
	}
}

func TestTopLevelToolLifecycle(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_start", "toolCallId": "t1", "toolName": "bash",
		"args": map[string]any{"command": "ls"},
	}))
	if tc := s.Snapshot().Tools[0]; tc.Status != "running" || tc.Action != "ls" {
		t.Fatalf("tool start: %+v", tc)
	}
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_update", "toolCallId": "t1",
		"partialResult": map[string]any{"content": []any{map[string]any{"type": "text", "text": "a.txt\n"}}},
	}))
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_end", "toolCallId": "t1", "isError": false,
	}))
	snap := s.Snapshot()
	if len(snap.Tools) != 1 {
		t.Fatalf("want one tool card, got %d", len(snap.Tools))
	}
	tc := snap.Tools[0]
	if tc.Status != "completed" || tc.IsError {
		t.Fatalf("tool end status: %+v", tc)
	}
	if tc.Partial == "" {
		t.Fatal("partialResult text was not accumulated")
	}
}

func TestToolFailed(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "tool_execution_start", "toolCallId": "t", "toolName": "bash", "args": map[string]any{}}))
	s.Apply(frame(t, map[string]any{"type": "tool_execution_end", "toolCallId": "t", "isError": true}))
	if tc := s.Snapshot().Tools[0]; tc.Status != "failed" || !tc.IsError {
		t.Fatalf("failed tool: %+v", tc)
	}
}

// --- behavior 4: queue, retry/compaction, bash, extension UI ---

func TestQueueUpdate(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type":     "queue_update",
		"steering": []any{"a", "b"},
		"followUp": []any{map[string]any{"content": "c"}, map[string]any{"text": "d"}},
	}))
	snap := s.Snapshot()
	if !reflect.DeepEqual(snap.Steering, []string{"a", "b"}) {
		t.Fatalf("steering = %#v", snap.Steering)
	}
	if !reflect.DeepEqual(snap.FollowUp, []string{"c", "d"}) {
		t.Fatalf("followUp = %#v", snap.FollowUp)
	}
}

func TestRetryAndCompactionBanners(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "auto_retry_start", "attempt": 2, "errorMessage": "rate limit"}))
	snap := s.Snapshot()
	if snap.Retry == nil || snap.Retry.Attempt != 2 || snap.Retry.Message != "rate limit" || !snap.Retry.WillRetry {
		t.Fatalf("retry banner: %+v", snap.Retry)
	}
	if snap.Phase != "retrying" {
		t.Fatalf("retry phase = %q", snap.Phase)
	}
	s.Apply(frame(t, map[string]any{"type": "auto_retry_end"}))
	if s.Snapshot().Retry != nil {
		t.Fatal("retry banner not cleared")
	}

	s.Apply(frame(t, map[string]any{"type": "compaction_start"}))
	snap = s.Snapshot()
	if snap.Compaction == "" || snap.Phase != "compacting" {
		t.Fatalf("compaction start: %q / %q", snap.Compaction, snap.Phase)
	}
	s.Apply(frame(t, map[string]any{"type": "compaction_end"}))
	if s.Snapshot().Compaction != "" {
		t.Fatal("compaction banner not cleared on success")
	}
	s.Apply(frame(t, map[string]any{"type": "compaction_start"}))
	s.Apply(frame(t, map[string]any{"type": "compaction_end", "errorMessage": "boom"}))
	if got := s.Snapshot().Compaction; got != "boom" {
		t.Fatalf("compaction error banner = %q", got)
	}
}

func TestBashOutputAppends(t *testing.T) {
	s := New()
	// "output" is the authoritative field (rpc-manager.ts:480); "text" is a
	// defensive fallback. Both append to the single BashOutput string.
	s.Apply(frame(t, map[string]any{"type": "bash_output", "command": "!ls", "output": "a.txt\n"}))
	s.Apply(frame(t, map[string]any{"type": "bash_output", "command": "!ls", "text": "b.txt\n"}))
	if got := s.Snapshot().BashOutput; got != "a.txt\nb.txt\n" {
		t.Fatalf("BashOutput = %q", got)
	}
}

func TestExtensionRequestEnqueueAndClose(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type": "extension_ui_request", "id": "e1", "method": "confirm",
		"title": "Sure?", "message": "Do it", "timeout": 30,
	}))
	snap := s.Snapshot()
	if len(snap.ExtensionRequests) != 1 {
		t.Fatalf("blocking request not enqueued: %+v", snap.ExtensionRequests)
	}
	r := snap.ExtensionRequests[0]
	if r.ID != "e1" || r.Method != "confirm" || r.Title != "Sure?" || r.Message != "Do it" || r.Timeout != 30 {
		t.Fatalf("request fields: %+v", r)
	}
	s.Apply(frame(t, map[string]any{"type": "extension_ui_closed", "id": "e1"}))
	if got := len(s.Snapshot().ExtensionRequests); got != 0 {
		t.Fatalf("extension_ui_closed did not dequeue, %d remain", got)
	}
}

func TestNonBlockingExtensionMethodsDoNotEnqueue(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "extension_ui_request", "method": "setStatus", "statusKey": "k", "statusText": "busy"}))
	if got := s.Snapshot().StatusText; got != "busy" {
		t.Fatalf("setStatus StatusText = %q", got)
	}
	s.Apply(frame(t, map[string]any{"type": "extension_ui_request", "method": "setWidget", "widgetKey": "w", "widgetLines": []any{"l1", "l2"}}))
	if got := s.Snapshot().WidgetLines; !reflect.DeepEqual(got, []string{"l1", "l2"}) {
		t.Fatalf("setWidget WidgetLines = %#v", got)
	}
	s.Apply(frame(t, map[string]any{"type": "extension_ui_request", "method": "setTitle", "title": "New Title"}))
	if got := s.Snapshot().Title; got != "New Title" {
		t.Fatalf("setTitle Title = %q", got)
	}
	s.Apply(frame(t, map[string]any{"type": "extension_ui_request", "method": "notify", "message": "note", "notifyType": "warning"}))
	s.Apply(frame(t, map[string]any{"type": "extension_ui_request", "method": "set_editor_text", "text": "draft"}))
	if got := len(s.Snapshot().ExtensionRequests); got != 0 {
		t.Fatalf("non-blocking methods must not enqueue, %d queued", got)
	}
}

// --- behavior 5: malformed frames, deep copies, misc API ---

func TestResponseFrameIgnored(t *testing.T) {
	s := New()
	if s.Apply(frame(t, map[string]any{"type": "response", "command": "prompt", "success": true})) {
		t.Fatal("response frames must be ignored (false)")
	}
}

func TestMalformedFramesNeverPanic(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	raws := []string{
		"null", "[]", "not json", "123", `"a string"`, "true", "",
		`{"type":"message_update","assistantMessageEvent":"bad"}`,
		`{"type":"message_end","message":123}`,
		`{"type":"tool_execution_start","args":"bad","toolCallId":123,"toolName":[]}`,
		`{"type":"tool_execution_update","partialResult":42}`,
		`{"type":"queue_update","steering":123,"followUp":{}}`,
		`{"type":"extension_ui_request","method":"select","options":{}}`,
		`{"type":"extension_ui_request","method":"confirm","title":[]}`,
		`{"type":"bash_output","output":123}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","text":123}}`,
		`{"type":"auto_retry_start","attempt":"bad"}`,
	}
	for _, raw := range raws {
		s.Apply(json.RawMessage(raw)) // must not panic
	}
	_ = s.Snapshot()
}

func TestUserKeyAndIsNested(t *testing.T) {
	if UserKey("hi", 0) != UserKey("hi", 0) {
		t.Fatal("UserKey is not deterministic")
	}
	if UserKey("hi", 0) == UserKey("hi", 1) || UserKey("a", 0) == UserKey("b", 0) {
		t.Fatal("UserKey does not distinguish text/images")
	}
	if !IsNested(frame(t, map[string]any{"type": "tool_execution_start", "parentToolCallId": "p"})) {
		t.Fatal("nested tool not detected")
	}
	if IsNested(frame(t, map[string]any{"type": "tool_execution_end", "parentToolCallId": ""})) {
		t.Fatal("empty parent id is not nested")
	}
	if IsNested(frame(t, map[string]any{"type": "queue_update", "parentToolCallId": "p"})) {
		t.Fatal("non-tool event is not nested")
	}
	if IsNested(frame(t, map[string]any{"type": "tool_execution_end", "parentToolCallId": 123})) {
		t.Fatal("non-string parent id is not nested")
	}
	if IsNested(json.RawMessage("null")) {
		t.Fatal("null frame is not nested")
	}
}

func TestSnapshotIsDeepCopy(t *testing.T) {
	s := New()
	s.Apply(frame(t, map[string]any{
		"type": "tool_execution_start", "toolCallId": "t", "toolName": "bash",
		"args": map[string]any{"command": "ls", "nested": map[string]any{"n": 1}},
	}))
	s.Apply(frame(t, map[string]any{
		"type": "extension_ui_request", "id": "e", "method": "select",
		"title": "T", "options": []any{"a", "b"},
	}))
	snap := s.Snapshot()
	snap.Tools[0].Args["command"] = "MUTATED"
	snap.Tools[0].Args["nested"].(map[string]any)["n"] = 99
	snap.ExtensionRequests[0].Options[0] = "MUTATED"
	snap2 := s.Snapshot()
	if snap2.Tools[0].Args["command"] == "MUTATED" || snap2.Tools[0].Args["nested"].(map[string]any)["n"] == 99 {
		t.Fatal("ToolCall.Args was not deep-copied")
	}
	if snap2.ExtensionRequests[0].Options[0] == "MUTATED" {
		t.Fatal("ExtensionRequest.Options was not deep-copied")
	}
}

func TestResetAndSetState(t *testing.T) {
	s := New()
	s.OptimisticUser("x")
	s.Apply(frame(t, map[string]any{"type": "agent_start"}))
	s.SetState([]Message{
		{ID: "1", Role: "user", Text: "from file"},
		{ID: "2", Role: "assistant", Text: "reply", Streaming: true},
	})
	snap := s.Snapshot()
	if len(snap.Messages) != 2 || snap.Running || snap.Phase != "idle" || snap.PendingUserKey != "" {
		t.Fatalf("SetState did not reset transient state: %+v", snap)
	}
	for _, m := range snap.Messages {
		if m.Streaming {
			t.Fatal("rebuilt message must not be streaming")
		}
	}
	s.Reset()
	if len(s.Snapshot().Messages) != 0 {
		t.Fatal("Reset did not clear messages")
	}
}

func TestConcurrentApplyAndSnapshot(t *testing.T) {
	s := New()
	raw, _ := json.Marshal(map[string]any{"type": "queue_update", "steering": []any{"x"}})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Apply(json.RawMessage(raw))
			_ = s.Snapshot()
		}()
	}
	wg.Wait()
}
