// Package reducer is the client-side agent event reducer for the native app.
// It consumes one raw event frame at a time (the AgentSessionEvent JSON as sent
// over the agent channel) and maintains the chat state: messages, tool cards,
// queues, retry/compaction banners, `!` output, extension UI requests and the
// window status/title/widget. It is a native port of handleAgentEvent in
// hooks/useAgentSession.ts, with the streaming text of streaming-message.ts.
//
// All state is guarded by a mutex; Snapshot returns a deep copy that is safe to
// read after the lock is released. Apply never panics on malformed frames.
package reducer

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"pigo/internal/toolfields"
)

// Message is one chat bubble. Role is "user", "assistant" or "toolResult".
type Message struct {
	ID, Role, Text       string // role: user | assistant | toolResult
	Streaming            bool
	Thinking             string
	ToolName, ToolCallID string
	// EntryID is pi's conversation entry id when get_messages reports one;
	// fork needs it, and a synthetic "history-N" id cannot be forked.
	EntryID string
	// IsError marks a failed toolResult message, so history replays keep
	// a failed tool card red instead of repainting it completed.
	IsError bool
	// Calls are the tool calls an assistant message carries. History replay
	// reads them from get_messages, where a call is a content block rather
	// than an event; the live path still fills Tools.
	Calls []Call
	// Usage is what the message cost, when pi reported it: new input tokens,
	// output tokens and tokens read from the prompt cache. Cache reads are
	// kept apart because they are usually most of a request's input and say
	// nothing about what the user just asked.
	Usage Usage
}

// Usage is one message's token counts.
type Usage struct{ In, Out, Cache int }

// Call is one tool call read from an assistant message's content.
type Call struct {
	ID, Name string
	Args     map[string]any
}

// ToolCall is one tool card. Status is running, completed, failed or cancelled.
type ToolCall struct {
	ID, Name, Action, Detail string
	Status                   string
	Args                     map[string]any
	Partial                  string
	IsError                  bool
	// Nested are the codemode (nested) calls this card ran. A nested call is
	// a summary line only — its output is never accumulated — so a card that
	// ran hundreds of sub-calls stays small.
	Nested []NestedCall
}

// NestedCall is one nested codemode execution inside a parent tool card. It
// carries the call's identity and a one-line summary (Action/Detail from
// toolfields.Format), never its output.
type NestedCall struct {
	ID, Name, Action, Detail, Status string
	IsError                          bool
}

// maxNestedCalls bounds the nested calls kept per parent card. A codemode run
// that fans out further stops recording new sub-calls (existing ones still
// update), so one pathological run cannot grow a card without limit.
const maxNestedCalls = 200

// ExtensionRequest is one extension_ui_request. Blocking methods
// (select/confirm/input/editor/custom) wait in ExtensionRequests for a
// extension_ui_response; the "set*"/notify/set_editor_text methods are applied
// directly to StatusText/WidgetLines/Title instead.
type ExtensionRequest struct {
	ID, Method, Title, Message, Placeholder, Prefill, Text string
	Options                                                []string
	StatusKey, StatusText, WidgetKey                       string
	WidgetLines                                            []string
	WidgetPlacement, NotifyType                            string
	Timeout                                                float64
}

// RetryInfo is the auto-retry banner: the attempt number, its message and
// whether another attempt will follow.
type RetryInfo struct {
	Attempt   int
	Message   string
	WillRetry bool
}

// Snapshot is a deep, concurrency-safe copy of the whole chat state.
type Snapshot struct {
	Messages          []Message
	Tools             []ToolCall
	Steering          []string
	FollowUp          []string
	Running           bool
	Phase             string // idle | waiting_model | streaming_text | running_tools | retrying | compacting
	Retry             *RetryInfo
	Compaction        string
	BashOutput        string
	ExtensionRequests []ExtensionRequest
	PendingUserKey    string
	StatusText        string
	WidgetLines       []string
	Title             string
	// LastError is the newest prompt_error's message, cleared by the next
	// agent_start: a hard model failure (401, quota) that auto-retry never
	// covers and that the transcript must show.
	LastError string
}

