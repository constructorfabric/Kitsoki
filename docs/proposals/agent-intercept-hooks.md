# Tooling: agent intercept hooks — wiring the gate into Claude / Codex / Copilot

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tooling
**Epic:**   ../pre-llm-intercept.md

## Why

Slice #1 ([`intercept-engine.md`](intercept-engine.md)) gives kitsoki a callable,
no-LLM gate (`kitsoki intercept`). Nothing wires it into the coding agents. Each
agent exposes a *different* pre-model interception contract — and two of the three
expose **none** — so this slice is the thin glue plus an installer plus the repo
binding, and, critically, an **honest capability matrix**. The temptation is to
imply "a hook for claude/codex/copilot"; the reality is one full path and two
degraded ones, and the proposal has to say so.

## What changes

- A `kitsoki hook install [--agent claude|codex|copilot]` subcommand that writes
  (idempotently, with a dry-run diff) the per-agent configuration.
- A **Claude Code** `UserPromptSubmit` shim that calls `kitsoki intercept` and, on
  a match, **blocks** the prompt and surfaces kitsoki's result — the agent's LLM is
  never invoked for that turn; on pass-through it exits silently and the prompt
  proceeds.
- The `intercept:` block on `webconfig.WebConfig` (`internal/webconfig/webconfig.go:55`),
  the per-repo binding the engine reads.
- Honest **degraded** paths for Codex/Copilot, which have no pre-model hook.

> One sentence: **make a recognized command never reach the agent's LLM where the
> host supports pre-model interception (Claude Code), and degrade honestly where
> it doesn't (Codex/Copilot).**

## The capability matrix (the heart of this slice)

