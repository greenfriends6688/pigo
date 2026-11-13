// Package toolfields extracts the display fields for tool-call cards.
// It is a 1:1 port of lib/tool-fields.ts from the pi-web web client, whose two
// exports are ported here:
//   - toolFieldsOfInput: a flat "all of the arguments or none" table, or null.
//   - toolInputPreview:  the one line a collapsed card shows after the name.
//
// Go maps have no insertion order, unlike JS Object.entries. Where the source
// reads "the first value in order", this port uses a deterministic ordering:
// array-valued arguments first, then a text-likeness ranking of keys, then
// alphabetical order. This reproduces the documented source outputs while
// never depending on random map iteration.
package toolfields

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	emptyFieldValue = "(empty)"
	previewMax      = 120
)

// field is one row of the flat argument table (toolFieldsOfInput).
type field struct {
	key       string
	value     string
	multiline bool
}

// Format extracts the display fields of a tool call, porting the two
// extraction functions of lib/tool-fields.ts 1:1.
//   - action is toolInputPreview(args): the one-line "what it does" text
//     (command / path / file_path / pattern / query, else its first readable
//     argument). It falls back to name when nothing can be read from args.
//   - detail is toolFieldsOfInput(args) rendered as "key: value" lines; when
//     args is not a flat record it falls back to a JSON blob, mirroring the web
//     card's <pre> JSON fallback.
//
// Missing fields and unexpected value types never panic.
func Format(name string, args map[string]any) (action, detail string) {
	action = preview(args)
	if action == "" {
		action = name
	}
	return action, fieldsDetail(args)
}

// Summary returns the one-line summary shown when a tool card is collapsed.
// It is toolInputPreview(args); name is used only as a fallback label when the
// arguments carry no readable text.
func Summary(name string, args map[string]any) string {
	if p := preview(args); p != "" {
		return p
	}
	return name
}

// preview is the 1:1 port of toolInputPreview(input).
func preview(args map[string]any) string {
	if args == nil {
		return ""
	}
	// Priority arguments: the command, path or query the call is about.
	for _, key := range []string{"command", "path", "file_path"} {
		sep := ", "
		if key == "command" {
			sep = " "
		}
		if t, ok := firstRankText(args[key], sep); ok {
			return clip(t)
		}
	}
	for _, key := range []string{"pattern", "query"} {
		if t, ok := firstRankText(args[key], ", "); ok {
			return clip(t)
		}
	}
	// Else its first argument that reads as itself, then the text inside its
	// first object argument (instead of "[object Object]").
	keys := orderedValueKeys(args)
	for _, k := range keys {
		if t, ok := firstRankText(args[k], ", "); ok {
			return clip(t)
		}
	}
	for _, k := range keys {
		if t, ok := nestedText(args[k]); ok {
			return clip(t)
		}
	}
	return ""
}

// fieldsDetail ports toolFieldsOfInput and returns its text rendering. When
// the input is not a flat record, it falls back to a JSON blob (the source's
// <pre> fallback). It never panics on nil, nested or wrong-typed args.
func fieldsDetail(args map[string]any) string {
	if fields := flatFields(args); fields != nil {
		lines := make([]string, 0, len(fields))
		for _, f := range fields {
			lines = append(lines, f.key+": "+f.value)
		}
		return strings.Join(lines, "\n")
	}
	if len(args) == 0 {
		return ""
	}
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// flatFields is the 1:1 port of toolFieldsOfInput. It returns nil (meaning the
// card falls back to JSON) for a non-object, an empty object, or ANY argument
// whose value is an object/array: "all of the arguments or none of them".
// Keys are sorted because a Go map has no insertion order.
func flatFields(args map[string]any) []field {
	if len(args) == 0 {
		return nil
	}
	keys := sortedKeys(args)
	out := make([]field, 0, len(keys))
	for _, k := range keys {
		var text string
		switch x := args[k].(type) {
		case nil:
			text = "null"
		case string:
			text = x
		case bool:
			text = strconv.FormatBool(x)
		default:
			num, ok := scalarText(x)
			if !ok {
				return nil // a nested value takes the whole card back to JSON
			}
			text = num
		}
		if text == "" {
			text = emptyFieldValue
		}
		out = append(out, field{key: k, value: text, multiline: strings.Contains(text, "\n")})
	}
	return out
}

// primitiveText is the 1:1 port of primitiveText: a non-empty string, a
// number or a boolean reads as itself; everything else reads as nothing.
func primitiveText(v any) (string, bool) {
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
	return scalarText(v)
}

// firstRankText is the 1:1 port of firstRankText(value, separator): a value
// that reads as itself, or a list of only such values joined by separator.
func firstRankText(v any, sep string) (string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return primitiveText(v)
	}
	if len(arr) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(arr))
	for _, item := range arr {
		if item == nil {
			continue // null items are allowed by `every` but read as nothing
		}
		if _, ok := scalarText(item); !ok {
			return "", false // an object/array item invalidates the whole list
		}
		if t, ok := primitiveText(item); ok {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, sep), true
}

// nestedText is the 1:1 port of nestedText: a nested value's first readable
// text (an object's first readable value, or a list's items' texts joined).
func nestedText(v any) (string, bool) {
	switch x := v.(type) {
	case []any:
		var parts []string
		for _, item := range x {
			if t, ok := primitiveText(item); ok {
				parts = append(parts, t)
			} else if t, ok := nestedText(item); ok {
				parts = append(parts, t)
			}
		}
		if len(parts) == 0 {
			return "", false
		}
		return strings.Join(parts, ", "), true
	case map[string]any:
		for _, k := range textOrderedKeys(x) {
			inner := x[k]
			if t, ok := firstRankText(inner, ", "); ok {
				return t, true
			}
			if t, ok := nestedText(inner); ok {
				return t, true
			}
		}
	}
	return "", false
}

// scalarText returns the JS String(value) for string/number/boolean-like Go
// values and reports false for nil, objects, slices and other non-scalars.
// JSON numbers arrive as float64; int/uint/json.Number are accepted too.
func scalarText(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case json.Number:
		return string(x), true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), true
	}
	return "", false
}

// orderedValueKeys returns the keys to read "in the tool's own order". Array
// arguments (commands, queries, paths, urls) are read first, then the rest;
// within each group keys are alphabetical for determinism.
func orderedValueKeys(m map[string]any) []string {
	keys := sortedKeys(m)
	arrays := make([]string, 0, len(keys))
	others := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := m[k].([]any); ok {
			arrays = append(arrays, k)
		} else {
			others = append(others, k)
		}
	}
	return append(arrays, others...)
}

// textOrderedKeys orders an object's keys by text-likeness (so a question's
// "question" field is read before its "header"/"options"), then alphabetically.
func textOrderedKeys(m map[string]any) []string {
	keys := sortedKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		ri, rj := textKeyRank(keys[i]), textKeyRank(keys[j])
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func textKeyRank(k string) int {
	switch k {
	case "text", "content", "question", "description", "name", "title", "label", "message", "value", "summary":
		return 0
	}
	return 1
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// clip trims to PREVIEW_MAX runes (the source's .slice(0, 120)). Runes are
// used so a multi-byte UTF-8 character is never cut in half.
func clip(s string) string {
	if utf8.RuneCountInString(s) <= previewMax {
		return s
	}
	n := 0
	for i := range s {
		if n == previewMax {
			return s[:i]
		}
		n++
	}
	return s
}
