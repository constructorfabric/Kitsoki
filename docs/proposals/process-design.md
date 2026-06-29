# Designing Processes in Kitsoki

**Status:** Draft v1 (2026-06-02). Methodology + implementation approach for
authoring *process stories* (PRD, design, review, chores — the
AI-SDLC pitch epic). Nothing built from this yet. The end goal is a
**story to make stories** (§6). When the methodology stabilizes, the durable
parts (§§1–5) migrate to `docs/stories/process-design.md` and this proposal
trims to the meta-story slice, then deletes per the proposal lifecycle.

**Kind:** Epic — spans a durable methodology, a new story (the meta-story),
and one small runtime gap (§4.3, first-class working folder).

---

## Why

We are about to author *many* process-shaped stories — the whole AI-SDLC
saga (the AI-SDLC pitch epic): PRD → design → epics → detailed
design → security/perf/devops review → implementation planning →
implementation → PR review → chores → bug fixing → improvement reviews.
Each is a multi-step business process with occasional model judgment.

The `kitsoki-story-authoring` skill covers the *YAML mechanics* (rooms,
effects, host calls, views). It does not answer the question that comes
*first*: **given a real process, how do you decide what each step is, where
the model is allowed in, how each step is validated, and how a human reviews
the work?** That decision is where the moat lives or dies — get it wrong and
you've rebuilt an agentic loop with extra steps. This document is that
missing layer: the design discipline above the YAML.

The idea room in `.kitsoki/stories/kitsoki-dev` is a poor first attempt because it
jumps straight to YAML without this discipline. We fix that by writing the
discipline down, then building a story that *applies* it to produce other
stories.

---

## 1. What a "process" is in kitsoki — session vs. workflow

A **story is a deterministic directed cyclic graph carrying a typed,
per-session scoped context (the `world`)**: states are nodes, transitions
are edges, and cycles are explicit, budgeted loops (the refine/clarify arcs).
"YAML state machine" is a shorthand worth retiring — YAML is merely how the
graph is *programmed and serialized*, not what it is. What matters is that
the graph and its transitions are **deterministic** — guaranteed structure,
testable, replayable — *except* at the gates where it delegates a single
bounded decision to an interpretive operator (§2.1). That is the moat in one
sentence: a deterministic graph with interpretation isolated to named,
recorded points.

There is no "workflow" object in the engine. The vocabulary is:

| Term | What it is | Lifetime |
|---|---|---|
| **Story** | The process *definition* — a deterministic directed cyclic graph (states + transitions) authored in YAML under `stories/<name>/`. The workflow template. | Source; versioned in git. |
| **Session** | One *running instance* of a story — the graph's current node plus its scoped `world`, persisted. ULID, keyed by an external `transport:thread` coordinate, durable in SQLite (`~/.kitsoki/sessions.db`) + a JSONL trace. | Created on first turn; resumable until it exits. |
| **Turn** | One intent dispatched against a session: render → operator/decider picks an intent → effects → host calls → next room. | Milliseconds to minutes. |

So the answer to "are session and workflow the same, can multiple sessions
run concurrently?":

- **Story : session :: class : instance.** The story is the workflow; a
  session is one live run of it.
- **Many sessions, one story, concurrently.** Each session is isolated by
  its session id and protected by a single-writer lock
  (`internal/store/sqlite.go`), keyed by `transport:thread` (e.g.
  `jira:PLTFRM-123`, or a local file path for the dogfood instance). Two
  operators fixing two different tickets are two sessions against one
  `bugfix` story; they never see each other's world.
- **The transport is a plugin; the session is the constant.** The same
  session can be driven from the TUI, a Jira comment, or a PR event — that
  is slide 12 of the pitch, and it is already how `kitsoki-dev` binds its
  `transport: host.append_to_file` so the bug file *is* the conversation log.

**Design consequence:** because sessions are concurrent and isolated, every
piece of per-run scratch state — the working folder especially (§4) — must
be **keyed to the session**, never to a shared path. `kitsoki-dev` already
does this: its workdir is `.worktrees/bf-<ticket_id>`, unique per ticket.

---

## 2. The core principle — maximize determinism, isolate interpretation

The moat (memory: *kitsoki moat is the architecture*) is one commitment:
**separate interpretive decisions from deterministic execution.** Process
design is that commitment made concrete. For every step you author, you are
answering one question: *does this step need a model at all, and if so, how
tightly can I box it?*

Every step is exactly one of three kinds. Push work down the list as far as
it will go:

| Kind | Mechanism | When | Cost |
|---|---|---|---|
| **Deterministic** | `set:` / `increment:` / `host.run` / `host.artifacts_dir` — no model | The step is a computation, a file write, a shell command, a composition of known inputs | Free, instant, perfectly repeatable |
| **Decision** | `host.agent.decide` — typed JSON verdict against a schema, read-only tools | A bounded judgment: classify, choose, score, extract | One short call; output is schema-validated |
| **Task** | `host.agent.task` — agentic write with an acceptance loop, schema-bound result | Open-ended generation that edits the working tree (write code, draft a doc) | The expensive tier; reserve it |

The canonical proof that this is *real*, not aspirational, is the `prd`
story. Look at `stories/prd/rooms/brief.yaml`: the brief is **not** an LLM
synthesis — it is `world.idea` + `world.clarification_log` composed by a
pure `host.artifacts_dir` write (`mode: replace`), no agent call at all.
The model only appears where genuine interpretation is needed: `clarifying`
and `references` use `decide`; `drafting` uses `task`. Four rooms, three
kinds, each chosen deliberately. That is the standard to hold every process
story to.

### 2.1 Gates and deciders — the seams between steps

A room ends in a **gate**: the set of intents whose guards currently pass
(`execution-modes-and-gate-deciders.md`, landed). Gate cardinality drives
the seam:

- **0 intents** → terminal / display room.
- **1 intent** → auto-advance; there is no decision.
- **>1 intents** → a **decision**, resolved by exactly one **decider**, and
  *recorded* as a `GateDecided` event.

The decider is the pluggable interpretive operator, and it is one of:

- **`default`** — a deterministic conditional (`emit_intent: X when <cond>`).
  Prefer this. A guard on world state is a free, testable decision.
- **`llm`** — an `host.agent.decide` judge: `{verdict, intent, confidence}`.
  Auto-fires above a confidence threshold; **bails to human** below it.
- **`human`** — the turn rests at the room; the operator picks. The safe
  default (`--mode staged`).

A run carries an **execution mode**: `staged` (a human resolves every
multi-way gate — safe default) or `one-shot` (the engine advances via
`default`/`llm` deciders, stopping only on a bail). A gate can pin its own
decider regardless of mode — that is how you get a mandatory human checkpoint
inside an otherwise-autonomous run, or an autonomous step inside a supervised
one.

**The design act** is, for each phase boundary: name the gate's intents,
pick the decider, and — critically — define the *validation* the gate
checks before it is even allowed to fire (§3).

---

## 3. Validation — deterministic vs. agentic

Validation is where most ad-hoc AI pipelines quietly fail: they let the
model both do the work *and* judge the work. Kitsoki's discipline is to
**validate deterministically wherever the property can be expressed
deterministically, and reserve agentic judgment for the genuinely
interpretive — always with a human floor.**

### 3.1 The validation hierarchy (cheapest first)

1. **Schema validation (free, automatic).** Every `decide`/`task` result is
   checked against its `schemas/*.json`; a malformed verdict is a retry, not
   a silent bad value. Structuring the output *is* validation. Make schemas
   tight — enums over free strings, required fields, bounded numbers.
2. **Deterministic postcondition (`host.run` → exit code).** The strongest
   validator a process owns. After a `task` writes code: `go build`, `go
   test`, `golangci-lint`, `kitsoki test flows`. After a doc is generated:
   a link-checker, a schema lint. Exit 0 gates the advance; non-zero routes
   to a `*_failed` room. This is objective truth, not opinion.
3. **Guards on world state (`when:`).** "Confidence ≥ threshold", "cycle <
   budget", "required artifact present". Pure, instant, and they read in the
   flow fixtures.
4. **Agentic judgment (`host.agent.decide`).** Only for properties no
   deterministic check can express: *is this PRD coherent? does this design
   actually address the requirement? is this the right decomposition?* The
   judge returns a confidence; the gate **bails to human** below the
   threshold. Never let an agentic judge be the *only* gate on a
   high-consequence step — back it with a deterministic check and/or a human
   checkpoint.
5. **Human checkpoint (the floor).** A `human`-pinned gate. The operator
   reviews the artifact on disk (§4) and confirms, refines, or aborts.

### 3.2 The validation sandwich

The shape every meaningful phase should take:

```
deterministic precondition  →  LLM does the work  →  deterministic postcondition  →  [agentic judge]  →  [human checkpoint]
  (guard: inputs present)       (decide / task)        (schema + host.run check)      (only if needed)     (staged mode)
```

Concretely, an implementation phase: guard that the task brief + workdir
exist → `task` writes the code → `host.run go test ./...` (exit code gates)
→ *optional* `decide` judge "does this satisfy the acceptance criteria?" →
operator confirms the diff. The model is sandwiched between two deterministic
checks and a human; it is never trusted to self-certify.

### 3.3 Deterministic > agentic, restated as a rule