| Agent | Pre-model hook | Bypass the LLM? | Show a result? | Our path |
|---|---|---|---|---|
| **Claude Code** | `UserPromptSubmit` | **yes** — `decision:"block"` (or exit 2) erases the prompt; no assistant turn | **yes** — as the **block `reason`** | **Full.** Shim blocks + surfaces `result_text`. |
| **Codex CLI** | none (only `PreToolUse`/`PostToolUse`, tool-level, post-reasoning) | no | n/a | **Degraded.** Explicit user-invoked `k <cmd>` alias → `kitsoki intercept`. Track [openai/codex#11870](https://github.com/openai/codex/issues/11870) (`BeforeModel`). |
| **GitHub Copilot** | `userPromptSubmitted` (**observe-only**) + `preToolUse` (tool-level) | no | n/a | **Degraded.** Observe-only logging; explicit `k <cmd>`; optional `kitsoki serve` MCP tool — **flagged model-in-the-loop, not a bypass.** |

The ceiling is the same insight as [operator-ask](../architecture/operator-ask.md),
inverted: *"a `PreToolUse`/pre-prompt hook can only allow/deny — it cannot supply a
result."* For operator-ask that ruled hooks **out** (answering needs a tool_result,
so it used an MCP tool). Here allow/deny is **exactly enough** — we *want* to deny
(block) the turn — and we accept that the result rides along as the block `reason`,
not as a synthetic assistant message. No agent supports faking the assistant's
voice pre-model, and we do not pretend otherwise (epic decision #2).

## The Claude Code shim contract

`UserPromptSubmit` delivers `{prompt, session_id, cwd, …}` on stdin. The shim:

```
1. read stdin JSON; resolve the binding from `.kitsoki.yaml` in `cwd`
2. if no `intercept:` block or intercept.enabled=false → exit 0 silently (prompt proceeds)
3. run: kitsoki intercept --session <session_id> --input <prompt>   (cwd = <cwd>)
4. exit 0  (matched + executed) → print {"decision":"block",
              "reason":"⌁ kitsoki handled this (no LLM):\n<result_text>"} ; exit 0
   exit 10 (pass-through)        → exit 0 silently  (prompt proceeds to the LLM)
   anything else / timeout       → exit 0 silently  (FAIL-OPEN — never wedge the agent)
```

Non-negotiables (principle of least surprise):

- **Fail-open.** A crashed, slow, or misconfigured interceptor must let the prompt
  through — the agent always gets the turn if kitsoki can't *confidently* handle
  it. The shim never blocks on error, only on a clean match.
- **Marked.** The block reason is prefixed (`⌁ kitsoki handled this (no LLM)`) so
  the user knows the agent did **not** answer — kitsoki did.
- **Escapable.** An `escape_prefix` (configurable; default off) forces a turn to
  the agent despite a match, so the user is never trapped on a phrasing the gate
  claims.
- **Fast.** The common non-command path is exit 10 from the lexical tiers
  (microseconds, epic OQ #3); the shim adds only process spawn.

## The binding — `.kitsoki.yaml intercept:`

Extends `webconfig.WebConfig` (the declared "stable extension point for
machine-global keys", `webconfig.go:55`); `.kitsoki.local.yaml` can override or
disable it per developer (same dichotomy as Claude Code's settings.local.json,
`webconfig.go:9`).

```yaml
intercept:
  enabled: true
  app: stories/dev-commands/app.yaml   # the bound app
  room: commands                        # the room to gate against
  confidence_bar: 0.90                  # synonym floor; deterministic (1.00) always wins (epic decision #1)
  tiers: [deterministic, synonym]       # no-LLM tiers only; add `embedding` to opt into recall-over-latency
  escape_prefix: "//"                   # optional: force pass-through even on a match
```

## Degraded paths (Codex / Copilot)

Neither has a pre-model hook, so a true bypass is impossible today. We offer two
honest fallbacks and **do not** auto-install either:

- **Explicit invoke.** A shell alias `k() { kitsoki intercept --input "$*"; }` (or
  a `/`-prefixed convention) lets the user run a known command deterministically
  *on purpose* — the value (deterministic + recorded + no tokens) survives even
  without auto-interception.
- **MCP tool (model-in-the-loop).** `kitsoki serve` already exposes a `transition`
  tool (`internal/mcp/server.go`); pointing an agent at the interceptor room
  through it lets the *model choose* to route a command there. This is **not** a
  bypass — the agent's LLM still ran to make that choice — and the matrix labels it
  as such so no one mistakes it for the Claude path.

We track the upstream requests (Codex `BeforeModel`, Copilot pre-prompt control);
the Claude shim is the reference, and a Codex/Copilot shim is a near-drop-in the
day they ship a pre-model hook.

## Synergy

With [`kitsoki-as-dependency.md`](kitsoki-as-dependency.md) slice #1 (the whole
`stories/` library embedded in the binary), the interceptor room can ship with
**just the binary**: a foreign repo gets command interception by adding a
`.kitsoki.yaml intercept:` block (pointing at `@kitsoki/<name>`) and running
`kitsoki hook install` — no story-source checkout. This slice does not depend on
that epic, but composes cleanly with it.

## Tasks

```
## 1. Shims + binding
- [ ] 1.1 `intercept:` schema on webconfig.WebConfig (+ .local override; load-time validation)
- [ ] 1.2 Claude Code `UserPromptSubmit` shim (block+reason on match; fail-open; marked; escapable)
- [ ] 1.3 `kitsoki hook install --agent claude` — idempotent, dry-run diff, `--write`

## 2. Degraded paths
- [ ] 2.1 Document the `k <cmd>` explicit-invoke alias for Codex/Copilot
- [ ] 2.2 Document the `kitsoki serve` MCP fallback — clearly labeled model-in-the-loop, NOT a bypass
- [ ] 2.3 Link the upstream pre-model-hook feature requests; note the shim is a near-drop-in when they land

## 3. Verification (no LLM, no real agent)
- [ ] 3.1 Shim unit: feed a fake stdin JSON → assert block JSON on a fixture match, silence on pass-through
- [ ] 3.2 Shim unit: interceptor error/timeout → fail-open (prompt proceeds)
- [ ] 3.3 `kitsoki hook install` idempotency + dry-run-diff test

## 4. Document
- [ ] 4.1 `docs/architecture/prompt-intercept.md` (agent-integration half + the capability matrix) + getting-started snippet; trim this slice from the epic
```

## Verification

The shim is a script over `kitsoki intercept`; tests feed it crafted stdin JSON and
assert stdout/exit — no real agent, no LLM. The matrix is verified against the
agents' published hook docs (Claude Code hooks; Codex `PreToolUse`/`PostToolUse`;
Copilot `userPromptSubmitted`/`preToolUse`), cited inline, and re-checked when those
surfaces change. A live end-to-end run inside a real Claude Code session is gated
and only on explicit request (it would exercise a real agent; the deterministic path
itself costs nothing).

## Open questions

1. **Installer scope** — write `settings.json` directly vs print-and-paste. *Lean:
   dry-run diff by default, `--write` to apply; idempotent.*
2. **Escape mechanism** — literal `escape_prefix` vs a slash convention vs
   config-only. *Lean: configurable `escape_prefix`, default off (a match is rare
   and high-confidence; offer the escape, don't impose one).*
3. **Offer the MCP fallback at all?** It is model-in-the-loop, so it is not the
   feature's headline. *Lean: document it as a clearly-labeled fallback; never
   auto-install it.*

## Non-goals

- The intercept engine itself — slice #1.
- Building pre-model hooks into Codex/Copilot — out of our control.
- Injecting a synthetic assistant turn — no agent supports it; we surface a
  marked block reason, not the agent's voice (epic decision #2).
