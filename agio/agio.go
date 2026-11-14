// Package agio is the contract between conversations with coding agents and
// what shows them. A Backend runs or mirrors conversations: package host
// runs them in this process, and package wire serves a Backend to clients
// in other processes and mirrors it there. Package chatview shows any
// Backend with NSGo, so an application chooses where its agents run by the
// Backend it passes, not by its views.
package agio

import (
	"slices"
	"time"

	"pigo/agio/agent"
	"pigo/agio/quota"
	"pigo/agio/transcript"
)

// Backend runs or mirrors conversations. Its methods may be called from any
// goroutine and never wait for an agent: what they start is reported
// through State.
type Backend interface {
	// State returns a snapshot that later changes leave alone.
	State() State
	// Watch calls fn, on any goroutine, after the state changed; changes
	// close together are reported once. stop ends it.
	Watch(fn func()) (stop func())
	// Create adds a conversation, which starts its agent with its first
	// message, and returns its ID once State shows it.
	Create(c Create) (string, error)
	// Send sends a message, starting the conversation's agent when needed.
	// A message to a running conversation interrupts the run instead of
	// queueing.
	Send(id string, m Message) error
	// Stop aborts the conversation's current run.
	Stop(id string) error
	// Choose changes the conversation's model, reasoning level or service
	// tier, live when its agent runs, or its directory. A conversation
	// keeps the tool and directory of its first message.
	Choose(id string, c Choice) error
	// Remove closes the conversation's agent and forgets it.
	Remove(id string) error
	// Discover looks for the agent CLIs again, such as after installing one.
	Discover()
	// Enable turns a tool on or off. A tool turned off is not looked for,
	// offers no models and starts no agents; its running agents finish.
	Enable(tool agent.Tool, on bool) error
	// EnableModels turns models on or off by key (agent.Key). A model
	// turned off is neither offered nor a default; conversations already
	// bound to it keep it.
	EnableModels(keys []string, on bool) error
	// RefreshQuota reads an account's limits now.
	RefreshQuota(p quota.Provider)
	// Close stops every agent the backend started, or disconnects.
	Close() error
}

// State is everything a backend shows.
type State struct {
	// Discovering is set while agent CLIs are being looked for.
	Discovering bool `json:"discovering,omitempty"`
	// Tools are every agent CLI the backend knows, usable or not; one
	// never found has neither Path, Catalog nor Err.
	Tools []Tool `json:"tools,omitempty"`
	// Quotas are the limits of the accounts signed in, in quota.Providers'
	// order, once read.
	Quotas []Quota `json:"quotas,omitempty"`
	// Conversations are newest first.
	Conversations []*Conversation `json:"conversations,omitempty"`
	// Err is why a remote backend cannot be reached, empty while it can.
	Err string `json:"err,omitempty"`
}

// Tool is one agent CLI and what looking for it found.
type Tool struct {
	Tool agent.Tool `json:"tool"`
	// Path is the executable, Version what it reported, both empty when
	// unknown.
	Path    string        `json:"path,omitempty"`
	Version string        `json:"version,omitempty"`
	Catalog agent.Catalog `json:"catalog"`
	// Err is why the tool cannot run.
	Err string `json:"err,omitempty"`
	// Disabled is set while the tool is turned off; DisabledModels are the
	// IDs of its models turned off.
	Disabled       bool     `json:"disabled,omitempty"`
	DisabledModels []string `json:"disabledModels,omitempty"`
}

// Offers reports whether the tool offers its model id.
func (t Tool) Offers(id string) bool { return t.Ready() && !slices.Contains(t.DisabledModels, id) }

