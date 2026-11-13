package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readTrust(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("sanitized trust.json is not valid JSON: %v", err)
	}
	return out
}

func TestSanitizeTrustStoreFlattensLegacyKey(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"trustedDirs":["/tmp/one","/tmp/two"],"/tmp/off":false,"/tmp/cleared":null}`
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeTrustStore(dir)
	out := readTrust(t, dir)
	if _, ok := out["trustedDirs"]; ok {
		t.Fatalf("legacy key survived: %v", out)
	}
	for _, k := range []string{"/tmp/one", "/tmp/two"} {
		if out[k] != true {
			t.Fatalf("%s not trusted: %v", k, out)
		}
	}
	if out["/tmp/off"] != false || out["/tmp/cleared"] != nil {
		t.Fatalf("valid decisions were lost: %v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "trust.json.bak")); err != nil {
		t.Fatal("no backup of the legacy store")
	}
}

func TestSanitizeTrustStoreDropsValuesPiRejects(t *testing.T) {
	dir := t.TempDir()
	bad := `{"good":true,"weird":{"nested":1},"list":[1,2]}`
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeTrustStore(dir)
	out := readTrust(t, dir)
	if len(out) != 1 || out["good"] != true {
		t.Fatalf("want only the valid decision, got %v", out)
	}
}

func TestSanitizeTrustStoreLeavesValidFileAlone(t *testing.T) {
	dir := t.TempDir()
	good := "{\"/tmp/a\":true}\n"
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeTrustStore(dir)
	data, _ := os.ReadFile(filepath.Join(dir, "trust.json"))
	if string(data) != good {
		t.Fatalf("a valid store was rewritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "trust.json.bak")); !os.IsNotExist(err) {
		t.Fatal("a valid store should not produce a backup")
	}
}

func TestSanitizeTrustStoreReplacesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sanitizeTrustStore(dir)
	if out := readTrust(t, dir); len(out) != 0 {
		t.Fatalf("want an empty store, got %v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "trust.json.broken")); err != nil {
		t.Fatal("the corrupt file was not preserved")
	}
}
