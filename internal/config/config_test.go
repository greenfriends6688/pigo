package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWriteRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	v, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("missing settings.json should be an empty object, got %v", v)
	}
	if err := s.SetSetting("language", "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("autoCompaction", true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if got["language"] != "zh-CN" || got["autoCompaction"] != true {
		t.Fatalf("unexpected settings: %v", got)
	}
	// The atomic write must not leave temporary files behind.
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 12 && e.Name()[:4] == ".tmp" {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestModelsWriteKeepsBackup(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetModels(map[string]any{"providers": []any{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModels(map[string]any{"providers": []any{"b"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	baks := 0
	for _, e := range entries {
		if len(e.Name()) > len("models.json.bak") && e.Name()[:len("models.json.bak")] == "models.json.bak" {
			baks++
		}
	}
	if baks != 1 {
		t.Fatalf("want one backup, got %d (%v)", baks, entries)
	}
	got, _ := s.Models()
	providers, ok := got["providers"].([]any)
	if !ok || len(providers) != 1 || providers[0] != "b" {
		t.Fatalf("bad models.json: %v", got)
	}
}

func TestForbiddenFiles(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.WriteFile(s.Path("auth.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("auth.json"); err != ErrForbidden {
		t.Fatalf("auth.json must never be read, got %v", err)
	}
	if err := s.Write("auth.json", map[string]any{"x": 1}, false); err != ErrForbidden {
		t.Fatalf("auth.json must never be written, got %v", err)
	}
	if _, err := s.Read("sessions-index.sqlite"); err != ErrForbidden {
		t.Fatalf("sessions-index.sqlite must never be read, got %v", err)
	}
}

func TestTrustAndListDirs(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SetTrusted("/tmp/a", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrusted("/tmp/b", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrusted("/tmp/a", false); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Trust()
	if _, legacy := v["trustedDirs"]; legacy {
		t.Fatalf("trust.json still carries the legacy key pi rejects: %v", v)
	}
	if v["/tmp/b"] != true || v["/tmp/a"] != false {
		t.Fatalf("bad trust.json: %v", v)
	}
	if dirs := s.TrustedDirs(); len(dirs) != 1 || dirs[0] != "/tmp/b" {
		t.Fatalf("bad trusted dirs: %v", dirs)
	}
	if err := os.MkdirAll(s.Path("skills/bid"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path("skills/other.ts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	got := s.Skills()
	if len(got) != 2 || got[0] != "bid" || got[1] != "other" {
		t.Fatalf("bad skills list: %v", got)
	}
}

func TestMalformedConfigDoesNotPanic(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := os.WriteFile(s.Path("settings.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settings(); err == nil {
		t.Fatal("want a parse error for malformed settings.json")
	}
}

func TestTrustedAncestorsAndPaths(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	root := filepath.Join(dir, "project")
	child := filepath.Join(root, "sub")
	if st.IsTrusted(child) {
		t.Fatal("a fresh store trusts nothing")
	}
	if err := st.SetTrusted(root, true); err != nil {
		t.Fatal(err)
	}
	if !st.IsTrusted(root) || !st.IsTrusted(child) {
		t.Fatal("a trusted ancestor must cover its children")
	}
	other := filepath.Join(dir, "other")
	if st.IsTrusted(other) {
		t.Fatal("an unrelated path must stay untrusted")
	}
	if err := st.SetTrusted(root, false); err != nil {
		t.Fatal(err)
	}
	if st.IsTrusted(child) {
		t.Fatal("untrusting failed")
	}

	mcpPath := filepath.Join(root, ".pi", "mcp.json")
	if err := st.WritePath(mcpPath, map[string]any{"mcpServers": map[string]any{}}, false); err != nil {
		t.Fatal(err)
	}
	raw, err := st.ReadPath(mcpPath)
	if err != nil || raw["mcpServers"] == nil {
		t.Fatalf("project mcp round trip: %v %+v", err, raw)
	}
}

// The app used to write {trustedDirs: [...]}; pi rejects that store outright
// ("value ... must be true, false, or null"), and every child then logged an
// error at startup. Reading it must migrate the file once.
func TestLegacyTrustStoreIsMigrated(t *testing.T) {
	s := NewStore(t.TempDir())
	legacy := `{"trustedDirs":["/tmp/one","/tmp/two"],"/tmp/off":false}`
	if err := os.WriteFile(s.Path("trust.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.IsTrusted("/tmp/one/sub") {
		t.Fatal("a legacy entry must still count as trusted")
	}
	v, _ := s.Trust()
	if _, ok := v["trustedDirs"]; ok {
		t.Fatalf("the legacy key must be gone: %v", v)
	}
	if v["/tmp/one"] != true || v["/tmp/two"] != true || v["/tmp/off"] != false {
		t.Fatalf("migration lost an entry: %v", v)
	}
	data, _ := os.ReadFile(s.Path("trust.json"))
	if strings.Contains(string(data), "trustedDirs") {
		t.Fatalf("the file still carries the key: %s", data)
	}
	// Only booleans and null may stay, or pi refuses the whole file.
	for k, value := range v {
		if value != nil {
			if _, ok := value.(bool); !ok {
				t.Fatalf("%s = %v is not a decision pi accepts", k, value)
			}
		}
	}
}
