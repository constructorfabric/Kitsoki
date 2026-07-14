# prd — PRD-authoring operator story

A reusable kitsoki story that turns a free-form idea (plus any existing
upstream requirement docs) into a PRD markdown document. It is a
pipeline-shaped operator story, structurally identical to
[`stories/bugfix/`](../bugfix/) and [`stories/dev-story/`](../dev-story/);
the novelty is the **document** artifact, a **conversational idea
discovery** intake, a **prior-art scout gate** that mints a per-PRD
workspace, a **multi-round clarification** step whose transcript
accumulates across rounds, and two **confirm-gated review rooms** (the
formalized `brief` and a curated doc `references` list) that the operator
signs off on before the PRD is written.

Every per-PRD artifact lands under a **slug-named workspace** —
`{{ workdir }}/.artifacts/prd/<slug>/` — as numbered files
(`001-brief.md`, `003-references.md`, `004-prd.md`), the same multi-file
workspace shape the `dev-story` proposal pipeline uses. On `accept` the
draft is **published** out of the gitignored workspace to its durable home
(`docs/prd/<slug>.md` by default, collision-safe). This mirrors
`dev-story`'s `design_workspace.star` / `publish_design.star` sandwich:
`scripts/prd_slug.star` mints + uniquifies the slug, `scripts/prd_publish.star`
moves the accepted draft.

No engine / widget changes — everything composes existing mechanisms (a
conversational chat room, `host.agent.*`, `host.artifacts_dir`,
`host.starlark.run` for the deterministic slug/publish glue,
the cycle-budget refine loop, named agents, tracing).

## Using it

### Launch

```sh
# Standalone, fast/cheap model (default haiku), against the current dir.
kitsoki run stories/prd/app.yaml

# Author a real PRD with a higher-quality model, rooted in another repo so
# the agents read that tree's docs and write the PRD there.
kitsoki run stories/prd/app.yaml --warp scenarios/hyperspot.yaml --claude-model opus

# Resume the session you were last in (the discovery chat persists).
kitsoki run stories/prd/app.yaml --continue
```

- **Harness** auto-selects the local `claude` binary when it's on `PATH`
  (no API key, no per-call cost); otherwise set `ANTHROPIC_API_KEY`. Pass
  `--claude-model opus` for the drafting-quality you'd want on a real PRD —
  the default `haiku` is for cheap walkthroughs.
- **Where it runs / writes** — everything is relative to `world.workdir`
  (default `.`). To author against another repo without `cd`-ing, use a
  warp basis (see `scenarios/hyperspot.yaml`) or set `workdir` /
  `upstream_paths` there. Seed `upstream_paths` (space/comma-separated
  files or dirs) so the analyst and author read your existing requirement
  docs without you having to mention them in chat.
- **Sessions + traces** persist automatically under the nearest
  `.kitsoki/sessions/`; `--db` overrides the session DB. Pretty-print a run
  with `kitsoki trace <path>`, or watch it live with `kitsoki status`.

### Walkthrough — what you type at each step

The session opens **parked at `idle`**. The flow is a six-room pipeline;
you drive it with free text (an LLM maps what you type to the room's
intents — you don't memorize commands):

1. **`idle` — talk the idea through.** Just describe what you want to
   build; the `interviewer` asks follow-ups and you reply, conversational,
   for as long as you like. When the problem/users/scope feel covered, type
   **`ready`** (or `start`) — that distills the conversation into the
   formal idea and moves on.
2. **`search` — prior-art gate.** The `scout` checks for an existing PRD /
   requirement doc that overlaps your idea BEFORE any artifact is written.
   If it finds one it **strongly urges you to amend the existing doc** —
   select it to `change_existing` (the amend target is captured), or
   `override_new` to start fresh anyway. With no overlap, **`confirm`**
   proceeds; while overlaps remain, `confirm` is not advertised and a typed
   `confirm` renders explicit guidance. Committing (any of the valid paths)
   mints the per-PRD slug + workspace (`prd_slug.star`) that every later
   artifact writes into.
3. **`clarifying` — answer the questions.** The `analyst` posts a numbered
   list of the gaps that most change the PRD. **Just type your answers in
   plain language** — in any order, with or without naming a number. The
   room's `default_intent: answer` routes each reply (deterministically,
   before the LLM) to the `answer_matcher` agent, which maps it to the
   question(s) it actually answers and drops them from the live list. A
   single reply may resolve several questions at once. When you've covered
   enough, type **`submit`** (or `skip` to move on with what you have).
   `regenerate` re-asks; `quit` bails.
