# Proposals

Design documents for kitsoki features that are **partially or not
yet implemented**.

New proposals start from a template in
[`templates/`](templates/) — pick `story`, `runtime`, `tui`, or
`tracing` for a focused change, or `epic` for one that spans several.
The [`proposal-authoring`](../skills/proposal-authoring/SKILL.md) skill
drives picking a template and decomposing a large change into slices.

## What lives here

- Proposals for features that haven't shipped: rationale, schema
  sketches, edge cases, phased delivery, and the decision points
  the author wants reviewed.
- Trimmed proposals: when a feature ships in pieces, the
  implemented sections migrate into normal `docs/` and this folder
  keeps only what's still in design.

## What doesn't

- **Documentation of shipped features.** Those live in `docs/`
  proper (`architecture.md`, `state-machine.md`, `transports.md`,
  `hosts.md`, `developer-guide.md`, `authoring.md`, `testing.md`)
  or in topic subfolders like `docs/stories/background-jobs/`. A proposal
  whose ideas have shipped is stale planning material — it does
  not belong here.
- **Fully-resolved planning history.** When everything in a
  proposal has shipped or been superseded, the file is deleted —
  the shipped docs and git history are authoritative.

## Every proposal carries a status line

The opening lines tell the reader what's implemented, what isn't,
and where to find the shipped pieces. Examples:

> **Status:** Draft v1. Nothing implemented yet.

> **Status:** v1 trimmed. Three of five surfaces shipped (see
> `docs/architecture/developer-guide.md` §6); two remain in design.

> **Status:** Draft v3. Refactored against `internal/chats/` after
> review; spike required (§0) before phase A.

## Lifecycle

1. A proposal lands here as a draft, with a status line that says
   "not implemented."
2. As implementation progresses, the proposal author migrates the
   implemented sections into normal `docs/`, trims the proposal,
   and updates the status line.
3. When everything in the proposal has shipped (or been fully
   superseded), the file is deleted. Git history preserves the
   planning record.

The goal: `docs/proposals/` stays a **small, current queue** of
what's being worked toward — not a graveyard of what was once
thought.

## Current proposals