// Store holds the reducer state. Use New to create one; all methods are safe
// for concurrent use.
type Store struct {
	mu sync.Mutex

	messages    []Message
	tools       []ToolCall
	steering    []string
	followUp    []string
	running     bool
	phase       string
	retry       *RetryInfo
	compaction  string
	bashOutput  string
	extRequests []ExtensionRequest
	pendingKey  string
	statusText  string
	widgetLines []string
	title       string

	// armed gates streaming events: it is set only by agent_start and cleared
	// by agent_end, so message_start/message_update arriving without a run are
	// dropped and cannot resurrect a ghost streaming bubble.
	armed        bool
	streamingIdx int             // index of the live streaming assistant message, -1 if none
	textSB       strings.Builder // accumulates streaming text (large deltas, no repeated copies)
	bashSB       strings.Builder // accumulates `!` command output
	lastError    string
	nextID       int
}

// New returns an empty, idle Store.
func New() *Store {
	return &Store{phase: "idle", streamingIdx: -1}
}

// Reset clears every field back to the initial idle state.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
}

// resetLocked is the lock-free body shared by Reset and SetState.
func (s *Store) resetLocked() {
	s.messages = nil
	s.tools = nil
	s.steering = nil
	s.followUp = nil
	s.running = false
	s.phase = "idle"
	s.retry = nil
	s.compaction = ""
	s.bashOutput = ""
	s.extRequests = nil
	s.pendingKey = ""
	s.statusText = ""
	s.widgetLines = nil
	s.title = ""
	s.armed = false
	s.streamingIdx = -1
	s.textSB.Reset()
	s.bashSB.Reset()
	s.lastError = ""
	s.nextID = 0
}

func (s *Store) genID() string {
	s.nextID++
	return "m" + strconv.Itoa(s.nextID)
}

// OptimisticUser inserts the optimistic user bubble when a prompt is sent and
// records its PendingUserKey (the text + images signature) so message_end can
// consume only that still-adjacent bubble. It also marks a run as active (as
// the web client's handleSend does) but does NOT arm streaming events: those
// still require an explicit agent_start.
func (s *Store) OptimisticUser(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finalizeStreaming("")
	s.messages = append(s.messages, Message{
		ID: s.genID(), Role: "user", Text: text, Streaming: false,
	})
	s.pendingKey = UserKey(text, 0)
	s.running = true
	s.phase = "waiting_model"
}

// SetState rebuilds the chat from a get_messages snapshot after a reconnect:
// all transient event state is cleared and only the supplied messages remain,
// never streaming.
func (s *Store) SetState(messages []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
	s.messages = make([]Message, len(messages))
	for i, m := range messages {
		m.Streaming = false
		s.messages[i] = m
	}
}

// Snapshot returns a deep copy of the whole state, safe to read concurrently.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := Snapshot{
		Running:        s.running,
		Phase:          s.phase,
		Compaction:     s.compaction,
		BashOutput:     s.bashOutput,
		PendingUserKey: s.pendingKey,
		StatusText:     s.statusText,
		Title:          s.title,
		LastError:      s.lastError,
	}
	out.Messages = make([]Message, len(s.messages))
	copy(out.Messages, s.messages)
	out.Tools = make([]ToolCall, len(s.tools))
	for i, tc := range s.tools {
		out.Tools[i] = tc
		out.Tools[i].Args = cloneMap(tc.Args)
		if len(tc.Nested) > 0 {
			out.Tools[i].Nested = make([]NestedCall, len(tc.Nested))
			copy(out.Tools[i].Nested, tc.Nested)
		}
	}
	out.Steering = cloneStrings(s.steering)
	out.FollowUp = cloneStrings(s.followUp)
	if s.retry != nil {
		r := *s.retry
		out.Retry = &r
	}
	out.ExtensionRequests = make([]ExtensionRequest, len(s.extRequests))
	for i, r := range s.extRequests {
		out.ExtensionRequests[i] = r
		out.ExtensionRequests[i].Options = cloneStrings(r.Options)
		out.ExtensionRequests[i].WidgetLines = cloneStrings(r.WidgetLines)
	}
	out.WidgetLines = cloneStrings(s.widgetLines)
	return out
}

