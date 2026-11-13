package agent

import (
	"strings"
	"testing"
	"time"
)

// TestSplitLinesWithStopDrainsAfterStop is the shutdown regression: with an
// unbuffered frames channel the reader used to block forever once the Client's
// dispatch goroutine was gone. Close() then hit its 5s deadline and SIGKILLed
// the process group, and pi died on EPIPE because nobody drained stdout.
func TestSplitLinesWithStopDrainsAfterStop(t *testing.T) {
	out := make(chan []byte)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- splitLinesWithStop(strings.NewReader("one\ntwo\nthree\n"), out, stop)
	}()
	// Let the first send block on the unread channel.
	time.Sleep(20 * time.Millisecond)
	close(stop)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read loop returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read loop still blocked after shutdown; Close would SIGKILL the child")
	}
	select {
	case frame := <-out:
		t.Fatalf("a frame was delivered after shutdown: %q", frame)
	default:
	}
}

func TestSplitLinesWithoutStopStillDeliversFrames(t *testing.T) {
	out := make(chan []byte, 4)
	if err := SplitLines(strings.NewReader("one\ntwo\n"), out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 frames, got %d", len(out))
	}
}
