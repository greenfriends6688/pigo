package sessions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// rawEntry is a tolerant view of one JSONL line. Every field is optional and
// unknown fields are ignored, so malformed or variant lines never break parsing.
type rawEntry struct {
	Type          string      `json:"type"`
	ID            string      `json:"id"`
	Timestamp     string      `json:"timestamp"`
	Cwd           string      `json:"cwd"`
	ParentSession string      `json:"parentSession"`
	Name          string      `json:"name"`
	CustomType    string      `json:"customType"`
	Message       *rawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// Read parses a single session file using only its first 16 KB and last 64 KB
// (files smaller than 80 KB are read in full). Modified comes from the file
// mtime. A malformed line is skipped, never fatal.
func Read(path string) (Info, error) {
	return readInfo(path, nil)
}

// readInfo is Read with optional warning collection, used by the scanner.
func readInfo(path string, warnings *[]string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	if st.IsDir() {
		return Info{}, fmt.Errorf("not a regular file")
	}

	data, err := readSlices(f, st.Size())
	if err != nil {
		return Info{}, err
	}

	info := Info{Path: path, Modified: st.ModTime()}
	parseLines(data, &info, warnings)
	info.Preview, info.LastTurnTools = previewOf(data)

	if info.Created.IsZero() {
		info.Created = info.Modified
	}
	if info.Cwd != "" {
		info.ProjectKey = projectKey(info.Cwd)
		info.Branch, info.IsWorktree = gitInfo(info.Cwd)
	} else if info.Path != "" {
		info.ProjectKey = projectKey(filepath.Base(filepath.Dir(info.Path)))
	}
	return info, nil
}

// readSlices returns the concatenation of the file's first headSize and last
// tailSize bytes. The trailing partial line of the head and the leading partial
// line of the tail are dropped so two half-lines are never glued into a fake
// JSON object. The returned length is bounded by headSize+tailSize regardless
// of the file size, which keeps reading O(1) per session.
func readSlices(f *os.File, size int64) ([]byte, error) {
	if size <= headSize+tailSize {
		buf := make([]byte, size)
		n, err := f.ReadAt(buf, 0)
		if err != nil && err != io.EOF {
			return nil, err
		}
		return buf[:n], nil
	}

	head := make([]byte, headSize)
	tail := make([]byte, tailSize)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, err
	}
	if _, err := f.ReadAt(tail, size-tailSize); err != nil {
		return nil, err
	}

	// Keep the head only through its last complete newline.
	if i := bytes.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	} else {
		head = nil // the entire head is one incomplete line
	}
	// Drop the tail's first (possibly partial) line.
	if i := bytes.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	} else {
		tail = nil
	}

	out := make([]byte, 0, len(head)+len(tail))
	out = append(out, head...)
	out = append(out, tail...)
	return out, nil
}

// parseLines folds every complete JSONL line into info. Bad lines are skipped
// and optionally recorded; no input can make this panic.
func parseLines(data []byte, info *Info, warnings *[]string) {
	for _, raw := range bytes.Split(data, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var e rawEntry
		if err := json.Unmarshal(line, &e); err != nil {
			addWarning(warnings, fmt.Sprintf("skip malformed line: %v", err))
			continue
		}
		switch e.Type {
		case "session":
			// Only the first header wins; it carries id/cwd/created/relation.
			if info.ID == "" {
				info.ID = e.ID
				info.Cwd = e.Cwd
				info.ParentSession = e.ParentSession
				info.Created = parseTime(e.Timestamp)
			}
		case "session_info":
			// The last session_info entry (typically appended at the tail) wins.
			info.Name = e.Name
		case "custom":
			// A pi-web:side entry marks a side chat, which the sidebar keeps
			// out of the parent's subagent list.
			if e.CustomType == "pi-web:side" {
				info.Side = true
			}
		case "message":
			info.MessageCount++
			if info.FirstMessage == "" && e.Message != nil && e.Message.Role == "user" {
				if txt := extractText(e.Message.Content); txt != "" {
					info.FirstMessage = truncateRunes(txt, firstMessageLimit)
				}
			}
		}
	}
}

// extractText returns the human text of message.content, which may be a plain
// string, an array of {type:"text",text:"..."} blocks (possibly mixed with
// image blocks), or any other shape. It never panics and returns "" for
// unsupported shapes such as images, numbers or objects.
func extractText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '[':
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		var b strings.Builder
		for _, blk := range blocks {
			if blk.Text == "" {
				continue // image/tool blocks have no text
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	return ""
}

// parseTime parses the ISO-8601/RFC3339 timestamps pi writes.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// addWarning appends a warning with a hard cap. A nil destination is ignored.
func addWarning(w *[]string, msg string) {
	if w == nil || len(*w) >= maxWarnings {
		return
	}
	*w = append(*w, msg)
}

// previewOf reads the newest turn from the lines the scanner already read:
// the last assistant text and how many tool calls followed the last user
// message. It never opens the file again.
func previewOf(data []byte) (snippet string, tools int) {
	lines := strings.Split(string(data), "\n")
	lastUser := -1
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		role := entry.Role
		if role == "" {
			role = entry.Message.Role
		}
		if role == "user" {
			lastUser = i
			break
		}
	}
	for i := len(lines) - 1; i > lastUser; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		content := entry.Content
		if len(content) == 0 {
			content = entry.Message.Content
		}
		blocks := blocksOf(content)
		for _, b := range blocks {
			if b.Type == "toolCall" || b.Type == "tool_use" {
				tools++
			}
		}
		if snippet == "" && entry.Message.Role == "assistant" {
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					snippet = firstLineOf(b.Text, 160)
				}
			}
		}
	}
	return snippet, tools
}

// Preview is a session's newest turn for a hover card: the last thing the
// assistant said, cut to a line or two, and how many tool calls that turn
// made. Only the file's tail is read, so a long session costs one small read.
func Preview(path string) (snippet string, tools int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	const tail = 256 << 10
	start := int64(0)
	if st.Size() > tail {
		start = st.Size() - tail
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", 0, err
	}
	lines := strings.Split(string(buf), "\n")
	// The first line of a tail read starts mid-entry: drop it.
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	var lastUser int = -1
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		role := entry.Role
		if role == "" {
			role = entry.Message.Role
		}
		if role == "user" && (entry.Type == "message" || entry.Type == "") {
			lastUser = i
			break
		}
	}
	for i := len(lines) - 1; i > lastUser; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var entry struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		content := entry.Content
		if len(content) == 0 {
			content = entry.Message.Content
		}
		blocks := blocksOf(content)
		for _, b := range blocks {
			switch b.Type {
			case "toolCall", "tool_use":
				tools++
			}
		}
		if snippet == "" && (entry.Message.Role == "assistant" || entry.Type == "assistant") {
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					snippet = firstLineOf(b.Text, 160)
				}
			}
		}
	}
	return snippet, tools, nil
}

// contentBlock is the little of a content block Preview reads.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// blocksOf reads a message's content as blocks, tolerating a plain string.
func blocksOf(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	var list []contentBlock
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var text string
	if json.Unmarshal(raw, &text) == nil && strings.TrimSpace(text) != "" {
		return []contentBlock{{Type: "text", Text: text}}
	}
	return nil
}

// firstLineOf cuts text to its first line, at most n runes.
func firstLineOf(text string, n int) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimSpace(text)
	r := []rune(text)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}
