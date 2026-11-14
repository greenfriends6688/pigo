package transcript

import (
	"slices"
	"strings"
	"time"
)

// Patch turns a receiver's copy of a conversation into the sender's. Reset
// replaces every turn; otherwise turns not mentioned stay as they are and
// new ones follow the last.
type Patch struct {
	Reset bool        `json:"reset,omitempty"`
	Turns []TurnPatch `json:"turns,omitempty"`
}

// TurnPatch carries a turn's changed parts: its header when any of it
// changed, and its new or changed segments in order.
type TurnPatch struct {
	ID       string         `json:"id"`
	Header   *Header        `json:"header,omitempty"`
	Segments []SegmentPatch `json:"segments,omitempty"`
}

// Header is everything of a turn but its segments.
type Header struct {
	Prompt  string    `json:"prompt,omitempty"`
	Files   []string  `json:"files,omitempty"`
	Status  Status    `json:"status,omitempty"`
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
	Err     string    `json:"err,omitempty"`
}

// SegmentPatch is a segment's state; Append adds Text to the receiver's text
// instead of replacing it, so streamed replies send only what arrived.
type SegmentPatch struct {
	Segment
	Append bool `json:"append,omitempty"`
}

func (t *Turn) header() Header {
	return Header{Prompt: t.Prompt, Files: t.Files, Status: t.Status, Started: t.Started, Ended: t.Ended, Err: t.Err}
}

func (h Header) equal(o Header) bool {
	return h.Prompt == o.Prompt && slices.Equal(h.Files, o.Files) && h.Status == o.Status &&
		h.Started.Equal(o.Started) && h.Ended.Equal(o.Ended) && h.Err == o.Err
}

// Clone copies turns so later changes to them leave the copy alone. Strings
// are shared, so a clone is cheap.
func Clone(turns []*Turn) []*Turn {
	c := make([]*Turn, len(turns))
	for i, t := range turns {
		copy := *t
		copy.Files = slices.Clone(t.Files)
		copy.Segments = make([]*Segment, len(t.Segments))
		for j, s := range t.Segments {
			seg := *s
			copy.Segments[j] = &seg
		}
		c[i] = &copy
	}
	return c
}

// Diff returns the patch that turns old, a receiver's copy, into cur, and
// whether anything changed. Turns are only ever appended to, except when a
// saved history arrives before live turns, which resets the receiver.
func Diff(old, cur []*Turn) (Patch, bool) {
	reset := len(old) > len(cur)
	for i := 0; !reset && i < len(old); i++ {
		reset = old[i].ID != cur[i].ID
	}
	if reset || len(old) == 0 && len(cur) == 0 {
		if !reset {
			return Patch{}, false
		}
		p := Patch{Reset: true}
		for _, t := range cur {
			p.Turns = append(p.Turns, full(t))
		}
		return p, true
	}
	var p Patch
	for i, t := range cur {
		if i >= len(old) {
			p.Turns = append(p.Turns, full(t))
			continue
		}
		if tp, changed := diffTurn(old[i], t); changed {
			p.Turns = append(p.Turns, tp)
		}
	}
	return p, len(p.Turns) > 0
}

func full(t *Turn) TurnPatch {
	h := t.header()
	tp := TurnPatch{ID: t.ID, Header: &h}
	for _, s := range t.Segments {
		tp.Segments = append(tp.Segments, SegmentPatch{Segment: *s})
	}
	return tp
}

func diffTurn(old, cur *Turn) (TurnPatch, bool) {
	tp := TurnPatch{ID: cur.ID}
	if h := cur.header(); !h.equal(old.header()) {
		tp.Header = &h
	}
	for i, s := range cur.Segments {
		if i >= len(old.Segments) || old.Segments[i].ID != s.ID {
			// Segments are appended in order; anything else replaces from here.
			tp.Segments = append(tp.Segments, SegmentPatch{Segment: *s})
			continue
		}
		o := old.Segments[i]
		if *o == *s {
			continue
		}
		sp := SegmentPatch{Segment: *s}
		if len(s.Text) > len(o.Text) && strings.HasPrefix(s.Text, o.Text) {
			sp.Text, sp.Append = s.Text[len(o.Text):], true
		}
		tp.Segments = append(tp.Segments, sp)
	}
	return tp, tp.Header != nil || len(tp.Segments) > 0
}

// Apply returns turns with p applied. It changes the turns in place and
// keeps their identity, so state held for them, such as their folding,
// survives.
func Apply(turns []*Turn, p Patch) []*Turn {
	if p.Reset {
		byID := map[string]*Turn{}
		for _, t := range turns {
			byID[t.ID] = t
		}
		turns = nil
		for _, tp := range p.Turns {
			t := byID[tp.ID]
			if t == nil {
				t = &Turn{ID: tp.ID}
			} else {
				t.Segments = nil
			}
			applyTurn(t, tp)
			turns = append(turns, t)
		}
		return turns
	}
	for _, tp := range p.Turns {
		i := slices.IndexFunc(turns, func(t *Turn) bool { return t.ID == tp.ID })
		if i < 0 {
			t := &Turn{ID: tp.ID}
			applyTurn(t, tp)
			turns = append(turns, t)
			continue
		}
		applyTurn(turns[i], tp)
	}
	return turns
}

func applyTurn(t *Turn, tp TurnPatch) {
	if h := tp.Header; h != nil {
		t.Prompt, t.Files, t.Status, t.Started, t.Ended, t.Err = h.Prompt, h.Files, h.Status, h.Started, h.Ended, h.Err
	}
	for _, sp := range tp.Segments {
		s := t.find(sp.ID)
		if s == nil {
			s = &Segment{ID: sp.ID}
			t.Segments = append(t.Segments, s)
		}
		text := sp.Text
		if sp.Append {
			text = s.Text + text
		}
		*s = sp.Segment
		s.Text = text
	}
}

func (t *Turn) find(id string) *Segment {
	for _, s := range t.Segments {
		if s.ID == id {
			return s
		}
	}
	return nil
}
