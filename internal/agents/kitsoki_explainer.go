package agents

import _ "embed"

//go:embed kitsoki_explainer.md
var kitsokiExplainerPrompt string

// kitsokiExplainer returns the builtin kitsoki-explainer Agent: the
// read-only Q&A sibling of kitsoki-engineer. Same domain (the kitsoki
// Go source tree) but the prompt is framed as "explain what you see;
// do not propose changes; do not edit anything." Surfaced through the
// builtin `kitsoki.ask` meta mode.
//
// Tool surface (informational — every claude subprocess currently
// runs with --permission-mode bypassPermissions, so this list
// documents intent for prompt authors and code reviewers rather
// than acting as a runtime gate):
//
//   - Read + Glob + Grep — the agent explores the kitsoki repo to
//     answer questions about it. No Edit, no Write, no Bash — the
//     prompt's refusal pattern points the user at `/meta kitsoki
//     edit` when they ask for a change. Per the bug format proposal
//     §1.1, the `ask` verb is the read-only sibling of `edit`; its
//     surface is exactly these three exploration tools so the agent
//     has no means to mutate source or run tests.
//
// DefaultCwd uses the `${KITSOKI_REPO}` env var; the metamode adapter
// runs os.ExpandEnv on cwd values, so an unset var resolves to the
// empty string and the harness will reject the call rather than run
// the explainer in a random directory.
func kitsokiExplainer() Agent {
	return Agent{
		Name:         "kitsoki-explainer",
		SystemPrompt: kitsokiExplainerPrompt,
		Model:        "",
		Tools: []string{
			"Read",
			"Glob",
			"Grep",
		},
		DefaultCwd: "${KITSOKI_REPO}",
	}
}
