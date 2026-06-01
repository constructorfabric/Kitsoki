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
  two remaining AI-collaborator surfaces (`kitsoki drive`,
  per-state `loading_view`). The other three v1 surfaces shipped
  and are documented in `docs/architecture/developer-guide.md` §6.
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
- [`prd-design.md`](prd-design.md) — a standalone PRD-authoring
  operator story (`stories/prd/`). Early draft; untracked work in
  progress.
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
- [`semantic-routing-proposal.md`](semantic-routing-proposal.md) —
  v1 shipped. The trimmed proposal keeps only open questions and
  the Oregon Trail calibration history. The user-facing reference
  for the shipped surface lives at
  [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md).