> If you can write a `host.run` command, a schema constraint, or a `when:`
> guard that decides correctness, **do that** and do not add a judge. Add an
> agentic judge only for the residue that no deterministic check can capture,
> and even then pair it with a confidence threshold and a human bail.

Every deterministic validator is also a **flow fixture** — it runs in CI
with zero LLM calls (`kitsoki test flows`), so the process is regression-
tested for free. Agentic judges cannot be (cheaply) regression-tested; that
is another reason to minimize them.

---

## 4. The working folder & review ergonomics

> People prefer editing a file in VS Code to typing into a terminal.

This is the right instinct and the engine already supports it as a
convention — we should make it a *standard* and (§4.3) eventually a primitive.

### 4.1 The pattern (works today)

1. **Each session gets a working folder.** A per-session directory — for
   code work, a git worktree at `.worktrees/<session-key>` (as `kitsoki-dev`
   does: `.worktrees/bf-<ticket_id>`); for doc work, any session-keyed dir.
   Its path lives in `world.workdir`.
2. **Artifacts are written there for review.** `host.artifacts_dir` writes
   one file per `thread:` under `{{ world.workdir }}/.artifacts`, with
   `mode: replace` so re-entry (a `/reload`, a refine loop) overwrites rather
   than stacks (`stories/prd/rooms/brief.yaml`). The room renders the *path*
   prominently so the operator can open it.
3. **A checkpoint room gates on `confirm`.** The turn rests; the operator
   opens the artifact in VS Code, edits it in place, and types `confirm`
   (or `refine feedback=…`, or `quit`). The terminal carries only the
   *decision*; the editor carries the *content*. This is the ergonomic
   inversion the user wants.
4. **The next step reads the edited file back.** A `host.run cat` / a file
   read / the next agent's `working_dir` picks up the operator's edits. The
   human's changes flow into the rest of the pipeline.

This is "inputs/outputs for interactive review" with no new machinery: the
working folder is the exchange surface, the artifact files are the
inputs/outputs, and VS Code is the editor. The trace records the artifact at
write time (memory: *narration belongs in the trace*) so review is auditable.

### 4.2 Why this beats a form or a chat for long content

Forms and conversational rooms are right for *short* structured values and
*open-ended capture* respectively (memory: *free-form capture is
conversational*). They are wrong for reviewing a 300-line PRD or a code diff.
The rule:

- **Conversational room** (`converse` + `host.chat.resolve`) → gather a raw
  idea / pitch. (`stories/prd/rooms/idle.yaml`.)
- **Form / `choice:`** → short structured values and gate decisions.
- **Working-folder artifact + `confirm` gate** → anything the operator needs
  to *read carefully or edit* — documents, diffs, generated stories.

### 4.3 Runtime gap — first-class per-session working folder (the one thing to consider building)

Today `world.workdir` is **story convention**: each story creates and tears
down its own folder, and a story that forgets to scope it per-session will
collide when two sessions run concurrently (§1). For a *family* of process
stories this is repeated, error-prone boilerplate.

Recommendation (a small `runtime` slice, to spin out from this epic): the
engine establishes a **per-session working folder at session start** —
gitignored, keyed by session id, exposed as a reserved `world.workdir` — and
cleans it up (or hands it off) on exit. Stories then *get* a safe,
concurrency-correct scratch dir for free and only choose what to write into
it. This is the substrate that makes the whole SDLC saga ergonomic; flag it
as the dependency before authoring more than one or two process stories by
hand. (Until it lands, follow the `kitsoki-dev` worktree-per-ticket pattern.)

---

## 5. The process-design recipe (implementation approach)

Given a real process, author it in this order. The first four steps are
*design on paper*; only then touch YAML.

1. **Spine.** Lay the process out as a linear sequence of phases (rooms).
   Most processes are a spine with explicit loops, not a graph —
   `idle → clarifying → brief → references → drafting → done` for `prd`.
   Make every loop explicit (a `refine`/`clarify` self-or-back arc) and
   budgeted (a `cycle_budgets:` cap, not an open agentic loop).
2. **Classify each phase** as deterministic / decision / task (§2). Default
   to deterministic; justify every `decide`; justify *hard* every `task`.
   Most "AI steps" people imagine are actually a deterministic composition
   feeding one bounded `decide`.
3. **Define each gate + decider + validation** (§§2.1, 3). For every phase
   boundary: the intents, the decider (prefer `default`), and the
   deterministic check that must pass before the gate fires. Write the
   `host.run` validator now — it is the acceptance criterion.
4. **Choose artifacts & checkpoints** (§4). What gets written to the working
   folder for review, and which gates are `human` (staged) vs. auto. The
   artifact list is the operator's review surface.
