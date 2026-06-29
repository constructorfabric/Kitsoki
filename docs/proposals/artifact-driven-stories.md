# Epic: artifact-driven stories (resumable instances, draft → publish → archive)

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 3 (0/3 shipped)

## Why

Several kitsoki stories already *are* artifact-driven: each phase hands an
agent/host output straight to disk as a named, shareable yaml/markdown file,
and the run-through is really the accretion of those files. The **design
pipeline** (`stories/dev-story/rooms/design*.yaml`) is the worked example — it
materializes a per-run workspace at `docs/proposals/.workspace/<slug>/`
(`stories/dev-story/scripts/design_workspace.py`), writes numbered check
artifacts into it as they're produced (`001-brief` … `005-proposal.md`, each via
`host.artifacts_dir` keyed by `thread:`), then promotes the final one to the
canonical `docs/proposals/<slug>.md` and mints a feature ticket
(`stories/dev-story/scripts/publish_design.py`).

But that whole lifecycle is **hand-rolled per story**, and three pieces the
pattern needs are missing or ad hoc:

1. **Resume is not real.** A design run creates a *fresh* workspace every time;
   there is no "you already have a draft for this — re-join it?" The only
   get-or-create-an-instance machinery we have is meta-mode's chat resolve, keyed
   `(AppID, room, scopeKey)` (`docs/stories/meta-mode.md` §8) — a different
   subsystem, not reusable by an ordinary story. Work that spans days or several
   people has nowhere to land.

2. **The draft → shared → published → archived lifecycle is implicit.** "Draft
   artifacts in a gitignored scratch dir" (`.gitignore:63`) vs "the published
   doc in a canonical location" exists only as `publish_design.py` move logic.
   There is no notion of *sharing* a draft (promoting the full artifact set to a
   collaborative location, distinct from publishing the doc), and no
   story-declared **disposition** for what happens to the artifacts after
   publish — keep them as-is, condense them, or destroy them.

3. **Nothing reclaims old instances.** Archived/leftover workspaces accrue with
   no retention policy and no surface that warns "these are big/old — delete
   them." The `workspace_manager` room (`stories/dev-story/rooms/workspace_manager.yaml`)
   is an explicit Wave-2 stub with cleanup deferred.

This epic formalizes "artifact-driven story" as a reusable story capability so a
story author declares its artifacts and their lifecycle once, and gets
resumable instances, the share-draft/publish split, post-publish disposition,
and GC for free — instead of re-deriving the design pipeline by hand.

## What changes

Once every slice ships, a story can declare an **artifact spec** — the named,
schema'd artifacts each phase produces, and what becomes of them after publish —
and the engine gives it:

- **Instances.** A run-through is a first-class *instance*: a workspace of the
  spec's artifacts, keyed by a story-declared identity. Each phase persists its
  agent/host output to the workspace **as soon as it's produced** (a schema'd
  [artifact-format](artifact-format.md) file); the world carries only a handle,
  the on-disk artifact is the source of truth (the
  recorded-media rule documented in
  [`story-style.md`](../stories/story-style.md#37-media--showing-a-recorded-artifact).
- **Resume + update mode.** On entry the story discovers existing in-progress
  instances and offers **re-join**; the operator can step *back* to an earlier
  phase, edit it, and **re-run** the pipeline in update mode — downstream
  artifacts are marked stale and re-derived.
- **A three-location lifecycle.** A **draft** instance lives in a private,
  gitignored workspace; **share draft** promotes the full artifact set to a
  shared workspace (collaboration, distinct from publishing); **publish** emits
  the canonical doc. After publish the story's declared **disposition** —
  `archive_as_is | condense | destroy` — decides the artifacts' fate.
- **GC with warnings.** Archived instances carry a retention policy; the TUI and
  web surface them, warn when they grow big or old, and offer one-click delete.

## Impact

- **Spans:** runtime (×2: the instance/resume substrate, then the
  promote/disposition/GC half) + tui (the resume picker + instance manager).
- **Net surface:** a new `artifacts:` story block + a small instance/workspace
  package, a promotion + GC host surface, and one operator surface (TUI + web)
  for re-join and cleanup. No change to the turn loop. Opt-in: a story without
  `artifacts:` behaves exactly as today.
- **Builds on (does not duplicate):** [`artifact-format.md`](artifact-format.md)
  owns the per-file schema/round-trip; [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md)
  owns the domain-model containers (Features/Proposals/Plans/TestSpecs) — this
  epic is the *orchestration* layer that produces and manages instances of
  artifacts that may be either format; the shipped media substrate in
  [`host.artifacts_dir`](../architecture/hosts.md#hostartifacts_dir) and
  [`artifact.emitted`](../tracing/trace-format.md#artifact-event-kind) owns
  recorded media handles, whose "artifact-of-record, not world" rule we inherit.
- **Docs on ship:** a new `docs/stories/artifact-driven-stories.md` (the
  author-facing pattern, with the migrated design pipeline as its example),
  cross-linked from `docs/stories/authoring.md`, `docs/stories/state-machine.md`,
  and `docs/tui/`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | instances + resume | runtime | `artifacts:` spec, the keyed workspace instance, discover/re-join, back-step + update-mode re-run | — | Draft | [`artifact-instances.md`](artifact-instances.md) |
| 2 | publish lifecycle | runtime | share-draft vs publish promotion, post-publish disposition (`archive_as_is`/`condense`/`destroy`), archive GC | 1 | Draft | [`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md) |
| 3 | instance console | tui | resume picker + instance manager (list draft/shared/archived, size/age warnings, delete) | 1 (re-join), 2 (warn/delete) | Draft | [`artifact-instance-console.md`](artifact-instance-console.md) |

## Sequencing

```
#1 instances + resume (runtime) ──▶ #2 publish lifecycle (runtime) ──▶ #3 console (tui, full)
        └──────────────────────────────────────────────────────────▶ #3 console (re-join only)
```

Slice 1 is the substrate every other slice needs (the instance concept). Slice 2
adds the back half of the lifecycle on top of it. Slice 3 can land its **re-join
picker** as soon as #1 ships (it only needs instance discovery) and gains the
**warn/delete** affordances once #2's disposition + GC exist — so #3 may ship in
two parts straddling #2.

## Shared decisions

These span slices; children defer here rather than re-litigating.

1. **Workspace location & gitignore.** Each story declares a `workspace_root`;
   the private draft workspace is a gitignored `.workspace/<key>/` sibling of the
   canonical publish location — generalizing what the design pipeline does at
   `docs/proposals/.workspace/<slug>/` (`.gitignore:63`). Home-anchored
   (`~/.kitsoki/…`, `internal/store/trace_path.go`) is for *session traces*, not
   artifact workspaces; the two stay distinct.
2. **Instance identity = get-or-create on a story-declared key.** Default to a
   slug derived from a phase-one field; resolve with the same get-or-create
   semantics `chats.Store.Resolve` gives meta-mode (`internal/chats/`). Same key →
   re-join; new key → new instance.
3. **Artifact-of-record, not world.** Each phase persists its output to the
   workspace immediately; the world holds only a handle/pointer. This follows
   the same rule as recorded media artifacts:
   [`artifact.emitted`](../tracing/trace-format.md#artifact-event-kind) is the
   durable record and world carries only a handle.
4. **One file format, not a new one.** Every artifact is an
   [artifact-format](artifact-format.md) file (frontmatter+body or data-primary);
   this epic never re-defines the file shape, it only decides *where files live
   and when they move*.

## Cross-cutting open questions

1. **Relationship to lifecycle-taxonomy.** Are a story's per-phase artifacts the
   taxonomy containers (Proposal/Plan/TestSpec) or arbitrary story-defined kinds?
   *Lean: story-defined kinds; the taxonomy is one consumer that happens to
   declare its containers as the artifacts.*
2. **What "shared workspace" physically is.** A committed git location, or a
   remote/transport-backed store? *Lean: a committed (or otherwise shared)
   filesystem location for v1; remote sharing is later work.*
3. **Update-mode invalidation.** When the operator edits phase N, do we auto-rerun
   N+1…M, or mark them stale and re-run on confirm? *Lean: mark stale + prompt,
   re-run on confirm — never silently discard downstream work.*

## Non-goals

- **Not the artifact file format.** [`artifact-format.md`](artifact-format.md)
  owns schema + lossless round-trip.
- **Not the domain-model taxonomy.** [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md)
  owns Features/Proposals/Plans/TestSpecs.
- **Not real-time co-editing.** Collaboration here is *over time* via shared
  drafts and re-join, not simultaneous multi-cursor editing of one instance.
- **Not a media producer.** `host.slidey.render`, `host.contact_sheet`, and
  `host.artifacts_dir` media emit own generating and recording MP4/PNG
  artifacts; this epic only places and dispositions them.
