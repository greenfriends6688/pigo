// Package chatview shows conversations with coding agents with NSGo's
// ui/chat: Conversation is one conversation's transcript and composer, and
// App a window of every conversation beside them. Both show an
// agio.Backend through a Model, the same for agents in this process
// (package host) and on a server (package wire).
package chatview

import (
	"time"

	"github.com/oneo-me/nsgo/core"
	agio "pigo/agio"
)

// Model follows a backend on the engine: State reads its latest snapshot
// and follows it, and requests run off the engine, as a remote backend
// answers over the network.
type Model struct {
	backend agio.Backend
	cx      *core.Scope
	state   agio.State
	rev     *core.Signal[uint64]
	// clock ticks each second while an agent works, for its timer, or
	// limits show, for their countdowns.
	clock *core.Signal[time.Time]
	// notice is why the last request failed, until another succeeds.
	notice *core.Signal[string]
}

// Bind follows b for as long as cx lives. It leaves closing b to the
// caller.
func Bind(cx *core.Scope, b agio.Backend) *Model {
	m := &Model{backend: b, cx: cx, state: b.State(), rev: core.NewSignal(cx, uint64(0)),
		clock: core.NewSignal(cx, time.Now()), notice: core.NewSignal(cx, "")}
	stop := b.Watch(func() { cx.Post(m.refresh) })
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				cx.Post(m.tick)
			case <-done:
				return
			}
		}
	}()
	cx.Cleanup(func() {
		stop()
		close(done)
	})
	return m
}

// Backend is the backend m follows.
func (m *Model) Backend() agio.Backend { return m.backend }

// State is the backend's latest state, which the caller follows.
func (m *Model) State() agio.State {
	m.rev.Get()
	return m.state
}

// Conversation is the conversation id, or an empty one while it is unknown.
func (m *Model) Conversation(id string) *agio.Conversation {
	if c := m.State().Conversation(id); c != nil {
		return c
	}
	return &agio.Conversation{ID: id}
}

// Notice is why the last request failed, empty once one succeeded.
func (m *Model) Notice() string { return m.notice.Get() }

func (m *Model) refresh() {
	m.state = m.backend.State()
	m.rev.Update(func(n uint64) uint64 { return n + 1 })
}

func (m *Model) tick() {
	// Limits count down to their next read and their resets.
	if len(m.state.Quotas) > 0 {
		m.clock.Set(time.Now())
		return
	}
	for _, c := range m.state.Conversations {
		if c.Running || c.Starting {
			m.clock.Set(time.Now())
			return
		}
	}
}

// now is the time, following the clock while an agent works.
func (m *Model) now() time.Time {
	m.clock.Get()
	return time.Now()
}

// Do runs a request off the engine and reports its failure as the notice;
// done, when set, then runs on the engine with its error.
func (m *Model) Do(request func(agio.Backend) error, done func(error)) {
	go func() {
		err := request(m.backend)
		m.cx.Post(func() {
			if err != nil {
				m.notice.Set(err.Error())
			} else {
				m.notice.Set("")
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

// Create adds a conversation, then calls done with its ID on the engine,
// where State already shows it.
func (m *Model) Create(c agio.Create, done func(id string)) {
	var id string
	m.Do(func(b agio.Backend) (err error) {
		id, err = b.Create(c)
		return err
	}, func(err error) {
		if err == nil {
			m.refresh()
			if done != nil {
				done(id)
			}
		}
	})
}

// live is fn following the backend.
func live[T any](m *Model, fn func() T) func() T {
	return func() T {
		m.rev.Get()
		return fn()
	}
}

// region builds its nodes again only when key changes, keeping what the
// user is doing inside it, such as typing or a picker's search, across
// other changes. key says everything build reads that it does not bind.
func region(m *Model, key func() string, build func(cx *core.Scope) core.Node) core.Node {
	return core.Func(func(cx *core.Scope) core.Node {
		memo := core.NewMemo(cx, live(m, key))
		return core.Dynamic(func(cx *core.Scope) core.Node {
			memo.Get()
			var node core.Node
			cx.Untrack(func() { node = build(cx) })
			return node
		})
	})
}
