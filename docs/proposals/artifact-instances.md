# Runtime: artifact-driven instances — keyed workspaces, resume, update-mode

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../artifact-driven-stories.md
**Depends on:** [`artifact-job-registry.md`](artifact-job-registry.md) for the
durable job identity this workspace attaches to.

## Why

The design pipeline persists each phase's output to a per-run workspace and reads
it back later, but the workspace plumbing is hand-rolled and **one-shot**:

- `stories/dev-story/scripts/design_workspace.star` mints
  `docs/proposals/.workspace/<slug>/` fresh; the room calls it `once: true` keyed
  on `world.design_workspace` so a `/reload` doesn't re-mint — but there is no
  lookup of a workspace that *already existed before this run*.
- Each artifact is written by `host.artifacts_dir` with a `thread:` prefix
  (`001-brief`, `002-existing-state`, … `005-proposal.md`) into that workspace —
  the "persist the processed representation as soon as the agent produces it"
  discipline, done by convention per room.
- `design_search.yaml` detects an *overlapping published proposal* and lets the
  operator `change_existing`, binding `design_change_target`; `publish_design.py`
  honors a `change_target` arg to rewrite in place. That is the **only** seed of
  "update an existing thing" — and it keys off the published doc, not a resumable
  in-progress instance.

So an operator who started a design yesterday, or a teammate picking it up, has
no way to find the half-finished workspace and re-join it, and no way to step
back to the brief, fix it, and re-run the later phases. Meta-mode solved exactly
this for *chats* — `(AppID, room, scopeKey)` get-or-create resolve, resumable and
archivable (`docs/stories/meta-mode.md` §8, `internal/chats/`) — but that
machinery is bolted to meta-mode and unavailable to an ordinary story.

## What changes

A story declares an **artifact spec**, and the engine treats a run-through as a
keyed **instance**: a workspace of that spec's artifacts that can be discovered,
re-joined, and re-run. One sentence: *an artifact-driven story phase persists its
output to a keyed workspace the moment it's produced, and the workspace is a
resumable, get-or-create instance — not a fresh scratch dir each run.*

