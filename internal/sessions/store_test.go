package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureSpec struct {
	file, id, cwd, name, first string
	mtime                      time.Time
}

// makeStoreFixture builds four sessions across three projects with distinct
// mtimes so ordering is deterministic: g1(4h) > a2(3h) > b1(2h) > a1(1h).
func makeStoreFixture(t *testing.T) (string, []string) {
	t.Helper()
	agentDir := t.TempDir()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	specs := []fixtureSpec{
		{"2026-09-01T00-00-00-000Z_a1.jsonl", "a1", "/Users/x/Desktop/Alpha", "Alpha One", "Hello ALPHA first", base.Add(1 * time.Hour)},
		{"2026-09-02T00-00-00-000Z_a2.jsonl", "a2", "/Users/x/Desktop/Alpha", "Alpha Two", "second alpha", base.Add(3 * time.Hour)},
		{"2026-09-03T00-00-00-000Z_b1.jsonl", "b1", "/Users/x/Desktop/Beta", "Beta Session", "你好 BETA", base.Add(2 * time.Hour)},
		{"2026-09-04T00-00-00-000Z_g1.jsonl", "g1", "/Users/x/Desktop/Gamma", "Gamma", "gamma msg", base.Add(4 * time.Hour)},
	}
	paths := make([]string, 0, len(specs))
	for _, sp := range specs {
		p := sessionPath(agentDir, sp.cwd, sp.file)
		writeLines(t, p,
			headerLine(sp.id, sp.cwd, ""),
			userLine("m-"+sp.id, jsonString(sp.first)),
			fmt.Sprintf(`{"type":"message","id":"as-%s","parentId":"m","timestamp":"2026-09-22T07:50:33.428Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`, sp.id),
			infoLine("i1-"+sp.id, "旧名字"),
			infoLine("i2-"+sp.id, sp.name),
		)
		if err := os.Chtimes(p, sp.mtime, sp.mtime); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	return agentDir, paths
}

func indexByPath(infos []Info) map[string]Info {
	m := make(map[string]Info, len(infos))
	for _, in := range infos {
		m[in.Path] = in
	}
	return m
}

func TestStoreCacheRoundTrip(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)

	if err := st.LoadCache(); err != nil {
		t.Fatal(err)
	}
	if got := st.List(); len(got) != 0 {
		t.Fatalf("fresh cache should be empty, got %d", len(got))
	}
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	got := st.List()
	if len(got) != len(paths) {
		t.Fatalf("List() = %d, want %d", len(got), len(paths))
	}
	if _, err := os.Stat(st.IndexPath()); err != nil {
		t.Fatalf("SaveCache did not write index: %v", err)
	}

	st2 := NewStore(agentDir)
	if err := st2.LoadCache(); err != nil {
		t.Fatal(err)
	}
	got2 := st2.List()
	if len(got2) != len(got) {
		t.Fatalf("LoadCache round-trip: %d infos, want %d", len(got2), len(got))
	}
	byPath := indexByPath(got2)
	for _, want := range got {
		o, ok := byPath[want.Path]
		if !ok {
			t.Fatalf("missing %s after round-trip", want.Path)
		}
		if o.ID != want.ID || o.Name != want.Name || o.FirstMessage != want.FirstMessage || !o.Modified.Equal(want.Modified) {
			t.Fatalf("round-trip mismatch for %s: %+v vs %+v", want.Path, o, want)
		}
	}

	// A corrupt cache is ignored, not fatal.
	if err := os.WriteFile(st.IndexPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st3 := NewStore(agentDir)
	if err := st3.LoadCache(); err != nil {
		t.Fatalf("corrupt cache should be ignored: %v", err)
	}
	if len(st3.List()) != 0 {
		t.Fatal("corrupt cache should yield an empty list")
	}
}

func TestRefreshIfChanged(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)

	changed, err := st.RefreshIfChanged()
	if err != nil || !changed {
		t.Fatalf("first RefreshIfChanged should rescan: changed=%v err=%v", changed, err)
	}
	changed, err = st.RefreshIfChanged()
	if err != nil || changed {
		t.Fatalf("unchanged tree should be a no-op: changed=%v err=%v", changed, err)
	}

	// Append a line and bump mtime: (path,size,mtime) changes.
	f, err := os.OpenFile(paths[0], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(infoLine("i-new", "改过的名字") + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(paths[0], future, future); err != nil {
		t.Fatal(err)
	}

	changed, err = st.RefreshIfChanged()
	if err != nil || !changed {
		t.Fatalf("changed file should trigger a rescan: changed=%v err=%v", changed, err)
	}
	got, ok := indexByPath(st.List())[paths[0]]
	if !ok || got.Name != "改过的名字" {
		t.Fatalf("rescan did not pick up the new name: %+v", got)
	}
}

func TestSearchContentFindsMessageText(t *testing.T) {
	agentDir := t.TempDir()
	cwd := "/Users/x/Desktop/Alpha"
	plain := sessionPath(agentDir, cwd, "2026-10-01T00-00-00-000Z_c1.jsonl")
	writeLines(t, plain,
		headerLine("c1", cwd, ""),
		userLine("m1", jsonString("先看看构建脚本，再改 DeployPipeline 的缓存键")),
		infoLine("i1", "Alpha One"),
	)
	// A second transcript whose only hit lives in a text block, to cover the
	// []block content shape as well as the plain-string one.
	blocky := sessionPath(agentDir, cwd, "2026-10-02T00-00-00-000Z_c2.jsonl")
	writeLines(t, blocky,
		headerLine("c2", cwd, ""),
		`{"type":"message","id":"m2","parentId":"h","timestamp":"2026-10-02T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"thinking","text":"推理"},{"type":"text","text":"部署流水线已经改好，deploy 缓存键也换了。"}]}}`,
	)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}

	hits := st.SearchContent("deploypipeline", 30)
	snippet, ok := hits[plain]
	if !ok {
		t.Fatalf("SearchContent missed the plain-string transcript: %v", hits)
	}
	if !strings.Contains(strings.ToLower(snippet), "deploypipeline") {
		t.Fatalf("snippet %q does not contain the query", snippet)
	}
	if strings.ContainsAny(snippet, "\n\t") {
		t.Fatalf("snippet should be one line, got %q", snippet)
	}

	// Case-insensitive, and trimmed so a stray space still matches.
	if got := st.SearchContent("  DEPLOYPIPELINE ", 30); len(got) != 1 {
		t.Fatalf("SearchContent trimmed/case-insensitive = %d hits, want 1", len(got))
	}
	// A block-array transcript is searched through its text blocks too.
	got := st.SearchContent("部署流水线", 30)
	if _, ok := got[blocky]; !ok || len(got) != 1 {
		t.Fatalf("SearchContent missed the block transcript: %v", got)
	}

	// No hit, no entry; an empty query scans nothing.
	if got := st.SearchContent("no-such-needle-anywhere", 30); len(got) != 0 {
		t.Fatalf("SearchContent(missing) = %v, want none", got)
	}
	if got := st.SearchContent("   ", 30); len(got) != 0 {
		t.Fatalf("SearchContent(blank) = %v, want none", got)
	}
	// limit is honoured: both transcripts mention deploy, one is returned.
	if got := st.SearchContent("deploy", 1); len(got) != 1 {
		t.Fatalf("SearchContent(limit 1) = %d hits, want 1", len(got))
	}
}

func TestSearchGroupsPage(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}

	// Search: case-insensitive substring over name/firstMessage/cwd.
	if got := st.Search("ALPHA"); len(got) != 2 {
		t.Fatalf("Search(ALPHA) = %d, want 2", len(got))
	}
	if got := st.Search("beta"); len(got) != 1 {
		t.Fatalf("Search(beta) = %d, want 1", len(got))
	}
	if got := st.Search("no-such-thing"); len(got) != 0 {
		t.Fatalf("Search(none) = %d, want 0", len(got))
	}
	if got := st.Search(""); len(got) != len(paths) {
		t.Fatalf("Search(empty) = %d, want all", len(got))
	}

	// Groups: three projects, items and groups newest-first.
	groups := st.Groups()
	if len(groups) != 3 {
		t.Fatalf("Groups() = %d, want 3", len(groups))
	}
	if groups[0].Title != "Gamma" {
		t.Fatalf("first group Title = %q, want Gamma (newest)", groups[0].Title)
	}
	for _, g := range groups {
		for i := 1; i < len(g.Items); i++ {
			if g.Items[i-1].Modified.Before(g.Items[i].Modified) {
				t.Fatalf("group %q not sorted", g.Title)
			}
		}
	}
	var alphaItems int
	for _, g := range groups {
		if g.Title == "Alpha" {
			alphaItems = len(g.Items)
		}
	}
	if alphaItems != 2 {
		t.Fatalf("Alpha group has %d items, want 2", alphaItems)
	}

	// Page: window over the newest-first list.
	all := st.List()
	if got := st.Page(0, 200); len(got) != len(all) {
		t.Fatalf("Page(0,200) = %d, want %d", len(got), len(all))
	}
	got := st.Page(1, 2)
	if len(got) != 2 || got[0].Path != all[1].Path || got[1].Path != all[2].Path {
		t.Fatalf("Page(1,2) mismatch: %+v", got)
	}
	if got := st.Page(10, 2); len(got) != 0 {
		t.Fatalf("Page(10,2) = %d, want 0", len(got))
	}
	if got := st.Page(0, 0); len(got) != len(all) {
		t.Fatalf("Page(0,0) should default to 200: got %d", len(got))
	}
}

