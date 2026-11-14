package main

// This file is the seam between native's own pi engine and the design
// components copied from nsgo-agio. The components speak agio.Backend; this
// bridge answers for native's appState, so the engine (RPC client, pool,
// reducer) stays exactly as it was.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oneo-me/nsgo/core"

	agio "pigo/agio"
	agioagent "pigo/agio/agent"
	"pigo/agio/chatview"
	"pigo/agio/quota"
	"pigo/agio/transcript"
)

// ogioDraftID is New task's conversation id. It stays stable for the draft's
// life; the session file pi writes after the first prompt is mapped behind it.
const ogioDraftID = "draft"

// pendingSend is a prompt accepted before the session could take it.
type pendingSend struct {
	id   string
	text string
	atts []imageAttachment
}

// agioBridge implements agio.Backend over native's appState.
type agioBridge struct {
	s *appState

	mu       sync.Mutex
	snap     agio.State
	watchers map[int]func()
	next     int

	// pending is only touched on the UI thread.
	pending       []pendingSend
	flushing      bool
	pendingModel  *modelItem
	pendingEffort string
}

func newAgioBridge(s *appState) *agioBridge {
	return &agioBridge{s: s, watchers: map[int]func(){}}
}

// attachChat binds the design components to this window, once.
func (s *appState) attachChat(cx *core.Scope) {
	if s.chatModel != nil {
		return
	}
	s.bridge = newAgioBridge(s)
	s.bridge.publish()
	s.chatModel = chatview.Bind(cx, s.bridge)
	go s.refreshQuotas()
	// The component state follows the signals native's UI already keeps.
	cx.Effect(func() {
		s.selected.Get()
		s.connected.Get()
		s.running.Get()
		s.models.Get()
		s.providers.Get()
		s.modelName.Get()
		s.thinking.Get()
		s.piSetup.Get()
		s.sessionsLoading.Get()
		s.settingsRev.Get()
		s.turns.Get()
		s.bridge.publish()
	})
}

// State implements agio.Backend.
func (b *agioBridge) State() agio.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snap
}

// Watch implements agio.Backend.
func (b *agioBridge) Watch(fn func()) func() {
	b.mu.Lock()
	id := b.next
	b.next++
	b.watchers[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.watchers, id)
		b.mu.Unlock()
	}
}

// publish rebuilds the state on the UI thread and wakes the watchers.
func (b *agioBridge) publish() {
	b.flush()
	s := b.s

	tool := agio.Tool{Tool: agioagent.Pi, Path: s.piPath, Version: s.piVersion}
	tool.Catalog = b.catalog()
	// A missing runtime matters only while no configured model can answer;
	// native's own setup card owns the install instructions.
	if len(tool.Catalog.Models) == 0 {
		tool.Err = s.piSetup.GetUntracked()
	}

	c := b.conversation()
	snap := agio.State{
		Discovering:   s.sessionsLoading.GetUntracked() || s.connecting,
		Tools:         []agio.Tool{tool},
		Conversations: []*agio.Conversation{c},
		Quotas:        append([]agio.Quota(nil), s.quotas...),
	}
	b.mu.Lock()
	b.snap = snap
	watchers := make([]func(), 0, len(b.watchers))
	for _, fn := range b.watchers {
		watchers = append(watchers, fn)
	}
	b.mu.Unlock()
	for _, fn := range watchers {
		fn()
	}
}