4. **`brief` — confirm the inputs.** A read-only review of the formalized
   idea + every clarification, written to `<workspace>/001-brief.md`. Pick
   **`confirm`** to proceed, **`clarify`** to ask another round *keeping*
   your answers, or **`restart from clarifying`/`idle`** to start that
   stage over.
5. **`references` — confirm the prior art.** The `researcher` searches the
   workdir's **docs** (not code) and proposes the documents the PRD should
   build on, each with the section(s) and a rationale, written to
   `<workspace>/003-references.md`. **`confirm`** to draft, **`refine "drop
   the rate-limit doc; also cite docs/auth.md §Tokens"`** to edit the list
   in place, or **`regenerate`** to search fresh.
6. **`drafting` — review the PRD.** The `author` writes the full document
   to **`<workspace>/004-prd.md`** and shows a digest. **`accept`**
   publishes it to its durable home (`docs/prd/<slug>.md`) and finishes;
   **`refine "<what to change>"`** re-drafts with the same inputs; or
   **`clarify`** if the draft revealed the inputs were incomplete (adds a
   Q&A round, then re-drafts).

**Per-PRD artifacts** land in the slug-named workspace
`{{ workdir }}/.artifacts/prd/<slug>/` — `001-brief.md`,
`003-references.md`, and the draft `004-prd.md` — as the numbered record of
the run. Each review screen shows its saved path prominently so you can
open or edit the file on disk directly. On `accept`, `004-prd.md` is
**published** out to `{{ workdir }}/docs/prd/<slug>.md` (the durable home;
`publish_durable_path` configures it), leaving the numbered checks behind
as the audit trail.

## Story graph

```
idle ─start─▶ search ─confirm─▶ clarifying ─submit_answers/skip─▶ brief ─confirm─▶ references ─confirm─▶ drafting
 │ (chat)       │ (scout →        │ (decide → questions)          │ (001-       │ (decide →           │ (task → 004-prd.md;
 │ ◀─discuss    │  overlap gate;  │ ◀─answer (free text, self)    │  brief.md)  │  doc references;    │  accept publishes to
 │              │  mints slug +   ├─regenerate ─▶ clarifying       ├─confirm     │  003-references.md) │  docs/prd/<slug>.md)
 │              │  workspace)     └─quit ─▶ @exit:abandoned        ├─restart_from├─confirm             ├─accept ─▶ @exit:done
 │              ├─change_existing ─▶ clarifying (amend target)     └─quit        ├─regenerate (budget) ├─refine ─▶ drafting (budget→abandoned)
 │              ├─override_new ─▶ clarifying                            ▲         ├─restart_from        ├─clarify ─▶ clarifying ◀─ another round
 │              ├─regenerate (re-scout) / quit                          └── clarify ──────────────────┤
 └─quit ─▶ @exit:abandoned                                                                            ├─ restart_from ─▶ idle | clarifying
                                                                                                      └─ quit ─▶ @exit:abandoned
```

`idle` is a **conversation**, not a form: the operator talks the idea
through with an `interviewer` agent (`discuss` self-loops), and `start`
distills the conversation into `world.idea` before advancing.

`search` is the **prior-art gate** (the `dev-story` `design_search`
analogue): a read-only `scout` runs BEFORE any artifact is written, so a
duplicate idea is caught here rather than after several clarify rounds.
Committing to the pipeline (`confirm` / `change_existing` / `override_new`)
runs `scripts/prd_slug.star` to mint the unique per-PRD slug + workspace; on
the amend path `prd_change_target` records the doc to edit in place.

`brief` and `references` are **confirm-gated review rooms** inserted
before the PRD is written:

- **`brief`** composes the brief *deterministically* — no LLM synthesis —
  out of what the earlier phases already produced (`world.idea` from
  `idle` + the clarification transcript from `clarifying`), writes it to
  `<workspace>/001-brief.md` via `host.artifacts_dir`, then runs a
  lightweight `brief_check` gate (`host.agent.decide`) that reads the file
  and returns `continue` / `clarify` before the operator `confirm`s.
- **`references`** runs the `researcher` (`host.agent.decide`, docs-only)
  to curate the existing documents the PRD must build on — each with the
  specific section(s) and a one-line rationale — also persisted to
  `<workspace>/003-references.md`. The confirmed list is handed to the
  author so the PRD is drafted against the cited sections. Two ways to
  re-run it: **`refine`** revises the *current* list in place (keep what
  applies; add / drop / re-scope per your instruction), while
  **`regenerate`** searches fresh. (`references_revise` carries that
  distinction into the researcher prompt.)

