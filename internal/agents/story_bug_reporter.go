package agents

import _ "embed"

//go:embed story_bug_reporter.md
var storyBugReporterPrompt string

// storyBugReporter returns the builtin story-bug-reporter Agent: files
// a bug against the running story (the kitsoki app the user is
// playing) by invoking `kitsoki bug create --target story`. Surfaced
// through the builtin `story.bug` meta mode.
//
// Tool surface (informational — every claude subprocess currently
// runs with --permission-mode bypassPermissions, so this list
// documents intent for prompt authors and code reviewers rather
// than acting as a runtime gate):
//
//   - Read + Grep + Glob — the agent's prompt directs it to read the
//     [context]-supplied `trace_file` to reconstruct what happened
//     instead of interrogating the user about it, and to scan the
//     app's `issues/bugs/` directory for likely duplicates before
//     filing. Glob is needed to enumerate that directory; Grep is
//     needed to search inside existing bug bodies for matching
//     symptoms.
//   - Bash(kitsoki bug create*) — the actual filing step. Pattern
//     form documents the only Bash invocation the agent should
//     produce; the prompt teaches it the exact command (with
//     `--target story`) explicitly.
//
// No Edit/Write — bug filers are read-only on the filesystem (by
// agreement, not enforcement); the only side effect is the markdown
// file the CLI subcommand writes.
//
// The "host.bugs.create" abstraction from the meta-mode proposal §1
// is realised as this CLI subcommand rather than a kitsoki-internal
// MCP tool — same observable behaviour, far smaller surface to
// wire up.
//
// No DefaultCwd — story bugs are filed under the running app's
// directory, which is already the harness's cwd when this agent
// runs (the metamode adapter chooses cwd from the app context).
func storyBugReporter() Agent {
	return Agent{
		Name:         "story-bug-reporter",
		SystemPrompt: storyBugReporterPrompt,
		Model:        "",
		Tools: []string{
			"Read",
			"Glob",
			"Grep",
			"Bash(kitsoki bug create*)",
		},
		DefaultCwd: "",
	}
}
