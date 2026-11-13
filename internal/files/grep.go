package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// GrepMaxFileSize bounds the files content search reads: an occasional huge
// log or dataset must not turn one keystroke into a multi-hundred-megabyte
// read.
const GrepMaxFileSize = 1 << 20

// GrepLimit is the default number of matching lines content search returns.
const GrepLimit = 200

// Match is one line whose text contains the query.
type Match struct {
	// Path is relative to the root, slash-separated for display.
	Path string
	// Line is 1-based.
	Line int
	// Text is the matched line, trimmed and shortened for the result row.
	Text string
}

// Grep walks root and returns the first limit lines whose text contains
// query, case-insensitively. It skips .git, binary files (a NUL byte or
// invalid UTF-8) and files larger than GrepMaxFileSize, so a repository can
// be searched without loading it whole. Unreadable entries are skipped
// rather than failing the search.
func Grep(root, query string, limit int) ([]Match, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = GrepLimit
	}
	needle := strings.ToLower(query)
	var out []Match
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= limit {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > GrepMaxFileSize {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			return nil // binary: searching it would only produce noise
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			out = append(out, Match{Path: rel, Line: i + 1, Text: shortenLine(line, 300)})
			if len(out) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return out, walkErr
	}
	return out, nil
}

// shortenLine trims a line for a result row, keeping long minified lines
// from carrying a megabyte into the UI.
func shortenLine(line string, maxRunes int) string {
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	return string(runes[:maxRunes]) + "…"
}
