package agents

import _ "embed"

//go:embed kitsoki_engineer.md
var kitsokiEngineerPrompt string

// kitsokiEngineer returns the builtin kitsoki-engineer Agent: edits Go
// code in the kitsoki repo, runs tests, and (with user direction)
// opens PRs. Surfaced through the builtin `self` meta mode.
//
// Tool surface (informational — every claude subprocess currently
// runs with --permission-mode bypassPermissions, so the list
// documents the agent's intended toolset for prompt authors and
// code reviewers rather than acting as a runtime gate). The names
// are in claude-CLI form; if a future iteration wires real gating
// they pass straight through to `--allowed-tools`.
//
// DefaultCwd uses the `${KITSOKI_REPO}` env var; the metamode adapter
// runs os.ExpandEnv on cwd values, so an unset var resolves to the
// empty string and the harness will reject the call rather than run
// the engineer in a random directory.
func kitsokiEngineer() Agent {
	return Agent{
		Name:         NameKitsokiEngineer,
		SystemPrompt: kitsokiEngineerPrompt,
		Model:        "",
		Tools: []string{
			"Read",
			"Write",
			"Edit",
			"Bash",
			"Glob",
			"Grep",
		},
		DefaultCwd: "${KITSOKI_REPO}",
	}
}