func TestSetPinnedAndArchived(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	p := paths[0]

	if err := st.SetPinned(p, true); err != nil {
		t.Fatal(err)
	}
	if got := indexByPath(st.List())[p]; !got.Pinned {
		t.Fatal("SetPinned(true) not visible via List")
	}
	if _, err := os.Stat(filepath.Join(agentDir, nativeDirName, metaFileName)); err != nil {
		t.Fatalf("meta.json not written: %v", err)
	}

	if err := st.SetArchived(p, true); err != nil {
		t.Fatal(err)
	}
	for _, in := range st.List() {
		if in.Path == p {
			t.Fatal("archived session must be excluded from List")
		}
	}
	if len(st.List()) != len(paths)-1 {
		t.Fatalf("List() = %d after archive, want %d", len(st.List()), len(paths)-1)
	}
	if len(st.Search("")) != len(paths)-1 || len(st.Page(0, 200)) != len(paths)-1 {
		t.Fatal("Search/Page must also exclude archived sessions")
	}
	for _, g := range st.Groups() {
		for _, in := range g.Items {
			if in.Path == p {
				t.Fatal("Groups must exclude archived sessions")
			}
		}
	}

	// Metadata survives a fresh Store loading the cache.
	st2 := NewStore(agentDir)
	if err := st2.LoadCache(); err != nil {
		t.Fatal(err)
	}
	for _, in := range st2.List() {
		if in.Path == p {
			t.Fatal("archived state was lost across LoadCache")
		}
	}

	if err := st.SetArchived(p, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := indexByPath(st.List())[p]; !ok {
		t.Fatal("un-archived session should reappear")
	}
}

func TestSetName(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	p := paths[0]
	before, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SetName(p, "用户改的新名字"); err != nil {
		t.Fatal(err)
	}
	after, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "用户改的新名字" {
		t.Fatalf("Name = %q after SetName", after.Name)
	}
	if after.ID != before.ID || after.Cwd != before.Cwd || after.FirstMessage != before.FirstMessage {
		t.Fatal("SetName damaged existing fields")
	}

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i, ln := range lines {
		var v map[string]any
		if err := json.Unmarshal(ln, &v); err != nil {
			t.Fatalf("line %d is not valid JSON after append: %v", i, err)
		}
	}
	var last map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"type", "id", "parentId", "timestamp", "name"} {
		if _, ok := last[k]; !ok {
			t.Fatalf("appended entry missing %q", k)
		}
	}
	if last["type"] != "session_info" {
		t.Fatalf("appended entry type = %v", last["type"])
	}
	// The live list sees the new name immediately.
	if got := indexByPath(st.List())[p]; got.Name != "用户改的新名字" {
		t.Fatalf("List() name not updated: %q", got.Name)
	}
}