From both review rooms (and `drafting`) the operator can go **back to
clarifying** two ways — the distinction matters:

- **`clarify`** (non-destructive) — *keeps* the answers so far
  (`clarification_log` is preserved); only this round's working answer
  state is cleared and the analyst re-runs to **refine/combine the prior
  questions and ask only what's still missing**. Use this to tweak or
  extend the questions without losing anything.
- **`restart_from clarifying`** (destructive) — *discards* the
  clarification record and re-questions from scratch.

Six rooms (`idle`, `search`, `clarifying`, `brief`, `references`,
`drafting`), two exits (`done`, `abandoned`). `drafting` is the checkpoint
room — same shape as every `bugfix` phase.

## Contract

### Entry state

`idle` — the operator talks the idea through with the `interviewer` agent
(a conversation, not a form), then types `start` (or "ready"). Set on
import via `entry: idle`.

### Exits

| Name | Description | `requires:` keys |
|---|---|---|
| `done` | PRD accepted; `prd_artifact` is final. | `prd_artifact` |
| `abandoned` | Operator or LLM bailed (`quit`), or a cycle budget was exhausted. | (none) |

Standalone load synthesises `__exit__done` / `__exit__abandoned`
terminals so `kitsoki run` and `kitsoki test flows` terminate cleanly.

### Rooms

| Room | On enter | Checkpoint? | On `accept` / advance |
|---|---|---|---|
| `idle` (conversational) | `host.chat.resolve` (get-or-create) opens the discovery chat; `interviewer` (`host.agent.converse`) replies per `discuss` turn | no — `discuss` self-loops the conversation | `search` (via `start`, which distills the chat into `world.idea`) |
| `search` | `scout` (`host.agent.decide`) → `prd_existing_state` (read-only prior-art scan; no artifact written) | yes — operator `confirm`s / `change_existing` / `override_new` | `clarifying`; the commit arc runs `prd_slug.star` (`host.starlark.run`) to mint `prd_slug` + `prd_workspace` |
| `clarifying` | `analyst` (`host.agent.decide`) → `clarifications` | no — operator answers in free text; `default_intent: answer` → `answer_matcher` (`host.agent.decide`) maps each reply to the question(s) it answers | `brief` (via `submit_answers` or `skip`) |
| `brief` | `host.artifacts_dir` writes `<workspace>/001-brief.md` from `world.idea` + `world.clarification_log` (deterministic — no agent) | yes — operator `confirm`s the brief | `references` (via `confirm`); `clarify` re-questions keeping the record; `restart_from` discards |
| `references` | `researcher` (`host.agent.decide`, docs-only) → `references`; `host.artifacts_dir` writes `<workspace>/003-references.md` | yes — operator `confirm`s the list | `drafting` (via `confirm`); `refine` revises the list in place; `regenerate` searches fresh (both budgeted); `clarify` re-questions keeping the record |
| `drafting` | `author` (`host.agent.task`) writes `<workspace>/004-prd.md` → `prd_artifact` (reads the confirmed `references`); optional `judge` | yes — `prd_artifact` | `@exit:done` (via `accept`, which runs `prd_publish.star` to publish the draft to `docs/prd/<slug>.md`) |

### World contract

Every key is declared with a type + default in `app.yaml` so the story
loads standalone for tests. Parent stories project the intake keys via
`world_in:`.