// catalog is what pi offers, from the models the session reported and, before
// one is connected, from the providers the settings hold.
func (b *agioBridge) catalog() agioagent.Catalog {
	s := b.s
	out := agioagent.Catalog{Model: b.modelKey(), Effort: s.thinking.GetUntracked()}
	seen := map[string]bool{}
	add := func(id, name, provider string, reasoning bool, window int64, images bool) {
		key := id
		if provider != "" {
			key = provider + "/" + id
		}
		if seen[key] {
			return
		}
		seen[key] = true
		m := agioagent.Model{Tool: agioagent.Pi, ID: key, Name: name, Provider: provider, ContextWindow: window, Images: images}
		if reasoning {
			m.Efforts = []string{"off", "minimal", "low", "medium", "high", "xhigh"}
			m.DefaultEffort = s.thinking.GetUntracked()
			if m.DefaultEffort == "" {
				m.DefaultEffort = "medium"
			}
		}
		if key == out.Model {
			m.Default = true
		}
		out.Models = append(out.Models, m)
	}
	for _, m := range s.models.GetUntracked() {
		add(m.ID, m.Name, m.Provider, m.Reasoning, int64(m.ContextWindow), m.ImageInput)
	}
	if len(out.Models) == 0 {
		for _, p := range s.providers.GetUntracked() {
			for _, m := range p.Models {
				add(m.ID, m.Name, p.ID, m.Reasoning, int64(m.ContextWindow), m.ImageInput)
			}
		}
	}
	return out
}

// modelKey is the live model as provider/id, the key the components compare.
func (b *agioBridge) modelKey() string {
	s := b.s
	name := s.modelName.GetUntracked()
	if name == "" {
		return ""
	}
	for _, m := range s.models.GetUntracked() {
		if m.Name == name || m.ID == name {
			if m.Provider != "" {
				return m.Provider + "/" + m.ID
			}
			return m.ID
		}
	}
	for _, p := range s.providers.GetUntracked() {
		for _, m := range p.Models {
			if m.Name == name || m.ID == name {
				return p.ID + "/" + m.ID
			}
		}
	}
	return name
}

// conversation is the selected conversation, or New task's draft.
func (b *agioBridge) conversation() *agio.Conversation {
	s := b.s
	id := s.selected.GetUntracked()
	var turns []*transcript.Turn
	if !s.transcriptLoading.GetUntracked() {
		turns = projectTurns(s.turns.GetUntracked())
	}
	if id == "" {
		return &agio.Conversation{ID: ogioDraftID, Dir: s.currentCwd(), Model: b.modelKey(),
			Effort: s.thinking.GetUntracked(), Starting: s.connecting, Running: s.running.GetUntracked(),
			Loading: s.transcriptLoading.GetUntracked(), ContextTokens: int64(s.ctxUsed.GetUntracked()),
			ContextWindow: int64(s.ctxWindow.GetUntracked()), Turns: turns}
	}
	c := &agio.Conversation{ID: id, Title: s.bridgeTitle(id), Dir: s.currentCwd(), Model: b.modelKey(),
		Effort: s.thinking.GetUntracked(), Cursor: id, Bound: true, Starting: s.connecting,
		Running: s.running.GetUntracked(), Loading: s.transcriptLoading.GetUntracked(),
		ContextTokens: int64(s.ctxUsed.GetUntracked()), ContextWindow: int64(s.ctxWindow.GetUntracked()), Turns: turns}
	if info, ok := s.store.Info(id); ok {
		c.Dir = info.Cwd
		c.Created = info.Created
		c.Updated = info.Modified
		if c.Title == "" {
			c.Title = info.Name
		}
	}
	return c
}

// bridgeTitle is a session's display title, from its name or first prompt.
func (s *appState) bridgeTitle(id string) string {
	if id == "" || id == ogioDraftID {
		return ""
	}
	if info, ok := s.store.Info(id); ok {
		if info.Name != "" {
			return info.Name
		}
		if info.FirstMessage != "" {
			return truncateRunes(info.FirstMessage, 60)
		}
	}
	return baseName(id)
}

// projectTurns is the structural view the components need. Rich cards still
// read the original turnRow through the Turn hook.
func projectTurns(rows []turnRow) []*transcript.Turn {
	out := make([]*transcript.Turn, 0, len(rows))
	for _, r := range rows {
		t := &transcript.Turn{ID: r.ID, Prompt: r.Prompt, Status: turnStatus(r), Started: r.StartedAt,
			Ended: r.StartedAt.Add(r.Elapsed), Err: r.Error}
		if r.Final != "" {
			t.Segments = append(t.Segments, &transcript.Segment{ID: r.ID + "-final", Kind: transcript.Text,
				Text: r.Final, Status: transcript.Completed})
		}
		out = append(out, t)
	}
	return out
}

