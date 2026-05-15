package agents

import _ "embed"

//go:embed kitsoki_bug_reporter.md
var kitsokiBugReporterPrompt string

// kitsokiBugReporter returns the builtin kitsoki-bug-reporter Agent:
// files a bug against kitsoki itself (not the running story) by
// invoking `kitsoki bug create --target kitsoki`. Surfaced through
// the builtin `kitsoki.bug` meta mode.
//
// Tool surface (informational — every claude subprocess currently
// runs with --permission-mode bypassPermissions, so this list
// documents intent for prompt authors and code reviewers rather
// than acting as a runtime gate):
//
//   - Read + Grep + Glob — the agent's prompt directs it to read the
//     [context]-supplied `trace_file` to reconstruct what happened
//     (including any stack trace) and to scan
//     `${KITSOKI_REPO}/issues/bugs/` for likely duplicates before
//     filing. Glob enumerates that directory; Grep searches inside
//     existing bug bodies for matching symptoms.
//   - Bash(kitsoki bug create*) — the actual filing step. Pattern
//     form documents the only Bash invocation the agent should
//     produce; the prompt teaches it the exact command (with
//     `--target kitsoki`) explicitly.
//
// No Edit/Write — bug filers are read-only on the filesystem (by
// agreement, not enforcement); the only side effect is the markdown
// file the CLI subcommand writes.
//
// DefaultCwd uses the `${KITSOKI_REPO}` env var; the metamode adapter
// runs os.ExpandEnv on cwd values, so an unset var resolves to the
// empty string and the harness will reject the call rather than run
// the reporter in a random directory. Filing from the kitsoki repo
// also lets the CLI auto-fill `kitsoki_rev` from `git rev-parse`.
func kitsokiBugReporter() Agent {
	return Agent{
		Name:         "kitsoki-bug-reporter",
		SystemPrompt: kitsokiBugReporterPrompt,
		Model:        "",
		Tools: []string{
			"Read",
			"Glob",
			"Grep",
			"Bash(kitsoki bug create*)",
		},
		DefaultCwd: "${KITSOKI_REPO}",
	}
}