| Key | Type | Description | Default |
|---|---|---|---|
| `idea` | string | The distilled pitch (set when the discovery chat ends). | `""` |
| `idea_chat_id` / `idea_chat_title` | string | Persistent discovery-chat thread. | `""` |
| `idea_message` / `idea_answer` | string | The operator's latest message / interviewer's latest reply. | `""` |
| `idea_session_id` | string | Claude session id, for resume across turns. | `""` |
| `idea_turns` | int | Discovery-chat turn count (view only). | `0` |
| `upstream_paths` | string | Space/comma-separated files or dirs the agents read (seed via warp, or mention in the chat). | `""` |
| `workdir` | string | Where upstream lives + the PRD is written; pins each agent's `working_dir`. | `"."` |
| `output_path` | string | Fallback PRD path, relative to `workdir`. Live runs write `<prd_workspace>/004-prd.md` instead; this default only applies to a flow that seeds `drafting` directly without minting a workspace. | `".artifacts/prd.md"` |
| `prd_slug` | string | Kebab slug minted at the `search` gate (`prd_slug.star`) — names the workspace + the published file. | `""` |
| `prd_workspace` | string | Per-PRD workspace, relative to `workdir`: `.artifacts/prd/<slug>`. Empty until the gate mints it; every numbered artifact writes under it. | `""` |
| `prd_existing_state` | object | `scout` result: `{ roadmap_fit, overlaps: [{path, summary, recommendation}] }` — drives the overlap gate. | `{}` |
| `prd_overlap_decision` | string | `new` (confirm / override_new) or `change_existing` — records the gate outcome for audit. | `""` |
| `prd_change_target` | string | On the amend path, the existing doc the author edits in place (publish reuses it rather than moving the draft). | `""` |
| `publish_durable_path` | string | Durable home the accepted PRD is published into, relative to `workdir`. | `"docs/prd"` |
| `prd_file` | string | Published path, bound by `prd_publish.star` on `accept` (the deliverable, out of the workspace). | `""` |
| `clarifications` | object | This round's `decide` result: `{ questions: [{id, question, why}] }`. | `{}` |
| `clarification_answers` | string | This round's replies, accumulated one `Qn: …` line per answered question (newest last). | `""` |
| `answered_count` | int | Questions answered this round; drives the "Answered so far (N/total)" readout. Reset on every new round. | `0` |
| `clarification_log` | string | Growing transcript of every prior round's Q&A (see below). | `""` |
| `brief_path` | string | Path the brief artifact was written to (`<workspace>/001-brief.md`). | `""` |
| `references` | object | `researcher` result: `{ items: [{ path, sections, rationale }] }`. | `{}` |
| `references_path` | string | Path the reference-list artifact was written to (`<workspace>/003-references.md`). | `""` |
| `references_cycle` | int | References re-run rounds consumed (`refine` or `regenerate`); caps via `references_budget`. | `0` |
| `references_budget` | int | Max references re-runs before `refine`/`regenerate` abandons. | `3` |
| `references_revise` | bool | `true` on a `refine` (revise the current list in place), `false` on a fresh `regenerate` — steers `prompts/references.md`. | `false` |
| `prd_artifact` | object | `task` result: `{ title, summary_markdown, file_path, confidence, needs_clarification, follow_up_questions }`. | `{}` |
| `refine_feedback` | string | Operator note carried into the next draft / question round. | `""` |
| `cycle` | int | Coarse global audit counter. | `0` |
| `clarifying_cycle` | int | Clarification rounds consumed; caps via `clarifying_budget`. | `0` |
| `clarifying_budget` | int | Max clarification rounds before `regenerate` abandons. | `3` |
| `drafting_cycle` | int | Draft refines consumed; caps via `drafting_budget`. | `0` |
| `drafting_budget` | int | Max draft refines before `refine` abandons. | `5` |
| `judge_mode` | string | `human` \| `llm` \| `llm_then_human`. | `human` |
| `judge_confidence_threshold` | float | Floor for auto-firing the judge's verdict. | `0.8` |
| `llm_verdict` | object | `{ verdict, intent, reason, confidence }` from the judge. | `{}` |
| `abandon_reason` | string | Structured reason set by an abandon arc. | `""` |
| `status` | string | `done` after `@exit:done`; `abandoned` on `@exit:abandoned`. | `""` |
| `thread` | string | Held for an optional future `transport.post` of the finished PRD. | `""` |

### Intent surface