func turnStatus(r turnRow) transcript.Status {
	switch {
	case r.Running:
		return transcript.Running
	case r.Failed || r.Error != "":
		return transcript.Failed
	default:
		return transcript.Completed
	}
}

// Create implements agio.Backend.
func (b *agioBridge) Create(c agio.Create) (string, error) {
	if c.Cursor != "" {
		path := c.Cursor
		b.s.cx.Post(func() { b.s.openSession(path) })
		return path, nil
	}
	dir := c.Dir
	id := ogioDraftID
	b.s.cx.Post(func() {
		if dir != "" && dir != b.s.currentCwd() {
			b.s.openProject(dir)
		}
		b.s.newSession()
	})
	return id, nil
}

// Send implements agio.Backend.
func (b *agioBridge) Send(id string, m agio.Message) error {
	b.s.cx.Post(func() { b.sendNow(id, m) })
	return nil
}

func (b *agioBridge) sendNow(id string, m agio.Message) {
	s := b.s
	if id != "" && id != ogioDraftID && id != s.currentSessionPath() && id != s.selected.GetUntracked() {
		s.openSession(id)
	}
	text := m.Text
	var atts []imageAttachment
	for _, path := range m.Files {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			data, err := os.ReadFile(path)
			if err == nil {
				atts = append(atts, imageAttachment{Name: filepath.Base(path), MIME: mimeOf(path), Data: data})
				continue
			}
		}
		if text != "" {
			text += "\n"
		}
		text += "@" + path
	}
	// New task has no session until its first prompt: start one, then hold
	// the prompt until it answers.
	if id == ogioDraftID && s.current() == nil && !s.connecting {
		s.newSession()
	}
	// The engine may still be connecting; hold the prompt until it can take it.
	if s.current() == nil || !s.connected.GetUntracked() {
		b.pending = append(b.pending, pendingSend{id: id, text: text, atts: atts})
		return
	}
	if len(atts) > 0 {
		s.attachments.Set(atts)
	}
	s.send(text)
}

// queue holds a prompt native's composer accepted before the session was
// ready. It runs on the UI thread.
func (b *agioBridge) queue(text string, atts []imageAttachment) {
	id := b.s.selected.GetUntracked()
	if id == "" {
		id = ogioDraftID
	}
	b.pending = append(b.pending, pendingSend{id: id, text: text, atts: append([]imageAttachment(nil), atts...)})
}

// flush sends the prompts held while the session connected.
func (b *agioBridge) flush() {
	if b.flushing || len(b.pending) == 0 {
		return
	}
	s := b.s
	if s.current() == nil || !s.connected.GetUntracked() {
		return
	}
	b.flushing = true
	if b.pendingModel != nil {
		m := *b.pendingModel
		b.pendingModel = nil
		s.setModel(m)
	}
	if b.pendingEffort != "" {
		e := b.pendingEffort
		b.pendingEffort = ""
		s.setThinkingLevel(e)
	}
	pending := b.pending
	b.pending = nil
	for _, p := range pending {
		if p.id != ogioDraftID && p.id != "" && p.id != s.currentSessionPath() && p.id != s.selected.GetUntracked() {
			b.pending = append(b.pending, p)
			continue
		}
		if len(p.atts) > 0 {
			s.attachments.Set(p.atts)
		}
		s.send(p.text)
	}
	b.flushing = false
}

// Stop implements agio.Backend.
func (b *agioBridge) Stop(string) error {
	b.s.cx.Post(b.s.stop)
	return nil
}

