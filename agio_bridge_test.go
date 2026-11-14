package main

import (
	"testing"
	"time"

	"pigo/agio/transcript"
)

func TestProjectTurnsKeepsIdentityAndStatus(t *testing.T) {
	at := time.Unix(100, 0)
	rows := []turnRow{
		{ID: "a", Prompt: "one", Final: "done", StartedAt: at, Elapsed: time.Second},
		{ID: "b", Prompt: "two", Running: true},
		{ID: "c", Prompt: "three", Failed: true, Error: "boom"},
	}
	got := projectTurns(rows)
	if len(got) != len(rows) {
		t.Fatalf("got %d turns, want %d", len(got), len(rows))
	}
	if got[0].ID != "a" || got[0].Prompt != "one" || got[0].Status != transcript.Completed {
		t.Fatalf("first turn = %+v", got[0])
	}
	if got[0].Ended.Sub(got[0].Started) != time.Second {
		t.Fatalf("first turn ran %v", got[0].Ended.Sub(got[0].Started))
	}
	if got[1].Status != transcript.Running {
		t.Fatalf("running turn status = %v", got[1].Status)
	}
	if got[2].Status != transcript.Failed || got[2].Err != "boom" {
		t.Fatalf("failed turn = %+v", got[2])
	}
	var final *transcript.Segment
	for _, s := range got[0].Segments {
		if s.Kind == transcript.Text {
			final = s
		}
	}
	if final == nil || final.Text != "done" {
		t.Fatalf("final segment = %+v", final)
	}
}

func TestSchemeFallsBackToTheFirst(t *testing.T) {
	if got := schemeOf(lightSchemes, "no-such-scheme").ID; got != "mist" {
		t.Fatalf("fallback scheme = %q", got)
	}
	if got := schemeOf(darkSchemes, "aurora").ID; got != "aurora" {
		t.Fatalf("chosen scheme = %q", got)
	}
}

func TestGrainAmountDefaults(t *testing.T) {
	if got := grainAmount(0); got != defaultGrain {
		t.Fatalf("zero amount = %d", got)
	}
	if got := grainAmount(500); got != 100 {
		t.Fatalf("large amount = %d", got)
	}
}
