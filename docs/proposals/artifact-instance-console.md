# TUI: artifact instance console — resume picker + archive cleanup

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui
**Epic:**   ../artifact-driven-stories.md
**Depends on:** slice 1 (`iface.instance.list` / re-join — [`artifact-instances.md`](artifact-instances.md)); slice 2 (`instance.gc` / disposition — [`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md)) for the warn/delete half

## Why

Slices 1–2 make a story's instances resumable and disposable, but the operator
can't *see* them. The discovery machinery exists only as CLI shadows of meta-mode
(`kitsoki chat list --room … --all-status --scope …`, `docs/stories/meta-mode.md`
§8) — there is no visual "you have a half-finished draft for this — re-join it?"
and no surface that says "these archived instances are big/old — delete them."

The `workspace_manager` room (`stories/dev-story/rooms/workspace_manager.yaml`) is
an explicit Wave-2 stub: it renders the *current* workspace and calls
`iface.workspace.list`, with cleanup deferred. The web home
(`SessionRegistry`, `docs/web/README.md`) lists live sessions but knows nothing
about an instance's lifecycle state or the archive that's accumulating behind it.

Without this surface, resume and GC are real in the engine but invisible in the
product — the operator never re-joins a draft and never reclaims an archive.

## What changes

One operator surface — rendered in the TUI and the web from the same typed-view
data — that (1) on entering an artifact-driven story shows a **resume picker** of
existing instances, and (2) provides an **instance manager** that lists
draft/shared/archived instances with size + age, **warns** when archives breach
the story's retention policy, and offers a guarded **delete**.

## Impact

- **Code:** promote `stories/dev-story/rooms/workspace_manager.yaml` from stub to a
  real instance-manager room driven by `iface.instance.list` + `instance.gc`
  (slices 1–2); a typed-view list element for instances; web renders the same view
  via the existing `DataSource`/runstatus path (`docs/web/README.md`).
- **Rendering:** a typed `instance_list` view element (rows: key, state badge,
  phase, age, size, a "⚠ stale" flag when retention is breached) — rendered via
  typed elements + pongo2, **never** hand-rolled Go strings. The resume picker is
  the same element scoped to in-progress instances with a re-join action per row.
- **Input:** intents `resume <key>` / `delete <key>` (typed-slot parsed); a
  `/instances` slash command to open the manager from anywhere in an
  artifact-driven story.
- **Docs on ship:** the operator-surface section of
  `docs/stories/artifact-driven-stories.md`; a note in `docs/tui/`.

## Mental model

"Your drafts and your archive, for this story." Entering the story is like opening
a doc app that asks *resume where you left off, or start fresh?*; the manager is
the "manage storage" screen that flags what's safe to clean up.

## Layout

```
Resume picker (story entry):          Instance manager (/instances):
┌──────────────────────────────┐      ┌───────────────────────────────────────┐
│ Resume a draft?               │      │ Instances · line-channel-console        │
│  ▸ line-channel-console       │      │  draft   line-channel-console  P3  2d   │
│      phase 3 · 2 days ago     │      │  shared  web-bug-report        ✓   5d   │
│  ▸ web-bug-report             │      │  archive q3-migration   ⚠ 90d · 240 MB  │
│      phase 5 · 5 days ago     │      │  archive old-spike      ⚠ 120d · 1.1 GB │
│  + start a new one            │      │                                         │
└──────────────────────────────┘      │  ⚠ 2 archives past retention — delete?  │
                                       └───────────────────────────────────────┘
```

## Rendering changes

A single typed `instance_list` element backs both views; the picker is it scoped
to non-terminal states with a per-row `resume` action, the manager is the full set
with `state`, `age`, `size`, a retention `⚠` flag, and a per-row `delete` action.
A summary banner ("N archives past retention — delete?") renders when
`instance.gc` (dry-run) returns reclaimable instances. All of it is data from the
host calls → typed elements → pongo2; no place builds layout with `fmt.Sprintf`.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| `/instances` | open the instance manager | available in any artifact-driven story |
| `resume <key>` | re-join that instance (`iface.instance.resolve`) | from the picker or the manager |
| `delete <key>` | reclaim an archived instance (`instance.gc apply`) | guarded confirm; refuses non-archived without `--force` |

## Rendering tests

The resume picker and manager are pure render-from-host-data (no concurrent I/O of
their own), so the bar is a typed-view golden, not a combined-I/O capture. But the
**delete confirm** interleaves a host call (`instance.gc apply`), its slog, and the
re-render — that path needs a `CapturedIO` test (per CLAUDE.md / the
`rendering-tests` skill) asserting the list re-renders cleanly after a reclaim and
the slog doesn't corrupt the frame. Confirm it fails without the change.

- `instance_list` golden — list with mixed states + a retention-breaching archive,
  asserts the `⚠` flag and summary banner render.
- delete-reclaim CapturedIO — `delete <key>` → `instance.gc apply` → re-render;
  asserts no frame corruption and the row is gone.

## Migration plan

`workspace_manager.yaml` is a stub today; this replaces its body rather than
running in parallel. The current minimal `iface.workspace.list` render is
superseded by `iface.instance.list`; the room keeps its navigation intents. No
cutover risk — nothing depends on the stub's current output.

## Tasks

```
## 1. Render
- [ ] 1.1 instance_list typed element + pongo2 template (state badge, age, size, ⚠)
- [ ] 1.2 Resume picker view (scoped to in-progress) + manager view (all states); wire into the room's View()

## 2. Drive
- [ ] 2.1 /instances command; resume/delete intents (typed-slot parsed); guarded delete confirm

## 3. Prove + document
- [ ] 3.1 instance_list golden + delete-reclaim CapturedIO test (verified to fail without the change)
- [ ] 3.2 Manual run + web parity screenshot; document the operator surface in docs/stories/artifact-driven-stories.md
```

## What we lose, honestly

The resume picker adds a step between "type the story name" and "start working":
solo authors with no existing draft see one extra "start a new one" confirmation.
Mitigate by auto-skipping the picker when `iface.instance.list` returns empty —
the picker only interrupts when there's actually something to resume.

## Open questions

1. **Web vs TUI parity depth.** Full manager (with delete) in both, or re-join in
   both but cleanup web-only at first? *Lean: re-join in both immediately; ship
   delete in both, since it's the same host call and typed view.*
2. **Retention warning placement.** Only inside `/instances`, or also a passive
   badge on story entry? *Lean: a quiet one-line banner on entry when archives
   breach retention; full detail in the manager — don't nag.*

## Non-goals

- **No engine lifecycle here.** Discovery (`iface.instance.list`), GC
  (`instance.gc`), and disposition come from slices 1–2; this slice only renders
  and drives them.
- **No live-session management.** The web home's `SessionRegistry` view
  (`docs/web/README.md`) is a separate surface; this is about *instances/drafts*,
  not running sessions.
