package agent

import (
	"strings"
	"sync"
)

// tailWriter keeps the last lines a child wrote to stderr. The child's own
// account of a failed start ("env: node: No such file or directory", "Cannot
// find module 'jiti'", "Invalid trust store ...") is the only thing that
// explains a disconnect; the log file has it, but the window has to show text.
type tailWriter struct {
	mu    sync.Mutex
	lines []string
	max   int
	buf   strings.Builder // the line being written, not yet complete
}

func newTailWriter(max int) *tailWriter {
	if max <= 0 {
		max = 40
	}
	return &tailWriter{max: max}
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range p {
		if b != '\n' {
			t.buf.WriteByte(b)
			continue
		}
		t.lines = append(t.lines, strings.TrimRight(t.buf.String(), "\r"))
		t.buf.Reset()
		if len(t.lines) > t.max {
			t.lines = t.lines[len(t.lines)-t.max:]
		}
	}
	return len(p), nil
}

// String returns the kept lines, without the few empty paddings a chatty child
// leaves behind, joined by newlines.
func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	if t.buf.Len() > 0 {
		lines = append(append([]string(nil), lines...), t.buf.String())
	}
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// LastLine is the one line worth putting in a toast: whatever the child said
// just before it went away.
func (t *tailWriter) LastLine() string {
	s := t.String()
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
