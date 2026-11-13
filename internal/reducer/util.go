package reducer

import (
	"encoding/json"
	"strings"
)

// decodeFrame parses one event frame into an object map. It returns nil for
// empty input, invalid JSON, JSON null, arrays and scalars — none of which can
// panic the reducer.
func decodeFrame(frame json.RawMessage) map[string]any {
	if len(frame) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(frame, &obj); err != nil {
		return nil
	}
	return obj
}

// str safely reads a string field. A missing or wrong-typed field is "".
func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// num safely reads a numeric field as float64. JSON numbers arrive as float64;
// a few other numeric kinds are accepted for robustness.
func num(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	}
	return 0
}

// truthy is a tolerant boolean test used for optional flags (queued,
// isStreaming, cancelled, ...).
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != "" && x != "false" && x != "0"
	case float64:
		return x != 0
	}
	return false
}

// objField safely reads an object field; a non-object becomes nil.
func objField(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

// stringArray safely reads an array of strings; non-string items are dropped.
func stringArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// textOf returns the readable text of a message/partial content value. A
// string reads as itself; an array of content blocks joins their "text" fields
// with newlines (mirroring extractMessageText); an object uses its "text".
func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, block := range x {
			switch b := block.(type) {
			case string:
				if b != "" {
					parts = append(parts, b)
				}
			case map[string]any:
				if t, ok := b["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return str(x, "text")
	}
	return ""
}

// deltaText extracts the incremental text from an assistantMessageEvent.
// The native contract uses "text"; the wire source also uses "delta"/"content",
// so all three are accepted, in that priority, for robustness.
func deltaText(ame map[string]any) string {
	for _, key := range []string{"text", "delta", "content"} {
		if v, ok := ame[key]; ok {
			if t := textOf(v); t != "" {
				return t
			}
		}
	}
	return ""
}

// contentInfo returns the plain text and the image count of a message's
// content, mirroring userMessageKey's {text, images} shape. Images are counted
// (the native API has no image signatures) and text blocks are joined with a
// newline, exactly like extractMessageText.
func contentInfo(content any) (string, int) {
	switch c := content.(type) {
	case string:
		return c, 0
	case []any:
		var parts []string
		images := 0
		for _, block := range c {
			m, ok := block.(map[string]any)
			if !ok {
				if s, ok := block.(string); ok && s != "" {
					parts = append(parts, s)
				}
				continue
			}
			switch str(m, "type") {
			case "text":
				if t, ok := m["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			case "image":
				images++
			}
		}
		return strings.Join(parts, "\n"), images
	}
	return "", 0
}

// queueTexts reads a queue (steering / followUp). Each element may be a plain
// string or an object carrying "content" / "text" / "message".
func queueTexts(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return []string{s}
		}
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, queueItemText(item))
	}
	return out
}

func queueItemText(item any) string {
	switch x := item.(type) {
	case string:
		return x
	case map[string]any:
		for _, key := range []string{"content", "text", "message"} {
			if v, ok := x[key]; ok {
				if t := textOf(v); t != "" {
					return t
				}
			}
		}
	}
	return ""
}

// bashOutputText reads the `!` command output. The authoritative field is
// "output" (lib/rpc-manager.ts:480 emits {type:"bash_output", output: tail}
// and hooks/useAgentSession.ts:2166 reads event.output). "text" and "data" are
// accepted as defensive fallbacks for other producers.
func bashOutputText(obj map[string]any) (string, bool) {
	for _, key := range []string{"output", "text", "data"} {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func isNestedFields(obj map[string]any) bool {
	switch str(obj, "type") {
	case "tool_execution_start", "tool_execution_update", "tool_execution_end":
	default:
		return false
	}
	p, ok := obj["parentToolCallId"].(string)
	return ok && p != ""
}

// parseExtensionRequest reads every field of an extension_ui_request, all
// safely typed, so an enqueued request is a complete copy of the frame.
func parseExtensionRequest(obj map[string]any) ExtensionRequest {
	return ExtensionRequest{
		ID:              str(obj, "id"),
		Method:          str(obj, "method"),
		Title:           str(obj, "title"),
		Message:         str(obj, "message"),
		Placeholder:     str(obj, "placeholder"),
		Prefill:         str(obj, "prefill"),
		Text:            str(obj, "text"),
		Options:         stringArray(obj["options"]),
		StatusKey:       str(obj, "statusKey"),
		StatusText:      str(obj, "statusText"),
		WidgetKey:       str(obj, "widgetKey"),
		WidgetLines:     stringArray(obj["widgetLines"]),
		WidgetPlacement: str(obj, "widgetPlacement"),
		NotifyType:      str(obj, "notifyType"),
		Timeout:         num(obj, "timeout"),
	}
}

// cloneMap deep-copies a map[string]any (nested maps and slices included).
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v // strings, numbers, bools and nil are immutable
}

// cloneStrings returns a non-nil copy (an empty input yields an empty slice).
func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}
