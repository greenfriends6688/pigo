package resources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsAndDormancy(t *testing.T) {
	agentDir, home, cwd := t.TempDir(), t.TempDir(), t.TempDir()
	global := filepath.Join(agentDir, "skills", "alpha", "SKILL.md")
	write(t, global, "---\nname: alpha\ndescription: The alpha skill\n---\n\n# Alpha\n")
	project := filepath.Join(cwd, ".pi", "skills", "alpha", "SKILL.md")
	write(t, project, "---\nname: alpha\ndescription: Project alpha override\n---\nbody\n")
	write(t, filepath.Join(cwd, ".pi", "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: Beta\n---\nbody\n")

	skills := Skills(agentDir, home, cwd)
	if len(skills) != 2 {
		t.Fatalf("skills = %+v", skills)
	}
	if skills[0].Name != "alpha" || skills[0].Source != "project" || !strings.Contains(skills[0].Description, "override") {
		t.Fatalf("the project skill must win: %+v", skills[0])
	}
	if err := SetDormant(skills[0].Path, true); err != nil {
		t.Fatal(err)
	}
	again := Skills(agentDir, home, cwd)
	if !again[0].Dormant {
		t.Fatal("the dormant flag was not written")
	}
	if body, _ := ReadSkill(skills[0].Path); !strings.Contains(body, "disable-model-invocation: true") || !strings.Contains(body, "body") {
		t.Fatalf("body changed: %q", body)
	}
	if err := SetDormant(skills[0].Path, false); err != nil {
		t.Fatal(err)
	}
	if again := Skills(agentDir, home, cwd); again[0].Dormant {
		t.Fatal("the dormant flag was not cleared")
	}
}

func TestAgentsListingAndSave(t *testing.T) {
	agentDir, cwd := t.TempDir(), t.TempDir()
	write(t, filepath.Join(agentDir, "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews code\nmodel: claude\n---\nYou review.\n")
	write(t, filepath.Join(cwd, ".pi", "agents", "helper.md"), "---\ndescription: Helps\n---\nYou help.\n")

	agents := Agents(agentDir, cwd, []string{"plan"})
	// pi's three own profiles come first, one of them switched off, then the
	// files: project before global.
	if len(agents) != len(BuiltinAgents)+2 {
		t.Fatalf("agents = %+v", agents)
	}
	if !agents[0].Builtin || agents[0].Name != "general-purpose" {
		t.Fatalf("built-ins first: %+v", agents[0])
	}
	if plan := agents[2]; plan.Name != "plan" || plan.Enabled {
		t.Fatalf("disabledBuiltIns did not switch plan off: %+v", plan)
	}
	if files := agents[len(BuiltinAgents):]; files[0].Name != "helper" || files[0].Source != "project" {
		t.Fatalf("project agent first: %+v", files[0])
	} else if files[1].Name != "reviewer" || files[1].Model != "claude" {
		t.Fatalf("global agent = %+v", files[1])
	}
	path := filepath.Join(agentDir, "agents", "new.md")
	if err := SaveAgent(path, "---\nname: new\n---\nPrompt\n"); err != nil {
		t.Fatal(err)
	}
	if body, _ := ReadAgent(path); !strings.Contains(body, "Prompt") {
		t.Fatalf("saved content = %q", body)
	}
	if err := DeleteAgent(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("agent file still exists")
	}
}