// UserKey builds the optimistic-bubble key: the text and image count, mirroring
// userMessageKey's {text, images} signature. It is used to consume only the
// last, adjacent optimistic bubble on a user message_end.
func UserKey(text string, images int) string {
	b, _ := json.Marshal(struct {
		Text   string `json:"text"`
		Images int    `json:"images"`
	}{text, images})
	return string(b)
}

// IsNested reports whether a frame is a nested (codemode) tool execution: one
// of the three tool_execution_* types with a non-empty parentToolCallId string.
// Such calls belong to their parent's card and are filtered from the top level.
func IsNested(frame json.RawMessage) (nested bool) {
	defer func() {
		if r := recover(); r != nil {
			nested = false
		}
	}()
	return isNestedFields(decodeFrame(frame))
}

// Apply consumes one raw frame. Response frames and unknown/malformed frames
// are ignored (returning false). It returns whether the UI needs a refresh.
// A recover guard makes it impossible for a malformed frame to panic the app.
func (s *Store) Apply(frame json.RawMessage) (changed bool) {
	defer func() {
		if r := recover(); r != nil {
			changed = false
		}
	}()
	obj := decodeFrame(frame)
	if obj == nil {
		return false
	}
	typ := str(obj, "type")
	if typ == "" || typ == "response" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	switch typ {
	case "agent_start":
		return s.onAgentStart()
	case "agent_end":
		return s.onAgentEnd()
	case "connected":
		return s.onConnected(obj)
	case "message_start":
		return s.onMessageStart(obj)
	case "message_update":
		return s.onMessageUpdate(obj)
	case "message_end":
		return s.onMessageEnd(obj)
	case "tool_execution_start":
		return s.onToolStart(obj)
	case "tool_execution_update":
		return s.onToolUpdate(obj)
	case "tool_execution_end":
		return s.onToolEnd(obj)
	case "queue_update":
		s.steering = queueTexts(obj["steering"])
		s.followUp = queueTexts(obj["followUp"])
		return true
	case "auto_retry_start", "summarization_retry_scheduled":
		s.retry = &RetryInfo{
			Attempt: int(num(obj, "attempt")), Message: str(obj, "errorMessage"), WillRetry: true,
		}
		s.phase = "retrying"
		return true
	case "auto_retry_end", "summarization_retry_attempt_start", "summarization_retry_finished":
		return s.clearRetry()
	case "compaction_start", "auto_compaction_start":
		reason := str(obj, "reason")
		if reason != "" {
			s.compaction = "Compacting (" + reason + ")…"
		} else {
			s.compaction = "Compacting context…"
		}
		s.phase = "compacting"
		return true
	case "compaction_end", "auto_compaction_end":
		if msg := str(obj, "errorMessage"); msg != "" {
			s.compaction = msg
		} else {
			s.compaction = ""
		}
		s.retry = nil
		s.phase = "waiting_model"
		return true
	case "bash_output":
		return s.onBashOutput(obj)
	case "extension_ui_request":
		return s.onExtensionRequest(obj)
	case "extension_ui_closed":
		return s.onExtensionClosed(obj)
	case "extension_error":
		return s.onExtensionError(obj)
	case "prompt_error":
		msg := str(obj, "errorMessage")
		if msg == "" {
			msg = str(obj, "error")
		}
		if msg == "" {
			msg = "Command failed"
		}
		s.statusText = msg
		s.lastError = msg
		return true
	default:
		return false
	}
}

// onAgentStart opens a run: running, waiting for the model, retry banner gone.
func (s *Store) onAgentStart() bool {
	s.finalizeStreaming("")
	s.running = true
	s.phase = "waiting_model"
	s.retry = nil
	s.lastError = ""
	s.armed = true
	return true
}

// onAgentEnd closes a run: idle, retry gone, streaming disarmed.
func (s *Store) onAgentEnd() bool {
	s.running = false
	s.phase = "idle"
	s.retry = nil
	s.armed = false
	return true
}

