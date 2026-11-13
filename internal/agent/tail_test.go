package agent

import (
	"strings"
	"testing"
)

func TestTailWriterKeepsLastLines(t *testing.T) {
	w := newTailWriter(3)
	if _, err := w.Write([]byte("one\ntwo\nthree\nfour\n")); err != nil {
		t.Fatal(err)
	}
	if got := w.String(); got != "two\nthree\nfour" {
		t.Fatalf("String = %q", got)
	}
	if got := w.LastLine(); got != "four" {
		t.Fatalf("LastLine = %q", got)
	}
}

func TestTailWriterHandlesPartialLines(t *testing.T) {
	w := newTailWriter(5)
	if _, err := w.Write([]byte("err: no node")); err != nil {
		t.Fatal(err)
	}
	if got := w.LastLine(); got != "err: no node" {
		t.Fatalf("LastLine before newline = %q", got)
	}
}

func TestTailWriterSkipsBlankLines(t *testing.T) {
	w := newTailWriter(5)
	if _, err := w.Write([]byte("ok\n\n\nboom\r\n")); err != nil {
		t.Fatal(err)
	}
	if got := w.LastLine(); got != "boom" {
		t.Fatalf("LastLine = %q", got)
	}
}

// TestExitErrorCarriesStderr is the disconnect-card contract: the reason shown
// to the user is what the child said, not just "signal: terminated".
func TestExitErrorCarriesStderr(t *testing.T) {
	f := newFakeTransport()
	f.eof(errTerminated{})
	if got := exitError(f); got == nil || got.Error() == "" {
		t.Fatalf("exitError = %v", got)
	}

	f2 := newFakeTransport()
	f2.eof(errTerminated{})
	f2.tail = "noise\nenv: node: No such file or directory\n\n"
	got := exitError(f2)
	if got == nil || !strings.Contains(got.Error(), "env: node: No such file or directory") {
		t.Fatalf("exitError = %q, want the stderr line appended", got)
	}
}

type errTerminated struct{}

func (errTerminated) Error() string { return "signal: terminated" }
