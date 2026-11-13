package agent

import (
	"fmt"
	"sync"
)

// fakeTransport is an in-memory Transport used by the client and pool tests.
// Tests push raw lines onto frames and can simulate stdout EOF with eof().
type fakeTransport struct {
	tail       string
	frames     chan []byte
	done       chan struct{}
	sentNotify chan struct{}

	mu       sync.Mutex
	sent     []Command
	err      error
	closed   bool
	eofOnce  sync.Once
	doneOnce sync.Once
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		frames:     make(chan []byte, 32),
		done:       make(chan struct{}),
		sentNotify: make(chan struct{}, 32),
	}
}

func (f *fakeTransport) Send(cmd Command) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return ErrClosed
	}
	cp := make(Command, len(cmd))
	for k, v := range cmd {
		cp[k] = v
	}
	f.sent = append(f.sent, cp)
	select {
	case f.sentNotify <- struct{}{}:
	default:
	}
	return nil
}

func (f *fakeTransport) Frames() <-chan []byte { return f.frames }
func (f *fakeTransport) Done() <-chan struct{} { return f.done }

func (f *fakeTransport) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeTransport) StderrTail() string { return f.tail }

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.doneOnce.Do(func() { close(f.done) })
	return nil
}

// push delivers one raw line to the client's dispatch goroutine.
func (f *fakeTransport) push(line string) { f.frames <- []byte(line) }

// eof simulates stdout reaching EOF (and the process ending).
func (f *fakeTransport) eof(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	f.eofOnce.Do(func() { close(f.frames) })
	f.doneOnce.Do(func() { close(f.done) })
}

func (f *fakeTransport) sentSnapshot() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Command, len(f.sent))
	copy(out, f.sent)
	return out
}

// waitSent blocks until at least n commands have been written to stdin, which
// guarantees the matching Call has registered its id->channel entry.
func (f *fakeTransport) waitSent(n int) {
	for len(f.sentSnapshot()) < n {
		<-f.sentNotify
	}
}

// responseLine builds a success response frame for a given id and command.
func responseLine(id, command, data string) string {
	return fmt.Sprintf(`{"id":%q,"type":"response","command":%q,"success":true,"data":%s}`, id, command, data)
}

// eventLine builds a simple event frame.
func eventLine(evType string) string {
	return fmt.Sprintf(`{"type":%q}`, evType)
}
