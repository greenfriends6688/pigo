package sessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RepairCwd copies a session whose stored working directory is gone to dir,
// with the header's cwd replaced by cwd: pi refuses to open a session whose
// recorded directory no longer exists. The copy keeps the session id and every
// entry, so its history stays readable. An existing copy is reused as is.
func RepairCwd(src, cwd, dir string) (string, error) {
	if src == "" || cwd == "" {
		return "", fmt.Errorf("sessions: repair needs a path and a cwd")
	}
	dest := filepath.Join(dir, filepath.Base(src))
	if st, err := os.Stat(dest); err == nil && st.Size() > 0 {
		return dest, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dir, ".repair-*.jsonl")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())

	w := bufio.NewWriter(tmp)
	r := bufio.NewReaderSize(in, 64<<10)
	replaced := false
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			if !replaced {
				if fixed, ok := setHeaderCwd(line, cwd); ok {
					line = fixed
					replaced = true
				}
			}
			if _, err := w.Write(line); err != nil {
				tmp.Close()
				return "", err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return "", readErr
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// setHeaderCwd rewrites the cwd of a session header line, keeping every other
// field and the line's own trailing newline.
func setHeaderCwd(line []byte, cwd string) ([]byte, bool) {
	body := bytes.TrimRight(line, "\r\n")
	var header map[string]any
	if json.Unmarshal(body, &header) != nil || header["type"] != "session" {
		return nil, false
	}
	header["cwd"] = cwd
	out, err := json.Marshal(header)
	if err != nil {
		return nil, false
	}
	if bytes.HasSuffix(line, []byte("\n")) {
		out = append(out, '\n')
	}
	return out, true
}
