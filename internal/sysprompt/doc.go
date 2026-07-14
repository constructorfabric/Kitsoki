// Package sysprompt composes the system prompt for every claude invocation
// kitsoki makes — the intent-routing harness and all host.agent.* verbs alike —
// from three cache-ordered layers:
//
//	┌─ Layer 1 — KITSOKI (project-visible, stable) ───────────────────── most stable
//	│  .kitsoki/system-prompt.md when present; templates/kitsoki.md fallback.
//	├─ Layer 2 — PROJECT (per-app, authored) ──────────────────────────
//	│  app.context / context_path / prompts/_project.md, rendered by the caller.
//	├─ Layer 3 — TASK (per-call) ──────────────────────────────────────  least stable
//	│  the verb contract + the agent persona (+ routing's Intent Library).
//	└──────────────────────────────────────────────────────────────────
//	        ⇩ passed as claude --system-prompt (REPLACES Claude Code's default)
//
// Why layered + ordered most-stable → least-stable: per-turn data (state, view,
// world, the user utterance) never enters the system prompt — it rides on stdin
// as the user message — so the composed prefix is byte-identical across every
// call a given agent/verb makes in a project and lands in Anthropic's prompt
// cache. Layer 1 (project-global) before Layer 2 (per-app) before Layer 3
// (per-agent) maximizes the shared cached prefix.
//
// # Boundaries
//
// This package is a pure, dependency-free leaf: it owns the embedded fallback
// Layer-1 fragment, the per-verb contract text, the join order, and the
// dynamic-sections policy, and does nothing else. It does NOT read files, render
// templates, reach the network, or read the clock — Compose is a pure function
// of its Spec. The caller (host / harness) resolves the project-visible Kitsoki
// layer, the Project layer, and the Task layer bodies through the app's existing
// prompt renderer (so `{% include "@shared/…" %}` and `{% extends %}` work) and
// passes the resolved strings in. That split is what keeps Compose exhaustively
// table-testable.
//
// # Replace, not append
//
// All callers pass the composed prompt via `--system-prompt`, which REPLACES
// Claude Code's default coding-agent system prompt rather than stacking under it
// (the older agent path used `--append-system-prompt`, so judges inherited the
// full Claude Code default). Composed.ExcludeDynamic carries the per-verb policy
// for `--exclude-dynamic-system-prompt-sections`: every verb excludes Claude
// Code's cwd/git/env/memory sections except [Task], whose agentic repo work
// legitimately wants them.
//
// See docs/architecture/system-prompt.md for the full model and cache rationale.
package sysprompt
