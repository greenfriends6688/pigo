package agent

import (
	"errors"
	"sync"
	"time"
)

// ErrPoolClosed is returned by Acquire after Close.
var ErrPoolClosed = errors.New("agent: pool closed")

// SessionHandle is a pooled RPC session. LastUsed is refreshed by Acquire and
// Touch.
type SessionHandle struct {
	ID        string
	Client    *Client
	Transport Transport
	Options   Options
	LastUsed  time.Time
}

type poolEntry struct {
	handle    *SessionHandle
	inUse     bool
	lastUsed  time.Time
	order     uint64 // monotonic LRU rank, immune to equal timestamps
	idleTimer *time.Timer
}

// Pool keeps at most max live sessions. An idle session is closed after idle;
// when a new key would exceed max, the least-recently-used session is evicted.
type Pool struct {
	mu     sync.Mutex
	max    int
	idle   time.Duration
	closed bool

	entries map[string]*poolEntry
	counter uint64

	// newTransport is the factory used by Acquire. It is a field so tests can
	// inject an in-memory Transport instead of spawning node.
	newTransport func(Options) (Transport, error)
}

// NewPool creates a pool with the given capacity and idle timeout. max <= 0
// disables LRU eviction; idle <= 0 disables idle expiry.
func NewPool(max int, idle time.Duration) *Pool {
	return &Pool{
		max:          max,
		idle:         idle,
		entries:      make(map[string]*poolEntry),
		newTransport: NewStdioTransport,
	}
}

// Acquire returns the session for key, creating it if needed. When the pool is
// full the least-recently-used entry is evicted and closed. A stale entry whose
// client already disconnected is replaced.
func (p *Pool) Acquire(key string, opts Options, onEvent func(Frame), onDisconnect func(error)) (*SessionHandle, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrPoolClosed
	}

	if e, ok := p.entries[key]; ok {
		dead := false
		select {
		case <-e.handle.Client.Done():
			dead = true
		default:
		}
		if !dead {
			if e.idleTimer != nil {
				e.idleTimer.Stop()
				e.idleTimer = nil
			}
			e.inUse = true
			p.counter++
			e.order = p.counter
			e.lastUsed = time.Now()
			e.handle.LastUsed = e.lastUsed
			h := e.handle
			p.mu.Unlock()
			return h, nil
		}
		delete(p.entries, key)
		go p.closeEntry(e)
	}

	for p.max > 0 && len(p.entries) >= p.max {
		// Only an idle session may go: evicting the one the window is using
		// killed the running answer and showed up as "与 agent 的连接已断开".
		// The pool may therefore hold one more than max while every entry is
		// in use, which ends when the extra sessions are released.
		oldestKey, oldest := p.oldestIdle()
		if oldest == nil {
			break
		}
		delete(p.entries, oldestKey)
		go p.closeEntry(oldest)
	}

	t, err := p.newTransport(opts)
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	client := NewClient(t, onEvent, onDisconnect)
	now := time.Now()
	h := &SessionHandle{
		ID:        key,
		Client:    client,
		Transport: t,
		Options:   opts,
		LastUsed:  now,
	}
	p.counter++
	p.entries[key] = &poolEntry{handle: h, inUse: true, lastUsed: now, order: p.counter}
	p.mu.Unlock()
	return h, nil
}

// Rekey moves the live session at oldKey to newKey. A new session starts
// under a provisional key and learns its real session file from the RPC, so
// the entry has to follow without killing the process. It is a no-op when
// oldKey is absent, newKey already exists, or either is empty.
func (p *Pool) Rekey(oldKey, newKey string) {
	if oldKey == "" || newKey == "" || oldKey == newKey {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entries[oldKey]
	if e == nil {
		return
	}
	if _, exists := p.entries[newKey]; exists {
		return
	}
	delete(p.entries, oldKey)
	e.handle.ID = newKey
	p.entries[newKey] = e
	if e.idleTimer != nil && p.idle > 0 {
		e.idleTimer.Stop()
		e.idleTimer = time.AfterFunc(p.idle, func() { p.evictIdle(newKey, e) })
	}
}

// Release marks key idle and starts (or resets) its idle timer.
func (p *Pool) Release(key string) {
	p.mu.Lock()
	e := p.entries[key]
	if e == nil {
		p.mu.Unlock()
		return
	}
	p.counter++
	e.order = p.counter
	e.inUse = false
	e.lastUsed = time.Now()
	e.handle.LastUsed = e.lastUsed
	if p.idle > 0 {
		if e.idleTimer == nil {
			e.idleTimer = time.AfterFunc(p.idle, func() { p.evictIdle(key, e) })
		} else {
			e.idleTimer.Reset(p.idle)
		}
	}
	p.mu.Unlock()
}

// Touch refreshes key's LRU rank and, when idle, its idle deadline.
func (p *Pool) Touch(key string) {
	p.mu.Lock()
	if e := p.entries[key]; e != nil {
		p.counter++
		e.order = p.counter
		e.lastUsed = time.Now()
		e.handle.LastUsed = e.lastUsed
		if !e.inUse && e.idleTimer != nil && p.idle > 0 {
			e.idleTimer.Reset(p.idle)
		}
	}
	p.mu.Unlock()
}

// Len returns the number of pooled sessions.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Close closes every session (TERM -> 5s -> KILL on each process group) and
// makes the pool unusable. It is idempotent.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	entries := make([]*poolEntry, 0, len(p.entries))
	for _, e := range p.entries {
		entries = append(entries, e)
	}
	p.entries = make(map[string]*poolEntry)
	p.mu.Unlock()

	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(e *poolEntry) {
			defer wg.Done()
			p.closeEntry(e)
		}(e)
	}
	wg.Wait()
	return nil
}

// oldestIdle is the least recently used entry that nothing is using, nil when
// every entry is in use.
func (p *Pool) oldestIdle() (string, *poolEntry) {
	var key string
	var oldest *poolEntry
	for k, e := range p.entries {
		if e.inUse {
			continue
		}
		if oldest == nil || e.order < oldest.order {
			key, oldest = k, e
		}
	}
	return key, oldest
}

func (p *Pool) evictIdle(key string, e *poolEntry) {
	p.mu.Lock()
	if p.entries[key] != e || e.inUse {
		p.mu.Unlock()
		return
	}
	delete(p.entries, key)
	p.mu.Unlock()
	p.closeEntry(e)
}

func (p *Pool) closeEntry(e *poolEntry) {
	if e.idleTimer != nil {
		e.idleTimer.Stop()
	}
	if e.handle.Client != nil {
		_ = e.handle.Client.Close()
	}
}
