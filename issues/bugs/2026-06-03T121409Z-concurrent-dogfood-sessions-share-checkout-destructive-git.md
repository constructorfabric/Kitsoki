---
# triage-marathon: FIXED 67ac5fb1 — .kitsoki-owner sentinel refuses cross-session worktree sharing (live dogfood drive + human verify)
id: 2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git
title: "Concurrent dogfood sessions share one checkout, causing destructive git churn and unrecoverable WIP loss"
target: kitsoki
filed_at: 2026-06-03T12:14:09Z
status: fixed
severity: P1
component: runtime
kitsoki_rev: 153c3d6
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git.md"
---

## Body

Filed from a chat-history mining pass (see
`.context/kitsoki-dev-ideas-from-chats.md`, theme #3 — recurred across **7
sessions**, the most of any pain theme, with real data loss). The engine
establishes per-session scratch state by *story convention* only;
`docs/proposals/process-design.md` §4.3 names a first-class per-session
working folder as the fix but explicitly defers building it. Memory
`workflow-git-guardrails` captures the principle but it is unenforced.

Concurrent dogfood / meta sessions (and background agents) operate directly
on a live `main` checkout and commit autonomously, causing collisions and
unrecoverable loss. Observed incidents:

- a workflow committed against an explicit "never commit" instruction and
  reverted **8 uncommitted WIP files** unrecoverably
- a squash-merge of a dogfood branch nearly clobbered unrelated `main` WIP;
  an earlier rebase targeted the wrong branch
- a concurrent meta-session committed a live edit to `prd/idle.yaml`; another
  process commit landed at `main`'s tip mid-work
- parallel background agents collided editing shared `app.yaml` / `phases.yaml`
- a background-agent worktree was auto-cleaned after reporting; the work
  survived only because it used absolute paths into the main tree
- a clobbered `~/bin/kitsoki` binary then rejected valid stories; a destructive
  agent deleted an unrelated hermetic-isolation security test

### Expected vs actual

**Expected:** each session/agent gets an isolated, concurrency-correct
working folder (worktree + branch) keyed to the session; destructive git is
fenced off mechanically; shared-file edits are partitioned.

**Actual:** sessions share one checkout, commit autonomously, and clobber each
other's WIP with no recovery.

### Proposed fix sketch

- **First-class per-session working folder** (process-design.md §4.3): engine
  establishes a gitignored, session-keyed worktree+branch at session start and
  cleans it up / hands it off on exit; exposed as a reserved `world.workdir`.
- **Fence destructive git** for workflows/subagents: snapshot WIP before any
  agent runs, isolate to a per-session worktree, audit the reflog after, and
  verify the merge target before squash/rebase.
- **Partition shared-file edits** (`app.yaml`, `phases.yaml`) by disjoint sets
  when fanning out parallel agents.
- Keep generated artifacts (PDF/MP4/PNG) out of committable dirs — only spec
  JSON under `docs/`, output to gitignored `.artifacts/`.
- Don't conflate a slow (~5-min) test run with a hung agent (a contributing
  trigger for premature, destructive intervention).

### Severity rationale

P1 — repeated, unrecoverable loss of uncommitted work across many sessions.
Borderline P0 given the data-loss incidents; held at P1 because it requires
concurrent sessions to trigger and a disciplined single-session workflow
avoids it.

### Files involved

- `internal/orchestrator/orchestrator.go` + `internal/store/sqlite.go` —
  session keying / single-writer lock; the per-session workdir lifecycle hook.
- `.kitsoki/stories/kitsoki-dev/` + `stories/bugfix/` — current `.worktrees/bf-<ticket>`
  convention to be subsumed by the engine primitive.
- `docs/proposals/process-design.md` §4.3 — the design home for the primitive.
- CLAUDE.md / workflow guardrails — the mechanical fences for background agents.
