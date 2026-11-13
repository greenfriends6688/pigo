package toolfields

import (
	"strings"

	"pigo/internal/textdiff"
)

// Patch builds the unified diff a tool card renders for the tools that
// change files. pi's edit tool carries {path, edits: [{oldText, newText}]}
// (or a single {path, oldText, newText} pair) and its write tool {path,
// content}; neither arrives as a patch, so the card shows nothing without
// this. ok is false for every other tool or when no text pair can be read.
func Patch(name string, args map[string]any) (patch string, ok bool) {
	if args == nil {
		return "", false
	}
	lower := strings.ToLower(name)
	path := argString(args, "path", "file_path")
	switch {
	case strings.Contains(lower, "edit") || strings.Contains(lower, "patch"):
		return editsPatch(path, args)
	case strings.Contains(lower, "write") || strings.Contains(lower, "multiedit"):
		if content, has := argText(args["content"]); has {
			return textdiff.Unified(path, "", content, 3), true
		}
		return editsPatch(path, args)
	}
	return "", false
}

// editsPatch diffs every oldText/newText pair the call carries; the pairs
// are snippets, so each becomes its own file section of one patch.
func editsPatch(path string, args map[string]any) (string, bool) {
	var b strings.Builder
	found := false
	appendPair := func(oldText, newText string) {
		if oldText == newText {
			return
		}
		found = true
		b.WriteString(textdiff.Unified(path, oldText, newText, 3))
	}
	if list, isList := args["edits"].([]any); isList {
		for _, item := range list {
			m, isMap := item.(map[string]any)
			if !isMap {
				continue
			}
			oldText, _ := argText(m["oldText"])
			newText, _ := argText(m["newText"])
			appendPair(oldText, newText)
		}
	}
	if !found {
		if oldText, has := argText(args["oldText"]); has {
			newText, _ := argText(args["newText"])
			appendPair(oldText, newText)
		}
	}
	if !found {
		return "", false
	}
	return b.String(), true
}

// argString reads the first present string argument among the keys.
func argString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// argText reads a text value that may legitimately be empty, reporting
// whether the key held a string at all.
func argText(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}
