// Package config owns the read/write boundary for pi's on-disk configuration.
//
// PLAN §3.5: settings.json, models.json and trust.json are read and written
// here; mcp.json is read but prefer `pi mcp add/remove`; auth.json is never
// opened directly (use `pi auth check --json`); sessions-index.sqlite is
// pi's private file and is never touched.
package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrForbidden marks a file this package refuses to open at all.
var ErrForbidden = errors.New("config: file is owned by pi and must not be read directly")

// Store reads and writes configuration under the agent directory.
type Store struct {
	dir   string
	piBin string
	mu    sync.Mutex
}

// NewStore creates a store rooted at agentDir. An empty agentDir resolves to
// $PI_CODING_AGENT_DIR or ~/.pi/agent.
func NewStore(agentDir string) *Store {
	if agentDir == "" {
		if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
			agentDir = v
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				agentDir = ".pi/agent"
			} else {
				agentDir = filepath.Join(home, ".pi", "agent")
			}
		}
	}
	return &Store{dir: agentDir, piBin: ResolvePi()}
}

// Dir is the agent directory.
func (s *Store) Dir() string { return s.dir }

// Path joins a configuration file name onto the agent directory.
func (s *Store) Path(name string) string { return filepath.Join(s.dir, name) }

// Read loads a JSON object. A missing file yields an empty object so the UI
// can render before the user has ever changed a setting.
func (s *Store) Read(name string) (map[string]any, error) {
	if name == "auth.json" || name == "sessions-index.sqlite" {
		return nil, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return readObject(s.Path(name))
}

func readObject(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("config: %s: %w", filepath.Base(path), err)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// ReadPath reads a JSON object outside the agent directory (a project's
// .pi/mcp.json, for example). auth.json stays forbidden wherever it is.
func (s *Store) ReadPath(path string) (map[string]any, error) {
	if filepath.Base(path) == "auth.json" || filepath.Base(path) == "sessions-index.sqlite" {
		return nil, ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return readObject(path)
}

// WritePath writes a JSON object outside the agent directory atomically.
func (s *Store) WritePath(path string, v map[string]any, backup bool) error {
	if filepath.Base(path) == "auth.json" || filepath.Base(path) == "sessions-index.sqlite" {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(path, v, backup)
}

// Write atomically replaces a JSON object. When backup is true the previous
// bytes are copied to a sibling .bak file first (models.json needs this).
func (s *Store) Write(name string, v map[string]any, backup bool) error {
	if name == "auth.json" || name == "sessions-index.sqlite" {
		return ErrForbidden
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(s.Path(name), v, backup)
}

func (s *Store) writeLocked(path string, v map[string]any, backup bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if backup {
		if old, err := os.ReadFile(path); err == nil {
			bak := fmt.Sprintf("%s.bak.pi-web-native-%d", path, time.Now().UnixNano())
			_ = os.WriteFile(bak, old, 0o600)
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + fmt.Sprintf(".tmp-%d", time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Set changes one key of settings.json (or trust.json) and persists it.
func (s *Store) Set(name, key string, value any) error {
	obj, err := s.Read(name)
	if err != nil {
		return err
	}
	obj[key] = value
	return s.Write(name, obj, name == "models.json")
}

// Settings is settings.json.
func (s *Store) Settings() (map[string]any, error) { return s.Read("settings.json") }

// SetSetting writes one settings.json key.
func (s *Store) SetSetting(key string, value any) error { return s.Set("settings.json", key, value) }

// RemoveSetting deletes one settings.json key.
func (s *Store) RemoveSetting(key string) error {
	obj, err := s.Read("settings.json")
	if err != nil {
		return err
	}
	delete(obj, key)
	return s.Write("settings.json", obj, false)
}

// Models is models.json.
func (s *Store) Models() (map[string]any, error) { return s.Read("models.json") }

// SetModels replaces models.json, keeping a backup of the previous file.
func (s *Store) SetModels(v map[string]any) error { return s.Write("models.json", v, true) }

// MCP is the on-disk mcp.json. Prefer MCPList for live server state.
func (s *Store) MCP() (map[string]any, error) { return s.Read("mcp.json") }

// SetMCP replaces the global mcp.json, keeping a backup.
func (s *Store) SetMCP(v map[string]any) error { return s.Write("mcp.json", v, true) }

// ProjectMCP reads <cwd>/.pi/mcp.json.
func (s *Store) ProjectMCP(cwd string) (map[string]any, error) {
	return s.ReadPath(filepath.Join(cwd, ".pi", "mcp.json"))
}

// SetProjectMCP writes <cwd>/.pi/mcp.json.
func (s *Store) SetProjectMCP(cwd string, v map[string]any) error {
	return s.WritePath(filepath.Join(cwd, ".pi", "mcp.json"), v, true)
}

// Trust is pi's trust.json: a flat map of directory -> true|false|null where
// true means trusted, false a recorded "do not trust" and null a cleared
// decision. pi refuses any other value ("Invalid trust store ... value for
// ... must be true, false, or null"), so this store never writes one.
func (s *Store) Trust() (map[string]any, error) {
	obj, err := s.Read("trust.json")
	if err != nil {
		return nil, err
	}
	return s.normalizeTrust(obj), nil
}

// normalizeTrust rewrites the legacy {trustedDirs: [...]} layout (and any
// value pi would reject) into pi's map, writing the file back once when the
// shape changed: a trust store pi cannot parse makes every child log an error
// at startup and the window look like it was never connected.
func (s *Store) normalizeTrust(obj map[string]any) map[string]any {
	out := make(map[string]any, len(obj)+4)
	for k, v := range obj {
		switch v := v.(type) {
		case bool:
			out[k] = v
		case nil:
			out[k] = nil
		case []any:
			if k == "trustedDirs" {
				for _, item := range v {
					if dir, ok := item.(string); ok && dir != "" {
						out[filepath.Clean(dir)] = true
					}
				}
			}
		}
	}
	if _, ok := obj["trustedDirs"]; ok {
		_ = s.Write("trust.json", out, false)
		return out
	}
	for k, v := range obj {
		if _, ok := out[k]; !ok {
			_ = s.Write("trust.json", out, false)
			break
		}
		if _, ok := v.(bool); !ok && v != nil {
			_ = s.Write("trust.json", out, false)
			break
		}
	}
	return out
}

// TrustedDirs lists the directories the user trusted.
func (s *Store) TrustedDirs() []string {
	obj, err := s.Trust()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(obj))
	for dir, value := range obj {
		if trusted, ok := value.(bool); ok && trusted {
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

// IsTrusted reports whether cwd, or an ancestor of it, is trusted, as pi's
// findNearestTrustEntry does.
func (s *Store) IsTrusted(cwd string) bool {
	obj, err := s.Trust()
	if err != nil {
		return false
	}
	dir := filepath.Clean(cwd)
	for {
		if value, ok := obj[dir]; ok {
			if trusted, isBool := value.(bool); isBool {
				return trusted
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// SetTrusted records a directory's decision in pi's layout.
func (s *Store) SetTrusted(cwd string, trusted bool) error {
	obj, err := s.Trust()
	if err != nil {
		return err
	}
	obj[filepath.Clean(cwd)] = trusted
	return s.Write("trust.json", obj, false)
}

// ResolvePi finds the pi CLI. PATH is tried first, then the well-known
// npm-global and ~/.local/bin locations (GUI apps start with a minimal PATH).
func ResolvePi() string {
	if p, err := exec.LookPath("pi"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, ".local", "bin", "pi"),
		filepath.Join(home, ".npm-global", "bin", "pi"),
		"/usr/local/bin/pi",
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return "pi"
}

// RunPi invokes the official pi CLI. This is the only sanctioned way to
// inspect auth state or mutate MCP servers.
func (s *Store) RunPi(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, s.piBin, args...)
	cmd.Env = append(os.Environ(), "PI_CODING_AGENT_DIR="+s.dir)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("pi %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("pi %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// MCPListJSON returns the raw output of `pi mcp list --json`.
func (s *Store) MCPListJSON(ctx context.Context) (json.RawMessage, error) {
	out, err := s.RunPi(ctx, "mcp", "list", "--json")
	if err != nil {
		return nil, err
	}
	if !json.Valid(bytes.TrimSpace(out)) {
		return nil, fmt.Errorf("config: pi mcp list did not return JSON: %s", bytes.TrimSpace(out))
	}
	return json.RawMessage(bytes.TrimSpace(out)), nil
}

// Providers lists the provider ids configured in models.json, in file order
// where the map allows it.
func (s *Store) Providers() []string {
	models, err := s.Models()
	if err != nil {
		return nil
	}
	raw, ok := models["providers"]
	if !ok {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case map[string]any:
		for id := range v {
			out = append(out, id)
		}
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				for _, key := range []string{"id", "name", "provider"} {
					if id, ok := m[key].(string); ok && id != "" {
						out = append(out, id)
						break
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// AuthCheck reports login state per provider. `pi auth check` requires
// --provider or --model and exits 2 without one, so this runs it once per
// provider in models.json. auth.json is deliberately never opened here.
func (s *Store) AuthCheck(ctx context.Context) (map[string]any, error) {
	providers := s.Providers()
	if len(providers) == 0 {
		return map[string]any{"status": "models.json 里还没有配置 provider"}, nil
	}
	out := make(map[string]any, len(providers))
	for _, p := range providers {
		body, err := s.RunPi(ctx, "auth", "check", "--provider", p, "--json", "--no-refresh")
		if err != nil {
			out[p] = "失败：" + err.Error()
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(body), &v); err != nil {
			out[p] = strings.TrimSpace(string(body))
			continue
		}
		out[p] = v
	}
	return out, nil
}

// ListDirs lists immediate child directories of a named folder under the
// agent directory (skills/, extensions/).
func (s *Store) ListDirs(name string) []string {
	entries, err := os.ReadDir(s.Path(name))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		} else if strings.HasSuffix(e.Name(), ".ts") || strings.HasSuffix(e.Name(), ".js") {
			out = append(out, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	sort.Strings(out)
	return out
}

// Skills lists installed skills (directory names only).
func (s *Store) Skills() []string { return s.ListDirs("skills") }

// Extensions lists installed extensions.
func (s *Store) Extensions() []string { return s.ListDirs("extensions") }
