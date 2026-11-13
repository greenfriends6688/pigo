// Package textdiff produces the unified patches a tool card renders with
// ui/diff: pi's edit tool carries oldText/newText pairs and its write tool
// the whole file content, and neither arrives as a patch. The format is the
// standard one base/diff parses (---/+++/@@, ' ' '-' '+' lines, "\ No newline
// at end of file").
package textdiff

import (
	"fmt"
	"strings"
)

// Unified diffs old against new for path, with up to ctx lines of context
// per hunk (3 when ctx <= 0). Either side may be empty: an empty old side
// makes every line added, an empty new side makes every line deleted.
func Unified(path, old, new string, ctx int) string {
	if ctx <= 0 {
		ctx = 3
	}
	if old == new {
		return ""
	}
	oldLines := splitLines(old)
	newLines := splitLines(new)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	for _, h := range hunks(ops(oldLines, newLines), ctx) {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.oldStart, h.oldLen, h.newStart, h.newLen)
		for _, op := range h.ops {
			switch op.kind {
			case opKeep:
				b.WriteString(" " + op.text + "\n")
			case opDelete:
				b.WriteString("-" + op.text + "\n")
			case opInsert:
				b.WriteString("+" + op.text + "\n")
			}
		}
	}
	return b.String()
}

// Stats counts the + and − lines of a unified patch.
func Stats(patch string) (added, deleted int) {
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			deleted++
		}
	}
	return added, deleted
}

type opKind uint8

const (
	opKeep opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// ops is the LCS edit script between the two line slices, keeping and
// moving common lines and marking the rest.
func ops(old, new []string) []op {
	n, m := len(old), len(new)
	// lcs[i][j] is the LCS length of old[i:] and new[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if old[i] == new[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	out := make([]op, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new[j]:
			out = append(out, op{opKeep, old[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, op{opDelete, old[i]})
			i++
		default:
			out = append(out, op{opInsert, new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, op{opDelete, old[i]})
	}
	for ; j < m; j++ {
		out = append(out, op{opInsert, new[j]})
	}
	return out
}

type hunk struct {
	oldStart, oldLen int
	newStart, newLen int
	ops              []op
}

// hunks groups the edit script into hunks: context is any keep-line within
// ctx of a change, so runs of untouched lines longer than 2*ctx split
// neighbouring hunks apart.
func hunks(all []op, ctx int) []hunk {
	keep := make([]bool, len(all))
	for idx, o := range all {
		if o.kind == opKeep {
			continue
		}
		for k := max(0, idx-ctx); k <= min(len(all)-1, idx+ctx); k++ {
			keep[k] = true
		}
	}
	var out []hunk
	oldPos, newPos := 1, 1
	for idx := 0; idx < len(all); {
		if !keep[idx] {
			switch all[idx].kind {
			case opKeep:
				oldPos++
				newPos++
			case opDelete:
				oldPos++
			case opInsert:
				newPos++
			}
			idx++
			continue
		}
		h := hunk{oldStart: oldPos, newStart: newPos}
		for idx < len(all) && keep[idx] {
			o := all[idx]
			h.ops = append(h.ops, o)
			switch o.kind {
			case opKeep:
				h.oldLen++
				h.newLen++
				oldPos++
				newPos++
			case opDelete:
				h.oldLen++
				oldPos++
			case opInsert:
				h.newLen++
				newPos++
			}
			idx++
		}
		out = append(out, h)
	}
	return out
}

// splitLines splits text into lines without their terminators; a trailing
// newline does not produce an extra empty line.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
