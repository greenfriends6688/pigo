// Package resources finds pi's skills and subagents on disk and edits the
// few frontmatter keys the settings page owns.
package resources

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one SKILL.md pi would load.
type Skill struct {
	Name, Description, Path, BaseDir, Source string
	Dormant                                  bool
}

// Agent is one subagent profile: a file pi reads, or one of pi's own
// built-ins, whose Enabled switch is the settings file's disabledBuiltIns.
type Agent struct {
	Name, Description, Model, Path, Source string
	// Builtin marks pi's own profile, Display its panel name, Prompt the
	// system prompt the read-only view shows and Tools what it may call.
	Builtin bool
	Display string
	Prompt  string
	Tools   []string
	Enabled bool
}

// Skills lists the skill directories pi reads, project first so a project
// override of the same name wins.
func Skills(agentDir, home, cwd string) []Skill {
	dirs := []struct{ dir, source string }{
		{filepath.Join(cwd, ".pi", "skills"), "project"},
		{filepath.Join(cwd, ".agents", "skills"), "project-user"},
		{filepath.Join(agentDir, "skills"), "global"},
		{filepath.Join(home, ".agents", "skills"), "user"},
	}
	seen := map[string]bool{}
	var out []Skill
	for _, d := range dirs {
		entries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(d.dir, e.Name(), "SKILL.md")
			meta, ok := readFrontmatter(path)
			if !ok {
				continue
			}
			name := meta["name"]
			if name == "" {
				name = e.Name()
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Skill{
				Name:        name,
				Description: meta["description"],
				Path:        path,
				BaseDir:     filepath.Dir(path),
				Source:      d.source,
				Dormant:     strings.EqualFold(meta["disable-model-invocation"], "true"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// ReadSkill returns a SKILL.md, capped so a huge file cannot freeze the UI.
func ReadSkill(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const cap = 1 << 20
	if len(data) > cap {
		data = data[:cap]
	}
	return string(data), nil
}

// SaveSkill writes a skill's SKILL.md back, keeping its frontmatter as the
// editor left it. Only a file under a skills directory is written: the path
// comes from the listing, so a caller cannot point it elsewhere.
func SaveSkill(path, body string) error {
	if !strings.HasSuffix(path, ".md") {
		return fmt.Errorf("resources: a skill is a SKILL.md file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// SetDormant adds or removes disable-model-invocation in a skill's
// frontmatter, leaving every other line alone.
func SetDormant(path string, dormant bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	head, body, ok := splitFrontmatter(text)
	if !ok {
		if !dormant {
			return nil
		}
		return fmt.Errorf("resources: %s has no frontmatter", filepath.Base(path))
	}
	lines := strings.Split(head, "\n")
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		key := strings.TrimSpace(strings.SplitN(line, ":", 2)[0])
		if key == "disable-model-invocation" {
			if dormant {
				out = append(out, "disable-model-invocation: true")
				replaced = true
			}
			continue
		}
		out = append(out, line)
	}
	if dormant && !replaced {
		out = append(out, "disable-model-invocation: true")
	}
	next := "---\n" + strings.Join(out, "\n") + "\n---\n" + body
	if !strings.HasSuffix(next, "\n") {
		next += "\n"
	}
	return os.WriteFile(path, []byte(next), 0o600)
}

// Agents lists the subagent profiles in the global directory and the
// project's two agent directories.
func Agents(agentDir, cwd string, disabled []string) []Agent {
	dirs := []struct{ dir, source string }{
		{filepath.Join(cwd, ".pi", "agents"), "project"},
		{filepath.Join(cwd, ".agents", "agents"), "project-user"},
		{filepath.Join(agentDir, "agents"), "global"},
	}
	seen := map[string]bool{}
	var out []Agent
	for _, d := range dirs {
		entries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			path := filepath.Join(d.dir, e.Name())
			meta, _ := readFrontmatter(path)
			name := meta["name"]
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".md")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Agent{Name: name, Description: meta["description"], Model: meta["model"],
				Path: path, Source: d.source, Enabled: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	// pi's own profiles are listed first; a file of the same name replaces
	// its built-in outright, as pi resolves them.
	off := map[string]bool{}
	for _, name := range disabled {
		off[strings.ToLower(strings.TrimSpace(name))] = true
	}
	builtins := make([]Agent, 0, len(BuiltinAgents))
	for _, b := range BuiltinAgents {
		if seen[b.Name] {
			continue
		}
		builtins = append(builtins, Agent{
			Name: b.Name, Display: b.Display, Description: b.Description, Source: "内置",
			Builtin: true, Prompt: b.Prompt, Tools: b.Tools, Enabled: !off[strings.ToLower(b.Name)],
		})
	}
	return append(builtins, out...)
}

// ReadAgent returns an agent file, capped.
func ReadAgent(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	const cap = 1 << 20
	if len(data) > cap {
		data = data[:cap]
	}
	return string(data), nil
}

// SaveAgent writes an agent profile, creating its directory.
func SaveAgent(path, content string) error {
	if !strings.HasSuffix(path, ".md") {
		return fmt.Errorf("resources: an agent profile is a .md file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// DeleteAgent removes a profile file.
func DeleteAgent(path string) error { return os.Remove(path) }

func readFrontmatter(path string) (map[string]string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	head, _, ok := splitFrontmatter(string(buf[:n]))
	if !ok {
		return map[string]string{}, true
	}
	meta := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return meta, true
}

// splitFrontmatter returns the lines between the leading --- fences and the
// body after them.
func splitFrontmatter(text string) (head, body string, ok bool) {
	if !strings.HasPrefix(text, "---") {
		return "", text, false
	}
	rest := text[3:]
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", text, false
	}
	head = strings.TrimSuffix(rest[:end], "\r")
	body = rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r")
	body = strings.TrimPrefix(body, "\n")
	return head, body, true
}
