package resources

import "strings"

// BuiltinAgent is one of pi's own subagent profiles. They live in the SDK,
// not in a file: the app lists them so the Agents page shows what the Agent
// tool can already start, and their switch writes pi's disabledBuiltIns
// instead of copying a file over them.
type BuiltinAgent struct {
	Name, Display, Description, Prompt string
	Tools                              []string
}

// BuiltinAgents is pi's three profiles, in the order the panel shows them.
// The prompts and tool sets are pi's (pi-web's lib/subagents.ts): the app
// must not change what a subagent is allowed to do.
var BuiltinAgents = []BuiltinAgent{
	{
		Name:        "general-purpose",
		Display:     "General purpose",
		Description: "Handle a focused implementation or investigation task",
		Prompt:      "Work autonomously on the delegated task. Keep the final answer concise and include important files, decisions, and remaining risks.",
		Tools:       []string{"read", "bash", "edit", "write", "grep", "find", "ls"},
	},
	{
		Name:        "explore",
		Display:     "Explore",
		Description: "Quickly inspect a codebase without modifying it",
		Prompt:      "Explore the codebase to answer the delegated question. Do not modify files. Report concrete findings with file paths and relevant symbols.",
		Tools:       []string{"read", "grep", "find", "ls"},
	},
	{
		Name:        "plan",
		Display:     "Plan",
		Description: "Design an implementation plan without modifying files",
		Prompt:      "Produce an implementation-ready plan for the delegated task. Inspect the repository as needed, do not modify files, and call out dependencies, risks, and verification steps.",
		Tools:       []string{"read", "grep", "find", "ls"},
	},
}

// Builtin returns pi's profile with this name, in any case.
func Builtin(name string) (BuiltinAgent, bool) {
	for _, a := range BuiltinAgents {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return BuiltinAgent{}, false
}