// onConnected resumes a streaming run after a reconnect (isStreaming=true) and
// retains only the extension requests the server still holds.
func (s *Store) onConnected(obj map[string]any) bool {
	if truthy(obj["isStreaming"]) {
		s.running = true
		s.phase = "waiting_model"
		s.armed = true
	}
	if raw, ok := obj["pendingExtensionUiIds"].([]any); ok {
		keep := make(map[string]bool, len(raw))
		for _, id := range raw {
			if v, ok := id.(string); ok {
				keep[v] = true
			}
		}
		filtered := make([]ExtensionRequest, 0, len(s.extRequests))
		for _, r := range s.extRequests {
			if keep[r.ID] {
				filtered = append(filtered, r)
			}
		}
		s.extRequests = filtered
	}
	return true
}

// onMessageStart sets up the streaming assistant bubble. Without agent_start
// the frame is dropped entirely (ghost-bubble guard); transcript system and
// user starts are ignored as in the web client.
func (s *Store) onMessageStart(obj map[string]any) bool {
	if !s.armed {
		return false
	}
	msg := objField(obj, "message")
	role := str(msg, "role")
	if role == "system" || role == "user" {
		return false
	}
	if role == "assistant" {
		s.finalizeStreaming("")
		s.ensureStreaming("")
		s.phase = "streaming_text"
		return true
	}
	return false
}

// onMessageUpdate accumulates text into the current streaming assistant
// message (never creating a second bubble for a delta). text_delta appends;
// text_end carries the authoritative block text; text_start just ensures a
// bubble exists.
func (s *Store) onMessageUpdate(obj map[string]any) bool {
	if !s.armed {
		return false
	}
	ame := objField(obj, "assistantMessageEvent")
	switch str(ame, "type") {
	case "text_delta":
		s.appendDelta(deltaText(ame))
		return true
	case "text_start":
		s.ensureStreaming("")
		return true
	case "text_end":
		s.ensureStreaming("")
		if c := textOf(ame["content"]); c != "" {
			s.textSB.Reset()
			s.textSB.WriteString(c)
			if s.hasStreaming() {
				s.messages[s.streamingIdx].Text = s.textSB.String()
			}
		}
		s.phase = "streaming_text"
		return true
	default:
		return false
	}
}

// onMessageEnd finalizes a completed message. A user message consumes the last
// adjacent optimistic bubble (replacing it with the real message, or deleting
// it when queued) instead of appending a duplicate. Other messages end the
// stream and are appended when there is no live streaming bubble.
func (s *Store) onMessageEnd(obj map[string]any) bool {
	if !s.running {
		return false
	}
	msg := objField(obj, "message")
	if str(msg, "role") == "system" {
		return false
	}
	if truthy(obj["queued"]) || truthy(msg["queued"]) {
		return s.dropOptimistic()
	}
	if str(msg, "role") == "user" {
		return s.onUserEnd(msg)
	}

	finalText := textOf(msg["content"])
	if str(msg, "role") == "assistant" && s.hasStreaming() {
		if s.textSB.Len() > 0 {
			s.messages[s.streamingIdx].Text = s.textSB.String()
		} else if finalText != "" {
			s.messages[s.streamingIdx].Text = finalText
		}
		s.messages[s.streamingIdx].Streaming = false
		if u := usageOf(msg); u != (Usage{}) {
			s.messages[s.streamingIdx].Usage = u
		}
		s.streamingIdx = -1
		s.textSB.Reset()
	} else if msg != nil {
		s.messages = append(s.messages, s.buildMessage(msg))
	} else {
		s.finalizeStreaming("")
	}
	s.phase = "waiting_model"
	return true
}

// onUserEnd replaces the adjacent optimistic bubble with the delivered user
// message. If no bubble is adjacent, the delivered message is appended (a
// steering/follow-up delivery must still render).
func (s *Store) onUserEnd(msg map[string]any) bool {
	pending := s.pendingKey
	s.pendingKey = ""
	delivered := s.buildMessage(msg)

	if len(s.messages) > 0 && pending != "" {
		last := &s.messages[len(s.messages)-1]
		if last.Role == "user" && UserKey(last.Text, 0) == pending {
			*last = delivered // replace, never append a duplicate
			return true
		}
	}
	s.messages = append(s.messages, delivered)
	return true
}

