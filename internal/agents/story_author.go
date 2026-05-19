package agents

import _ "embed"

//go:embed story_author.md
var storyAuthorPrompt string

// storyAuthor returns the builtin story-author Agent: a conversational
// YAML/story editor that uses claude-cli's built-in Read/Edit/Write
// tools directly. The post-turn commit step (see
// internal/host/meta_commit.go) wraps every edit in a git commit
// (amending HEAD across same-chat turns), so the agent doesn't need
// to think about version control.
func storyAuthor() Agent {
	return Agent{
		Name:         "story-author",
		SystemPrompt: storyAuthorPrompt,
		Model:        "",
		// Tools list is informational — every claude subprocess runs
		// with --permission-mode bypassPermissions so the agent has
		// access to the full builtin tool surface anyway. Listing the
		// edit-side tools explicitly documents intent for prompts and
		// human reviewers.
		Tools:      []string{"Read", "Edit", "Write", "Bash", "Grep", "Glob"},
		DefaultCwd: "",
	}
}
