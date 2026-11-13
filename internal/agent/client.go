package agent

import (
	"context"
	"errors"
	"strconv"
	"sync"
)

// ErrDisconnected is returned by a pending Call when the transport ends before
// its response arrives.
var ErrDisconnected = errors.New("agent: transport disconnected")

// ErrClientClosed is returned by Call after Close and is passed to
// onDisconnect for an explicit client shutdown.
var ErrClientClosed = errors.New("agent: client closed")

// Client multiplexes responses to Callers and forwards every other frame to
// onEvent. All parsing and callbacks run on a single dispatch goroutine, never
// on the transport's stdout read loop.
type Client struct {
	t            Transport
	onEvent      func(Frame)
	onDisconnect func(error)

	mu      sync.Mutex
	pending map[string]chan *Frame
	nextID  uint64
	closed  bool

	disconnected chan struct{}
	discErr      error
	discOnce     sync.Once
	stopped      chan struct{}
}

// NewClient starts the dispatch goroutine for t. onEvent is always invoked from
// that goroutine; onDisconnect is invoked exactly once when the transport ends.
func NewClient(t Transport, onEvent func(Frame), onDisconnect func(error)) *Client {
	c := &Client{
		t:            t,
		onEvent:      onEvent,
		onDisconnect: onDisconnect,
		pending:      make(map[string]chan *Frame),
		disconnected: make(chan struct{}),
		stopped:      make(chan struct{}),
	}
	go c.dispatch()
	return c
}

// Call injects a fresh id, sends cmd and waits only for the matching response
// frame. Responses may arrive out of order and interleaved with events; the
// id->channel map is mutex-protected. Context cancellation and transport
// disconnect both wake every waiting Call immediately, so no goroutine leaks.
func (c *Client) Call(ctx context.Context, cmd Command) (*Frame, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	c.nextID++
	id := strconv.FormatUint(c.nextID, 10)
	ch := make(chan *Frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	out := cloneCommand(cmd)
	out[FID] = id
	if err := c.t.Send(out); err != nil {
		c.dropPending(id)
		return nil, err
	}

	select {
	case f := <-ch:
		return f, nil
	case <-ctx.Done():
		c.dropPending(id)
		return nil, ctx.Err()
	case <-c.disconnected:
		c.dropPending(id)
		return nil, c.disconnectError()
	}
}

// Send writes cmd fire-and-forget: it does not inject an id and does not wait
// for a response.
func (c *Client) Send(cmd Command) error {
	return c.t.Send(cloneCommand(cmd))
}

// Done is closed when the client disconnects or is closed.
func (c *Client) Done() <-chan struct{} { return c.disconnected }

// Close stops the transport and wakes every pending Call. It is idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	err := c.t.Close()
	c.finishDisconnect(ErrClientClosed)
	return err
}

func (c *Client) dispatch() {
	defer close(c.stopped)
	frames := c.t.Frames()
	for {
		select {
		case line, ok := <-frames:
			if !ok {
				c.finishDisconnect(c.t.Err())
				return
			}
			c.handleLine(line)
		case <-c.disconnected:
			return
		}
	}
}

func (c *Client) handleLine(line []byte) {
	f, err := ParseFrame(line)
	if err != nil {
		// Blank or malformed lines are ignored; framing stays robust.
		return
	}

	if f.Type == FrameResponse && f.ID != "" {
		c.mu.Lock()
		ch := c.pending[f.ID]
		if ch != nil {
			delete(c.pending, f.ID)
		}
		c.mu.Unlock()
		if ch != nil {
			ch <- &f // buffered, never blocks
			return
		}
	}

	// Events, and responses with no waiting Caller, go to the reducer.
	if c.onEvent != nil {
		c.onEvent(f)
	}
}

func (c *Client) finishDisconnect(err error) {
	c.discOnce.Do(func() {
		c.mu.Lock()
		c.discErr = err
		c.mu.Unlock()
		close(c.disconnected)
		if c.onDisconnect != nil {
			c.onDisconnect(err)
		}
	})
}

func (c *Client) disconnectError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.discErr != nil {
		return c.discErr
	}
	return ErrDisconnected
}

func (c *Client) dropPending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func cloneCommand(cmd Command) Command {
	out := make(Command, len(cmd)+1)
	for k, v := range cmd {
		out[k] = v
	}
	return out
}
