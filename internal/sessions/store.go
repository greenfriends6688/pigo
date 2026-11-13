package sessions

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	nativeDirName   = "pi-web-native"
	cacheFileName   = "sessions.json"
	metaFileName    = "meta.json"
	defaultPageSize = 200
)

// fileKey identifies a cached parse result by (size, mtime).
type fileKey struct {
	Size  int64 `json:"size"`
	Mtime int64 `json:"mtime"` // UnixNano
}

// cacheEntry is one parsed session indexed by its file key.
type cacheEntry struct {
	Key  fileKey `json:"key"`
	Info Info    `json:"info"`
}

// cacheFile is the on-disk index cache.
type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

// metaItem holds pi-web-native-only session metadata. It lives in our own
// meta.json and never touches pi's files.
type metaItem struct {
	Pinned   bool   `json:"pinned,omitempty"`
	Archived bool   `json:"archived,omitempty"`
	Unread   bool   `json:"unread,omitempty"`
	Name     string `json:"name,omitempty"`
}

type metaFile struct {
	Version int                 `json:"version"`
	Items   map[string]metaItem `json:"items"`
}

// sessionInfoEntry is the entry appended by SetName.
type sessionInfoEntry struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`
	Name      string `json:"name"`
}

// Store is a concurrency-safe, cached view over the session tree. Every
// exported method takes the lock itself; the *Locked helpers assume the caller
// already holds s.mu.
type Store struct {
	mu       sync.RWMutex
	agentDir string

	infos    []Info
	cache    map[string]cacheEntry
	meta     map[string]metaItem
	warnings []string
	sig      string
}

// NewStore creates a Store rooted at agentDir (DefaultAgentDir when empty).
// Call LoadCache for a fast first render, then Refresh or RefreshIfChanged to
// bring the index up to date.
func NewStore(agentDir string) *Store {
	if agentDir == "" {
		agentDir = DefaultAgentDir()
	}
	return &Store{
		agentDir: agentDir,
		infos:    []Info{},
		cache:    make(map[string]cacheEntry),
		meta:     make(map[string]metaItem),
		warnings: []string{},
	}
}

// IndexPath returns the path of the sessions.json index cache.
func (s *Store) IndexPath() string {
	return filepath.Join(s.agentDir, nativeDirName, cacheFileName)
}

func (s *Store) metaPath() string {
	return filepath.Join(s.agentDir, nativeDirName, metaFileName)
}

// LoadCache reads the index cache. A missing, corrupt, or version-mismatched
// cache is silently ignored; a valid cache is immediately usable via List.
func (s *Store) LoadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadCacheLocked()
}

func (s *Store) loadCacheLocked() error {
	// A freshly created Store has no signature, which would make the first
	// RefreshIfChanged rescan every file even though the cache is warm. Stat
	// the tree once here (no file content is read) so the very first poll is
	// a no-op, as PLAN §3.4 requires for the <50 ms cold render.
	defer func() { s.sig = signatureOf(listSessionFiles(s.agentDir)) }()
	meta := s.loadMetaLocked()
	b, err := os.ReadFile(s.IndexPath())
	if err != nil {
		s.cache = make(map[string]cacheEntry)
		s.infos = s.infosFromCacheLocked(meta)
		return nil
	}
	var cf cacheFile
	if json.Unmarshal(b, &cf) != nil || cf.Version != cacheVersion || cf.Entries == nil {
		s.cache = make(map[string]cacheEntry)
		s.infos = []Info{}
		return nil
	}
	s.cache = cf.Entries
	s.infos = s.infosFromCacheLocked(meta)
	return nil
}

// infosFromCacheLocked rebuilds the visible list from the cache, applying our
// own metadata and sorting newest first.
func (s *Store) infosFromCacheLocked(meta map[string]metaItem) []Info {
	out := make([]Info, 0, len(s.cache))
	for path, ce := range s.cache {
		info := ce.Info
		info.Path = path
		applyMeta(&info, meta[path])
		out = append(out, info)
	}
	sortInfos(out)
	return out
}

// applyMeta overlays pi-web-native metadata onto a parsed session.
func applyMeta(info *Info, m metaItem) {
	info.Pinned = m.Pinned
	info.Archived = m.Archived
	info.Unread = m.Unread
	if m.Name != "" {
		info.Name = m.Name
	}
}

// loadMetaLocked (re)loads meta.json into s.meta; missing/corrupt files yield
// an empty map.
func (s *Store) loadMetaLocked() map[string]metaItem {
	b, err := os.ReadFile(s.metaPath())
	if err != nil {
		s.meta = make(map[string]metaItem)
		return s.meta
	}
	var mf metaFile
	if json.Unmarshal(b, &mf) != nil || mf.Version != metaVersion || mf.Items == nil {
		s.meta = make(map[string]metaItem)
		return s.meta
	}
	s.meta = mf.Items
	return s.meta
}

func (s *Store) saveMetaLocked() error {
	b, err := json.MarshalIndent(metaFile{Version: metaVersion, Items: s.meta}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.metaPath(), b)
}

// SaveCache atomically persists the index cache (temp file + os.Rename).
func (s *Store) SaveCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCacheLocked()
}

func (s *Store) saveCacheLocked() error {
	b, err := json.Marshal(cacheFile{Version: cacheVersion, Entries: s.cache})
	if err != nil {
		return err
	}
	return writeAtomic(s.IndexPath(), b)
}

// writeAtomic writes data to path via a temp file in the same directory and a
// single os.Rename, so readers never observe a partially written file.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// Refresh rescans the tree, merges the index cache and pin/archive metadata,
// sorts by Modified (newest first), and persists the cache.
func (s *Store) Refresh() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshWithFilesLocked(listSessionFiles(s.agentDir))
}

func (s *Store) refreshWithFilesLocked(files []fileStat) error {
	meta := s.loadMetaLocked()
	infos := make([]Info, 0, len(files))
	newCache := make(map[string]cacheEntry, len(files))
	warnings := make([]string, 0)

	for _, f := range files {
		key := fileKey{Size: f.Size, Mtime: f.Mtime.UnixNano()}
		var info Info
		if ce, ok := s.cache[f.Path]; ok && ce.Key == key {
			info = ce.Info // cache hit: no file content read
		} else {
			var err error
			info, err = readInfo(f.Path, &warnings)
			if err != nil {
				addWarning(&warnings, fmt.Sprintf("%s: %v", f.Path, err))
				continue
			}
			if info.ID == "" {
				addWarning(&warnings, fmt.Sprintf("%s: missing session header", f.Path))
				continue
			}
		}
		info.Path = f.Path
		info.Modified = f.Mtime
		applyMeta(&info, meta[f.Path])
		infos = append(infos, info)
		newCache[f.Path] = cacheEntry{Key: key, Info: info}
	}

	sortInfos(infos)
	s.infos = infos
	s.cache = newCache // also drops entries for files that no longer exist
	s.warnings = warnings
	s.sig = signatureOf(files)
	return s.saveCacheLocked()
}

// signatureOf hashes the sorted (path,size,mtime) triples so a change can be
// detected by stat alone, without reading any file content.
func signatureOf(files []fileStat) string {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", f.Path, f.Size, f.Mtime.UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RefreshIfChanged stats every *.jsonl and only rescans when any
// (path,size,mtime) triple changed. It returns whether a rescan happened and
// is intended for cheap UI polling.
func (s *Store) RefreshIfChanged() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := listSessionFiles(s.agentDir)
	sig := signatureOf(files)
	if s.sig != "" && sig == s.sig {
		return false, nil
	}
	if err := s.refreshWithFilesLocked(files); err != nil {
		return true, err
	}
	return true, nil
}

// Info returns one session's indexed row, and whether the store knows it.
func (s *Store) Info(path string) (Info, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, info := range s.infos {
		if info.Path == path {
			return info, true
		}
	}
	return Info{}, false
}

// List returns a copy of all non-archived sessions, newest first.
func (s *Store) List() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.visibleLocked()
}

// visibleLocked returns a freshly allocated slice of non-archived infos.
// s.infos is already sorted by Modified (newest first).
func (s *Store) visibleLocked() []Info {
	out := make([]Info, 0, len(s.infos))
	for _, in := range s.infos {
		if in.Archived {
			continue
		}
		out = append(out, in)
	}
	return out
}

// Search returns non-archived sessions whose Name, FirstMessage, or Cwd
// contains q (case-insensitive substring). An empty q matches everything.
func (s *Store) Search(q string) []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q = strings.ToLower(q)
	out := make([]Info, 0)
	for _, in := range s.visibleLocked() {
		if q == "" ||
			strings.Contains(strings.ToLower(in.Name), q) ||
			strings.Contains(strings.ToLower(in.FirstMessage), q) ||
			strings.Contains(strings.ToLower(in.Cwd), q) {
			out = append(out, in)
		}
	}
	return out
}

// Content-search budget. A search reads full transcripts, unlike the index
// scan, so it is bounded in files, bytes and wall time: the sidebar must stay
// responsive on a library of sessions that each grew to megabytes.
const (
	contentMaxFiles    = 300
	contentMaxFileSize = 8 << 20
	contentBudget      = 3 * time.Second
	contentSnippetHalf = 60 // runes kept on each side of the hit
)

// SearchContent scans visible sessions' jsonl for q and returns
// path -> snippet (one line, ~120 runes around the first hit).
//
// Only message entries are searched: the text of a string content or of
// "text"/"thinking" blocks. Matching is case-insensitive; a session yields at
// most one snippet (its first hit). Malformed lines and unreadable files are
// skipped silently, so a broken transcript never fails the search.
func (s *Store) SearchContent(q string, limit int) map[string]string {
	out := make(map[string]string)
	needle := strings.TrimSpace(q)
	if needle == "" {
		return out
	}
	if limit <= 0 {
		limit = 30
	}
	paths := s.contentPaths()
	if len(paths) > contentMaxFiles {
		paths = paths[:contentMaxFiles]
	}
	deadline := time.Now().Add(contentBudget)
	for _, path := range paths {
		if len(out) >= limit || time.Now().After(deadline) {
			break
		}
		if snippet, ok := searchSessionFile(path, needle); ok {
			out[path] = snippet
		}
	}
	return out
}

// contentPaths copies the visible sessions' file paths, newest first, so the
// scan holds no store lock while it reads files.
func (s *Store) contentPaths() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vis := s.visibleLocked()
	out := make([]string, 0, len(vis))
	for _, in := range vis {
		out = append(out, in.Path)
	}
	return out
}

// searchSessionFile returns the first snippet in one transcript that contains
// needle, reading at most contentMaxFileSize bytes of it.
func searchSessionFile(path, needle string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return "", false
	}
	size := st.Size()
	if size > contentMaxFileSize {
		// The cut may leave a partial last line; it simply fails to parse.
		size = contentMaxFileSize
	}
	data := make([]byte, size)
	if size > 0 {
		if _, err := f.ReadAt(data, 0); err != nil && err != io.EOF {
			return "", false
		}
	}
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) != nil || e.Type != "message" || e.Message == nil {
			continue
		}
		if snippet, ok := snippetFromContent(e.Message.Content, needle); ok {
			return snippet, true
		}
	}
	return "", false
}

// snippetFromContent finds needle in a message's content and returns the text
// around the first hit. Content is a plain string or an array of blocks whose
// "text" and "thinking" blocks carry the prose.
func snippetFromContent(raw json.RawMessage, needle string) (string, bool) {
	var text string
	var s string
	if json.Unmarshal(raw, &s) == nil {
		text = s
	} else {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return "", false
		}
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if (b.Type == "text" || b.Type == "thinking") && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if text == "" {
		return "", false
	}
	runes := []rune(text)
	needleRunes := []rune(strings.ToLower(needle))
	at := foldIndex(runes, needleRunes)
	if at < 0 {
		return "", false
	}
	start := at - contentSnippetHalf
	if start < 0 {
		start = 0
	}
	end := at + len(needleRunes) + contentSnippetHalf
	if end > len(runes) {
		end = len(runes)
	}
	snippet := strings.Join(strings.Fields(string(runes[start:end])), " ")
	if snippet == "" {
		return "", false
	}
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return snippet, true
}

// foldIndex is strings.Index over runes, case-insensitively: the needle is
// already lowercased and every candidate rune is lowered before comparing, so
// an index measured in runes can slice the original text safely.
func foldIndex(text, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(text) {
		return -1
	}
	for i := 0; i+len(needle) <= len(text); i++ {
		match := true
		for j, want := range needle {
			if unicode.ToLower(text[i+j]) != want {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// Groups buckets non-archived sessions by ProjectKey. Items within a group and
// the groups themselves are ordered by Modified (newest first).
func (s *Store) Groups() []Group {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byKey := make(map[string][]Info)
	for _, in := range s.visibleLocked() {
		byKey[in.ProjectKey] = append(byKey[in.ProjectKey], in)
	}
	groups := make([]Group, 0, len(byKey))
	for key, items := range byKey {
		// items is a fresh slice; sorting it cannot mutate s.infos.
		sortInfos(items)
		groups = append(groups, Group{
			ProjectKey: key,
			Title:      projectTitle(items[0].Cwd, key),
			Items:      items,
		})
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i].Items[0].Modified, groups[j].Items[0].Modified
		if a.Equal(b) {
			return groups[i].ProjectKey < groups[j].ProjectKey
		}
		return a.After(b)
	})
	return groups
}

// Page returns a window of the non-archived, newest-first list. limit <= 0
// falls back to 200; a negative offset is clamped to 0.
func (s *Store) Page(offset, limit int) []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vis := s.visibleLocked()
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if offset >= len(vis) {
		return []Info{}
	}
	end := offset + limit
	if end > len(vis) || end < offset { // guard against overflow and overrun
		end = len(vis)
	}
	out := make([]Info, end-offset)
	copy(out, vis[offset:end])
	return out
}

// Archived returns the archived sessions, newest first.
func (s *Store) Archived() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0)
	for _, in := range s.infos {
		if in.Archived {
			out = append(out, in)
		}
	}
	return out
}

// Children returns sessions spawned by a subagent, newest first.
func (s *Store) Children() []Info {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0)
	for _, in := range s.infos {
		if in.ParentSession != "" && !in.Archived && !in.Side {
			out = append(out, in)
		}
	}
	return out
}

// ChildrenOf lists the child sessions of one parent session, the ones its
// own transcript spawned. A nil parent matches none, as an empty one does.
func (s *Store) ChildrenOf(parent string) []Info {
	if parent == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Info, 0)
	for _, in := range s.infos {
		if in.ParentSession == parent && !in.Archived && !in.Side {
			out = append(out, in)
		}
	}
	return out
}

// Delete removes a session file and every trace of it from the index, the
// cache and the pin/archive metadata. pi's other sessions are untouched.
func (s *Store) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	meta := s.loadMetaLocked()
	delete(meta, path)
	if err := s.saveMetaLocked(); err != nil {
		return err
	}
	delete(s.cache, path)
	out := s.infos[:0]
	for _, in := range s.infos {
		if in.Path != path {
			out = append(out, in)
		}
	}
	s.infos = out
	return s.saveCacheLocked()
}

// SetName appends a valid session_info entry (type/id/parentId/timestamp/name)
// to the session file, preserving every existing line. A missing trailing
// newline is repaired within the same single write. The in-memory name and the
// cached entry are updated/invalidated so the change is visible immediately and
// the next RefreshIfChanged re-reads the file.
func (s *Store) SetName(path, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	needsNL, parentID, err := appendPreflight(path)
	if err != nil {
		return err
	}
	line, err := json.Marshal(sessionInfoEntry{
		Type:      "session_info",
		ID:        newEntryID(),
		ParentID:  parentID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Name:      name,
	})
	if err != nil {
		return err
	}
	buf := make([]byte, 0, len(line)+2)
	if needsNL {
		buf = append(buf, '\n')
	}
	buf = append(buf, line...)
	buf = append(buf, '\n')

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, werr := f.Write(buf) // exactly one write
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}

	// size/mtime changed: invalidate the cache key and update the live list.
	delete(s.cache, path)
	for i := range s.infos {
		if s.infos[i].Path == path {
			s.infos[i].Name = name
		}
	}
	return nil
}

// appendPreflight checks whether the file's last byte is a newline and reads
// the id of the last complete line to use as the new entry's parentId.
func appendPreflight(path string) (needsNL bool, parentID string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, "", err
	}
	size := st.Size()
	if size > 0 {
		var last [1]byte
		if _, e := f.ReadAt(last[:], size-1); e == nil {
			needsNL = last[0] != '\n'
		}
	}

	const probe = 16 * 1024
	n := size
	if n > probe {
		n = probe
	}
	buf := make([]byte, n)
	if n > 0 {
		if _, e := f.ReadAt(buf, size-n); e != nil && e != io.EOF {
			return needsNL, "", e
		}
	}
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) == nil {
			parentID = e.ID
		}
		break
	}
	return needsNL, parentID, nil
}

// newEntryID returns a short random hex id, matching pi's entry id style.
func newEntryID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%08x", uint32(time.Now().UnixNano()))
}

// SetPinned persists the pin flag in pi-web-native/meta.json and updates all
// in-memory views. pi's own files are never modified.
func (s *Store) SetPinned(path string, pinned bool) error {
	return s.setMeta(path, func(m *metaItem) { m.Pinned = pinned })
}

// SetArchived persists the archive flag; archived sessions are excluded from
// List/Search/Groups/Page. pi's own files are never modified.
func (s *Store) SetArchived(path string, archived bool) error {
	return s.setMeta(path, func(m *metaItem) { m.Archived = archived })
}

// SetUnread marks a session unread or read. The app marks a session read when
// it is opened and unread from the row's menu, so a list of hundreds of
// sessions can still show which ones were never looked at.
func (s *Store) SetUnread(path string, unread bool) error {
	return s.setMeta(path, func(m *metaItem) { m.Unread = unread })
}

// SetAllArchived archives (or restores) every non-archived session last
// modified before t, and reports how many changed. One write keeps the whole
// sweep out of the UI's way; sessions already in the requested state are left
// alone.
func (s *Store) SetAllArchivedBefore(projectKey string, before time.Time, archived bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta := s.loadMetaLocked()
	changed := 0
	for _, info := range s.infos {
		if info.Archived == archived || info.Modified.After(before) {
			continue
		}
		if projectKey != "" && info.ProjectKey != projectKey {
			continue
		}
		item := meta[info.Path]
		item.Archived = archived
		meta[info.Path] = item
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	s.meta = meta
	if err := s.saveMetaLocked(); err != nil {
		return 0, err
	}
	return changed, s.saveCacheLocked()
}

// DeleteAll removes every session of one project (a group's project key is
// its normalized cwd) and reports how many files it deleted.
func (s *Store) DeleteAll(projectKey string) (int, error) {
	paths := make([]string, 0, 8)
	for _, info := range s.infos {
		if projectKey == "" || info.ProjectKey == projectKey {
			paths = append(paths, info.Path)
		}
	}
	deleted := 0
	var first error
	for _, path := range paths {
		if err := s.Delete(path); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		deleted++
	}
	return deleted, first
}

func (s *Store) setMeta(path string, mutate func(*metaItem)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reload from disk so flags written by another Store are never dropped.
	meta := s.loadMetaLocked()
	item := meta[path]
	mutate(&item)
	meta[path] = item
	if err := s.saveMetaLocked(); err != nil {
		return err
	}
	for i := range s.infos {
		if s.infos[i].Path == path {
			applyMeta(&s.infos[i], item)
		}
	}
	if ce, ok := s.cache[path]; ok {
		applyMeta(&ce.Info, item)
		s.cache[path] = ce
	}
	return nil
}

// LastWarnings returns a copy of the warnings produced by the last Refresh.
func (s *Store) LastWarnings() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, len(s.warnings))
	copy(out, s.warnings)
	return out
}
