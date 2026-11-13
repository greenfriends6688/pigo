package agent

import (
	"errors"
	"testing"
	"time"
)

func newTestPool(max int, idle time.Duration) *Pool {
	p := NewPool(max, idle)
	p.newTransport = func(Options) (Transport, error) { return newFakeTransport(), nil }
	return p
}

func TestPoolLRUEviction(t *testing.T) {
	p := newTestPool(2, 0)
	defer p.Close()

	h1, err := p.Acquire("k1", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Release("k1")
	h2, err := p.Acquire("k2", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Release("k2")
	// k1 is the least recently used idle entry, so k3 takes its place.
	h3, err := p.Acquire("k3", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == nil || h2 == nil || h3 == nil {
		t.Fatal("Acquire returned a nil handle")
	}
	if p.Len() != 2 {
		t.Fatalf("Len = %d, want 2", p.Len())
	}

	p.mu.Lock()
	_, hasK1 := p.entries["k1"]
	_, hasK2 := p.entries["k2"]
	_, hasK3 := p.entries["k3"]
	p.mu.Unlock()
	if hasK1 {
		t.Fatal("oldest key k1 should have been evicted")
	}
	if !hasK2 || !hasK3 {
		t.Fatalf("k2/k3 should remain (hasK2=%v hasK3=%v)", hasK2, hasK3)
	}

	// A live key is reused (same handle, no new process).
	h2Again, err := p.Acquire("k2", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h2Again != h2 {
		t.Fatal("Acquire on a live key should reuse the handle")
	}

	// Re-acquiring an evicted key creates a brand-new session; k3 goes idle
	// first so there is something the pool may evict.
	p.Release("k3")
	h1Again, err := p.Acquire("k1", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h1Again == h1 {
		t.Fatal("evicted key should come back as a fresh handle")
	}
	if p.Len() != 2 {
		t.Fatalf("Len = %d after re-acquire, want 2", p.Len())
	}
}

// TestPoolKeepsInUseSessions is the connection-drop fix: a session the window
// is in may not be evicted to make room, however old it is.
func TestPoolKeepsInUseSessions(t *testing.T) {
	p := newTestPool(2, 0)
	defer p.Close()

	if _, err := p.Acquire("running", Options{}, nil, nil); err != nil { // in use, oldest
		t.Fatal(err)
	}
	if _, err := p.Acquire("idle", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.Release("idle")
	if _, err := p.Acquire("third", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	_, hasRunning := p.entries["running"]
	_, hasIdle := p.entries["idle"]
	p.mu.Unlock()
	if !hasRunning {
		t.Fatal("the in-use session was evicted: the open answer would disconnect")
	}
	if hasIdle {
		t.Fatal("the idle session should have made room")
	}
}

func TestPoolTouchUpdatesLRU(t *testing.T) {
	p := newTestPool(2, 0)
	defer p.Close()

	if _, err := p.Acquire("k1", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.Release("k1")
	if _, err := p.Acquire("k2", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.Release("k2")
	// Touch k1 so k2 becomes the least-recently-used idle entry.
	p.Touch("k1")
	if _, err := p.Acquire("k3", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}

	p.mu.Lock()
	_, hasK1 := p.entries["k1"]
	_, hasK2 := p.entries["k2"]
	p.mu.Unlock()
	if hasK2 || !hasK1 {
		t.Fatalf("Touch did not protect k1 (hasK1=%v hasK2=%v)", hasK1, hasK2)
	}
}

func TestPoolIdleExpiry(t *testing.T) {
	p := newTestPool(4, 80*time.Millisecond)
	defer p.Close()

	if _, err := p.Acquire("only", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	p.Release("only")

	deadline := time.Now().Add(3 * time.Second)
	for p.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if p.Len() != 0 {
		t.Fatal("idle session was not evicted")
	}
}

func TestPoolClose(t *testing.T) {
	p := newTestPool(4, 0)
	if _, err := p.Acquire("a", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire("b", Options{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 0 {
		t.Fatalf("Len = %d after Close, want 0", p.Len())
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := p.Acquire("c", Options{}, nil, nil); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("Acquire after Close err = %v, want ErrPoolClosed", err)
	}
}

func TestPoolRekeyMovesLiveSession(t *testing.T) {
	p := newTestPool(4, 0)
	defer p.Close()

	h, err := p.Acquire("new:/tmp/project", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Rekey("new:/tmp/project", "/sessions/2026-11-12_abc.jsonl")
	if p.Len() != 1 {
		t.Fatalf("Len = %d after Rekey, want 1", p.Len())
	}
	again, err := p.Acquire("/sessions/2026-11-12_abc.jsonl", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again != h {
		t.Fatal("the rekeyed session must be found under its new key")
	}
	if again.ID != "/sessions/2026-11-12_abc.jsonl" {
		t.Fatalf("handle ID = %q", again.ID)
	}
	// The old key starts a new session.
	fresh, err := p.Acquire("new:/tmp/project", Options{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh == h {
		t.Fatal("the old key must not reach the moved session")
	}
	p.Rekey("missing", "other")
	if p.Len() != 2 {
		t.Fatalf("a Rekey of a missing key changed the pool: %d", p.Len())
	}
}
