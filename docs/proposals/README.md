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

- [`pre-llm-intercept.md`](pre-llm-intercept.md) — **epic.** Expose kitsoki's
  no-LLM semantic-routing stack as a **pre-LLM gate** in front of a coding agent:
  intercept the user's input, check it against a bound room, and when it is a
  recognized command ("rebase this onto main") handle it deterministically in the
  kitsoki story while the agent's main LLM is **never invoked** for that turn —
  everything unrecognized passes through untouched. Honest about the ceiling: only
  Claude Code has a pre-model hook (block + result-as-reason); Codex/Copilot have
  none and get a degraded path. Nothing implemented yet; two slices (0/2):
  - [`intercept-engine.md`](intercept-engine.md) (runtime) — `kitsoki intercept`:
    split *classify* (a no-LLM, zero-side-effect `Orchestrator.Classify` pass) from
    *execute*, gate conservatively on the `semroute.Verdict`, and emit structured
    JSON + exit codes the hook branches on.
  - [`agent-intercept-hooks.md`](agent-intercept-hooks.md) (tooling) — the Claude
    Code `UserPromptSubmit` shim (block + marked, fail-open) + `kitsoki hook
    install` + the `.kitsoki.yaml intercept:` binding + the honest Claude / Codex /
    Copilot capability matrix.
- [`stories-as-trainable-models.md`](stories-as-trainable-models.md) — **epic.**
  Reframe a kitsoki story as a quasi-deterministic, **trainable** model of a
  domain: forward pass = running a session, training set = the event log, but the
  "weights" being adjusted are the story's scripts/prompts/workflow graph, not a
  tensor. Subsumes the **training half** of the
  [4-layer self-improvement model](../competitive-analysis/market-research.md):
  L1–L2 (validate+nudge, recycle-to-prior-step) stay as the *adaptive forward
  pass*, L3–L4 (self-patch, cross-run mining) become the trainable model — with
  the existing [`tools/session-mining/`](../../tools/session-mining/README.md)
  ladder as the L4 substrate. Three slices (0/3): the **loss**
  ([`reward-function.md`](reward-function.md), runtime), the **gradient** via
  failure→success credit assignment ([`credit-assignment.md`](credit-assignment.md),
  tracing), and the **optimizer step + validation gate**
  ([`training-loop.md`](training-loop.md), runtime+story).
