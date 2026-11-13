package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseFrameValid(t *testing.T) {
	line := []byte(`{"id":"1","type":"response","command":"get_state","success":true,"error":"","data":{"messageCount":3},"extra":[1,2]}`)
	f, err := ParseFrame(line)
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if f.ID != "1" || f.Type != FrameResponse || f.Command != CmdGetState {
		t.Fatalf("unexpected fields: %+v", f)
	}
	if !f.Success {
		t.Fatal("Success should be true")
	}
	if f.Error != "" {
		t.Fatalf("unexpected error field: %q", f.Error)
	}
	if string(f.Data) != `{"messageCount":3}` {
		t.Fatalf("unexpected data: %s", f.Data)
	}
	if _, ok := f.Raw["extra"]; !ok {
		t.Fatal("Raw must retain every field for the reducer")
	}
	if !json.Valid(f.Data) {
		t.Fatal("Data must be valid raw JSON")
	}
}

func TestParseFrameFailureShape(t *testing.T) {
	f, err := ParseFrame([]byte(`{"id":"7","type":"response","command":"bash","success":false,"error":"boom"}`))
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	if f.Success {
		t.Fatal("Success should be false")
	}
	if f.Error != "boom" {
		t.Fatalf("Error = %q", f.Error)
	}
	if f.Data != nil {
		t.Fatalf("Data should be nil when absent, got %s", f.Data)
	}
}

func TestParseFrameStripsCR(t *testing.T) {
	f, err := ParseFrame([]byte("{\"id\":\"1\",\"type\":\"response\",\"success\":true}\r"))
	if err != nil {
		t.Fatalf("ParseFrame with CR: %v", err)
	}
	if f.ID != "1" || !f.Success {
		t.Fatalf("unexpected fields: %+v", f)
	}
}

func TestParseFrameEmptyLine(t *testing.T) {
	for _, line := range []string{"", "   ", "\r", " \t \r"} {
		if _, err := ParseFrame([]byte(line)); !errors.Is(err, ErrEmptyLine) {
			t.Fatalf("ParseFrame(%q) error = %v, want ErrEmptyLine", line, err)
		}
	}
}

func TestParseFrameInvalidJSON(t *testing.T) {
	for _, line := range []string{"not json", "{", "[1,2", `{"id":}`} {
		if _, err := ParseFrame([]byte(line)); err == nil {
			t.Fatalf("ParseFrame(%q) should fail", line)
		}
	}
}

func TestParseFrameUnicodeSeparators(t *testing.T) {
	// U+2028/U+2029 are legal inside JSON strings and must survive parsing.
	line := "{\"type\":\"message_update\",\"text\":\"a\u2028b\u2029c\"}"
	f, err := ParseFrame([]byte(line))
	if err != nil {
		t.Fatalf("ParseFrame: %v", err)
	}
	var text string
	if err := json.Unmarshal(f.Raw["text"], &text); err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if !strings.Contains(text, "\u2028") || !strings.Contains(text, "\u2029") {
		t.Fatalf("separators lost: %q", text)
	}
}

func TestExtensionUIResponseHelpers(t *testing.T) {
	check := func(cmd Command, want string) {
		t.Helper()
		b, err := json.Marshal(cmd)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if m[FType] != CmdExtensionUIResponse {
			t.Fatalf("type = %v", m[FType])
		}
		if m[FID] != "dialog-1" {
			t.Fatalf("id = %v", m[FID])
		}
		if _, ok := m[want]; !ok {
			t.Fatalf("missing %q in %s", want, b)
		}
	}
	check(ConfirmResponse("dialog-1", true), FConfirmed)
	check(ValueResponse("dialog-1", "choice"), FValue)
	check(CancelResponse("dialog-1"), FCancelled)

	if got := ConfirmResponse("dialog-1", false)[FConfirmed]; got != false {
		t.Fatalf("confirmed = %v", got)
	}
	if got := CancelResponse("dialog-1")[FCancelled]; got != true {
		t.Fatalf("cancelled = %v", got)
	}
}

