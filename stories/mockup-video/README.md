# mockup-video — brief → mockup walkthrough video → refine on a flag

A standalone story that drives the design-mockup-video loop end to end: a
discovery conversation distils a **scenario brief**, an agent authors the
source (static HTML mockups + a tour manifest, *or* a slidey deck;
`medium: tour | deck`), the shipped `visual-outputs` producers render a
walkthrough MP4 (carrying the slice-1 **chapter sidecar**), the operator
reviews it inline, and a **refine** checkpoint edits the *exact* source that
produced each flagged moment — fed by inline feedback or by the structured
feedback notes the slice-2 web `/review` panel dispatches.

It is the produce → review → refine cousin of `stories/ui-fix/` (media
showcase + gallery) and `stories/bugfix/` (refine arc), for video instead of
code.

```
kitsoki run stories/mockup-video/app.yaml
```

## Rooms

```
intake ──(converse: distil brief)──▶ brief-gate ──ok──▶ authoring
   ▲                                    │clarify          │ accept
   └────────────────────────────────────┘                ▼
                                              ┌──── rendering ◀── rerender ──┐
                                              │  deck → slidey ; tour → run  │
                                              └──────────────────────────────┘
                                                          │ (auto)
                                                          ▼
                                                       review
                          media(video) + drained feedback.jsonl + checkpoint
                          accept→done · refine→refining · rerender→rendering · quit→@exit:abandoned
                                                          │ refine
                                                          ▼
                                                      refining ──▶ rendering
```

| Room | Split | What it does |
|---|---|---|
| `intake` | interpretive | `mode: conversational` discovery (`host.oracle.converse` + `host.chat.resolve`). `ready` distils the brief `{feature, scenarios[], medium, notes}`. A chat, not a form (memory: *freeform-capture-is-conversational*). |
| `brief-gate` | interpretive | `host.oracle.decide` against `prompts/brief_ready.md` — is the brief concrete? Auto-routes `ok` → authoring / `clarify` → intake. |
| `authoring` | interpretive | **One** `host.oracle.task` authors the source, workspace-jailed (memory: *task-agents-must-not-implement*). `accept` → rendering ; `revise` re-authors. `once:`-guarded. |
| `rendering` | **deterministic** | `deck` → `host.slidey.render` mp4 ; `tour` → `host.run scripts/record_tour.sh` (wraps the `kitsoki-ui-demo` recorder). Both emit `<out>.mp4` + `<out>.chapters.json` (slice 1), then `host.artifacts_dir` → `video_handle` / `chapters_handle`. Auto-advances to review. |
| `review` | deterministic | `media(video_handle)` inline + a pointer to `/review?video={handle}`. `on_enter` drains `feedback.jsonl` (slice-2 web notes) into `feedback_batch`. Checkpoint: `accept`/`refine`/`rerender`/`quit`. |
| `refining` | interpretive | **One** `host.oracle.task` edits each feedback note's `source_ref` target (the HTML page for `tour`, the scene object for `slidey`), the notes treated as **binding directives** with a per-note compliance checklist (memory: *refine-honours-operator-guidance*). Records the iteration, re-renders. |
| `done` | — | Gallery: the final video, the scenarios it covers, the refine log per iteration. |

## World contract

| Key | Meaning |
|---|---|
| `workspace` | Per-session dir under `.artifacts/mockup-video/` — the task write-jail; transient, never committed. |
| `medium` | `tour` (HTML + Playwright) \| `deck` (slidey). Chosen in the brief; default `tour`. |
| `brief` | `{feature, scenarios:[...], medium, notes}` — distilled in intake. |
| `source` | `{kind, paths:[...], spec_path}` — the authored render entrypoint. |
| `video_handle` / `chapters_handle` | The current render + its chapter sidecar (slice 1). |
| `feedback_batch` | `{items:[{source_ref, time_range, frame_handle, instruction}]}` — drained web notes. |
| `iterations` | `{items:[{video_handle, feedback_addressed, instruction}]}` — one entry per refine pass; preserved on quit. |
| `cycle` / `refine_budget` | Refine cycle counter and cap (default 6). |

## Exits

- `done` — `requires: [video_handle]`; the accepted walkthrough.
- `abandoned` — operator quit; partial `iterations` preserved.

## Host requirements

`host.oracle.converse` / `decide` / `task`, `host.slidey.render` (deck render),
`host.run` (tour render via `record_tour.sh` + feedback drain via
`drain_feedback.sh`), `host.artifacts_dir` (media-emit → handle),
`host.chat.resolve` (intake chat), `host.starlark.run` (iteration accumulate).

The tour path reuses the `kitsoki-ui-demo` Playwright recorder and the deck
path reuses slidey (epic shared decision 4) — this story authors briefs and
refines source; it builds neither recorder.

## Flows

All Mode-2, intent-only, no LLM (CLAUDE.md). Oracle/host calls stubbed by
per-invoke `id` (memory: *oracle-stub-by-id*); the render/run/artifacts and
starlark accumulate are stubbed to fixture handles. `kitsoki test flows
stories/mockup-video/app.yaml` → 8/8 pass.

| Flow | Coverage |
|---|---|
| `happy_deck` | Full path, `medium=deck`; `video_handle` bound, `done` reached. |
| `happy_tour` | `medium=tour`; `record_tour.sh` (stub) ran, chapter sidecar handle bound. |
| `brief_clarify_then_ok` | brief-gate clarify → intake → re-distil → re-gate. |
| `authoring_revise` | authoring → revise(feedback) → re-author → accept. |
| `refine_inline_then_accept` | review → inline `refine` → refining → re-render → accept; iterations grew by 1. |
| `refine_from_feedback_jsonl` | drained web notes (one `slidey`, one `tour`) drive one refine pass over both `source_ref`s. |
| `refine_budget_exhaust` | refine blocked once `cycle >= refine_budget`; accept still works. |
| `quit_mid_loop` | review → quit → `@exit:abandoned`; partial iterations preserved. |

## See also

- [`docs/stories/mockup-video.md`](../../docs/stories/mockup-video.md) — the narrative design.
- [`docs/skills/kitsoki-ui-demo/SKILL.md`](../../docs/skills/kitsoki-ui-demo/SKILL.md) — the tour recorder this story drives.
- [`docs/skills/slidey-authoring/SKILL.md`](../../docs/skills/slidey-authoring/SKILL.md) — the deck renderer.
- [`stories/ui-fix/`](../ui-fix/), [`stories/bugfix/`](../bugfix/) — the produce/review/refine cousins.