// Quota is one account's latest limits. A failed read keeps the previous
// Usage and says why in Err.
type Quota struct {
	Provider quota.Provider `json:"provider"`
	Usage    *quota.Usage   `json:"usage,omitempty"`
	Err      string         `json:"err,omitempty"`
	// Expired is set when the credential expired, Status to the HTTP
	// status of a failed request, with the provider's Message.
	Expired  bool   `json:"expired,omitempty"`
	Status   int    `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	Checking bool   `json:"checking,omitempty"`
	// Checked is when it was last read and Next when it is read again.
	Checked time.Time `json:"checked,omitzero"`
	Next    time.Time `json:"next,omitzero"`
}

// Ready reports whether the tool can run.
func (t Tool) Ready() bool { return !t.Disabled && t.Err == "" && len(t.Catalog.Models) > 0 }

// Ready reports whether any tool can run a model.
func (s State) Ready() bool { return len(s.Models()) > 0 }

// Models lists the usable tools' models in their order, but those turned
// off.
func (s State) Models() []agent.Model {
	var models []agent.Model
	for _, t := range s.Tools {
		for _, m := range t.Catalog.Models {
			if t.Offers(m.ID) {
				models = append(models, m)
			}
		}
	}
	return models
}

// Quota returns the limits of the account a model key uses, or nil.
func (s State) Quota(key string) *Quota {
	m, ok := s.Model(key)
	if !ok {
		return nil
	}
	p, ok := quota.For(m)
	if !ok {
		return nil
	}
	for i := range s.Quotas {
		if s.Quotas[i].Provider == p {
			return &s.Quotas[i]
		}
	}
	return nil
}

// Model returns a model of a usable tool by key (agent.Key), turned off or
// not.
func (s State) Model(key string) (agent.Model, bool) {
	tool, id := agent.ParseKey(key)
	for _, t := range s.Tools {
		if t.Tool != tool || !t.Ready() {
			continue
		}
		for _, m := range t.Catalog.Models {
			if m.ID == id {
				return m, true
			}
		}
	}
	return agent.Model{}, false
}

// Conversation returns the conversation id, or nil.
func (s State) Conversation(id string) *Conversation {
	for _, c := range s.Conversations {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Conversation is one conversation with an agent.
type Conversation struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Dir is where its agent works, on the backend's computer.
	Dir string `json:"dir"`
	// Model is the effective model key (agent.Key), Effort its reasoning
	// level and Tier its service tier, "" for standard.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Tier   string `json:"tier,omitempty"`
	// Cursor resumes the agent's own session after a restart, once it
	// started one.
	Cursor string `json:"cursor,omitempty"`
	// Bound is set once the conversation's tool is fixed by its first
	// message.
	Bound bool `json:"bound,omitempty"`
	// Starting is set while its agent starts, Running while it works and
	// Loading while its saved history is read.
	Starting bool `json:"starting,omitempty"`
	Running  bool `json:"running,omitempty"`
	Loading  bool `json:"loading,omitempty"`
	// Err is a problem outside any turn, such as a failed configuration.
	Err           string    `json:"err,omitempty"`
	ContextTokens int64     `json:"contextTokens,omitempty"`
	ContextWindow int64     `json:"contextWindow,omitempty"`
	Created       time.Time `json:"created,omitzero"`
	Updated       time.Time `json:"updated,omitzero"`
	// Turns travel apart from the rest, as patches.
	Turns []*transcript.Turn `json:"-"`
}

// Create describes a new conversation. Its agent works in Dir, or where
// the backend chooses when it is empty; Cursor resumes a session the agent
// saved, whose history it reads, and then Model must name the session's
// tool, and Created and Updated, when set, keep when it was. Empty choices
// follow the tools' defaults.
type Create struct {
	Dir     string    `json:"dir"`
	Title   string    `json:"title,omitempty"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Tier    string    `json:"tier,omitempty"`
	Cursor  string    `json:"cursor,omitempty"`
	Created time.Time `json:"created,omitzero"`
	Updated time.Time `json:"updated,omitzero"`
}

// Message is what the user sends. Files are paths on the backend's
// computer; images among them go inline to models that accept them.
type Message struct {
	Text  string   `json:"text"`
	Files []string `json:"files,omitempty"`
}

// Choice changes what its set fields name. Dir is a directory on the
// backend's computer; Title renames the conversation, which an agent
// naming it later renames again.
type Choice struct {
	Title  *string `json:"title,omitempty"`
	Dir    *string `json:"dir,omitempty"`
	Model  *string `json:"model,omitempty"`
	Effort *string `json:"effort,omitempty"`
	Tier   *string `json:"tier,omitempty"`
}
