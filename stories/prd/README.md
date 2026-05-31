# prd — PRD-authoring operator story

A reusable kitsoki story that turns a free-form idea (plus any existing
upstream requirement docs) into a PRD markdown document. It is a
pipeline-shaped operator story, structurally identical to
[`stories/bugfix/`](../bugfix/) and [`stories/dev-story/`](../dev-story/);
the novelty is the **document** artifact, a **conversational idea
discovery** intake, and a **multi-round clarification** step whose
transcript accumulates across rounds.

No host / engine / widget changes — everything composes existing
mechanisms (a conversational chat room, `host.oracle.*`, the cycle-budget
refine loop, named agents, tracing).

Standalone:

```
kitsoki run stories/prd/app.yaml
```

## Story graph

```
idle  ── start ──▶  clarifying  ── submit_answers / skip ──▶  drafting
 │  (discovery chat)     │  (decide → only-new questions)        │  (task → PRD.md)
 │  ◀─ discuss (self)    ├─ regenerate ─▶ clarifying (cycle++)   ├─ accept ─▶ @exit:done
 │                       └─ quit ─▶ @exit:abandoned              ├─ refine ─▶ drafting (budget→abandoned)
 │                            ▲                                  ├─ clarify ─▶ clarifying  ◀── another Q&A round
 │                            └──────────── clarify ─────────────┤
 │                                                               ├─ restart_from ─▶ idle | clarifying
 └─ quit ─▶ @exit:abandoned                                      └─ quit ─▶ @exit:abandoned
```

`idle` is a **conversation**, not a form: the operator talks the idea
through with an `interviewer` agent (`discuss` self-loops), and `start`
distills the conversation into `world.idea` before advancing.

Three rooms (`idle`, `clarifying`, `drafting`), two exits (`done`,
`abandoned`). `drafting` is the checkpoint room — same shape as every
`bugfix` phase.

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
| `idle` (conversational) | `host.chat.create` + `interviewer` (`host.oracle.converse`) opens the discovery chat | no — `discuss` self-loops the conversation | `clarifying` (via `start`, which distills the chat into `world.idea`) |
| `clarifying` | `analyst` (`host.oracle.decide`) → `clarifications` | no — operator answers | `drafting` (via `submit_answers` or `skip`) |
| `drafting` | `author` (`host.oracle.task`) writes the PRD → `prd_artifact`; optional `judge` | yes — `prd_artifact` | `@exit:done` (via `accept`) |

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
| `workdir` | string | Where upstream lives + the PRD is written; pins each oracle's `working_dir`. | `"."` |
| `output_path` | string | PRD filename, relative to `workdir`. | `"PRD.md"` |
| `clarifications` | object | This round's `decide` result: `{ questions: [{id, question, why}] }`. | `{}` |
| `clarification_answers` | string | The operator's free-text replies for this round. | `""` |
| `clarification_log` | string | Growing transcript of every prior round's Q&A (see below). | `""` |
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
| `submit_answers` | `answers` (req) | Append this round's Q&A to `clarification_log`, increment `clarifying_cycle`, advance to `drafting`. |
| `skip` | — | Draft with what we have (no answers this round; no log append). |
| `regenerate` | (opt) `feedback` | Re-ask this round's questions (self-transition; re-fires the analyst). `clarifying_cycle >= clarifying_budget` → `@exit:abandoned`. |
| `accept` | — | From `drafting`: finish via `@exit:done` (re-pins `prd_artifact`, sets `status=done`). |
| `refine` | `feedback` (req) | Re-draft with the same inputs. `drafting_cycle >= drafting_budget` → `@exit:abandoned` (`abandon_reason=drafting_budget_exhausted`). |
| `clarify` | — | Another clarification round, then re-draft with more to work from. Preserves `clarification_log`. |
| `restart_from` | (opt) `stage` | Redo, not extend: `idle` re-pitches (wipes the log); `clarifying` (default) re-questions (discards the record). |
| `quit` | — | Bail; exits via `@exit:abandoned`. |
| `look` | — | Re-render the current view. |

