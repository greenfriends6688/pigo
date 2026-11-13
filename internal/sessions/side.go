package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SideBoundaryText is what the model reads at the end of a side chat's
// inherited history: pi sends custom messages to every provider, so the
// note says the history above is background, not the current task.
const SideBoundaryText = "[侧聊] 以上内容是父会话的冻结背景，不属于本次侧聊；只有此后的消息才是当前任务。"

// SideTools are the read-mostly tools a side chat keeps of its parent's.
const SideTools = "read,bash,grep,find,ls"

// ForkSide writes a side chat's file beside its parent: a byte copy of the
// branch that gets a header id of its own, the pi-web:side entry and the
// boundary note. It exists because pi's own `clone` rebinds the parent's
// process to the clone: the main conversation would keep answering into the
// side file, which is not something a tab in the right panel may do.
func ForkSide(src, parentSessionID string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("sessions: %s is empty", src)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		return "", fmt.Errorf("sessions: %s has no readable header: %w", src, err)
	}
	id := newEntryID() + newEntryID()
	now := time.Now().UTC()
	header["id"] = id
	header["timestamp"] = now.Format("2006-01-02T15:04:05.000Z")
	headerLine, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	lines[0] = string(headerLine)
	dst := filepath.Join(filepath.Dir(src), now.Format("2006-01-02T15-04-05-000Z")+"_"+id+".jsonl")
	if err := writeFileAtomic(dst, []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	if err := MarkSide(dst, parentSessionID); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// MarkSide turns a freshly forked session into a side chat, as pi-web's
// createSideSession does: a pi-web:side entry right after the header, the
// inherited first entry hung under it, and a hidden boundary message as the
// new leaf.
func MarkSide(path, parentSessionID string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 {
		return fmt.Errorf("sessions: %s is empty", path)
	}
	info, err := Read(path)
	if err != nil {
		return err
	}
	if info.Side {
		return nil // already marked: a second press has nothing to add
	}
	metaID := newEntryID()
	now := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]any{
		"type": "custom", "customType": "pi-web:side",
		"data":      map[string]any{"version": 1, "parentSessionId": parentSessionID, "createdAt": now},
		"id":        metaID,
		"parentId":  nil,
		"timestamp": now,
	}
	metaLine, _ := json.Marshal(meta)
	out := make([]string, 0, len(lines)+2)
	out = append(out, lines[0], string(metaLine))
	lastID := metaID
	// The inherited first entry was the header's child; make it the side
	// entry's child instead so the boundary has a path to the whole branch.
	if len(lines) > 1 {
		var first map[string]any
		if json.Unmarshal([]byte(lines[1]), &first) == nil {
			if first["parentId"] == nil {
				first["parentId"] = metaID
				if b, err := json.Marshal(first); err == nil {
					lines[1] = string(b)
				}
			}
		}
		out = append(out, lines[1])
	}
	for _, line := range lines[2:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		var entry struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ID != "" {
			lastID = entry.ID
		}
	}
	boundaryID := newEntryID()
	boundary := map[string]any{
		"type": "custom_message", "customType": "pi-web:side-boundary",
		"content": SideBoundaryText, "display": false,
		"id": boundaryID, "parentId": lastID, "timestamp": now,
	}
	boundaryLine, _ := json.Marshal(boundary)
	out = append(out, string(boundaryLine))
	if info.ID == "" {
		return fmt.Errorf("sessions: %s has no header id", path)
	}
	// A second press of "新建侧聊" can mark the same clone twice; the rename
	// then found the first call's temp file already gone ("no such file or
	// directory"). A temp of its own and one retry keep both calls honest.
	if err := writeFileAtomic(path, []byte(strings.Join(out, "\n")+"\n")); err != nil {
		return err
	}
	return nil
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory, exclusive to this call, and retries once if the temp vanished
// between writing it and the rename.
func writeFileAtomic(path string, data []byte) error {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
		if err != nil {
			return err
		}
		tmp := f.Name()
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, path); err == nil {
			return nil
		} else if !isNotExist(err) {
			_ = os.Remove(tmp)
			return err
		}
		_ = os.Remove(tmp)
	}
	return os.ErrNotExist
}

// isNotExist reports a missing file, which a retry can answer.
func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// IsSide reports whether a session file is a side chat, for callers that
// hold only the path.
func IsSide(path string) bool {
	info, err := Read(path)
	return err == nil && info.Side
}

// SideParent returns the parent session id of a side chat, empty otherwise.
func SideParent(path string) string {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return ""
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var entry struct {
			CustomType string `json:"customType"`
			Data       struct {
				ParentSessionID string `json:"parentSessionId"`
			} `json:"data"`
		}
		if err := dec.Decode(&entry); err != nil {
			return ""
		}
		if entry.CustomType == "pi-web:side" {
			return entry.Data.ParentSessionID
		}
	}
}
