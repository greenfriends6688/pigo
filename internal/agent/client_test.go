package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

type callResult struct {
	frame *Frame
	err   error
}

func TestCallOutOfOrderResponses(t *testing.T) {
	f := newFakeTransport()
	events := make(chan Frame, 8)
	c := NewClient(f, func(fr Frame) { events <- fr }, func(error) {})
	defer c.Close()

	rA := make(chan callResult, 1)
	rB := make(chan callResult, 1)
	go func() {
		fr, err := c.Call(context.Background(), Command{FType: CmdGetState})
		rA <- callResult{fr, err}
	}()
	go func() {
		fr, err := c.Call(context.Background(), Command{FType: CmdGetMessages})
		rB <- callResult{fr, err}
	}()
	f.waitSent(2)

	// The two goroutines may register in either order, so discover the ids
	// from the commands actually written to stdin.
	ids := map[string]string{}
	for _, cmd := range f.sentSnapshot() {
		typ, _ := cmd[FType].(string)
		id, _ := cmd[FID].(string)
		ids[typ] = id
	}
	idState, idMsgs := ids[CmdGetState], ids[CmdGetMessages]
	if idState == "" || idMsgs == "" || idState == idMsgs {
		t.Fatalf("Call did not inject distinct ids: %v", ids)
	}

	// Responses arrive in reverse order of the calls.
	f.push(responseLine(idMsgs, CmdGetMessages, `{"messages":[]}`))
	f.push(responseLine(idState, CmdGetState, `{"messageCount":3}`))

	a, b := <-rA, <-rB
	if a.err != nil || b.err != nil {
		t.Fatalf("Call errors: A=%v B=%v", a.err, b.err)
	}
	if a.frame.Command != CmdGetState || b.frame.Command != CmdGetMessages {
		t.Fatalf("responses crossed: A=%q B=%q", a.frame.Command, b.frame.Command)
	}
	if string(a.frame.Data) != `{"messageCount":3}` || string(b.frame.Data) != `{"messages":[]}` {
		t.Fatalf("wrong data: A=%s B=%s", a.frame.Data, b.frame.Data)
	}
	select {
	case fr := <-events:
		t.Fatalf("matched response leaked to onEvent: %+v", fr)
	default:
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending map not drained: %d", n)
	}
}

func TestCallContextCancel(t *testing.T) {
	f := newFakeTransport()
	c := NewClient(f, nil, nil)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, Command{FType: CmdGetState})
		errCh <- err
	}()
	f.waitSent(1)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return on context cancellation")
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending map leaked after cancel: %d", n)
	}
}

func TestCallEOFDisconnectBroadcast(t *testing.T) {
	f := newFakeTransport()
	boom := errors.New("boom")
	discCh := make(chan error, 2)
	c := NewClient(f, nil, func(err error) { discCh <- err })

	errCh := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), Command{FType: CmdGetState})
		errCh <- err
	}()
	f.waitSent(1)
	f.eof(boom)

	select {
	case err := <-errCh:
		if !errors.Is(err, boom) {
			t.Fatalf("pending Call err = %v, want boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending Call was not woken on EOF")
	}
	select {
	case err := <-discCh:
		if !errors.Is(err, boom) {
			t.Fatalf("onDisconnect err = %v, want boom", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onDisconnect was not called")
	}
	select {
	case <-discCh:
		t.Fatal("onDisconnect called more than once")
	default:
	}
	if _, open := <-c.Done(); open {
		t.Fatal("Client.Done should be closed after disconnect")
	}
}

func TestEventsInterleavedWithResponse(t *testing.T) {
	f := newFakeTransport()
	events := make(chan Frame, 8)
	c := NewClient(f, func(fr Frame) { events <- fr }, nil)
	defer c.Close()

	res := make(chan *Frame, 1)
	go func() {
		fr, _ := c.Call(context.Background(), Command{FType: CmdPrompt})
		res <- fr
	}()
	f.waitSent(1)

	// Requirement 6: responses and events share stdout; events may precede the
	// response and must not disturb the waiting Call.
	f.push(eventLine(EvAgentStart))
	f.push(eventLine(EvMessageStart))
	for i := 0; i < 2; i++ {
		select {
		case fr := <-events:
			if fr.Type != EvAgentStart && fr.Type != EvMessageStart {
				t.Fatalf("unexpected event %q", fr.Type)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event was not delivered to onEvent")
		}
	}
	select {
	case <-res:
		t.Fatal("Call returned before its response frame")
	default:
	}

	id, _ := f.sentSnapshot()[0][FID].(string)
	f.push(responseLine(id, CmdPrompt, `{"disposition":"started"}`))
	select {
	case fr := <-res:
		if fr.Command != CmdPrompt || string(fr.Data) != `{"disposition":"started"}` {
			t.Fatalf("wrong response: %+v", fr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return on its response frame")
	}

	// After the prompt's started response all progress is delivered as events.
	f.push(eventLine(EvMessageEnd))
	select {
	case fr := <-events:
		if fr.Type != EvMessageEnd {
			t.Fatalf("post-response event = %q", fr.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("post-response event lost")
	}
}

func TestSendIsFireAndForget(t *testing.T) {
	f := newFakeTransport()
	c := NewClient(f, nil, nil)
	defer c.Close()

	// A command without an id stays without an id (Send never injects one).
	if err := c.Send(Command{FType: CmdAbort}); err != nil {
		t.Fatal(err)
	}
	f.waitSent(1)
	cmd := f.sentSnapshot()[0]
	if _, ok := cmd[FID]; ok {
		t.Fatal("Send must not inject an id")
	}
	if cmd[FType] != CmdAbort {
		t.Fatalf("type = %v", cmd[FType])
	}

	// Extension UI responses legitimately carry their own dialog id; Send must
	// pass it through unchanged.
	if err := c.Send(ConfirmResponse("dlg-1", true)); err != nil {
		t.Fatal(err)
	}
	f.waitSent(2)
	cmd2 := f.sentSnapshot()[1]
	if cmd2[FType] != CmdExtensionUIResponse || cmd2[FID] != "dlg-1" || cmd2[FConfirmed] != true {
		t.Fatalf("extension_ui_response mangled: %v", cmd2)
	}
}
