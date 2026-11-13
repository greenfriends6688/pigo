package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The pi the user already has. pigo used to drive only its own bundled copy of
// pi's RPC bundle; the terminal's pi is the one with the user's models,
// extensions and trust, and running it is what pi-gpui does. The bundle stays
// as a fallback for machines without pi.
const (
	// MinPiMinor is the oldest pi that speaks the RPC this app was written
	// against (1.4). Newer minors of the same major still work; a new major
	// means the protocol may have moved and the app was not tested on it.
	MinPiMajor = 1
	MinPiMinor = 4

	piVersionTimeout = 15 * time.Second
)

// PiVersion is a parsed `pi --version`.
type PiVersion struct{ Major, Minor, Patch int }

func (v PiVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Supported reports whether this app can drive that pi.
func (v PiVersion) Supported() bool {
	return v.Major == MinPiMajor && v.Minor >= MinPiMinor
}

// DiscoverPi returns the pi executable to run: PI_EXECUTABLE when it points at
// a file, else the `pi` on PATH, else the one a login shell finds (a GUI app
// launched from Finder inherits almost no PATH, so nvm/volta installs only
// show up through the shell).
func DiscoverPi() string {
	if explicit := strings.TrimSpace(os.Getenv("PI_EXECUTABLE")); explicit != "" {
		if st, err := os.Stat(explicit); err == nil && !st.IsDir() {
			return explicit
		}
	}
	return FindExecutable("pi")
}

// FindExecutable looks name up on PATH, then through the user's login shell.
func FindExecutable(name string) string {
	if p, err := exec.LookPath(name); err == nil && p != "" {
		if abs, absErr := filepath.Abs(p); absErr == nil {
			return abs
		}
		return p
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(context.Background(), piVersionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lc", "command -v "+name).Output()
	if err != nil {
		return ""
	}
	// Interactive shells print banners; the answer is the last absolute path.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "/") {
			continue
		}
		if st, statErr := os.Stat(line); statErr == nil && !st.IsDir() {
			return line
		}
	}
	return ""
}

// ReadPiVersion runs `<pi> --version`. pi is usually a `#!/usr/bin/env node`
// script, so the lookup only works with the executable's own directory on
// PATH (nvm and ~/.local keep node next to pi).
func ReadPiVersion(pi string) (PiVersion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), piVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, pi, "--version")
	cmd.Env = PathWithDir(pi)
	out, err := cmd.Output()
	if err != nil {
		return PiVersion{}, fmt.Errorf("agent: run %s --version: %w", pi, err)
	}
	return ParsePiVersion(string(out))
}

// ParsePiVersion finds the first x.y.z in text, so "pi v1.4.0" and "1.4.0"
// both parse.
func ParsePiVersion(text string) (PiVersion, error) {
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r == '.')
	}) {
		parts := strings.Split(field, ".")
		if len(parts) != 3 {
			continue
		}
		var nums [3]int
		ok := true
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				ok = false
				break
			}
			nums[i] = n
		}
		if ok {
			return PiVersion{Major: nums[0], Minor: nums[1], Patch: nums[2]}, nil
		}
	}
	return PiVersion{}, fmt.Errorf("agent: no version in %q", strings.TrimSpace(text))
}

// PathWithDir returns the child's environment with the executable's directory
// first on PATH, so `#!/usr/bin/env node` resolves next to pi.
func PathWithDir(executable string) []string {
	env := os.Environ()
	dir := filepath.Dir(executable)
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+dir+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, "PATH="+dir)
	}
	return out
}
