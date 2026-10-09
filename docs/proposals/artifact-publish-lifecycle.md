# Runtime: artifact publish lifecycle — share-draft, publish, disposition, GC

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../artifact-driven-stories.md
**Depends on:** [`artifact-instances.md`](artifact-instances.md) for the keyed
workspace and [`artifact-job-registry.md`](artifact-job-registry.md) for the
durable job identity.

## Why

[`artifact-instances.md`](artifact-instances.md) gives a story a resumable **draft** instance in a
gitignored workspace. The back half of the lifecycle — making the work shareable,
publishing the canonical doc, and then disposing of the artifacts — exists today
only as the design pipeline's bespoke `publish_design.py`:

- It **moves** `005-proposal.md` from `docs/proposals/.workspace/<slug>/` to the
  canonical `docs/proposals/<slug>.md` (collision-safe), mints a feature ticket,
  and honors profile world keys for placement (`design_durable_path`,
  `design_doc_filename`, `design_ticket_dir` — an empty `design_ticket_dir` skips
  the ticket, per the dev-story README).
- The numbered check artifacts (`001`…`004`) are **left in the workspace** as the
  per-proposal record — a single hard-coded disposition, not a choice.

Three gaps follow:

1. **No share-draft.** Publishing emits the *doc*. There is no way to share the
   *full artifact set* — the brief, the prior-art scan, the references, the
   intermediate decisions — with a collaborator while the work is still in
   flight. "Promote my draft so a teammate can re-join it" has no home distinct
   from "publish the finished doc."

2. **Disposition is hard-coded.** A story can't say "after publish, condense the
   workspace to just the brief + final" or "destroy it" or "archive it whole." The
   design pipeline's keep-everything is the only behavior, baked into a script.
   [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md) names exactly this durable/
   transient split (Features/TestSpecs accumulate; Proposals/Plans are trimmed)
   but has no mechanism to enact it.

3. **Nothing reclaims archives.** Kept artifacts accrue forever with no retention
   policy and no way to enumerate-and-reclaim — the data
   [`artifact-instance-console.md`](artifact-instance-console.md) needs to warn
   on.

## What changes

A story's `artifacts:` spec gains lifecycle policy, and the engine gains the
promotion + disposition + GC host calls to enact it. One sentence: *promoting a
draft is two distinct moves — **share** (copy the full artifact set to a shared
workspace) and **publish** (render/place the canonical doc) — and after publish
the story's declared **disposition** decides whether the artifacts are kept,
condensed, or destroyed, under a retention policy that GC enforces.*

## Impact

- **Code seams:** generalize `stories/dev-story/scripts/publish_design.py` into an
  `iface.instance.promote` / `.publish` / `.dispose` host surface backed by
  `internal/instance/` (slice 1); a `instance.gc` enumerate+reclaim call. The
  doc *render* on publish reuses the artifact-format `Render` path
  ([artifact-format.md](artifact-format.md)) where a data-primary artifact is the
  source.
- **Vocabulary:** lifecycle fields on `artifacts:` + the promote/publish/dispose/gc
  host calls + world keys (table below).