This slice owns the **workspace front half** of the lifecycle: declare →
instance → persist → discover → re-join → back-step/update-mode. Promotion and
disposition (share/publish/archive/GC) are
[`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md); the operator
surface is [`artifact-instance-console.md`](artifact-instance-console.md).

## Impact

- **Code seams:** a new `internal/instance/` (workspace resolve/list/state) modeled
  on the get-or-create shape of `internal/chats/` `Store.Resolve`; an
  `iface.instance.*` host surface; load-time validation in the story loader.
- **Vocabulary:** a story-level `artifacts:` block + `iface.instance.resolve` /
  `.list` host calls + a few world keys (table below). No new effect verbs.
- **Stories affected:** none change behavior unless they adopt `artifacts:`. The
  design pipeline (`stories/dev-story/rooms/design*.yaml`) migrates onto it as the
  worked example (task 3.1) — `design_workspace.star`'s mint-only logic becomes a
  `resolve` (get-or-create), and `design_search`'s amend path becomes generic
  update-mode.
- **Backward compat:** opt-in. A story with no `artifacts:` block is unchanged;
  existing cassettes/flows for the design rooms keep passing because the artifact
  *writes* still go through `host.artifacts_dir` — only the workspace *resolution*
  changes from mint to get-or-create.
- **Docs on ship:** `docs/stories/artifact-driven-stories.md` (new),
  cross-linked from `docs/stories/authoring.md` and `docs/stories/state-machine.md`.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| story block | `artifacts:` | `{ workspace_root, key, items: [{name, phase, schema?, kind?}] }` | declares the spec; `key` is an expression over the world (default a slug) |
| host call | `iface.instance.resolve` | `{key} → {instance_id, workspace, state, phase, is_new}` | get-or-create by key; `is_new=false` ⇒ a draft already existed |
| host call | `iface.instance.list` | `{} → [{instance_id, key, state, phase, updated_at, size_bytes}]` | discover instances for this story (drives the re-join gate + slice 3) |
| world key | `instance` | `{id, workspace, key, state, phase}` | bound by `resolve`; the on-disk workspace is source of truth, this is the handle |
| world key | `instance_phase_stale` | `[string]` | phases marked stale by a back-step; the re-run gate consumes it |

## The model

```
enter story
  └▶ iface.instance.list ──▶ [resume gate]
         ├─ in-progress instance(s) exist ──▶ re-join? ──yes─▶ resolve(existing key) ──▶ jump to instance.phase
         └─ none / decline ─────────────────▶ resolve(new key) ──▶ phase 1

each phase P:
  agent/host produces output
     └▶ host.artifacts_dir {thread: items[P].name, workdir: instance.workspace}   (persist ASAP — already how design rooms write)
     └▶ artifact-of-record on disk; world.instance unchanged except phase pointer

back-step to phase N (update mode):
  mark phases N+1…M as instance_phase_stale
     └▶ re-enter N, edit, re-run ──▶ [stale gate] re-derive each stale downstream artifact (prompt before discard — shared decision #3)
```

Interpretive vs deterministic: **resolve/list/persist/back-step are
deterministic** — pure file + key operations, fully replayable. The only
interpretive steps are the phases' own agent decisions, which are unchanged and
already recorded. This slice adds no new LLM call site and must not.

## Decision recording

Instance lifecycle transitions are deterministic facts, but they're worth a trace
datapoint so a run's instance history is reconstructable: emit a lightweight
`instance.lifecycle` event on `resolve` (new vs re-join), back-step, and
update-mode re-run, carrying `{instance_id, key, from_phase, to_phase, reason}`.
Artifact *writes* already emit `journal.ArtifactEvent` / `KindArtifactEmitted`
(`internal/journal/`) — reuse that, don't duplicate it. If `instance.lifecycle`
needs a new `EventKind`, that's a small tracing concern; note it for the consumer
([artifact-instance-console.md](artifact-instance-console.md) and runstatus) but it adds no
interpretive decision to the moat.

## Engine seams & invariants

`internal/instance/` is a library called from the `iface.instance.*` host
handlers, not a new turn-loop stage (same posture as `internal/artifact/` in
[artifact-format.md](artifact-format.md)). Load-time invariants (fail fast at
story load, not mid-run):

1. Every `artifacts.items[].phase` must name a real state in the story.
2. `artifacts.key` must be a resolvable world expression; absent, default to a
   slug of the first item's phase output.
3. If an item carries a `schema:`, it must resolve in the artifact-format
   registry (depends on that proposal's registry; until then `schema:` is advisory).

## Backward compatibility / migration

Opt-in and mechanical. The design pipeline is the migration proof:
`design_workspace.star`'s mint becomes `iface.instance.resolve` (get-or-create, so
re-entry re-joins instead of minting a second `<slug>/`); the `design_search` →
`design_change_target` amend path becomes the generic back-step/update-mode gate.
The numbered-artifact writes are untouched. Existing flow fixtures for the design
rooms keep passing; a new fixture covers the re-join path (an instance that
already exists on disk).

## Tasks

```
## 1. Engine
- [ ] 1.1 internal/instance: Resolve(key) get-or-create + List() over a workspace_root, state/phase from on-disk artifacts
- [ ] 1.2 `artifacts:` story block + load-time invariants (phase exists, key resolvable, schema resolvable)
- [ ] 1.3 iface.instance.resolve/.list host handlers; bind world.instance
- [ ] 1.4 Back-step + instance_phase_stale: re-enter phase N, mark N+1…M stale; stale gate re-derives on confirm
- [ ] 1.5 instance.lifecycle trace datapoint (resolve/back-step/re-run); reuse ArtifactEvent for writes

## 2. Verification
- [ ] 2.1 kitsoki turn: resolve creates a workspace; second resolve of same key re-joins (is_new=false)
- [ ] 2.2 Flow fixture: enter story with a pre-existing on-disk instance → re-join gate fires → jumps to saved phase
- [ ] 2.3 Flow fixture: back-step to phase 1, edit, re-run → downstream artifact re-derived; legacy (no-artifacts) story unaffected

## 3. Adopt + document
- [ ] 3.1 Migrate stories/dev-story design rooms onto artifacts:/iface.instance (mint→resolve, amend→update-mode)
- [ ] 3.2 Write docs/stories/artifact-driven-stories.md (pattern + design-pipeline example); cross-link authoring.md, state-machine.md
```

## Verification

No LLM. `kitsoki turn --state … --intent … --world @w.json` probes the
resolve/list/back-step host calls directly. The re-join and update-mode paths are
intent-only **flow fixtures** seeded with an on-disk workspace fixture (artifacts
already present), asserting the resume gate routes to the saved phase and that a
back-step marks the right phases stale. The migrated design rooms are then
exercised by their existing cassette-backed fixtures unchanged (the agent phases
are mocked, per CLAUDE.md — no real LLM).

## Open questions

1. **Key derivation default.** Slug of phase-one title vs a required explicit
   `key:` expression. *Lean: default to a slug, allow an explicit expression —
   matches how `<slug>` already names the design workspace.*
2. **Workspace scan cost.** `List()` reads each workspace's state from disk; for
   many instances that's N stat-walks. *Lean: cheap for v1 (tens of instances);
   add a per-workspace `instance.yaml` state cache only if it bites.*
3. **Where stale lives.** `instance_phase_stale` in the world (replayable) vs a
   field in the on-disk instance state. *Lean: world, so replay reconstructs it;
   revisit if it must survive across sessions.*

## Non-goals

- **No promotion/publish/archive here.** Draft → shared → published →
  disposition is [`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md).
- **No operator surface here.** The re-join picker and instance manager are
  [`artifact-instance-console.md`](artifact-instance-console.md); this slice only
  exposes the host calls they drive.
- **No durable run URL/index here.** The artifact job registry and trace service
  own session attachment, run URLs, and by-handle artifact serving.
- **No new artifact file format.** Writes stay on `host.artifacts_dir` /
  [artifact-format](artifact-format.md).
