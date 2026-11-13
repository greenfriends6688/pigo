//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pigo/internal/agent"
)

// TestRealSessionShape opens a real session file and prints what get_messages
// returns, so the native transcript can be checked against the shapes the
// subprocess actually sends rather than an assumed one.
func TestRealSessionShape(t *testing.T) {
	path := os.Getenv("PI_SESSION_FILE")
	if path == "" {
		t.Skip("set PI_SESSION_FILE to a session .jsonl to inspect it")
	}
	dir := t.TempDir()
	// A private agent dir keeps the real ~/.pi/agent lock out of the way: the
	// session file is copied in so the shapes are still the real ones.
	agentDir := filepath.Join(dir, "agent")
	sessionsDir := filepath.Join(agentDir, "sessions", "copy")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(sessionsDir, filepath.Base(path))
	if err := os.WriteFile(copied, body, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := agent.Options{
		NodePath:    agent.ResolveNode(),
		RpcBundle:   agent.ResolveBundle(""),
		Cwd:         filepath.Dir(path),
		AgentDir:    agentDir,
		SessionPath: copied,
		LogPath:     filepath.Join(dir, "agent.log"),
	}
	if opts.NodePath == "" || opts.RpcBundle == "" {
		t.Skip("node or rpc-entry.js not available")
	}
	transport, err := agent.NewStdioTransport(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client := agent.NewClient(transport, func(agent.Frame) {}, func(error) {})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if _, err := client.Call(ctx, agent.Command{"type": agent.CmdGetState}); err != nil {
		t.Fatalf("get_state: %v", err)
	}
	resp, err := client.Call(ctx, agent.Command{"type": agent.CmdGetMessages})
	if err != nil {
		t.Fatalf("get_messages: %v", err)
	}
	if !resp.Success {
		t.Fatalf("get_messages failed: %s", resp.Error)
	}
	var payload struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(resp.Data, &payload); err != nil {
		t.Fatalf("decode: %v (raw: %.200s)", err, resp.Data)
	}
	t.Logf("messages: %d", len(payload.Messages))
	shown := 0
	for i, raw := range payload.Messages {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Role == "system" {
			continue
		}
		t.Logf("[%d] role=%s content=%.320s", i, m.Role, m.Content)
		if shown++; shown >= 6 {
			break
		}
	}
	converted := messagesFromRPC(payload.Messages)
	t.Logf("converted: %d messages", len(converted))
	for i, m := range converted {
		text := strings.ReplaceAll(m.Text, "\n", " ")
		if len(text) > 120 {
			text = text[:120] + "…"
		}
		t.Logf("  [%d] %s %q", i, m.Role, text)
	}
}
