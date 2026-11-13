package sessions

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// fileStat is the (path, size, mtime) triple used as a cache key and for change
// detection in RefreshIfChanged.
type fileStat struct {
	Path  string
	Size  int64
	Mtime time.Time
}

// listSessionFiles walks the session tree and returns every *.jsonl file with
// its stat, sorted by path. Missing or unreadable entries are skipped; a
// missing session root simply yields an empty list.
func listSessionFiles(agentDir string) []fileStat {
	root := SessionRoot(agentDir)
	out := make([]fileStat, 0)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil // tolerate missing or unreadable paths
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
			return nil
		}
		fi, e := d.Info()
		if e != nil {
			return nil
		}
		out = append(out, fileStat{Path: p, Size: fi.Size(), Mtime: fi.ModTime()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Scan performs a full scan of all session files under agentDir. Unreadable
// individual files and malformed lines are skipped (Store.Refresh exposes the
// warnings), so one bad file never fails the whole scan. The result is sorted
// by Modified, newest first.
func Scan(agentDir string) ([]Info, error) {
	infos, _ := scanWithWarnings(agentDir)
	return infos, nil
}

// scanWithWarnings is Scan with warning collection, reused conceptually by the
// Store's refresh path.
func scanWithWarnings(agentDir string) ([]Info, []string) {
	files := listSessionFiles(agentDir)
	warnings := make([]string, 0)
	infos := make([]Info, 0, len(files))
	for _, f := range files {
		info, err := readInfo(f.Path, &warnings)
		if err != nil {
			addWarning(&warnings, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		if info.ID == "" {
			addWarning(&warnings, fmt.Sprintf("%s: missing session header", f.Path))
			continue
		}
		info.Modified = f.Mtime
		infos = append(infos, info)
	}
	sortInfos(infos)
	return infos, warnings
}

// sortInfos sorts by Modified (newest first), breaking ties by path for a
// deterministic order.
func sortInfos(infos []Info) {
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Modified.Equal(infos[j].Modified) {
			return infos[i].Path < infos[j].Path
		}
		return infos[i].Modified.After(infos[j].Modified)
	})
}