- **Stories affected:** the design pipeline migrates its publish room onto
  `iface.instance.publish` with `disposition: archive_as_is` (today's behavior),
  proving parity; profile world keys are passed through unchanged.
- **Backward compat:** opt-in with today's behavior as the default —
  `disposition` defaults to `archive_as_is` (keep the workspace, as the design
  pipeline does now), so a story that only adopts publish sees no surprise.
- **Docs on ship:** the lifecycle half of `docs/stories/artifact-driven-stories.md`;
  a note in `docs/stories/state-machine.md` on the new host calls.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| spec field | `artifacts.share_root` | `path` | shared-workspace location (committed/shared), distinct from the gitignored draft `workspace_root` |
| spec field | `artifacts.publish` | `{path, filename?, render?}` | canonical doc location; `render` names the data-primary artifact to render, else moves a body artifact |
| spec field | `artifacts.disposition` | `archive_as_is \| condense \| destroy` | post-publish fate; `condense` keeps items flagged `keep: true` |
| spec field | `artifacts.retention` | `{max_count?, max_age?, max_bytes?}` | archive GC policy; drives slice-3 warnings |
| host call | `iface.instance.promote` | `{instance_id} → {share_path}` | draft → shared: copies the full artifact set to `share_root` |
| host call | `iface.instance.publish` | `{instance_id} → {doc_path, ...}` | renders/places the canonical doc; supersedes `publish_design.py` |
| host call | `iface.instance.dispose` | `{instance_id, disposition} → {result}` | applies disposition after publish |
| host call | `instance.gc` | `{retention} → [{instance_id, reclaimable, size, age}]` | enumerate archives breaching policy (dry-run); `apply: true` reclaims |

## The model

```
draft (gitignored .workspace/<key>/)              ← slice 1 owns this
   │
   ├─ share ─▶ shared workspace (share_root, full artifacts)   ──┐ collaborate / re-join from a shared location
   │                                                             │
   └─ publish ─▶ canonical doc (publish.path)  ──after publish──▶ disposition:
                                                                    archive_as_is → keep workspace under archive/
                                                                    condense      → keep items{keep:true}, drop the rest
                                                                    destroy       → remove the workspace

archive/  ──instance.gc(retention)──▶ list breaching {age|count|bytes} ──▶ (slice 3 warns) ──apply──▶ reclaim
```

**Share vs publish are independent moves**, not stages: an instance can be shared
without being published (work-in-progress collaboration) and published without a
prior share (solo author). Both are deterministic file operations; the only
interpretive step a `condense` might want (an LLM summary of the workspace) is the
*story's* recorded decision, not engine machinery — keep `condense` mechanical
(keep-flagged items) in v1 and leave LLM summarization to the story.

## Decision recording

Promotion, publish, and disposition are deterministic — record them as
`instance.lifecycle` datapoints (the event slice 1 introduces), carrying
`{instance_id, action: share|publish|dispose, paths, disposition}` so a run's
"where did the artifacts go" is reconstructable from the trace. GC reclaim is an
operator action ([artifact-instance-console.md](artifact-instance-console.md)); record the
enumerate-and-reclaim as a lifecycle datapoint too. No new interpretive decision.

## Engine seams & invariants

Same posture as slice 1 — a library behind host handlers, no turn-loop stage.
Invariants:

1. `disposition: destroy` must refuse to run **before** a successful publish *or*
   share (don't silently delete unpublished, unshared work) — fail the host call
   with a clear error, the `ticket.*` error convention
   (`internal/host/localfiles_ticket.go`).
2. `publish.path` outside the gitignored draft `workspace_root` (you can't publish
   into the scratch dir); `share_root` must differ from `workspace_root`.
3. `condense` with no item marked `keep: true` is a load-time error (it would
   destroy everything — the author meant `destroy`).

## Backward compatibility / migration

The design pipeline is the parity proof: its publish room moves from
`host.run python publish_design.py` to `iface.instance.publish` +
`dispose{archive_as_is}`, passing the existing profile world keys
(`design_durable_path` etc.) straight through. Default `disposition:
archive_as_is` reproduces today's "leave the checks in the workspace" behavior, so
the migration is observable-equivalent and the existing publish flow fixture
passes unchanged. `instance.gc` is new and inert until a `retention` policy is
declared.

## Tasks

```
## 1. Engine
- [ ] 1.1 iface.instance.promote (draft → share_root, full copy)
- [ ] 1.2 iface.instance.publish (render data-primary via artifact-format, or move body artifact; profile-key placement) — supersedes publish_design.py
- [ ] 1.3 iface.instance.dispose (archive_as_is | condense{keep:true} | destroy) + the safety invariants
- [ ] 1.4 instance.gc (enumerate by retention; dry-run default, apply:true reclaims)
- [ ] 1.5 instance.lifecycle datapoints for share/publish/dispose/gc

## 2. Verification
- [ ] 2.1 kitsoki turn: publish places the doc at the profile path; archive_as_is keeps the workspace
- [ ] 2.2 Flow fixture: condense drops non-keep items, keeps flagged ones; destroy-before-publish is refused
- [ ] 2.3 instance.gc unit: retention by age/count/bytes selects the right instances; dry-run reclaims nothing

## 3. Adopt + document
- [ ] 3.1 Migrate the design publish room onto iface.instance.publish + dispose{archive_as_is}; delete publish_design.py's moved logic
- [ ] 3.2 Document the lifecycle half of docs/stories/artifact-driven-stories.md
```

## Verification

No LLM. The promote/publish/dispose/gc host calls are exercised by
`kitsoki turn` probes and intent-only flow fixtures over a seeded on-disk
workspace — assert the doc lands at the profile path, that each disposition leaves
the expected files, that `destroy` is refused pre-publish, and that `instance.gc`
selects exactly the retention-breaching instances. The migrated design publish
room is covered by the existing publish fixture (agent phases mocked).

## Open questions

1. **`condense` mechanics.** Keep-flagged file subset (mechanical, v1) vs an
   LLM-authored summary artifact. *Lean: mechanical keep-set in v1; an LLM
   condense is a story-recorded decision the story can add later.*
2. **Shared workspace location.** A committed in-repo path vs a separate shared
   tree vs a transport-backed remote. *Lean: a committed/shared filesystem path
   for v1 (cross-cutting open question #2 in the epic).*
3. **GC trigger.** On story entry, on a schedule, or only operator-initiated from
   slice 3. *Lean: compute on entry + on demand (cheap), reclaim only on operator
   confirm — never auto-delete.*

## Non-goals

- **No instance/resume substrate here.** That's
  [`artifact-instances.md`](artifact-instances.md); this slice consumes its
  instance concept.
- **No operator surface here.** The warn/delete UI is
  [`artifact-instance-console.md`](artifact-instance-console.md); this slice provides the `instance.gc`
  data it renders.
- **No artifact file format.** Rendering on publish reuses
  [artifact-format](artifact-format.md)'s `Render`; this slice doesn't define it.
