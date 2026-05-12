# Proposals

Design documents for kitsoki features that are **partially or not
yet implemented**.

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
  or in topic subfolders like `docs/background-jobs/`. A proposal
  whose ideas have shipped is stale planning material — it does
  not belong here.
- **Fully-resolved planning history.** When everything in a
  proposal has shipped or been superseded, the file moves to
  [`docs/archive/`](../archive/) so the history stays
  discoverable without cluttering the in-flight queue.

## Every proposal carries a status line

The opening lines tell the reader what's implemented, what isn't,
and where to find the shipped pieces. Examples:

> **Status:** Draft v1. Nothing implemented yet.

> **Status:** v1 trimmed. Three of five surfaces shipped (see
> `docs/developer-guide.md` §6); two remain in design.

> **Status:** Draft v3. Refactored against `internal/chats/` after
> review; spike required (§0) before phase A.

## Lifecycle

1. A proposal lands here as a draft, with a status line that says
   "not implemented."
2. As implementation progresses, the proposal author migrates the
   implemented sections into normal `docs/`, trims the proposal,
   and updates the status line.
3. When everything in the proposal has shipped (or been fully
   superseded), the file moves to `docs/archive/`.

The goal: `docs/proposals/` stays a **small, current queue** of
what's being worked toward — not a graveyard of what was once
thought.

## Current proposals

- [`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) —
  two remaining AI-collaborator surfaces (`kitsoki drive`,
  per-state `loading_view`). Most of v1 shipped.
- [`story-imports-proposal.md`](story-imports-proposal.md) —
  namespaced cross-repo composition via an `imports:` block on
  AppDef. Supersedes the sub-room composition sketch that lived
  in the bugfix-room proposal.
- [`claude-code-sessions-proposal.md`](claude-code-sessions-proposal.md) —
  chats PTY mode, input queue, and multi-transport drive,
  extending `internal/chats/`. Validation spike required before
  phase A.

## Archive

Fully shipped or superseded proposals live in
[`docs/archive/`](../archive/). Notable entries:

- `background-jobs-proposal.md` — fully shipped; see
  [`docs/background-jobs/`](../background-jobs/) for the
  canonical guide.
- `bugfix-room-proposal.md` — §3-§7 shipped (see
  `docs/transports.md`, `docs/state-machine.md` §9-§10,
  `docs/hosts.md`, `internal/store/external_keys.go`); §8
  (sub-room composition) superseded by `story-imports-proposal.md`.