- [`agent-action-transcripts.md`](agent-action-transcripts.md) — (tracing)
  surface langfuse-grade per-tool-call detail for every agent in the web UI by
  persisting the claude stream-json we already capture (`ClaudeRun.RawEvents`)
  to a per-call **sidecar** keyed by `call_id` — referenced from the trace by a
  pointer only (no detail inlined) — and generalizing it as an optional
  `AskResponse.Transcript` Oracle-interface seam (claude / `local_llm` /
  subprocess). Detailed prior-art survey (Langfuse, OTel GenAI, Phoenix et al.,
  Claude Code jsonl, OSS viewers). Nothing implemented yet.
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
- [`external-project-targeting.md`](external-project-targeting.md) — **epic.**
  Point `dev-story` at a **foreign repo** by filling a small **profile**
  (ticket adapter + doc-template set + placement rule + commit/CI discipline)
  rather than editing the pipeline; fold `prd` into `dev-story` and chain the
  published PRD into the design pipeline (PRD→Design as one walk).
  `constructorfabric/gears-rust` is the worked example (`gears-sdlc`
  PRD/DESIGN docs under `gears/<gear>/docs/`, the copy-me template).
  **Slices #1 (profile substrate), #3 (PRD→Design chain), and #4 (gears-rust
  instance) shipped** — migrated to the
  [dev-story README](../../stories/dev-story/README.md#doc-profile--targeting-an-external-project)
  and the [gears-rust README](https://github.com/constructorfabric/gears-rust/blob/docs/kitsoki-integration/stories/gears-rust/README.md); their child
  proposals are deleted. (#3 also renamed dev-story's "proposal" pipeline to
  the **design** pipeline; per-gear placement shipped as a plain
  `publish_durable_path` + `doc_filename` override, not the `doc_placement`
  enum the children sketched.) The epic stays open to track the one **deferred**
  slice (GitHub integration comes later):
  - [`gh-ticket-adapter.md`](gh-ticket-adapter.md) (runtime, deferred) — a `gh`-backed
    glue provider satisfying the `ticket` interface against GitHub issues.
- [`github-issues-tracker.md`](github-issues-tracker.md) — **epic.** Move
  kitsoki's own bug + feature tracker from the in-repo `issues/*.md` pile to
  **GitHub Issues** on `constructorfabric/Kitsoki` (canonical even from a
  personal fork). **Slices #1–#3 shipped** (the `create` op + conventions, bug
  filing via CLI + the web Report-bug modal with evidence as release assets, and
  the design-pipeline feature publish) — their detail now lives in
  [`hosts.md → host.gh.ticket`](../architecture/hosts.md#hostghticket--github-issues-backed-tracker)
  and the child proposals are deleted. **Slice #4's tooling is shipped**
  (`kitsoki issues migrate` + the `issues/` freeze); only the **cutover** remains
  — the real bulk migration + rebinding `kitsoki-dev` to `host.gh.ticket`. Hard
  cutover; supersedes `bug-sync-proposal.md`. One slice left:
  - [`issues-migration-to-github.md`](issues-migration-to-github.md) (runtime) —
    `kitsoki issues migrate` is shipped + the `issues/` archive frozen; the
    `kitsoki-dev` rebind to `host.gh.ticket` (the cutover) is the deferred last
    step.
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
- [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) — **runtime.** A YAML
  domain model for the early project lifecycle: composable **Features**
  (media / help / tutorials / acceptance criteria at every level) →
  **Proposals** (the spine as data) → **Plans** (tasks with expected files +
  per-file change descriptions) → **TestSpecs** (scenarios tracing back to
  feature acceptance criteria, mapped to harness + fixture + evidence).
  Pure-YAML containers with pinned JSON Schemas, markdown embedded inline or
  via a generalized `!include`, and a deterministic two-layer validation
  (per-file schema + catalog lint: DAGs, refs, coverage). Initial design for
  review; nothing implemented yet.
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
- `oracle-off-ramp.md` — a per-room `oracle_off_ramp:` opt-in: when free text
  maps to no declared intent, hand the turn to an oracle `converse` answer
  instead of rejecting, with no state/world change. **Shipped**; the proposal
  was retired into the narrative docs — see
  [`docs/stories/architecture.md`](../stories/architecture.md) §9,
  [`docs/stories/state-machine.md`](../stories/state-machine.md) §11,
  [`docs/embedded/app-schema.md`](../embedded/app-schema.md) (`OffRampDef`), and
  the runnable [`stories/off-ramp-demo/`](../../stories/off-ramp-demo/).
- `web-text-input-floor.md` — (tui, web) always offer a free-text composer in
  the web UI, even when a `choice:` widget is shown. Closed the biggest gap in
  the [text-only contract](../architecture/transports.md#7-every-story-must-work-text-only)
  and unblocked the oracle off-ramp on the web. **Shipped** as the `showTextFloor`
  free-text floor (`tools/runstatus/src/components/InputBar.vue`); the proposal
  was retired.
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
- [`trace-introspection.md`](trace-introspection.md) — **epic.** Enrich
  `runstatus` trace viewing (inspired by a Langfuse comparison) while leaning
  into the decision-provenance moat: co-equal view modes, decision-first
  detail, recorded decide alternatives, human annotation, and single-call
  operator replay. Nothing implemented yet; decomposed into six slices:
  - [`trace-observation-kinds.md`](trace-observation-kinds.md) (tracing) — a
    derived semantic kind taxonomy over `EventKind` (decision / oracle-call /
    host-call / narration / world-mutation / routing / lifecycle) so every
    consumer badges, colors, and collapses by category; no wire change.
  - [`trace-decision-detail.md`](trace-decision-detail.md) (tui) — hero the
    gate/routing detail with the decision (available → chosen → confidence-vs-
    threshold → reason → bailed) and demote prompt/response to an evidence
    drawer.
  - [`trace-view-modes.md`](trace-view-modes.md) (tui) — co-equal Tree /
    Timeline-waterfall / Graph view modes over the one event stream + a
    sortable/filterable Home triage table (cost / duration / bailed).
  - [`decision-alternatives.md`](decision-alternatives.md) (runtime) — the
    decide verdict gains a ranked `alternatives` list, recorded in
    `gate_decided`; selection stays deterministic (record-only).
  - [`trace-annotation.md`](trace-annotation.md) (tracing) — a read-only
    `trace.annotation` event in a trace-adjacent sidecar; operators score /
    label / comment a gate or turn, making traces a labeled dataset.
  - [`replay-decision.md`](replay-decision.md) (runtime) — `kitsoki
    replay-call`: reconstruct one recorded oracle call from the embedded story
    and re-dispatch it against a different operator / edited prompt, then diff
    the verdict — the pluggable-operator moat made visible.
- [`semantic-routing-proposal.md`](semantic-routing-proposal.md) —
  v1 shipped. The trimmed proposal keeps only open questions and
  the Oregon Trail calibration history. The user-facing reference
  for the shipped surface lives at
  [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md).
- [`embeddings.md`](embeddings.md) — **epic.** All 3 slices shipped. See
  [`docs/architecture/embeddings.md`](../architecture/embeddings.md) (substrate
  + `oracle.search`) and [`docs/architecture/semantic-routing.md`](../architecture/semantic-routing.md)
  §6 (routing tier). Child slice files deleted.
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
- [`ui-fix-story.md`](ui-fix-story.md) — **story.** A new `stories/ui-fix/`
  review→per-group fix pipeline over the `kitsoki-ui-review` skill's
  `verdict.json`: a deterministic dedup (`host.starlark.run`) feeds an
  interpretive **pattern-review** gate (`host.oracle.decide` clusters 371
  findings into ranked root-cause **groups** — never blind iteration), then a
  loop fixes **one group per agent instance** (`host.oracle.task` scoped to
  `tools/runstatus/src/`) with a human diff checkpoint, a no-LLM geometry+axe
  re-audit proving it cleared, and a **before/after slideshow/video per
  group** via the shipped `visual-outputs` media seam (`host.contact_sheet` /
  `host.slidey.render` → `media` element). `done` is a before/after gallery.
  Composes existing hosts only. Nothing implemented yet.
- ~~story-editor-view (epic) + story-graph-api / story-editor-shell /
  oracle-workbench (slices)~~ — **shipped.** The story editor surface
  (`/editor` route, BFS room list, hook / domain-model / typed-view detail,
  meta chat, oracle workbench with cassette browser + isolated replay, reusable
  `StoryViewer.vue`) now lives in narrative docs:
  [`docs/tui/story-editor.md`](../tui/story-editor.md). Proposals deleted.
- [`mockup-video-studio.md`](mockup-video-studio.md) — **epic.** Author UI
  design-proposal walkthrough videos as a recorded process **and** improve
  them in the web UI: flag a scene or time-range, grab the frame, instruct
  the LLM, watch the video re-render. Builds on the `visual-outputs` media
  seam (slices 2–3 are prerequisites). Nothing implemented yet; decomposed
  into three slices:
  - [`video-frame-seam.md`](video-frame-seam.md) (runtime) — a
    producer-agnostic **chapter sidecar** (scene↔timestamp + `source_ref`) +
    a deterministic `host.video.frame` still-grab, backed by one
    `internal/video` extractor shared by a host call and the slice-2 web RPC.
  - [`video-feedback-mode.md`](video-feedback-mode.md) (tui) — a `/review`
    web panel: player + chapter timeline + flag-scene/range + per-flag PNG +
    chat → structured, source-targeted **feedback notes** (capture + dispatch;
    the LLM edit is the story's recorded decision).
  - [`mockup-video-authoring.md`](mockup-video-authoring.md) (story) — a new
    `stories/mockup-video/`: brief → author HTML+tour *or* slidey deck
    (`medium: tour | deck`) → render (chapter sidecar) → review → refine-loop
    on each flag → gallery.
- [`project-init.md`](project-init.md) — **story.** A new **init phase** woven
  into the dev-story hub (`go_init` from `main`, runnable standalone on a fresh
  repo): ask the few preferences it can't infer, deterministically **discover**
  the repo's shape, **mine the project's own transcripts** (the
  [`tools/session-mining/`](../../tools/session-mining/) kit — distinct from
  `dev-story-mining`, which tunes dev-story's *own* gates) to fine-tune the
  loop, then emit a single **schema-validated report** (`project-profile/v1`,
  drafted + proven in `notes/project-profile.schema.json`) of *what it intends to
  set up* — dev server + readiness, frontend/backend, local/dev/staging/prod
  environments, rules, conventions (recommend kitsoki's
  `.context`/`.artifacts`/`.worktrees` or keep the project's own + manage
  `.gitignore`), and the existing testing it integrates with. **Propose-then-
  confirm:** on confirm it compiles the profile to a generated dev-story instance
  (`stories/<id>-dev/`, generalizing the `kitsoki-dev`/`gears-rust` binding),
  adopts conventions, and verifies the loop (boot → readiness → tests →
  golden-path UI). Composes existing hosts only. Nothing implemented yet.
- [`work-decomposition.md`](work-decomposition.md) — **story.** A new
  `stories/decompose/` sub-story imported into dev-story: hand it an accepted
  proposal (or epic + children) and an interactive discovery conversation
  distils scope, an `oracle.decide` emits a brief manifest the MCP submit
  validator structurally enforces, a deterministic `host.run` renders + lints
  it to `decomposition.yaml` (acyclic DAG, coverage), an adversarial
  `oracle.decide` judges feasibility + completeness, and a coordination board
  dispatches each brief into the `impl` import one at a time with a human gate.
  Nothing implemented yet.
- [`hybrid-session-driving.md`](hybrid-session-driving.md) — **runtime.** Let
  `kitsoki web` drive a live session (e.g. `stories/bugfix`) from the browser
  while Jira/Bitbucket keep receiving artifacts write-only. Decouples *driving*
  (inbound intents) from *transport* (output-only): the runstatus server stamps
  an operator identity into `last_reply_author` (so ACL-guarded `continue` stops
  silently no-opping), attaches to the persisted session store loop.py uses (so
  one ticket can be co-driven), and gains an opt-in inbound poll→intent bridge
  for Jira/PR replies. All opt-in; loop.py's existing path unchanged. Nothing
  implemented yet.
- [`line-messenger-channel.md`](line-messenger-channel.md) — **epic.** Make
  LINE a first-class **customer-interaction channel** with kitsoki as the engine
  and **web presence**: a merchant authors a story once, provisions a LINE
  Official Account from the web console, and every customer who messages it gets
  their own session — the first inbound event *creates* one keyed
  `line:<channel>:<src>` (the multi-customer model the engine lacks today), and
  customer free text routes through the existing `internal/semroute`. Builds on
  the inbound bridge + transport registry + external-key store + operator-ask;
  the turn loop is unchanged. Nothing implemented yet; decomposed into four slices:
  - [`line-webhook-ingress.md`](line-webhook-ingress.md) (runtime) — a LINE-signed
    webhook handler + a **get-or-create session factory** (the one novel engine
    concept: an external event with no prior session creates one) that drives raw
    customer text under the writer lock.
  - [`line-transport.md`](line-transport.md) (runtime) — a `transport.Transport`
    for the LINE Messaging API (reply-token fast path + push fallback); typed
    view → text + **room-intents-as-quick-reply-buttons**.
  - [`line-commerce-stories.md`](line-commerce-stories.md) (story) — two copy-me
    examples, `stories/line-store/` (browse → cart → checkout) and
    `stories/line-booking/` (availability → reserve → confirm), composing
    existing hosts only; channel-agnostic YAML.
  - [`line-channel-console.md`](line-channel-console.md) (tui) — the merchant's
    web home: provision a channel (creds + story binding + webhook URL) and
    watch/assist the live customer sessions it spawns (operator-ask inbox).
- [`vscode-extension.md`](vscode-extension.md) — **tui.** Embed the shipped
  runstatus web UI (`docs/tui/web-ui.md`) as a native VS Code surface: chat in
  the sidebar, trace/state diagram in the bottom panel, themed to the editor.
  The extension bundles the SPA in a webview and spawns `kitsoki web` as a child
  process, relaying the existing JSON-RPC/SSE over `postMessage` — backend
  unchanged, one new `BridgeSource` behind the existing `DataSource` factory.
  Distinct from (and complementary to) the inverse `/ide` work
  (`ide-integration.md`). Desktop-only. Nothing implemented yet.

- [`review-externally.md`](review-externally.md) — **epic.** Review kitsoki's
  edits where you actually read them — the IDE or the system diff viewer, not a
  cramped terminal pane. **Slice #2 shipped** (OSC 8 `.md` links + `/open`, now
  in `docs/tui/README.md`); **slice #1 Phase A shipped** (`host.diff.open`:
  connected-IDE accept/reject verdict capture + view-only system-difftool
  fallback, in `docs/architecture/hosts.md`), with its Phase B turn-suspend gate
  and a story adoption still remaining.
  - [`diff-open-fallback.md`](diff-open-fallback.md) — **runtime** (slice #1).
  - tui-md-links — **tui** (slice #2): shipped, file deleted.
