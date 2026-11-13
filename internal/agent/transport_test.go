package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildArgsEmpty(t *testing.T) {
	got := buildArgs(Options{})
	want := []string{"--mode", "rpc"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs = %v, want %v", got, want)
	}
}

func TestBuildArgsSessionVariants(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want []string
	}{
		{"resume", Options{SessionPath: "/tmp/s.jsonl"}, []string{"--mode", "rpc", "--session", "/tmp/s.jsonl"}},
		{"new", Options{SessionID: "abc-123"}, []string{"--mode", "rpc", "--session-id", "abc-123"}},
		{"fork", Options{ForkID: "fork-9"}, []string{"--mode", "rpc", "--fork", "fork-9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildArgs(tc.opts); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("buildArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildArgsAllFlags(t *testing.T) {
	opts := Options{
		Tools:        "read,bash",
		ExcludeTools: "ask_question",
		NoMCP:        true,
		ExtraArgs:    []string{"--model", "gpt-4o"},
	}
	want := []string{
		"--mode", "rpc",
		"--tools", "read,bash",
		"--exclude-tools", "ask_question",
		"--no-mcp",
		"--model", "gpt-4o",
	}
	if got := buildArgs(opts); !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs = %v, want %v", got, want)
	}
}

func TestResolveBundleResourceDirPriority(t *testing.T) {
	dir := t.TempDir()
	rpcDir := filepath.Join(dir, "rpc")
	if err := os.MkdirAll(rpcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(rpcDir, "rpc-entry.js")
	if err := os.WriteFile(bundle, []byte("// stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolveBundle(dir); got != bundle {
		t.Fatalf("ResolveBundle = %q, want %q", got, bundle)
	}
}

func TestResolveBundleNoPanic(t *testing.T) {
	// The npm-global fallback is environment-dependent; merely assert the
	// result is a string and, when non-empty, points at a real file.
	got := ResolveBundle("")
	if got != "" {
		if _, err := os.Stat(got); err != nil {
			t.Fatalf("ResolveBundle returned %q but stat failed: %v", got, err)
		}
	}
}

func TestResolveNodeNonEmpty(t *testing.T) {
	if got := ResolveNode(); got == "" {
		t.Fatal("ResolveNode returned an empty string")
	}
}

func TestResolveAgentDirEnv(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/custom/agent/dir")
	if got := ResolveAgentDir(); got != "/custom/agent/dir" {
		t.Fatalf("ResolveAgentDir = %q", got)
	}
}

func TestResolveAgentDirFallback(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	got := ResolveAgentDir()
	if filepath.Base(got) != "agent" || !contains(got, ".pi") {
		t.Fatalf("ResolveAgentDir fallback = %q", got)
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

func TestGuardArgsPresent(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "rpc-entry.js")
	guard := filepath.Join(dir, "epipe-guard.cjs")
	if err := os.WriteFile(guard, []byte("// guard"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"--require", guard}
	if got := guardArgs(bundle); !reflect.DeepEqual(got, want) {
		t.Fatalf("guardArgs = %v, want %v", got, want)
	}
}

func TestGuardArgsAbsent(t *testing.T) {
	if got := guardArgs(filepath.Join(t.TempDir(), "rpc-entry.js")); got != nil {
		t.Fatalf("guardArgs = %v, want nil", got)
	}
}
