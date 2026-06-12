# Epic: dynamic model & provider/harness control

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 3 (0/3 shipped) + a demo deliverable (§Demo deliverable)

## Why

Kitsoki already forks several coding-agent CLIs and retargets their
endpoints, but **every one of those choices is frozen at session startup
and only reachable through CLI flags / YAML / env**. The operator who is
running a session in the TUI or web UI cannot say "answer the next turn
with codex instead of claude," "point claude at my synthetic.new
subscription," or "use the local llama.cpp model for this." Worse, the
machinery to express each of those exists but is split across **four
orthogonal axes** an end user should never have to learn:

- **backend** — which CLI is forked (`--oracle claude|copilot|codex`),
  session-global, flag/env only
  ([`docs/architecture/oracle-backends.md`](../architecture/oracle-backends.md)).
- **provider** — env overrides that retarget the `claude` subprocess's
  endpoint (`ANTHROPIC_BASE_URL` …), per-invocation but **YAML-only** and
  today a **no-op under codex/copilot**
  ([`docs/architecture/oracle-providers.md`](../architecture/oracle-providers.md)).
- **plugin** — which component answers (`builtin.claude_cli`,
  `builtin.local_llm` llama.cpp, `mcp_http` …), load-time YAML
  ([`docs/architecture/oracle-plugin.md`](../architecture/oracle-plugin.md)).
- **model** — `--model`, set per agent/effect/provider
  (`internal/app/types.go:799`).

The user's mental model is just **two** knobs — *which model* and *which
provider/harness* — and they want them live, from `/model` and `/provider`
(TUI) and equivalent web controls. This epic introduces a single
**harness profile** abstraction that bundles the four axes behind one
named, operator-selectable thing, makes the active selection mutable at
runtime (next-turn semantics), and surfaces it on both operator surfaces.

## What changes

Once every slice has shipped:

- An operator declares **harness profiles** once in `~/.kitsoki.yaml` /
  project `.kitsoki.yaml` — each a named bundle of `{backend, env, model,
  plugin}`. Examples ship in docs: `claude-native`, `synthetic-claude`,
  `synthetic-codex`, `codex-native`, `llama-local`.
- Every session carries a **mutable active selection** (`profile` +
  optional `model` override) guarded by a lock. `/provider <name>` (or the
  web picker) swaps the profile; `/model <id>` picks among the models the
  active profile advertises; raw-axis overrides (`/provider backend=codex`)
  are available for power users. Changes take effect on the **next** turn /
  oracle dispatch — in-flight calls finish on the old selection.
- Provider `env` is applied to **whichever backend CLI is forked**, not
  just `claude` — so "synthetic.new on codex" works, closing today's
  no-op-under-codex gap.
- The active selection is shown in both UIs and **recorded in the trace**
  on every oracle call, so a transcript says exactly which profile/model
  produced each answer.

## Impact

- **Spans:** runtime (substrate), tui (slash commands), web (RPC + control);
  tracing is consumed, not newly invented (the existing
  `OracleCalledPayload.Model` field at `internal/host/oracle_event_sink.go:70`
  gains a sibling profile field).
- **Net surface:** one new config block (`harness_profiles:` in
  `.kitsoki.yaml`), one new session-scoped mutable selection + its
  driver/orchestrator API, two TUI slash commands, two web RPC methods + a
  header control, a one-line trace field.
- **Docs on ship:** a new `docs/architecture/harness-profiles.md` (the
  profile model + precedence), additions to
  `docs/architecture/oracle-providers.md` (env-under-all-backends),
  `docs/tui/README.md` (the two commands), and the web surface doc.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | harness-profiles | runtime | `harness_profiles:` config + mutable session selection + next-turn rebuild + provider-env under every backend + trace field | — | Draft | [`harness-profiles.md`](harness-profiles.md) |
| 2 | model-provider-commands | tui | `/model` and `/provider` slash commands (list + select + raw override) over slice 1's API | 1 | Draft | [`model-provider-commands.md`](model-provider-commands.md) |
| 3 | web-harness-control | web (tui-shaped) | RPC `runstatus.session.set_selection` + header profile/model picker, parity with #2 | 1 | Draft | [`web-harness-control.md`](web-harness-control.md) |

## Sequencing