| Intent | Slots | Description |
|---|---|---|
| `discuss` | `message` (req) | Send a free-text message in the idea-discovery conversation (self-loops `idle`). |
| `start` | — | From `idle`: distill the conversation into `world.idea` and advance to `clarifying`. |
| `answer` | `text` (req) · `n` (opt, int) | Record a free-text answer; self-loops `clarifying` (does NOT regenerate). The room's `default_intent`, so any non-verb prose routes here deterministically (before the LLM); the `answer_matcher` agent maps `text` to the question(s) it answers (`n`, if the operator named one, is a hint) and appends the matched `Qn: …` line(s) to `clarification_answers`, bumping `answered_count`. |
| `submit_answers` | — | Pure verb (no slots). Advance to the `brief` review on the answers gathered so far (accumulated by `answer`); appends the round to `clarification_log` and increments `clarifying_cycle`. |
| `skip` | — | Move on to the brief with what we have (no answers this round; no log append). |
| `confirm` | — | Confirm the current review step (`brief` → `references`, or `references` → `drafting`). |
| `regenerate` | (opt) `feedback` | Re-generate the current step's machine output FROM SCRATCH: in `clarifying`, re-ask this round's questions (`clarifying_cycle >= clarifying_budget` → `@exit:abandoned`); in `references`, a fresh doc search (`references_revise=false`; `references_cycle >= references_budget` → `@exit:abandoned`). Self-transition; re-fires the on_enter agent call. |
| `accept` | — | From `drafting`: finish via `@exit:done` (re-pins `prd_artifact`, sets `status=done`). |
| `refine` | `feedback` (req) | Refine BUILDING ON the current artifact (not a fresh start): in `drafting`, re-draft with the same inputs (`drafting_cycle >= drafting_budget` → `@exit:abandoned`); in `references`, revise the current list in place (`references_revise=true`; `references_cycle >= references_budget` → `@exit:abandoned`). |
| `clarify` | — | **Non-destructive** back-to-clarifying from `brief` / `references` / `drafting`: **keeps** `clarification_log`, clears only this round's working answers, and the analyst refines/combines the prior questions and asks only what's still missing. The loop re-passes through `brief` + `references`. |
| `restart_from` | (opt) `stage` | **Destructive** redo, not extend: `idle` re-pitches (wipes the log); `clarifying` re-questions (discards the record). Offered from `brief`, `references`, and `drafting`. |
| `quit` | — | Bail; exits via `@exit:abandoned`. |
| `look` | — | Re-render the current view. |

### Host requirements

| Handler | Used by | File |
|---|---|---|
| `host.chat.resolve` | `idle` (get-or-create the discovery chat; idempotent across `on_enter` re-fires) | `internal/host/chat_handlers.go` |
| `host.agent.converse` | `idle` (interviewer discovery chat + distill) | `internal/host/agent_converse.go` |
| `host.agent.decide` | `search` (scout), `clarifying` (analyst), `references` (researcher), `drafting` (judge) | `internal/host/agent_decide.go` |
| `host.agent.task` | `drafting` (author, writes the PRD) | `internal/host/agent_task.go` |
| `host.artifacts_dir` | `brief` (001-brief.md), `references` (003-references.md), into the per-PRD workspace; `mode: replace` for `on_enter` idempotency | `internal/host/artifacts_dir_transport.go` |
| `host.starlark.run` | `search` (`prd_slug.star` mints the slug + workspace), `drafting` (`prd_publish.star` publishes the accepted draft) | `internal/host/starlark_run.go` |

`host.chat.*` needs a ChatStore wired into the session — `kitsoki run`
provides one (via `--db`); standalone flow fixtures stub it.

v1 does not post the finished PRD out-of-band; mirroring it to an inbox /
Confluence / a ticket is a one-line `iface.transport.post` effect on the
`accept` arc when wanted (see "Resolved decisions" below).

### Agents (persona table)

Named agents attribute model + token usage to each step in the trace.

| Persona | Verb | Tools | Role |
|---|---|---|---|
| `interviewer` | `converse` | `Read`, `Grep`, `Glob` | Runs the idea-discovery chat; helps the operator sharpen the problem, users, and scope. |
| `scout` | `decide` | `Read`, `Grep`, `Glob` | The prior-art gate: scans the workdir's docs for overlapping PRDs / requirement docs + roadmap fit, before any artifact is written. |
| `analyst` | `decide` | `Read`, `Grep`, `Glob` | Reads the idea + upstream docs + the transcript; asks only genuinely-new questions. |
| `answer_matcher` | `decide` | (none) | Maps each free-text operator reply to the clarifying question(s) it answers (by position) — so number-less, out-of-order answers land correctly. Returns only the delta for that reply. |
| `researcher` | `decide` | `Read`, `Grep`, `Glob` | Searches the working dir's **docs** (not code) for prior art / constraints; curates the reference list with sections + rationale. |
| `author` | `task` | `Read`, `Grep`, `Glob`, `Write`, `Edit` | Writes the PRD markdown to disk (reads the curated references); produces `prd_artifact`. |
| `judge` | `decide` | (none) | Optional auto-advance gate (off by default). |

## The multi-round clarification loop

The `clarify` arc closes a loop back into `clarifying` for another Q&A
round, and each round **appends** to `clarification_log` rather than
overwriting it. The append is a string concat done in the
`submit_answers` effect, newest-round-first:

```
── Round N ──
Questions: {compact sorted-key JSON of world.clarifications}
Answers:   {this round's accumulated "Qn: …" replies}

{prior clarification_log}
```

Two mechanics make this work (both load-time gotchas — see the design
note in `rooms/clarifying.yaml`):

- **Leading the block with the literal `── Round …` divider** sidesteps
  the `RenderValue` short-circuit: a `set:` value that both *starts* with
  `{{` and *ends* with `}}` is evaluated as a typed expression (which
  fails to compile here), so the newest-round-first ordering is load-bearing.
- **`Questions: {{ world.clarifications }}` embeds** the object, which
  splices it as compact sorted-key JSON. A *bare* `key: "{{ world.obj }}"`
  would instead store the live map — fine for the `accept` re-pin of
  `prd_artifact`, wrong for a transcript line.

The analyst's prompt receives the accumulated log and is told to ask
**only new questions, or return an empty list** — that "only-new" framing
is what keeps round 2+ from re-asking resolved points.

`clarify` vs `refine` vs `restart_from` — three deliberately distinct
exits from the `drafting` checkpoint:

- `refine` keeps the same inputs, asks the author to revise the prose.
- `clarify` says "the inputs are incomplete" → another clarification
  round that *adds to* the transcript, then re-drafts.
- `restart_from clarifying` discards the clarification record and starts
  the questioning over (`idle` goes further: re-pitch from scratch).

The author can self-flag `needs_clarification: true` with
`follow_up_questions`; the `drafting` view raises a banner pointing the
operator at `clarify`, and the next round's analyst turns those follow-ups
into questions.

## Judge polymorphism

`drafting` runs the same `on_enter` chain in all three judge modes,
gated by `when:` — not a fork in the graph (the `bugfix` pattern):

| Mode | Behaviour at the draft checkpoint |
|---|---|
| `human` (default) | No judge call; the operator decides. |
| `llm` | Run the `judge`; when its verdict is not `uncertain` and `confidence >= judge_confidence_threshold`, `emit_intent:` auto-fires the verdict's intent the same turn. Otherwise the state holds. |
| `llm_then_human` | Same auto-fire path; falls through to the human view on an uncertain / low-confidence verdict. |

`emit_intent:` is depth-capped at `machine.EmitIntentMaxDepth` (= 8).

## Resolved design decisions

From the original design note (now retired), resolved as implemented:

1. **PRD output** — `agent.task` writes the document to
   `<prd_workspace>/004-prd.md` (a slug-named per-PRD workspace under
   `{{ workdir }}/.artifacts/prd/`, giving `files_changed` in the trace)
   and returns a `summary_markdown` for the checkpoint view. On `accept`,
   `prd_publish.star` publishes it out to the durable home
   `{{ workdir }}/{{ publish_durable_path }}/<slug>.md`. The slug + workspace
   are minted at the `search` gate by `prd_slug.star` — the same workspace /
   publish sandwich as `dev-story`'s proposal pipeline.
2. **Idea capture** — a conversational discovery chat (`agent.converse`),
   not a form: a free-form pitch is awkward to type into one input field,
   so the operator talks it through and `start` distills it. **Clarification
   capture** is a structured numbered *list* the operator answers one at a
   time by number (`answer n=…`, e.g. "number 3 …"); the screen tracks which
   are answered and `submit_answers` advances on the accumulated replies (a
   pasted blob still overrides). Driven by typed free text rather than a menu
   selection because a choice-form param can fill only one slot, not the
   `n`+`text` pair.
3. **Upstream ingestion** — passed as `upstream_paths`; the `analyst` /
   `author` agents read them via `Read`/`Grep`, so their reads land as
   `agent.tool_call` trace events (serving the "what files were used" ask).
4. **Sharing the PRD** — out of scope for v1; add an
   `iface.transport.post` (or `host.inbox.add`) effect on the `accept` arc.