// dropOptimistic removes the last bubble when it is the one recorded by
// OptimisticUser (the queued-prompt case clears the bubble entirely).
func (s *Store) dropOptimistic() bool {
	pending := s.pendingKey
	s.pendingKey = ""
	if pending != "" && len(s.messages) > 0 {
		last := &s.messages[len(s.messages)-1]
		if last.Role == "user" && UserKey(last.Text, 0) == pending {
			s.messages = s.messages[:len(s.messages)-1]
		}
	}
	return pending != ""
}

// RollbackSend removes the optimistic bubble after the prompt's preflight
// failed, but only while pi has not acknowledged or started answering it.
// Once the user message was delivered (pendingKey consumed) or any tool /
// reply started, the prompt may have reached the agent: the caller must
// reconcile and keep the bubble instead of hiding a real turn.
func (s *Store) RollbackSend() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingKey == "" || s.anyRunningTool() || s.hasStreaming() {
		return false
	}
	s.dropOptimistic()
	s.running = false
	s.phase = ""
	return true
}

// buildMessage converts a message object from a frame into a Message.
func (s *Store) buildMessage(msg map[string]any) Message {
	text, _ := contentInfo(msg["content"])
	id := str(msg, "id")
	if id == "" {
		id = str(msg, "entryId")
	}
	if id == "" {
		id = s.genID()
	}
	return Message{
		ID:         id,
		Role:       str(msg, "role"),
		Text:       text,
		Streaming:  false,
		ToolName:   str(msg, "toolName"),
		ToolCallID: str(msg, "toolCallId"),
		Usage:      usageOf(msg),
	}
}

// usageOf reads a message's usage, whichever of the two shapes pi sends:
// the fields at the top or under "usage".
func usageOf(msg map[string]any) Usage {
	raw, _ := msg["usage"].(map[string]any)
	if raw == nil {
		return Usage{}
	}
	num := func(key string) int {
		if v, ok := raw[key].(float64); ok {
			return int(v)
		}
		return 0
	}
	return Usage{In: num("input"), Out: num("output"), Cache: num("cacheRead") + num("cacheWrite")}
}

func (s *Store) hasStreaming() bool {
	return s.streamingIdx >= 0 && s.streamingIdx < len(s.messages)
}

// ensureStreaming creates the single live assistant bubble if none exists.
func (s *Store) ensureStreaming(initial string) {
	if s.hasStreaming() {
		return
	}
	s.textSB.Reset()
	s.textSB.WriteString(initial)
	s.messages = append(s.messages, Message{
		ID: s.genID(), Role: "assistant", Text: s.textSB.String(), Streaming: true,
	})
	s.streamingIdx = len(s.messages) - 1
}

// appendDelta appends one text chunk to the streaming bubble. A strings.Builder
// avoids re-copying the whole (potentially multi-megabyte) accumulated text.
func (s *Store) appendDelta(text string) {
	s.ensureStreaming("")
	if text == "" {
		return
	}
	s.textSB.WriteString(text)
	if s.hasStreaming() {
		s.messages[s.streamingIdx].Text = s.textSB.String()
	}
	s.phase = "streaming_text"
}

// finalizeStreaming closes the live bubble (used at run/message boundaries).
func (s *Store) finalizeStreaming(fallback string) {
	if s.hasStreaming() {
		if s.textSB.Len() > 0 {
			s.messages[s.streamingIdx].Text = s.textSB.String()
		} else if fallback != "" {
			s.messages[s.streamingIdx].Text = fallback
		}
		s.messages[s.streamingIdx].Streaming = false
	}
	s.streamingIdx = -1
	s.textSB.Reset()
}