// Choose implements agio.Backend.
func (b *agioBridge) Choose(id string, c agio.Choice) error {
	b.s.cx.Post(func() {
		s := b.s
		if c.Title != nil {
			s.renameTo(*c.Title)
		}
		if c.Dir != nil {
			s.openProject(*c.Dir)
		}
		// A draft has no child yet. Keep the choice and apply it when the
		// session comes up, as the model picker must work before the first
		// prompt.
		draft := id == ogioDraftID && s.current() == nil
		if c.Model != nil {
			if m, ok := b.modelItem(*c.Model); ok {
				if draft {
					b.pendingModel = &m
					s.modelName.Set(m.Name)
				} else {
					s.setModel(m)
				}
			}
		}
		if c.Effort != nil {
			if draft {
				b.pendingEffort = *c.Effort
				s.thinking.Set(*c.Effort)
			} else {
				s.setThinkingLevel(*c.Effort)
			}
		}
	})
	return nil
}

// modelItem finds a model by provider/id, or builds one from the key.
func (b *agioBridge) modelItem(key string) (modelItem, bool) {
	provider, id := "", key
	if p, rest, ok := strings.Cut(key, "/"); ok {
		provider, id = p, rest
	}
	for _, m := range b.s.models.GetUntracked() {
		if m.ID == id && (provider == "" || m.Provider == provider) {
			return m, true
		}
	}
	for _, p := range b.s.providers.GetUntracked() {
		if provider != "" && p.ID != provider {
			continue
		}
		for _, m := range p.Models {
			if m.ID == id {
				return modelItem{ID: m.ID, Name: m.Name, Provider: p.ID, Reasoning: m.Reasoning}, true
			}
		}
	}
	if provider == "" {
		return modelItem{}, false
	}
	return modelItem{ID: id, Name: id, Provider: provider}, true
}

// Remove implements agio.Backend.
func (b *agioBridge) Remove(id string) error {
	b.s.cx.Post(func() {
		if id == ogioDraftID || id == "" {
			b.s.newSession()
			return
		}
		b.s.deleteTarget.Set(id)
		b.s.commitDelete()
	})
	return nil
}

// Discover implements agio.Backend.
func (b *agioBridge) Discover() {
	b.s.cx.Post(b.s.reconnect)
}

// Enable implements agio.Backend. Native has one agent, pi, and no switch.
func (b *agioBridge) Enable(agioagent.Tool, bool) error { return nil }

// EnableModels implements agio.Backend. Native's Models page owns the
// whitelist, so the component's switches stay inert.
func (b *agioBridge) EnableModels([]string, bool) error { return nil }

// RefreshQuota implements agio.Backend. Quotas arrive in a later pass.
func (b *agioBridge) RefreshQuota(p quota.Provider) { b.s.cx.Post(b.s.refreshQuotas) }

// Close implements agio.Backend; native's cleanup closes the pool.
func (b *agioBridge) Close() error { return nil }

// refreshQuotas reads the accounts pi is signed in to, once per five
// minutes, off the UI thread.
func (s *appState) refreshQuotas() {
	if os.Getenv("PIGO_NO_AGENT") == "1" || s.quotaBusy {
		return
	}
	s.quotaBusy = true
	home, _ := os.UserHomeDir()
	read := quota.Reader(home)
	env := os.Environ()
	go func() {
		now := time.Now()
		out := make([]agio.Quota, len(quota.Providers))
		var wg sync.WaitGroup
		for i, p := range quota.Providers {
			i, p := i, p
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				usage, err := read(ctx, env, p)
				cancel()
				q := agio.Quota{Provider: p, Checked: now, Next: now.Add(5 * time.Minute)}
				if err == nil {
					q.Usage = &usage
				} else {
					q.Err = err.Error()
				}
				out[i] = q
			}()
		}
		wg.Wait()
		s.cx.Post(func() {
			s.quotas = out
			s.quotaBusy = false
			if s.bridge != nil {
				s.bridge.publish()
			}
		})
	}()
}
