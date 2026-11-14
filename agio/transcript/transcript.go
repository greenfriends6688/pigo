// Package transcript folds the neutral agent event stream into turns: one
// user request and the agent work it caused, through settling. A host
// builds them from live and saved events; a remote client receives them as
// patches and only shows them.
package transcript

import (
	"fmt"
	"strings"
	"time"

	"pigo/agio/agent"
)

// Status is whether a turn or segment runs, or how it ended.
type Status uint8

const (
	Running Status = iota
	Completed
	Failed
	Cancelled
)

// Segment kinds.
const (
	Text     = "text"
	Thinking = "thinking"
	Tool     = "tool"
)

// MaxToolOutput bounds retained tool output; full output stays in the
// agent's own history.
const MaxToolOutput = 8000

// Segment is one step of a turn: assistant text, thinking or a tool call.
type Segment struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`
	Status  Status `json:"status,omitempty"`
}

// Turn is one user request and the agent work it caused. Files are the
// attachment paths sent with the prompt, on the device that ran it.
type Turn struct {
	ID       string     `json:"id"`
	Prompt   string     `json:"prompt,omitempty"`
	Files    []string   `json:"files,omitempty"`
	Status   Status     `json:"status,omitempty"`
	Segments []*Segment `json:"segments,omitempty"`
	Started  time.Time  `json:"started,omitzero"`
	Ended    time.Time  `json:"ended,omitzero"`
	Err      string     `json:"err,omitempty"`
	// Replacing is set while the prompt waits for the agent to start it
	// after interrupting a run; events until then belong to that run.
	Replacing bool `json:"-"`
}

// New starts a running turn for prompt.
func New(id, prompt string, files []string, now time.Time) *Turn {
	return &Turn{ID: id, Prompt: prompt, Files: files, Status: Running, Started: now}
}

// Segment returns the segment id, adding it with kind when it is new.
func (t *Turn) Segment(id, kind string) *Segment {
	for _, s := range t.Segments {
		if s.ID == id {
			return s
		}
	}
	s := &Segment{ID: id, Kind: kind, Status: Running}
	t.Segments = append(t.Segments, s)
	return s
}

// Final is the trailing assistant text, shown outside the execution fold.
func (t *Turn) Final() *Segment {
	if n := len(t.Segments); n > 0 && t.Segments[n-1].Kind == Text && strings.TrimSpace(t.Segments[n-1].Text) != "" {
		return t.Segments[n-1]
	}
	return nil
}

// Finish settles a running turn; earlier failures and cancellation persist.
func (t *Turn) Finish(status Status, now time.Time) {
	if t.Status == Running {
		t.Status = status
	}
	if t.Err != "" && t.Status == Completed {
		t.Status = Failed
	}
	for _, s := range t.Segments {
		if s.Status == Running {
			s.Status = t.Status
			if t.Status == Completed {
				s.Status = Completed
			}
		}
	}
	if t.Ended.IsZero() {
		t.Ended = now
	}
}

// Worked is how long a settled turn ran, or zero when unknown.
func (t *Turn) Worked() time.Duration {
	if t.Started.IsZero() || t.Ended.IsZero() {
		return 0
	}
	return max(0, t.Ended.Sub(t.Started))
}

// Apply folds a step event into the turn.
func (t *Turn) Apply(e agent.Event) {
	switch e.Type {
	case agent.TextDelta:
		s := t.Segment(e.Segment, Text)
		s.Text += e.Text
	case agent.TextDone:
		s := t.Segment(e.Segment, Text)
		s.Text = e.Text
		s.Status = Completed
	case agent.ThinkingDelta:
		s := t.Segment(e.Segment, Thinking)
		s.Text += e.Text
	case agent.ThinkingDone:
		s := t.Segment(e.Segment, Thinking)
		s.Text = e.Text
		s.Status = Completed
	case agent.ToolStart:
		s := t.Segment(e.Segment, Tool)
		if e.Tool != "" {
			s.Tool = e.Tool
		}
		if e.Preview != "" {
			s.Preview = e.Preview
		}
	case agent.ToolOutput:
		t.Segment(e.Segment, Tool).Text = BoundOutput(e.Text)
	case agent.ToolEnd:
		s := t.Segment(e.Segment, Tool)
		if s.Tool == "" {
			s.Tool = e.Tool
		}
		if e.Text != "" {
			s.Text = BoundOutput(e.Text)
		}
		s.Status = Completed
		if e.Failed {
			s.Status = Failed
		}
	case agent.Error:
		t.Err = agent.FirstLine(e.Text)
	case agent.Cancelled:
		t.Status = Cancelled
	}
}

// FromHistory rebuilds settled turns from a saved conversation.
func FromHistory(events []agent.Event) []*Turn {
	var turns []*Turn
	var current *Turn
	for i, e := range events {
		if e.Type == agent.UserMessage {
			if current != nil {
				current.Finish(Completed, current.Ended)
			}
			current = &Turn{ID: fmt.Sprintf("history-%d", i), Prompt: e.Text, Status: Running, Started: e.Time}
			turns = append(turns, current)
			continue
		}
		if current == nil {
			current = &Turn{ID: fmt.Sprintf("history-%d", i), Status: Running}
			turns = append(turns, current)
		}
		current.Apply(e)
		if !e.Time.IsZero() {
			current.Ended = e.Time
		}
	}
	if current != nil {
		current.Finish(Completed, current.Ended)
	}
	return turns
}

// BoundOutput keeps the end of long tool output.
func BoundOutput(s string) string {
	if len(s) <= MaxToolOutput {
		return s
	}
	cut := len(s) - MaxToolOutput
	for cut < len(s) && !utf8Start(s[cut]) {
		cut++
	}
	return "…\n" + s[cut:]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Reported reads marker only from the turn's final assistant reply, never
// from tool output or an earlier message. It is the agent's declaration,
// not proof that checks ran.
func (t *Turn) Reported(marker string) bool {
	final := t.Final()
	if final == nil {
		return false
	}
	for line := range strings.Lines(final.Text) {
		if strings.Trim(strings.TrimSpace(line), "`*_#- ") == marker {
			return true
		}
	}
	return false
}