func TestSetNameNoTrailingNewline(t *testing.T) {
	agentDir := t.TempDir()
	cwd := "/Users/x/Desktop/NoNewline"
	p := sessionPath(agentDir, cwd, "nonl.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	content := headerLine("nn-1", cwd, "") + "\n" +
		userLine("m1", jsonString("first")) + "\n" +
		infoLine("i1", "旧") // deliberately no trailing newline
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := st.SetName(p, "补换行后的名字"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	for i, ln := range lines {
		var v map[string]any
		if err := json.Unmarshal(ln, &v); err != nil {
			t.Fatalf("line %d invalid after repairing newline: %v (%q)", i, err, ln)
		}
	}
	info, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "补换行后的名字" || info.FirstMessage != "first" {
		t.Fatalf("Read after SetName = %+v", info)
	}
}

func TestScanSkipsBadFilesWithoutFailing(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	bad := sessionPath(agentDir, "/Users/x/Desktop/Bad", "bad.jsonl")
	writeLines(t, bad, "not json", "{broken", "")

	infos, err := Scan(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != len(paths) {
		t.Fatalf("bad file should be skipped, got %d want %d", len(infos), len(paths))
	}
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(st.LastWarnings()) == 0 {
		t.Fatal("expected warnings for the bad file")
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				_ = st.List()
				_ = st.Search("a")
				_ = st.Groups()
				_ = st.Page(0, 3)
				_ = st.LastWarnings()
				if j%10 == 0 {
					_ = st.SetPinned(paths[i%len(paths)], j%20 == 0)
				}
				if j%20 == 0 {
					_, _ = st.RefreshIfChanged()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestDeleteAndArchived(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := st.SetArchived(paths[0], true); err != nil {
		t.Fatal(err)
	}
	archived := st.Archived()
	if len(archived) != 1 || archived[0].Path != paths[0] {
		t.Fatalf("archived = %+v", archived)
	}
	if len(st.List()) != 3 {
		t.Fatalf("list has %d sessions, want 3", len(st.List()))
	}
	if err := st.Delete(paths[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths[0]); !os.IsNotExist(err) {
		t.Fatalf("session file still exists: %v", err)
	}
	if len(st.Archived()) != 0 || len(st.List()) != 3 {
		t.Fatalf("after delete: archived=%d list=%d", len(st.Archived()), len(st.List()))
	}
	// A fresh store must not resurrect the deleted session or its metadata.
	again := NewStore(agentDir)
	if err := again.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(again.List()) != 3 || len(again.Archived()) != 0 {
		t.Fatalf("reloaded: list=%d archived=%d", len(again.List()), len(again.Archived()))
	}
}

func TestChildrenListsSubagentSessions(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	parent := paths[1]
	// The fixture's headerLine's third argument is the parent session.
	childPath := sessionPath(agentDir, "/Users/x/Desktop/Alpha", "2026-09-05T00-00-00-000Z_child.jsonl")
	writeLines(t, childPath, headerLine("child", "/Users/x/Desktop/Alpha", "a2"), userLine("m-child", jsonString("sub task")))
	st := NewStore(agentDir)
	if err := st.Refresh(); err != nil {
		t.Fatal(err)
	}
	children := st.Children()
	if len(children) != 1 || children[0].ID != "child" {
		t.Fatalf("children = %+v", children)
	}
	if children[0].ParentSession == "" || parent == "" {
		t.Fatal("fixture is incomplete")
	}
}
