package sessions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// ---------- helpers ----------

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func headerLine(id, cwd, parent string) string {
	return fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-09-22T07:50:04.747Z","cwd":%q,"parentSession":%q}`, id, cwd, parent)
}

func userLine(id, contentJSON string) string {
	return fmt.Sprintf(`{"type":"message","id":%q,"parentId":"h","timestamp":"2026-09-22T07:50:22.792Z","message":{"role":"user","content":%s}}`, id, contentJSON)
}

func infoLine(id, name string) string {
	return fmt.Sprintf(`{"type":"session_info","id":%q,"parentId":"h","timestamp":"2026-09-28T03:10:43.590Z","name":%q}`, id, name)
}

func sessionPath(agentDir, cwd, filename string) string {
	return filepath.Join(SessionRoot(agentDir), DirName(cwd), filename)
}

// ---------- DirName ----------

func TestDirName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/Desktop/BidBook", "--Users-x-Desktop-BidBook--"},
		{"/Users/yingjing/Desktop/PI NEXT", "--Users-yingjing-Desktop-PI NEXT--"},
		{"/Users/yingjing/Desktop/重构pi web", "--Users-yingjing-Desktop-重构pi web--"},
		// pi replaces each separator char individually, so the adjacent ':' and
		// '\' in a Windows drive prefix each become a dash (two dashes).
		{`C:\work\repo`, "--C--work-repo--"},
		{"/", "----"},
	}
	for _, c := range cases {
		if got := DirName(c.in); got != c.want {
			t.Fatalf("DirName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefaultAgentDirAndSessionRoot(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "/custom/agent")
	if got := DefaultAgentDir(); got != "/custom/agent" {
		t.Fatalf("DefaultAgentDir() = %q", got)
	}
	if got := SessionRoot("/custom/agent"); got != filepath.Join("/custom/agent", "sessions") {
		t.Fatalf("SessionRoot() = %q", got)
	}
}

// ---------- Read ----------

func TestReadFields(t *testing.T) {
	agentDir := t.TempDir()
	cwd := "/Users/x/Desktop/BidBook"
	p := sessionPath(agentDir, cwd, "2026-09-22T07-50-04-747Z_sess1.jsonl")
	long := strings.Repeat("甲", 260) // 260 runes, multi-byte UTF-8
	writeLines(t, p,
		headerLine("sess-1", cwd, "parent-9"),
		userLine("m1", jsonString(long)),
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-09-22T07:50:33.428Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`,
		infoLine("i1", "旧名字"),
		infoLine("i2", "最终名字"),
	)

	mt := time.Date(2026, 10, 1, 2, 3, 4, 0, time.UTC)
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}

	info, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "sess-1" {
		t.Fatalf("ID = %q", info.ID)
	}
	if info.Path != p {
		t.Fatalf("Path = %q", info.Path)
	}
	if info.Cwd != cwd {
		t.Fatalf("Cwd = %q", info.Cwd)
	}
	if info.Name != "最终名字" {
		t.Fatalf("Name = %q, want last session_info", info.Name)
	}
	if info.ParentSession != "parent-9" {
		t.Fatalf("ParentSession = %q", info.ParentSession)
	}
	if want := string([]rune(long)[:firstMessageLimit]); info.FirstMessage != want {
		t.Fatalf("FirstMessage len(runes)=%d, want %d", len([]rune(info.FirstMessage)), firstMessageLimit)
	}
	if !utf8.ValidString(info.FirstMessage) {
		t.Fatal("FirstMessage is not valid UTF-8 (rune truncation broke it)")
	}
	if !info.Created.Equal(parseTime("2026-09-22T07:50:04.747Z")) {
		t.Fatalf("Created = %v", info.Created)
	}
	if !info.Modified.Equal(mt) {
		t.Fatalf("Modified = %v, want mtime %v", info.Modified, mt)
	}
	if info.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2", info.MessageCount)
	}
	if info.ProjectKey != projectKey(cwd) {
		t.Fatalf("ProjectKey = %q", info.ProjectKey)
	}
}

// TestReadLargeFileHeadTail proves the O(1) head+tail strategy: a >200 KB (and
// a ~1 MB) file still yields both the first message (head) and the last
// session_info (tail), while never returning the bulky middle content.
func TestReadLargeFileHeadTail(t *testing.T) {
	agentDir := t.TempDir()
	cwd := "/Users/x/Desktop/BigProject"
	first := "FIRST_HELLO_第一条用户消息"
	name := "TAIL_NAME_尾部会话名"
	filler := strings.Repeat("MIDDLE_MARKER_XYZ_中间填充_", 80)

	for _, fillLines := range []int{160, 900} {
		p := sessionPath(agentDir, cwd, fmt.Sprintf("big_%d.jsonl", fillLines))
		var b strings.Builder
		b.WriteString(headerLine("big-1", cwd, "") + "\n")
		b.WriteString(userLine("m1", jsonString(first)) + "\n")
		for i := 0; i < fillLines; i++ {
			b.WriteString(fmt.Sprintf(`{"type":"custom","id":"c%d","note":%q}`+"\n", i, filler))
		}
		b.WriteString(infoLine("i-tail", name) + "\n")
		data := []byte(b.String())
		if len(data) <= 200*1024 {
			t.Fatalf("test file too small: %d bytes", len(data))
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}

		info, err := Read(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.FirstMessage != first {
			t.Fatalf("fill=%d FirstMessage = %q, want head's first message", fillLines, info.FirstMessage)
		}
		if info.Name != name {
			t.Fatalf("fill=%d Name = %q, want tail's session_info", fillLines, info.Name)
		}
		all := info.ID + info.Name + info.FirstMessage + info.Cwd + info.Branch + info.ProjectKey
		if strings.Contains(all, "MIDDLE_MARKER_XYZ") {
			t.Fatal("middle bulk content leaked into Info")
		}

		// Reading volume stays bounded regardless of file size.
		f, err := os.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		slice, err := readSlices(f, st.Size())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(slice) > headSize+tailSize {
			t.Fatalf("read %d bytes for a %d-byte file: not O(1)", len(slice), st.Size())
		}
		if !strings.Contains(string(slice), "FIRST_HELLO") || !strings.Contains(string(slice), "TAIL_NAME") {
			t.Fatal("head/tail slices lost the first message or the tail session_info")
		}
	}
}

// TestReadMalformedAndVariantContent ensures bad lines, non-JSON, text arrays
// and image arrays never panic and never fail the whole read.
func TestReadMalformedAndVariantContent(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Read panicked on malformed input: %v", r)
		}
	}()
	agentDir := t.TempDir()
	cwd := "/Users/x/Desktop/Robust"
	p := sessionPath(agentDir, cwd, "robust.jsonl")
	writeLines(t, p,
		headerLine("rob-1", cwd, ""),
		`this is not json at all`,
		`{"type":"message","id":"m1","message":{"role":"user","content":[{"type":"text","text":"来自数组的消息"}]}}`,
		`{"type":"message","id":"m2","message":{"role":"user","content":[{"type":"image","data":"AAAA","mimeType":"image/png"}]}}`,
		`{"type":"message","id":"m3","message":{"role":"assistant","content":null}}`,
		`{"type":"message","id":"m4","message":{"role":"user","content":{"unexpected":"object"}}}`,
		`{"type":"message","id":"m5","message":{"role":"user","content":12345}}`,
		`{"type":"message","id":"m6","message":{"role":"user","content":[{"type":"text","text":"图片之后"}]}}`,
		`{"type":"session_info","id":"i1","name":"健壮名字"}`,
		`{broken json`,
	)

	info, err := Read(p)
	if err != nil {
		t.Fatalf("Read should tolerate bad lines: %v", err)
	}
	if info.ID != "rob-1" || info.Name != "健壮名字" {
		t.Fatalf("ID/Name = %q / %q", info.ID, info.Name)
	}
	if info.FirstMessage != "来自数组的消息" {
		t.Fatalf("FirstMessage = %q", info.FirstMessage)
	}
}

func TestGitInfo(t *testing.T) {
	dir := t.TempDir()

	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	br, wt := gitInfo(repo)
	if br != "main" || wt {
		t.Fatalf("normal repo: branch=%q worktree=%v", br, wt)
	}

	wtDir := filepath.Join(dir, "wt")
	gd := filepath.Join(dir, "mainrepo", ".git", "worktrees", "wt")
	if err := os.MkdirAll(gd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gd, "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+gd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	br, wt = gitInfo(wtDir)
	if br != "feature/x" || !wt {
		t.Fatalf("worktree: branch=%q worktree=%v", br, wt)
	}

	if br, wt := gitInfo(filepath.Join(dir, "nonexistent")); br != "" || wt {
		t.Fatalf("missing cwd should yield empty branch: %q %v", br, wt)
	}
}

func TestScan(t *testing.T) {
	agentDir, paths := makeStoreFixture(t)
	infos, err := Scan(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != len(paths) {
		t.Fatalf("Scan returned %d infos, want %d", len(infos), len(paths))
	}
	// Newest first.
	for i := 1; i < len(infos); i++ {
		if infos[i-1].Modified.Before(infos[i].Modified) {
			t.Fatal("Scan result is not sorted by Modified desc")
		}
	}
	if _, err := Scan(filepath.Join(agentDir, "does-not-exist")); err != nil {
		t.Fatalf("Scan on missing root should not fail: %v", err)
	}
}

func TestPreviewReadsTheLastTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-01-01_abc.jsonl")
	body := `{"type":"session","id":"abc","cwd":"/tmp","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"第一问"}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"第一答"}]}}` + "\n" +
		`{"type":"message","message":{"role":"user","content":"第二问"}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"正在改文件"},{"type":"toolCall","id":"c1","name":"edit"},{"type":"toolCall","id":"c2","name":"write"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	snippet, tools, err := Preview(path)
	if err != nil {
		t.Fatal(err)
	}
	if snippet != "正在改文件" {
		t.Fatalf("snippet = %q", snippet)
	}
	if tools != 2 {
		t.Fatalf("tools = %d, want 2 (only the last turn's)", tools)
	}
	// The scanner fills the same two fields, so a row's tooltip costs no read.
	info, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Preview != "正在改文件" || info.LastTurnTools != 2 {
		t.Fatalf("info preview = %q, tools = %d", info.Preview, info.LastTurnTools)
	}
}