func (s *Store) toolIdx(id string) int {
	for i := range s.tools {
		if s.tools[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) anyRunningTool() bool {
	for i := range s.tools {
		if s.tools[i].Status == "running" {
			return true
		}
	}
	return false
}

// onToolStart opens a top-level tool card. Nested codemode calls belong to
// their parent's card and are recorded there instead of in Tools.
func (s *Store) onToolStart(obj map[string]any) bool {
	if isNestedFields(obj) {
		return s.onNestedStart(obj)
	}
	id := str(obj, "toolCallId")
	if id == "" {
		id = s.genID()
	}
	name := str(obj, "toolName")
	args := cloneMap(objField(obj, "args"))
	action, detail := toolfields.Format(name, args)
	if idx := s.toolIdx(id); idx >= 0 {
		s.tools[idx].Name = name
		s.tools[idx].Args = args
		s.tools[idx].Action = action
		s.tools[idx].Detail = detail
		s.tools[idx].Status = "running"
	} else {
		s.tools = append(s.tools, ToolCall{
			ID: id, Name: name, Action: action, Detail: detail,
			Status: "running", Args: args,
		})
	}
	s.phase = "running_tools"
	return true
}

// nestedParent locates a frame's parent card. -1 means the parent is not
// known, and the nested frame must be dropped: a nested call never creates a
// top-level card of its own.
func (s *Store) nestedParent(obj map[string]any) int {
	parent := str(obj, "parentToolCallId")
	if parent == "" {
		return -1
	}
	return s.toolIdx(parent)
}

// nestedCallIdx finds a nested call inside its parent card. pidx is -1 when
// the parent is unknown, nidx -1 when the parent exists but the call does not.
func (s *Store) nestedCallIdx(obj map[string]any) (pidx, nidx int) {
	pidx = s.nestedParent(obj)
	if pidx < 0 {
		return -1, -1
	}
	id := str(obj, "toolCallId")
	if id == "" {
		return pidx, -1
	}
	for i := range s.tools[pidx].Nested {
		if s.tools[pidx].Nested[i].ID == id {
			return pidx, i
		}
	}
	return pidx, -1
}

// onNestedStart records a nested codemode call on its parent's card, keyed by
// toolCallId so a repeated start updates the row in place.
func (s *Store) onNestedStart(obj map[string]any) bool {
	pidx := s.nestedParent(obj)
	if pidx < 0 {
		return false
	}
	id := str(obj, "toolCallId")
	if id == "" {
		return false
	}
	name := str(obj, "toolName")
	action, detail := toolfields.Format(name, cloneMap(objField(obj, "args")))
	parent := &s.tools[pidx]
	for i := range parent.Nested {
		if parent.Nested[i].ID == id {
			parent.Nested[i].Name = name
			parent.Nested[i].Action = action
			parent.Nested[i].Detail = detail
			parent.Nested[i].Status = "running"
			parent.Nested[i].IsError = false
			return true
		}
	}
	if len(parent.Nested) >= maxNestedCalls {
		return false
	}
	parent.Nested = append(parent.Nested, NestedCall{
		ID: id, Name: name, Action: action, Detail: detail, Status: "running",
	})
	return true
}

// onNestedUpdate keeps a nested call's status on running. Its partialResult is
// deliberately not accumulated: the row is a one-line summary, and keeping
// every chunk of output would grow a codemode card without showing any of it.
func (s *Store) onNestedUpdate(obj map[string]any) bool {
	pidx, nidx := s.nestedCallIdx(obj)
	if nidx < 0 {
		return false
	}
	n := &s.tools[pidx].Nested[nidx]
	changed := n.Status != "running"
	n.Status = "running"
	// A start frame may have been missed (reconnect); refresh the summary
	// from this frame when the row carries no name yet.
	if name := str(obj, "toolName"); name != "" && n.Name == "" {
		n.Name = name
		n.Action, n.Detail = toolfields.Format(name, cloneMap(objField(obj, "args")))
		changed = true
	}
	return changed
}

// onNestedEnd marks a nested call completed/failed/cancelled. An end whose
// start was never seen still appends the finished row so the parent lists it.
func (s *Store) onNestedEnd(obj map[string]any) bool {
	pidx := s.nestedParent(obj)
	if pidx < 0 {
		return false
	}
	id := str(obj, "toolCallId")
	if id == "" {
		return false
	}
	isErr := truthy(obj["isError"])
	status := "completed"
	if isErr {
		status = "failed"
	}
	if truthy(obj["cancelled"]) || truthy(obj["aborted"]) {
		status = "cancelled"
	}
	parent := &s.tools[pidx]
	for i := range parent.Nested {
		if parent.Nested[i].ID == id {
			parent.Nested[i].Status = status
			parent.Nested[i].IsError = isErr
			return true
		}
	}
	if len(parent.Nested) >= maxNestedCalls {
		return false
	}
	name := str(obj, "toolName")
	action, detail := toolfields.Format(name, cloneMap(objField(obj, "args")))
	parent.Nested = append(parent.Nested, NestedCall{
		ID: id, Name: name, Action: action, Detail: detail, Status: status, IsError: isErr,
	})
	return true
}

// onToolUpdate accumulates partialResult text into the tool's Partial field.
func (s *Store) onToolUpdate(obj map[string]any) bool {
	if isNestedFields(obj) {
		return s.onNestedUpdate(obj)
	}
	id := str(obj, "toolCallId")
	idx := s.toolIdx(id)
	if idx < 0 {
		s.tools = append(s.tools, ToolCall{
			ID: id, Name: str(obj, "toolName"), Status: "running",
		})
		idx = len(s.tools) - 1
	}
	if pr := objField(obj, "partialResult"); pr != nil {
		s.tools[idx].Partial += textOf(pr["content"])
		if truthy(pr["isError"]) {
			s.tools[idx].IsError = true
		}
	}
	return true
}

// onToolEnd marks a tool completed/failed/cancelled and restores the phase
// when no tool is still running.
func (s *Store) onToolEnd(obj map[string]any) bool {
	if isNestedFields(obj) {
		return s.onNestedEnd(obj)
	}
	id := str(obj, "toolCallId")
	isErr := truthy(obj["isError"])
	status := "completed"
	if isErr {
		status = "failed"
	}
	if truthy(obj["cancelled"]) || truthy(obj["aborted"]) {
		status = "cancelled"
	}
	idx := s.toolIdx(id)
	if idx < 0 {
		s.tools = append(s.tools, ToolCall{
			ID: id, Name: str(obj, "toolName"), Status: status, IsError: isErr,
		})
	} else {
		s.tools[idx].Status = status
		s.tools[idx].IsError = isErr
		if name := str(obj, "toolName"); name != "" {
			s.tools[idx].Name = name
		}
	}
	if !s.anyRunningTool() {
		s.phase = "waiting_model"
	}
	return true
}

func (s *Store) clearRetry() bool {
	s.retry = nil
	if s.phase == "retrying" {
		s.phase = "waiting_model"
	}
	return true
}

// onBashOutput appends the `!` command output tail. The authoritative field is
// "output" (see bashOutputText); a strings.Builder avoids repeated big copies.
func (s *Store) onBashOutput(obj map[string]any) bool {
	out, ok := bashOutputText(obj)
	if !ok {
		return false
	}
	s.bashSB.WriteString(out)
	s.bashOutput = s.bashSB.String()
	return true
}

// onExtensionRequest enqueues blocking requests and applies the non-blocking
// "set*" / notify / set_editor_text methods directly (no response needed).
func (s *Store) onExtensionRequest(obj map[string]any) bool {
	r := parseExtensionRequest(obj)
	switch r.Method {
	case "select", "confirm", "input", "editor", "custom":
		s.enqueueExt(r)
		return true
	case "notify":
		s.statusText = r.Message
		return true
	case "setStatus":
		s.statusText = r.StatusText
		return true
	case "setWidget":
		s.widgetLines = cloneStrings(r.WidgetLines)
		return true
	case "setTitle":
		s.title = r.Title
		return true
	case "set_editor_text":
		s.statusText = r.Text
		return true
	default:
		return false
	}
}

// enqueueExt dedups by id; a custom panel re-render updates in place.
func (s *Store) enqueueExt(r ExtensionRequest) {
	if r.ID == "" {
		s.extRequests = append(s.extRequests, r)
		return
	}
	for i := range s.extRequests {
		if s.extRequests[i].ID == r.ID {
			if r.Method == "custom" {
				s.extRequests[i] = r
			}
			return
		}
	}
	s.extRequests = append(s.extRequests, r)
}

func (s *Store) onExtensionClosed(obj map[string]any) bool {
	id := str(obj, "id")
	for i := range s.extRequests {
		if s.extRequests[i].ID == id {
			s.extRequests = append(s.extRequests[:i], s.extRequests[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Store) onExtensionError(obj map[string]any) bool {
	msg := str(obj, "error")
	if msg == "" {
		msg = str(obj, "errorMessage")
	}
	if msg == "" {
		msg = "Extension command failed"
	}
	s.statusText = msg
	s.lastError = msg
	return true
}
