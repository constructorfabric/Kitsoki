// Package baseskills ships the kitsoki agent toolkit — the project skills
// (.agents/skills) and shared subagents (.agents/agents) — embedded in the
// binary so `kitsoki project-tools install` can install them into a freshly
// onboarded project that has no kitsoki checkout on disk.
//
// The model mirrors how the kitsoki repo itself is laid out (see the repo
// AGENTS.md): .agents/skills/<name>/SKILL.md and .agents/agents/<name>.md are
// the Codex-standard source of truth, and Claude Code discovers them through
// relative symlinks under .claude/skills/<name> and .claude/agents/<name>.md.
// [Install] reproduces exactly that shape in a target project: it materializes
// the embedded toolkit, copies the source trees into the target's .agents/, and
// links them into .claude/. It also registers the kitsoki studio MCP server in
// the target's .mcp.json so an attached coding agent can drive kitsoki there.
//
// The embed/materialize machinery is the same content-addressed, staged-copy
// pattern as internal/basestories: `make embed-skills` stages .agents/{skills,
// agents} into this package's assets/ subdir (gitignored), and [Materialize]
// extracts a content-hashed copy to the user cache. An un-staged binary (only
// the committed .gitkeep placeholder embedded) reports [ErrNotStaged] rather
// than failing to compile. Like basestories, this package's own mechanism
// stays embed + local-override, never a fetcher; internal/kitgit is where
// S2 added the first real (git) fetcher, as a resolveImportSource tier
// alongside — not replacing — this one.
package baseskills

import "errors"

// ErrNotStaged is returned by [Materialize]/[Install] when the agent toolkit
// has not been staged into this binary (only the .gitkeep placeholder is
// embedded). Run `make embed-skills` before building.
var ErrNotStaged = errors.New("kitsoki agent toolkit not staged into this binary: run `make embed-skills` (copies .agents/{skills,agents} into the embed dir) before building")