### Host requirements

| Handler | Used by | File |
|---|---|---|
| `host.chat.create` | `idle` (creates the discovery chat) | `internal/host/chat_handlers.go` |
| `host.oracle.converse` | `idle` (interviewer discovery chat + distill) | `internal/host/oracle_converse.go` |
| `host.oracle.decide` | `clarifying` (analyst), `drafting` (judge) | `internal/host/oracle_decide.go` |
| `host.oracle.task` | `drafting` (author, writes the PRD) | `internal/host/oracle_task.go` |

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
| `analyst` | `decide` | `Read`, `Grep`, `Glob` | Reads the idea + upstream docs + the transcript; asks only genuinely-new questions. |
| `author` | `task` | `Read`, `Grep`, `Glob`, `Write`, `Edit` | Writes the PRD markdown to disk; produces `prd_artifact`. |
| `judge` | `decide` | (none) | Optional auto-advance gate (off by default). |

## The multi-round clarification loop

The `clarify` arc closes a loop back into `clarifying` for another Q&A
round, and each round **appends** to `clarification_log` rather than
overwriting it. The append is a string concat done in the
`submit_answers` effect, newest-round-first:

```
── Round N ──
Questions: {compact sorted-key JSON of world.clarifications}
Answers:   {operator's reply blob}

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

1. **PRD output** — `oracle.task` writes the document to
   `{{ workdir }}/{{ output_path }}` (durable, gives `files_changed` in the
   trace) and returns a `summary_markdown` for the checkpoint view.
2. **Idea capture** — a conversational discovery chat (`oracle.converse`),
   not a form: a free-form pitch is awkward to type into one input field,
   so the operator talks it through and `start` distills it. **Clarification
   capture** stays a structured numbered *list* with a single free-text
   answer blob via `submit_answers` — the list step the original request
   explicitly asked for.
3. **Upstream ingestion** — passed as `upstream_paths`; the `analyst` /
   `author` agents read them via `Read`/`Grep`, so their reads land as
   `oracle.tool_call` trace events (serving the "what files were used" ask).
4. **Sharing the PRD** — out of scope for v1; add an
   `iface.transport.post` (or `host.inbox.add`) effect on the `accept` arc.
5. **LLM judge** — the machinery is carried (cheap), default `human`.

## Flow fixtures

Deterministic, hermetic (host stubs only — no LLM). Run:

```
kitsoki test flows stories/prd/app.yaml
```

| Fixture | Proves |
|---|---|
| `happy_path.yaml` | `idle` (discuss → distill) `→ clarifying → drafting → @exit:done`. |
| `refine_loop.yaml` | `refine` re-enters `drafting`; `drafting_cycle` + `refine_feedback` advance. |
| `multi_round_clarify.yaml` | Two `clarify → submit` rounds; `clarifying_cycle == 2` and `clarification_log` accumulates both rounds (newest first). |
| `budget_exhausted.yaml` | `refine` at `drafting_cycle == drafting_budget` → `@exit:abandoned` with `abandon_reason`. |

## File layout

```
stories/prd/
  app.yaml                 — manifest
  README.md                — this file
  rooms/
    idle.yaml              — idea discovery (conversational)
    clarifying.yaml        — generate + answer questions (multi-round)
    drafting.yaml          — write the PRD + checkpoint
  prompts/
    clarify.md             — analyst: only-new clarifying questions
    draft_prd.md           — author: write the PRD, self-assess completeness
    judge_prd.md           — judge: accept / refine / clarify / uncertain
  schemas/
    clarifications.json    — { questions: [{id, question, why}] }
    prd_artifact.json       — { title, summary_markdown, file_path, confidence, needs_clarification, follow_up_questions }
    judge_verdict.json      — { verdict, intent, reason, confidence }
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
