package agents

import _ "embed"

//go:embed story_explainer.md
var storyExplainerPrompt string

// storyExplainer returns the builtin story-explainer Agent: the
// read-only Q&A sibling of story-author. Same domain knowledge
// (story YAML structure, state machine, intents, prompts, scripts)
// but the prompt is framed as "explain what you see; do not propose
// changes; do not edit anything." Surfaced through the builtin
// `story.ask` meta mode.
//
// Tool surface (informational — every claude subprocess currently
// runs with --permission-mode bypassPermissions, so this list
// documents intent for prompt authors and code reviewers rather
// than acting as a runtime gate):
//
//   - Read + Glob + Grep — the agent explores the story tree to
//     answer questions about it. No Edit, no Write, no Bash, no
//     host.* tool — the prompt's refusal pattern points the user at
//     `/meta story edit` when they ask for a change. Per the bug
//     format proposal §1.1, the `ask` verb is the read-only sibling
//     of `edit`; its surface is exactly these three exploration
//     tools so the agent has no means to mutate the tree.
//
// No DefaultCwd — the metamode adapter pins cwd to the running
// app's directory the same way it does for story-author.
func storyExplainer() Agent {
	return Agent{
		Name:         "story-explainer",
		SystemPrompt: storyExplainerPrompt,
		Model:        "",
		Tools: []string{
			"Read",
			"Glob",
			"Grep",
		},
		DefaultCwd: "",
	}
}