5. **LLM judge** — the machinery is carried (cheap), default `human`.
6. **Review gates before drafting** — the `brief` and `references` rooms
   make the operator confirm the inputs before any PRD is written.
   - **The brief is composed deterministically** — it is *not* a new LLM
     synthesis. The earlier phases already produced everything it needs
     (`world.idea` from `idle`, the clarification transcript from
     `clarifying`), so the room just composes them and persists to
     `<workspace>/001-brief.md`. A lightweight `brief_check` gate then
     reads the file and returns `continue` / `clarify`.
   - **References uses `host.agent.decide`** (the canonical
     structured-artifact verb), the same verb as `clarifying`. In flow
     fixtures the two `on_enter` deciders are kept apart by *what each
     binds* (`clarifying` → `.questions`, `references` → `.items`) — a
     questions-only stub leaves references' `.items` empty, a valid state —
     so no fixture asserting both shapes runs them in one session
     (`brief_references_path` starts at `brief` to isolate the researcher).
     Do **not** switch one room to a different agent verb to dodge a
     stub-name collision.
   - **Artifacts land in the per-PRD workspace** under
     `{{ workdir }}/.artifacts/prd/<slug>/` as numbered files
     (`001-brief.md`, `003-references.md`, `004-prd.md`) so they sit in the
     operator's tree and never collide across PRDs, and the writes use
     `mode: replace` so an `on_enter` re-fire overwrites rather than
     stacking duplicates.

## Flow fixtures

Deterministic, hermetic (host stubs only — no LLM). Run:

```
kitsoki test flows stories/prd/app.yaml
```

| Fixture | Proves |
|---|---|
| `happy_path.yaml` | Full path `idle → clarifying → brief → references → drafting → @exit:done`; the `brief`/`references` artifact writes dispatch. |
| `brief_references_path.yaml` | `brief → references → drafting → @exit:done`; the `references` list is bound + persisted (asserts content). Starts at `brief` so the only `decide` is the researcher's. |
| `clarify_from_brief.yaml` | `brief —clarify→ clarifying` PRESERVES `clarification_log` (vs `restart_from`, which wipes it); answering + submitting appends a second round (`clarifying_cycle` 1 → 2). |
| `references_refine.yaml` | `refine` sets `references_revise=true` (revise in place) and `regenerate` sets it `false` (fresh search); both budget via `references_cycle`; `confirm` clears the flag. |
| `answer_one_by_one.yaml` | `answer n=…` accumulates out-of-order; `submit_answers` advances to `brief` on the accumulated replies. |
| `refine_loop.yaml` | `refine` re-enters `drafting`; `drafting_cycle` + `refine_feedback` advance. |
| `multi_round_clarify.yaml` | Two `clarify → submit → confirm → confirm` rounds; `clarifying_cycle == 2` and `clarification_log` accumulates both rounds (newest first); passing through `brief`/`references` doesn't disturb the transcript. |
| `skip_to_brief.yaml` | `skip` from `clarifying` advances to `brief` with NO round recorded — `clarification_log` stays empty and `clarifying_cycle` does not move (the clean-move-on contrast to `submit_answers`). |
| `clarifying_regenerate.yaml` | `regenerate` re-asks: discards this round's working answers, carries `refine_feedback`, bumps `clarifying_cycle`; at `clarifying_budget` the next `regenerate` → `@exit:abandoned` (`clarifying_budget_exhausted`). |
| `restart_from_clarifying.yaml` | The DESTRUCTIVE back-path: `restart_from clarifying` WIPES `clarification_log` + every cycle counter (the deliberate contrast to `clarify_from_brief`, which preserves them). |
| `restart_from_idle.yaml` | `restart_from idle` from `drafting` re-pitches: resets the full pipeline (record + all cycles + `refine_feedback`) and lands back in `idle`. |
| `references_budget.yaml` | `refine`/`regenerate` at `references_cycle == references_budget` → `@exit:abandoned` (`references_budget_exhausted`) — the references analogue of `budget_exhausted`. |
| `budget_exhausted.yaml` | `refine` at `drafting_cycle == drafting_budget` → `@exit:abandoned` with `abandon_reason`. |
| `quit_from_references.yaml` | `quit` from a review room → `@exit:abandoned` stamped with the room-specific `abandon_reason` (`abandoned_at_references`). |
| `llm_judge.yaml` | Full path in `judge_mode: llm` with an *uncertain* verdict → HOLDS at `drafting`; also proves the three `host.agent.decide` call sites (`analyst_questions`, `references_research`, `judge_verdict`) are stubbed apart by invoke `id:` via `by_call:`. |
| `judge_auto_accept.yaml` | The other judge half — a *confident, non-uncertain* verdict (`accept@0.92 ≥ threshold`) makes `drafting`'s `on_enter` `emit_intent: accept` the same turn, so a single `confirm` into `drafting` auto-advances to `@exit:done`. |
| `prd_overlap_no_matches.yaml` | The prior-art gate, clean (greenfield) path: the scout finds no overlap, `confirm` mints the slug + workspace and advances to `clarifying`. |
| `prd_overlap_proposes_change.yaml` | The scout surfaces an overlap; typed `confirm` stays in `search` with visible guidance, and `change_existing` captures `prd_change_target` (the amend doc) while still minting a workspace for the check artifacts. |
| `prd_search_override.yaml` | `override_new` starts a NEW PRD despite a detected overlap (the discouraged escape hatch), recording `prd_overlap_decision=new` and clearing any prior overlap feedback. |
| `slug_collision.yaml` | The `search` gate's workspace mint is collision-suffixed: an existing `<slug>` workspace / published PRD pushes the new one to `<slug>-2` (`prd_slug.star` uniquify). |
| `publish_to_durable.yaml` | `accept` runs `prd_publish.star` to publish `004-prd.md` out of the workspace to `docs/prd/<slug>.md`, binding `prd_file` to the durable path. |
| `references_semantic_preseed.yaml` | The optional semantic pre-seed populates the researcher's `reference_hits` before it curates. |
| `references_no_embeddings_fallback.yaml` | The semantic pre-seed fails cleanly (no embeddings configured) and the researcher still curates from scratch. |
| `brief_ready_judge_passes.yaml` | The `brief_check` gate returns `continue` — the brief advances to `references`. |
| `brief_ready_judge_clarify.yaml` | The `brief_check` gate returns `clarify` — the brief loops back to `clarifying`. |
| `capture_existing_in_discovery.yaml` | The operator names docs mid-chat in `idle`; they thread into `upstream_paths` for the scout + researcher. |
| `once_reload_safe.yaml` | `on_enter` is idempotent: a room whose `once:`-guarded result is already bound re-renders from cache on `/reload` instead of re-running the call. |
| `error_clarifying_holds.yaml` | A failed analyst run HOLDS in `clarifying` (surfacing `last_error`) rather than bouncing. |
| `error_references_steps_back.yaml` | A failed researcher run steps BACK one stage to `brief`. |
| `error_drafting_steps_back.yaml` | A failed author run steps BACK one stage to `references` (the research is already curated). |

