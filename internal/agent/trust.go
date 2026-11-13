package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sanitizeTrustStore repairs ~/.pi/agent/trust.json before an RPC child is
// started. pi's readTrustFile rejects the file outright when any value is not
// true, false or null; older pi builds (and other pi clients) wrote
// {"trustedDirs": [...]} instead of a flat directory map. One such key made
// every child exit at startup with "Invalid trust store", which the window
// reported as a disconnect and dutifully retried three times.
//
// The rewrite is idempotent, keeps a backup and uses an atomic rename so a
// concurrently starting child never reads a half-written file.
func sanitizeTrustStore(agentDir string) {
	if agentDir == "" {
		return
	}
	path := filepath.Join(agentDir, "trust.json")
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// A corrupt file is equally fatal to pi. Keep it for inspection and
		// start from an empty store rather than killing every child.
		_ = os.WriteFile(path+".broken", data, 0o600)
		_ = writeTrustStore(path, map[string]any{})
		return
	}

	out := make(map[string]any, len(raw))
	changed := false
	for key, value := range raw {
		text := strings.TrimSpace(string(value))
		var decision bool
		switch {
		case text == "null":
			out[key] = nil
		case json.Unmarshal(value, &decision) == nil:
			out[key] = decision
		case key == "trustedDirs":
			var dirs []string
			if json.Unmarshal(value, &dirs) == nil {
				for _, dir := range dirs {
					if dir != "" {
						out[filepath.Clean(dir)] = true
					}
				}
			}
			changed = true
		default:
			// Any other shape is one pi would refuse. Drop it.
			changed = true
		}
	}
	if !changed {
		return
	}
	_ = os.WriteFile(path+".bak", data, 0o600)
	_ = writeTrustStore(path, out)
}

func writeTrustStore(path string, out map[string]any) error {
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(out))
	for _, k := range keys {
		ordered[k] = out[k]
	}
	data, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "trust-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