```
#1 (runtime substrate) ─┬─▶ #2 (tui slash commands) ─┐
                        └─▶ #3 (web control)         ─┴─▶ Demo deliverable (tour video + QA)
```

Slice 1 is the substrate both surfaces consume; #2 and #3 can proceed in
parallel once #1 exposes its driver/orchestrator API. The **demo
deliverable** is the epic's acceptance gate and runs last, exercising both
surfaces end-to-end.

## Shared decisions

1. **Two operator knobs, four internal axes.** `/model` and `/provider`
   are the only concepts users see; the backend/provider/plugin/model
   mapping is an implementation detail collapsed into the *profile*. Raw
   axes are reachable (`/provider backend=…`) but secondary — curated
   profiles are the headline (per the design Q&A: "both").
2. **Next-turn switching, never mid-flight.** A selection change rebuilds
   the harness lazily on the next dispatch; in-flight oracle calls are
   never torn down. This keeps slice 1 free of cancellation/race work.
3. **Profiles live in `.kitsoki.yaml` (global/machine).** Subscription
   tokens and binary paths are machine-specific and story-agnostic. The
   config loader is `internal/webconfig` (`webconfig.go:40`), extended with
   `harness_profiles:` (a story-level override is a future non-goal — see
   below).
4. **Profile = `{backend, env, model, models[], plugin}`.** One schema,
   reused verbatim by both surfaces. `env` carries `${VAR}` interpolation
   (same contract as `providers:` /  `oracle_plugins:`) so tokens stay out
   of the file.
5. **Secrets never echoed.** A profile's `env` values are never rendered in
   the TUI/web picker or the trace — only the profile name, backend, and
   model are shown.

## Cross-cutting open questions

1. **Per-session vs per-process selection.** Does a profile switch apply to
   the one focused session, or all live sessions on a `kitsoki web` server?
   *Lean: per-session* (the active selection lives on the session entry in
   `internal/runstatus/server`), with the `.kitsoki.yaml` `default_profile`
   as the new-session default.
2. **What does `/model` list when the profile is a backend without a model
   catalog (codex-native, copilot)?** Those CLIs own their model choice.
   *Lean:* `/model` shows the profile's declared `models:` if present, else
   a single "(backend default)" row and a note that the model is configured
   in that CLI's own config.

## Non-goals

- **Mid-flight cancellation / re-dispatch** of an in-progress oracle call
  (decision 2). A switch affects subsequent turns only.
- **Per-story profile declarations / story-level overrides.** Profiles are
  global-only in v1 (decision 3); a story-level merge is a later proposal if
  demand appears.
- **New backends or plugins.** This epic *selects among* what exists
  (claude/copilot/codex backends, `local_llm` plugin); adding a native
  synthetic.new backend or new plugin is separate. synthetic.new is reached
  via an existing backend + profile `env` retarget, not new code.
- **Cost/billing UI.** Surfacing per-profile cost is out of scope (codex and
  copilot report no dollar cost anyway — see oracle-backends.md).

## Demo deliverable

The epic's acceptance gate, per the request: an **adversarially reviewed,
tour-driven demo video** of the feature on the web UI.

- Author the tour with the **`kitsoki-ui-demo`** skill: a deterministic,
  no-LLM Playwright drive of a real `kitsoki web` server that opens the
  profile picker, switches `claude-native → synthetic-claude →
  codex-native → llama-local`, picks a model, and shows the active
  selection reflected in the transcript header and the per-call trace
  field. Output: per-scene screenshots + a shareable MP4/GIF + contact
  sheet under `.artifacts/`.
- Gate it with the **`kitsoki-ui-qa`** skill: a read-only vision agent
  judges each scenario ("profile actually switched", "model picker lists
  the profile's models", "trace shows the chosen profile") against cited
  frames, adversarially re-checks every pass, and emits a gated
  `qa-report.md` + `verdict.json`. The epic is not done until the verdict
  passes.

```
## Epic acceptance
- [ ] All three slices shipped, their detail migrated to docs/, child files deleted.
- [ ] `kitsoki-ui-demo` tour recorded against a real `kitsoki web` server (deterministic, no LLM).
- [ ] `kitsoki-ui-qa` verdict.json = pass on every scenario, adversarially re-checked.
- [ ] This epic file deleted; the slice table is empty.
```

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's plan, then delete the child file.
  When every slice has shipped AND the demo deliverable passes QA, delete
  this epic too.
-->