## File layout

```
stories/prd/
  app.yaml                 — manifest
  README.md                — this file
  rooms/
    idle.yaml              — idea discovery (conversational)
    search.yaml            — prior-art scout gate; mints the slug + workspace
    clarifying.yaml        — generate + answer questions (multi-round)
    brief.yaml             — formalize + confirm the inputs (deterministic; writes <workspace>/001-brief.md)
    references.yaml        — curate the doc reference list + confirm (writes <workspace>/003-references.md)
    drafting.yaml          — write <workspace>/004-prd.md + checkpoint; accept publishes to docs/prd/<slug>.md
  prompts/
    prd_existing_state.md  — scout: prior-art overlap scan + roadmap fit
    brief_check.md         — (brief gate prompt)
    clarify.md             — analyst: only-new clarifying questions
    references.md          — researcher: curate the doc reference list (docs only)
    draft_prd.md           — author: write the PRD, self-assess completeness
    judge_prd.md           — judge: accept / refine / clarify / uncertain
  schemas/
    prd_existing_state.json — { roadmap_fit, overlaps: [{path, summary, recommendation}] }
    brief-check.json        — brief-gate verdict
    clarifications.json     — { questions: [{id, question, why}] }
    references.json         — { items: [{path, sections, rationale}] }
    prd_artifact.json       — { title, summary_markdown, file_path, confidence, needs_clarification, follow_up_questions }
    judge_verdict.json      — { verdict, intent, reason, confidence }
  scripts/
    prd_slug.star          — mint + uniquify the slug; return the workspace path (host.starlark.run, search room)
    prd_slug.star.yaml     — typed Starlark sidecar
    prd_publish.star       — publish the accepted 004-prd.md to docs/prd/<slug>.md (host.starlark.run, drafting room)
  views/base.pongo         — standalone base (rooms use flattened views)
  flows/                   — deterministic flow fixtures
```

## See also

- [`stories/bugfix/`](../bugfix/) — the checkpoint / refine / judge
  patterns this story mirrors.
- [`stories/oregon-trail/rooms/trail_guide.yaml`](../oregon-trail/rooms/trail_guide.yaml)
  — the conversational-room pattern the `idle` discovery chat is modeled on.
- [`docs/stories/choice-widget.md`](../../docs/stories/choice-widget.md)
  — the `param:` reply capture used for clarification answers.
- [`docs/tracing/trace-format.md`](../../docs/tracing/trace-format.md)
  — how the per-step model + file attribution lands in the trace.