5. **Author** with `kitsoki-story-authoring`: `app.yaml` + `rooms/` +
   `prompts/` + `schemas/` + typed views. Persona table (`agents:`) per step
   so the trace attributes model + tokens per phase, as `prd` does.
6. **Lock every path with a no-LLM flow fixture**, then run. Each
   deterministic validator and each gate outcome becomes a `flows/*.yaml`
   that runs in CI free of LLM calls. *Then* `kitsoki run` it for real.

A process is "done" when: every phase is classified, every gate has a named
decider, every deterministic property has a `host.run` validator wired to a
`*_failed` arc, every reviewable output lands in the working folder, and the
flow suite is green.

---

## 6. The meta-story — a story to make stories

The payoff: the recipe in §5 is itself a process, so we encode it as a
process story whose **output artifact is a new story directory**. It dogfoods
every principle above, and its deterministic validator is *exquisite* —
"is the generated story well-formed?" is answered by the loader and the flow
runner, not by a judge.

Proposed spine (working name `story-forge`):

```
idle ─pitch─▶ intake ─submit─▶ spine ─confirm─▶ classify ─confirm─▶ scaffold ─built─▶ validate ─┬─pass─▶ review ─accept─▶ @exit:done
  (chat:       (converse →     (decide →         (decide /            (task → writes    (host.run:    │           (operator edits
   describe     the process     propose phase     operator marks       stories/<name>/   loader +      │            generated story
   the          in prose)       graph as          each phase det/      to {{workdir}})   kitsoki test  │            in VS Code,
   process)                     .artifacts)       decide/task +                          flows + viz)  │            confirms)
                                                  gates + validation)                                  └─fail─▶ scaffold (refine, budgeted)
```

Phase-by-phase, mapped to §§2–4:

| Phase | Kind | Validation | Artifact / review |
|---|---|---|---|
| **intake** | conversational (`converse`) | — | chat transcript |
| **spine** | `decide` | schema: ordered phase list | `.artifacts/spine.md` (operator confirms) |
| **classify** | `decide` + operator | schema: each phase ∈ {det,decide,task}, each gate has a decider | `.artifacts/design.md` (operator edits in VS Code) |
| **scaffold** | `task` (agentic write) | schema: file manifest written | `stories/<name>/**` in `{{workdir}}` |
| **validate** | **deterministic** `host.run` | `kitsoki` loader load + `kitsoki test flows` + `kitsoki viz` — **exit code is the gate** | failures routed to a refine loop |
| **review** | `human` checkpoint | — | operator opens the generated story, edits, accepts |

Why this is the right shape:

- **The hardest validation is free and deterministic.** A generated story
  either loads (loader invariants — §10 of the authoring skill) and its
  generated flow fixtures pass, or it doesn't. `validate` is a `host.run`
  gate, not a judge. An agentic judge appears *only* for the soft question
  "does this story actually capture the process the operator described?" —
  and even that bails to the human `review` checkpoint.
- **The working folder is the deliverable.** The new story is scaffolded
  into the session's workdir; the operator reviews and edits it in VS Code
  before it is accepted into the tree. Exactly the §4 ergonomics.
- **It is the same shape as `prd`.** Conversational intake → deterministic
  composition → review checkpoints → one agentic `task` that writes to disk
  → done. We are not inventing a new pattern; we are pointing the existing
  one at story authoring.

---

## 7. Open questions

1. **First-class workdir (§4.3) — before or alongside the meta-story?** It is
   a small runtime slice that every process story benefits from. Build it
   first, or hand-roll the worktree pattern in the meta-story and extract the
   primitive once a second story needs it?
2. **Does the meta-story emit flow fixtures it can run, or only the story
   skeleton?** The strongest `validate` gate requires the `scaffold` task to
   also generate at least one `flows/*.yaml` so `kitsoki test flows` has
   something to check. Probably yes — generating the happy-path flow is part
   of "well-formed".
3. **`decide` vs. operator in `classify`.** Should the engine propose the
   det/decide/task classification (a `decide`) with the operator correcting
   it, or should the operator drive and the model only advise? Leaning:
   model proposes, operator edits the `.artifacts/design.md` — keeps the
   human in the loop on the moat-critical decision.
4. **Where does the durable methodology live?** §§1–5 want to become
   `docs/stories/process-design.md` (the conceptual layer above
   `authoring.md`). Confirm that home before migrating.

---

## Non-goals

- Re-documenting YAML mechanics — that is `kitsoki-story-authoring`.
- A general code-generation framework — the meta-story generates *kitsoki
  stories*, nothing more.
- Auto-inferring which steps need a model — the operator classifies (§5.2);
  no inference magic.
