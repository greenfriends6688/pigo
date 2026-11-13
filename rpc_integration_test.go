//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pigo/internal/agent"
)

// TestAgentRPCIntegration is the PLAN §8 P1 acceptance check against the real
// pi RPC subprocess. It is opt-in (`go test -tags integration`) because it
// spawns node; the default `go tool nsgo test` stays hermetic.
func TestAgentRPCIntegration(t *testing.T) {
	dir := t.TempDir()
	opts := agent.Options{
		NodePath:  agent.ResolveNode(),
		RpcBundle: agent.ResolveBundle(""),
		Cwd:       dir,
		AgentDir:  filepath.Join(dir, "agent"),
		SessionID: "native-integration",
		LogPath:   filepath.Join(dir, "agent.log"),
	}
	if opts.NodePath == "" || opts.RpcBundle == "" {
		t.Skip("node or rpc-entry.js not available")
	}
	if err := os.MkdirAll(opts.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transport, err := agent.NewStdioTransport(opts)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan agent.Frame, 16)
	disconnected := make(chan error, 1)
	client := agent.NewClient(transport,
		func(f agent.Frame) {
			select {
			case events <- f:
			default:
			}
		},
		func(err error) { disconnected <- err },
	)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	state, err := client.Call(ctx, agent.Command{"type": agent.CmdGetState})
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if !state.Success || state.Command != agent.CmdGetState {
		t.Fatalf("get_state failed: %+v", state)
	}
	if len(state.Data) == 0 || !contains(string(state.Data), "sessionId") {
		t.Fatalf("get_state returned no session: %s", state.Data)
	}
	cmds, err := client.Call(ctx, agent.Command{"type": agent.CmdGetCommands})
	if err != nil {
		t.Fatalf("get_commands: %v", err)
	}
	if !cmds.Success {
		t.Fatalf("get_commands failed: %s", cmds.Error)
	}

	if err := transport.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-disconnected:
	case <-time.After(10 * time.Second):
		t.Fatal("no disconnect callback after closing the transport")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
