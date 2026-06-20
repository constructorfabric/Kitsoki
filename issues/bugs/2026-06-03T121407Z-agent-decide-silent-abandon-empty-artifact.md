---
id: 2026-06-03T121407Z-agent-decide-silent-abandon-empty-artifact
title: "agent.decide with no submit routes to success with an empty artifact instead of failing visibly"
target: kitsoki
filed_at: 2026-06-03T12:14:07Z
status: open
severity: P1
component: runtime
kitsoki_rev: 153c3d6
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-03T121407Z-agent-decide-silent-abandon-empty-artifact.md"
---

## Body

Filed from a chat-history mining pass (see
`.context/kitsoki-dev-ideas-from-chats.md`, theme #4 — recurred across 5
sessions; the user called decide-validation "fundamentally broken"). This
hits the moat directly: an interpretive decision that *silently succeeds with
no payload* undermines the deterministic-verdict guarantee. No proposal owns
it — `agent-off-ramp.md` is adjacent but solves a different problem (no-*match*
→ converse, not empty-*submit* → success).

When the LLM behind a `host.agent.decide` answers in prose (or returns an
empty `{}`) without calling the `submit` tool, the engine transitions to the
success arc carrying an **empty artifact** rather than routing to a failure
state. Stories paper over this with hand-rolled `when:` guards per room; the
fix belongs in the engine.

### Steps to reproduce

1. Enter any `decide`-bearing room (bugfix / code-review / cypilot /
   pr-refinement / docs-review).
2. Have the operator/LLM produce a prose answer (or empty `{}`) without the
   `submit` tool being called.
3. Observe the gate take the *success* transition with an empty artifact body
   (and, where the artifact is templated from world, a literal
   `<map[...]Value>` rendered into the file).

### Expected vs actual

**Expected:** an un-submitted / empty decide routes to a dedicated
`*_failed` review state; the operator sees a friendly clarification; artifact
bodies are validated non-empty before the success arc fires.

**Actual:** silent success, empty artifact, and a downstream `on_error: idle`
bounce when the empty artifact later breaks something.

### Proposed fix sketch

- Treat "no `submit` call" / empty `{}` as a **failure**, routing to a
  dedicated `*_failed` state — don't hand-roll `when:` guards per story.
- When the LLM answers in prose without calling `submit`, render a friendly
  `ClarifyResponse` / `LLM_CLARIFICATION` ("call the `submit` tool — here is a
  concrete JSON example; free-text is discarded"), not a raw harness error.
  Document the `decideDefaultMaxRetries` bounded-nudge loop.
- Add a **pure-JSON artifact mode** (a `.json` filename / `json` filter) so a
  world map templated into an artifact body doesn't render as
  `<map[...]Value>`.
- Close the **artifact round-trip test gap**: assert real `artifacts_dir`
  body content across bugfix / code-review / cypilot / pr-refinement /
  docs-review flow fixtures.
- Isolate agent subagents from operator-global Claude plugins
  (`--setting-sources project,local`) so e.g. a BMAD plugin can't hijack the
  interviewer.

### Severity rationale

P1 — silently corrupts the interpretive-decision contract that the whole
architecture rests on, and produces empty/garbage artifacts that cascade into
later failures. Workaround (per-room `when:` guards) exists but is exactly the
ad-hoc pattern the engine is supposed to eliminate.

### Files involved

- `internal/orchestrator/` — agent `decide` result handling / gate routing
  on empty submit.
- `internal/testrunner/flows.go` — artifact round-trip assertions.
- `docs/stories/state-machine.md` / `docs/stories/prompts.md` — document the
  `submit`-required contract and the `*_failed` arc convention.