- [`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) —
  one remaining AI-collaborator surface (per-state `loading_view`).
  Three v1 surfaces shipped (`docs/architecture/developer-guide.md` §6);
  the scripted `kitsoki drive` (§1) is superseded by the
  [`story-qa-agent`](story-qa-agent.md) epic, which makes it interactive.
- [`story-qa-agent.md`](story-qa-agent.md) — **epic.** A Claude agent
  that QAs a story by *using* it: given a persona + scenario it walks the
  story turn-by-turn, reading the exact human-fidelity screen (and a real
  screenshot of it), and reports view/navigation/intuitiveness/objective
  findings. Nothing implemented yet; decomposed into four slices:
  - [`qa-frame-seam.md`](qa-frame-seam.md) (tui) — one composer that
    returns the full screen (body + chrome) as `{text, ansi, metadata}`
    at any width; the live TUI renders through it too.
  - [`qa-drive-command.md`](qa-drive-command.md) (runtime) —
    `kitsoki drive`: persistent trace session, free-text input,
    `--harness live|replay`, VCR record/playback modes; emits the frame
    per turn.
  - [`qa-screenshot.md`](qa-screenshot.md) (tui) — `kitsoki shot`:
    ANSI→PNG of a frame for visual review.
  - [`qa-agent-skill.md`](qa-agent-skill.md) (tooling) — the `story-qa`
    subagent: persona + scenario → drive loop → scored UX rubric +
    report + screenshots + bug list.
- [`oracle-capability-model.md`](oracle-capability-model.md) — **epic.**
  One capability model governing **every** oracle (decide / ask / converse /
  task), unifying three ad-hoc restrictions and an overloaded boolean. Four
  cooperating layers — **toolbox** (a named, reusable tool grant) → **effect
  class** (`pure | read | write | external` + `deterministic`) → **layered
  enforcement** (tool allowlist for pure/read; OS sandbox for write/external) →
  **conformance** (the trace proves the box held). Nothing implemented yet;
  decomposed into three runtime slices + a conformance check:
  - [`effect-taxonomy.md`](effect-taxonomy.md) (runtime) — the classification
    substrate: `effect`/`deterministic` on host calls **and** agents, replacing
    `external_side_effect`; a load-time hard-fail for a read-only call holding a
    mutator. (Modelled on Acronis DTS's `deterministic_behavior` enum.)
  - [`toolbox-and-enforcement.md`](toolbox-and-enforcement.md) (runtime) —
    named `toolboxes:` + `tools_add:`; one effect-derived tool-layer policy for
    all four oracle kinds, collapsing the `mutationTools` deny, the converse
    read-only branch, and task's unrestricted spawn into one path.
  - [`task-fs-sandbox.md`](task-fs-sandbox.md) (runtime) — the kernel boundary
    beneath the tools: `sandbox:` (bwrap/Landlock) confines the write/external
    tiers so no tool — Write, Bash, python, sed — escapes the workspace; engine
    validates + persists the diff. PoC proven on this host.
  - conformance check folded into
    [`oracle-contract-eval.md`](oracle-contract-eval.md) (§Layer 1b) — offline
    lint that recorded tool uses never exceeded the declared toolbox/effect.
- [`artifact-format.md`](artifact-format.md) — a schema-verified
  markdown-with-frontmatter artifact format with **lossless** round-trip via
  `yaml.Node`, consolidating three hand-rolled artifact writers
  (`localfiles_ticket.go`, `cypilot_artifacts.go`, `append_file_transport.go`)
  that today reorder frontmatter and skip validation. Supports markdown as
  block-scalar fields (data-primary docs). Nothing implemented yet; no new deps.
- [`auto-advance-states-proposal.md`](auto-advance-states-proposal.md) —
  auto-fire `done` after `on_enter` chains complete, with `wait: true`
  to opt out. Nothing implemented yet.
- [`bug-sync-proposal.md`](bug-sync-proposal.md) — `kitsoki bug
  sync` pushes local bug files to GitHub / Jira. Format support
  shipped with the bug-filing CLI (see [`docs/stories/bugs.md`](../stories/bugs.md));
  the command itself remains in design.
- [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md) —
  chats PTY mode, input queue, and multi-transport drive.
  Phases 0/A/B/C shipped (see `docs/stories/meta-mode.md` §5 and
  `docs/architecture/hosts.md` for the user-facing surface); D/E/F/G partial
  or deferred; H not started. The status table at the top of the
  proposal is the source of truth for what's wired today.
- [`continue-mode-proposal.md`](continue-mode-proposal.md) — durable
  sessions via a unified trace journal (`kitsoki run --continue`).
  Phase A + Wave 2 shipped (`internal/journal/`, `--continue`, session
  verbs); Wave 3 dual-write mostly landed, with the metamode proposal
  ledger entries and `recovery_state` still TODO.
- [`execution-modes-and-gate-deciders.md`](execution-modes-and-gate-deciders.md) —
  one-shot / staged execution modes; intent gates resolved by a
  per-state decider. Engine core, CLI/flow surface, and docs-review
  migration shipped; pre-bind staging and the bugfix-story migration
  remain (§8).
- [`idempotent-on-enter.md`](idempotent-on-enter.md) — an opt-in `once:`
  flag on `invoke:` effects so the engine skips an on_enter host call whose
  `bind:` target is already populated — making `/reload` (and re-entry)
  idempotent without per-room `when:` guards. **`once:` shipped** (see
  `docs/stories/state-machine.md` §"`on_enter` must be idempotent"; the
  `proposal_*.yaml` rooms are migrated); the `/reload --force` companion to
  bypass it during authoring (Open question 1) remains.
- [`local-model-oracle.md`](local-model-oracle.md) — a `builtin.local_llm`
  oracle plugin that drives a local llama.cpp `llama-server` sidecar over
  OpenAI-compatible HTTP, with grammar-forced schema-valid output, for
  routing and small `decide` verdicts. Nothing implemented yet; spike (§0)
  required before committing.
- [`oracle-contract-eval.md`](oracle-contract-eval.md) — schema-conformance
  linting of cassette/flow mocks (Layer 1, offline) plus a per-call-site
  correctness eval (Layer 2, gated): `{input, expected}` datasets scored as a
  correctness % across backends (Claude vs free llama.cpp), so a call site can
  be routed to the cheap backend with evidence. Produces the measurement
  `local-model-oracle.md` consumes. Nothing implemented yet.
- [`oracle-off-ramp.md`](oracle-off-ramp.md) — a per-room
  `oracle_off_ramp:` opt-in: when free text maps to no declared intent,
  hand the turn to an oracle `converse` answer instead of rejecting, with
  no state/world change. Nothing implemented yet.
- [`stories/prd/`](../../stories/prd/README.md) — a standalone
  PRD-authoring operator story. Shipped; the design proposal was never
  committed, so its reference is the story README.
- [`runstatus-proposal.md`](runstatus-proposal.md) — Vue 3 web UI
  for inspecting a run: clickable state diagram + trace timeline +
  detail drawer. Phase 1 (artifact mode) ~90% shipped; the single-file
  HTML export, timeline virtualization, and live JSON-RPC + SSE mode
  remain.
- [`runstatus-trace-fidelity.md`](runstatus-trace-fidelity.md) —
  make the bugfix trace canonical (`oracle.call.*`, a distinct
  `machine.say` kind, `turn.input`) and rewire runstatus so each
  meaningful aspect renders once per column. Producer half shipped
  and documented in `docs/tracing/trace-format.md`; the runstatus
  consumer rewrite and fixture migration remain.
- [`starlark-host.md`](starlark-host.md) —
  `host.starlark.run` capability: sandboxed Starlark scripts bundled with a
  story, with a typed `ctx` API for HTTP and world access, fully integrated
  with the cassette/flow test system. Extends the deterministic replay border
  to cover scripted logic. Nothing implemented yet.
- [`semantic-routing-proposal.md`](semantic-routing-proposal.md) —
  v1 shipped. The trimmed proposal keeps only open questions and
  the Oregon Trail calibration history. The user-facing reference
  for the shipped surface lives at
  [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md).
- [`visual-outputs.md`](visual-outputs.md) — **epic.** Make a visual output
  a step produces (MP4 / PNG slideshow / slidey deck) a first-class,
  **recorded** media artifact: emitted under `.artifacts/`, recorded in the
  trace, shown inline in the web UI, pointed at in the TUI. Nothing
  implemented yet; decomposed into three slices:
  - [`media-artifact-substrate.md`](media-artifact-substrate.md) (runtime) —
    producer-agnostic core: a recorded `artifact` datapoint + opaque world
    handle + a `media` typed-view element + minimal TUI pointer rendering.
  - [`visual-producers.md`](visual-producers.md) (runtime) —
    `host.slidey.render` + `host.contact_sheet` host calls wrapping the
    existing standalone slidey + `contact-sheet.sh`, deterministically.
  - [`web-media-rendering.md`](web-media-rendering.md) (tui) — Vue `media`
    element (`<video>`/`<img>`/embed) + a `kitsoki web` route serving
    artifact binaries (live) and sidecar files (static export).
- [`view-rendering-readability.md`](view-rendering-readability.md) —
  **epic.** Make the typed element tree the single canonical view
  representation so prose reads cleanly across the TUI and the web,
  and give authors a `kitsoki view` proofing command + lint. Nothing
  implemented yet; decomposed into four slices:
  - [`view-canonical-typed.md`](view-canonical-typed.md) (runtime) —
    normalize every view shape to typed elements at load; always
    populate `TypedView`; `say:`→leading prose; demote `View string`.
  - [`view-tui-rendering.md`](view-tui-rendering.md) (tui) — collapse
    the four-stage width chain; render typed elements direct-to-styled;
    shrink Glamour to the code/raw escape hatch.
  - [`view-trace-and-web-typed.md`](view-trace-and-web-typed.md) (tracing) —
    record the typed tree in the trace; web renders every turn through
    `ViewElement`; delete the 80-col fossil fallback.
  - [`view-proofing-tooling.md`](view-proofing-tooling.md) (tui) —
    `kitsoki view` + lint catalog + cross-env golden/property tests +
    authoring-skill wiring.
- [`work-decomposition.md`](work-decomposition.md) — **story.** A new
  `stories/decompose/` sub-story imported into dev-story: hand it an accepted
  proposal (or epic + children) and an interactive discovery conversation
  distils scope, an `oracle.decide` emits a brief manifest the MCP submit
  validator structurally enforces, a deterministic `host.run` renders + lints
  it to `decomposition.yaml` (acyclic DAG, coverage), an adversarial
  `oracle.decide` judges feasibility + completeness, and a coordination board
  dispatches each brief into the `impl` import one at a time with a human gate.
  Nothing implemented yet.