func TestSplitLinesFourMegabyteLine(t *testing.T) {
	// Default bufio.Scanner would fail at 64 KiB; ReadBytes must handle 4 MiB.
	payload := bytes.Repeat([]byte{'x'}, 4<<20)
	input := make([]byte, 0, len(payload)+2)
	input = append(input, '{', '"', 'd', '"', ':', '"')
	input = append(input, payload...)
	input = append(input, '"', '}', '\n')

	out := make(chan []byte, 8)
	if err := SplitLines(bytes.NewReader(input), out); err != nil {
		t.Fatalf("SplitLines: %v", err)
	}
	line := <-out
	if len(line) != len(input)-1 {
		t.Fatalf("line length = %d, want %d", len(line), len(input)-1)
	}
	if line[len(line)-1] == '\n' {
		t.Fatal("line still has trailing newline")
	}
	select {
	case extra := <-out:
		t.Fatalf("unexpected extra line of %d bytes", len(extra))
	default:
	}
}

func TestSplitLinesCRLF(t *testing.T) {
	r := strings.NewReader("{\"a\":1}\r\n{\"b\":2}\n{\"c\":3}\r\n")
	out := make(chan []byte, 8)
	if err := SplitLines(r, out); err != nil {
		t.Fatalf("SplitLines: %v", err)
	}
	want := []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}
	for i, w := range want {
		line := <-out
		if string(line) != w {
			t.Fatalf("line %d = %q, want %q", i, line, w)
		}
	}
}

func TestSplitLinesUnicodeSeparators(t *testing.T) {
	// U+2028/U+2029 must NOT be treated as line boundaries (byte 0x0A only).
	line := "{\"text\":\"a\u2028b\u2029c\"}\n"
	out := make(chan []byte, 8)
	if err := SplitLines(strings.NewReader(line), out); err != nil {
		t.Fatalf("SplitLines: %v", err)
	}
	got := string(<-out)
	if !strings.Contains(got, "\u2028") || !strings.Contains(got, "\u2029") {
		t.Fatalf("separators lost: %q", got)
	}
	select {
	case extra := <-out:
		t.Fatalf("Unicode separator wrongly split a line: %q", extra)
	default:
	}
}

func TestSplitLinesUTF8SplitAcrossChunks(t *testing.T) {
	// A multi-byte UTF-8 char ("世" = E4 B8 96) is split across two writes on a
	// pipe. Splitting by byte (not rune) and buffering across reads must keep it
	// intact.
	pr, pw := io.Pipe()
	out := make(chan []byte, 8)

	go func() {
		// First chunk ends with the first byte of 世.
		_, _ = pw.Write([]byte(`{"type":"message_update","text":"` + "\xE4"))
		// Second chunk carries the remaining bytes and the LF terminator.
		_, _ = pw.Write([]byte("\xB8\x96" + `"}` + "\n"))
		_ = pw.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- SplitLines(pr, out) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SplitLines: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SplitLines deadlocked on a chunk-split UTF-8 char")
	}

	line := <-out
	if !bytes.Contains(line, []byte("世")) {
		t.Fatalf("UTF-8 sequence corrupted: %q", line)
	}
	f, err := ParseFrame(line)
	if err != nil {
		t.Fatalf("ParseFrame after split: %v", err)
	}
	var text string
	if err := json.Unmarshal(f.Raw["text"], &text); err != nil || text != "世" {
		t.Fatalf("text = %q, err = %v", text, err)
	}
}

func TestSplitLinesFinalUnterminatedLine(t *testing.T) {
	out := make(chan []byte, 8)
	if err := SplitLines(strings.NewReader("{\"final\":true}"), out); err != nil {
		t.Fatalf("SplitLines: %v", err)
	}
	if got := string(<-out); got != `{"final":true}` {
		t.Fatalf("final line = %q", got)
	}
	select {
	case extra := <-out:
		t.Fatalf("unexpected extra line %q", extra)
	default:
	}
}

func TestSplitLinesReadError(t *testing.T) {
	want := errors.New("boom")
	pr, pw := io.Pipe()
	out := make(chan []byte, 8)
	go func() {
		_, _ = pw.Write([]byte("{\"a\":1}\n"))
		_ = pw.CloseWithError(want)
	}()
	err := SplitLines(pr, out)
	if !errors.Is(err, want) {
		t.Fatalf("SplitLines error = %v, want %v", err, want)
	}
	if got := string(<-out); got != `{"a":1}` {
		t.Fatalf("first line = %q", got)
	}
}
