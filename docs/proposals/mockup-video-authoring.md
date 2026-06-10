# Story: mockup-video — Brief → Mockup Walkthrough Video → Refine On a Flag

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../mockup-video-studio.md

## Why

We design features in our **tour-based walkthrough style** — static HTML
mockups walked through user scenarios, or a slidey deck — but producing one
is a manual skill expedition: hand-write the HTML/deck, hand-write a tour
manifest, run the recorder, watch it, notice scene 3 clips on mobile,
hand-edit, re-run. There is no structured, recorded process, and no clean
way to act on "this moment is wrong."

This story drives that loop end to end: a discovery conversation distils a
**scenario brief**, an agent authors the source (HTML mockups + tour, *or* a
slidey deck — `medium: tour | deck`), the shipped `visual-outputs` producers
render a walkthrough MP4 (carrying the slice-1 **chapter sidecar**), the
operator reviews it, and a **refine** checkpoint edits the *exact* source
that produced each flagged moment — fed either by inline feedback or by the
structured feedback notes the slice-2 web panel dispatches. It is the
produce→review→refine cousin of `stories/ui-fix/`, for video instead of code.

## What changes

A new standalone story `stories/mockup-video/` — a brief → render → refine
pipeline with a checkpoint loop, structurally a cousin of `stories/bugfix/`
(refine arc) and `stories/ui-fix/` (media showcase + gallery):

- **`intake`** — a `mode: conversational` discovery room (converse + chat;
  memory: *freeform-capture-is-conversational*) distils the **brief**:
  the feature/proposal being mocked, the **user scenarios** to walk, and the
  `medium` (`tour` = HTML + walkthrough, `deck` = slidey). A judge gate
  (`host.oracle.decide`) confirms the brief is concrete before proceeding.
- **`authoring`** — **one** `host.oracle.task`, scoped to a workspace dir,
  authors the source from the brief: `tour` → static HTML mockup page(s) +
  a tour manifest (`{id, route, target, title, body, …}`,
  `docs/skills/kitsoki-ui-demo/SKILL.md`); `deck` → a slidey JSON deck
  (`docs/decks/arch-and-usage.json` shape). Binds `source = {kind, paths,
  spec_path}`. Human checkpoint on the authored source.
- **`rendering`** — **deterministic** render (no LLM): `deck` →
  `host.slidey.render {spec_path, format: mp4}`; `tour` → `host.run`
  wrapping the `kitsoki-ui-demo` Playwright recorder (epic shared decision 4).
  Both emit the **chapter sidecar** (slice 1). `host.artifacts_dir`
  media-emit → `video_handle` (+ `chapters_handle`).
- **`review`** — renders the **`media` video** inline + a pointer to the web
  feedback mode (`kitsoki web … → /review?video={handle}`). Drains feedback
  notes (`feedback.jsonl` from slice 2) **and** accepts inline `refine
  feedback="…"`. Checkpoint: `accept` → `done`; `refine` → `refining`;
  `rerender` → `rendering`; `quit` → `@exit:abandoned`.
- **`refining`** — **one** `host.oracle.task` per feedback batch: for each
  note it edits the source the note's `source_ref` points at (the HTML page
  for `kind: tour`, the scene object in the deck for `kind: slidey`), guided
  by the note's `instruction` + captured still, then routes back to
  `rendering`. The feedback notes are **binding directives**, framed with a
  compliance checklist (memory: *refine-honours-operator-guidance*).
- **`done`** — a **gallery**: the final video (`media`), the scenarios it
  covers, and the feedback addressed across iterations.

## Impact

- **Net-new:** `stories/mockup-video/` — ~6 rooms, 2–3 prompts, 2 schemas,
  ~8 flow fixtures, an HTML mockup template + a starter slidey spec, README.
- **Engine/host changes:** none — composes existing + epic hosts:
  `host.oracle.converse`/`decide` (intake + brief gate), `host.oracle.task`
  (author + refine, scoped writes), `host.slidey.render` + `host.run` (render;
  `visual-outputs` #2 + `kitsoki-ui-demo` recorder), the slice-1 chapter
  sidecar, `host.artifacts_dir` media-emit
  (`internal/host/artifacts_dir_transport.go:238`), the `media` view element
  (`internal/app/view_element.go:477`).
- **Dependencies:** `visual-outputs` #2 (`host.slidey.render`) + #3 (web
  `media` rendering); epic slice 1 (chapter sidecar — so refine can target
  the flagged scene); soft-dep epic slice 2 (the web feedback panel — without
  it, `refine feedback="…"` inline still drives the loop). If the media
  substrate regressed, `review`/`done` degrade to a path pointer (TUI
  behaviour anyway).
- **Reuses existing tooling, not new tooling:** the tour recorder is the
  `kitsoki-ui-demo` Playwright machinery; the deck renderer is slidey. This
  story authors briefs and refines source — it builds neither recorder.
- **Docs on ship:** `docs/stories/mockup-video.md`; entry in
  `proposals/README.md`; cross-link from `kitsoki-ui-demo` /
  `slidey-authoring` skills ("to author + iterate a mockup video as a
  recorded process, drive `stories/mockup-video`").

## Moat alignment

The interpretive/deterministic split is the spine, every step a recorded
datapoint (memory: *kitsoki-moat-is-architecture*):

- **Brief distillation** (`intake`) and **brief-concrete gate** are the
  interpretive *scoping* — `converse` + a recorded `decide`.
- **Source authoring** (`authoring`) and **refine** (`refining`) are the
  interpretive *execution* — `host.oracle.task`, scoped tool grant; the diff
  and reasoning recorded. The fixer never grades its own homework.
- **Rendering** is **deterministic** — slidey/Playwright produce the same
  bytes from the same source; recorded as host calls + an `artifact`
  datapoint, displayed from the record (memory: *narration-belongs-in-trace*).
- **The flag→refine link is explicit and recorded:** a feedback note carries
  `source_ref`, so "edit scene 3" is a traceable edge from a video moment to
  a source change — not a vibe.

## Story graph

```
intake ──(converse: distil brief {feature, scenarios[], medium})──▶ brief-gate
   brief-gate: oracle.decide "concrete enough?"  ── clarify ──▶ intake (self)
                                                  └─ ok ──────▶ authoring
   authoring: ONE oracle.task authors source (scoped to workspace)
              tour → HTML pages + tour manifest ; deck → slidey deck
              checkpoint: accept ──▶ rendering ; revise(feedback) ──▶ authoring
                                   │
                                   ▼
   ┌───────────────────────────  rendering  ◀──────────────── rerender ──┐
   │  deck → host.slidey.render mp4 ; tour → host.run record_tour.sh      │
   │  both → chapters.json (slice 1) ──▶ host.artifacts_dir ──▶ handle    │
   └─────────────────────────────────────────────────────────────────────┘
                                   │
                                   ▼
                                review
        view: media(video_handle) + "open /review?video=… to flag scenes"
        on_enter: drain feedback.jsonl (slice 2) ──▶ feedback_batch
        checkpoint:
          accept ──▶ done
          refine (inline feedback="…" or drained batch) ──▶ refining
          rerender ──▶ rendering
          quit ──▶ @exit:abandoned
                                   │ refine
                                   ▼
                              refining
        on_enter: ONE oracle.task edits each note's source_ref target
                  per its instruction + still (binding directives)
                  ──▶ rendering
                                   │  (accept)
                                   ▼
                                done
        view: media(final video) + scenarios + feedback log per iteration
```

`exits:` — `done: { requires: [video_handle] }`, `abandoned: {}` (quit).
Progress (the rendered handles per iteration, the feedback addressed) is
preserved so a partial `done` is honest.

## World schema (sketch)

```yaml
world:
  workspace:     { type: string, default: ".artifacts/mockup-video/<session>" }
  medium:        { type: string, default: "tour" }      # tour | deck
  brief:         { type: object, default: {} }           # {feature, scenarios:[...], medium, notes}
  source:        { type: object, default: {} }           # {kind, paths:[...], spec_path}
  video_handle:  { type: string, default: "" }           # current render
  chapters_handle:{ type: string, default: "" }          # slice-1 sidecar handle
  feedback_batch:{ type: object, default: {} }           # {items:[{source_ref,time_range,frame_handle,instruction}]}
  refine_feedback:{ type: string, default: "" }          # inline note (no web panel)
  iterations:    { type: object, default: {} }           # {items:[{video_handle, feedback_addressed:[...]}]}
  cycle:         { type: int,    default: 0 }
  refine_budget: { type: int,    default: 6 }
  abandon_reason:{ type: string, default: "" }
```

## Per-room sketch

- **`intake`** — `mode: conversational`; `converse` gathers the brief
  (feature, scenarios, `medium`). Pattern: `stories/prd/` discovery +
  memory *freeform-capture-is-conversational*. `done` intent → `brief-gate`.
- **`brief-gate`** — `host.oracle.decide` against `prompts/brief_ready.md`:
  is the brief concrete (scenarios named, medium chosen)? `clarify` →
  `intake`; `ok` → `authoring`. (Models `stories/dev-story/` brief check.)
- **`authoring`** — `set: workspace`, then **one** `host.oracle.task`
  (`prompts/author_source.md`) scoped to `workspace` (Read+Write+Bash in the
  workspace only; memory: *task-agents-must-not-implement* — prompt is the v1
  write-jail, engine allowlist is `oracle-capability-model`). Emits
  `source.json` `{kind, paths, spec_path}`. Checkpoint `accept`/`revise`.
  `once: true`.
- **`rendering`** — branch on `medium`: `deck` → `host.slidey.render
  {spec_path: source.spec_path, format: mp4}`; `tour` → `host.run
  scripts/record_tour.sh` over `source.paths` + the manifest. Both produce
  `<out>.mp4` + `<out>.chapters.json` (slice 1). Then `host.artifacts_dir
  {src_path, kind: video}` → `bind: video_handle` (+ `chapters_handle`).
  `once: true` per entry (re-render clears it).
- **`review`** — `on_enter` drains `feedback.jsonl` from `workspace` →
  `bind: feedback_batch` (empty if no web panel). View: `media` element on
  `video_handle`; a `kv:` of scenarios; a `prose:` pointer to
  `/review?video={handle}`; the checkpoint `list:`. Intents: `accept`
  (`requires: video_handle`) → `done`; `refine feedback=…` (optional slot —
  inline *or* uses the drained batch) → `refining`; `rerender` → `rendering`;
  `quit` → `@exit:abandoned`.
- **`refining`** — `when: cycle < refine_budget`; **one** `host.oracle.task`
  (`prompts/refine_source.md`) scoped to `workspace`, handed the
  `feedback_batch` (and/or `refine_feedback`) as **binding directives** with a
  per-note compliance checklist (memory: *refine-honours-operator-guidance*),
  editing each note's `source_ref` target. `cycle++` → `rendering`. At budget
  → `say:` "Refine budget exhausted" → `review`.
- **`done`** — `media` of the final video + a `template:` log of iterations
  and feedback addressed. No re-entry.

## Net-new files

```
stories/mockup-video/
├── app.yaml                       # world, hosts, root: intake
├── rooms/{intake,brief-gate,authoring,rendering,review,refining,done}.yaml
├── prompts/
│   ├── brief_ready.md             # brief → concrete? (decide)
│   ├── author_source.md           # brief → HTML+tour | slidey deck (task)
│   └── refine_source.md           # feedback notes → edit source_ref targets (task)
├── schemas/
│   ├── source.json                # { kind, paths, spec_path }
│   └── brief.json                 # { feature, scenarios, medium, notes }
├── templates/
│   ├── mockup.html.tmpl           # starter static mockup page (pongo2)
│   └── deck.starter.json          # starter slidey deck
├── scripts/
│   └── record_tour.sh             # host.run wrapper over kitsoki-ui-demo recorder (emits chapters.json)
├── flows/
│   ├── happy_deck.yaml            # intake→authoring→render(deck)→review→accept→done
│   ├── happy_tour.yaml            # medium=tour path
│   ├── brief_clarify_then_ok.yaml
│   ├── authoring_revise.yaml
│   ├── refine_inline_then_accept.yaml
│   ├── refine_from_feedback_jsonl.yaml   # drained web notes drive refine
│   ├── refine_budget_exhaust.yaml
│   └── quit_mid_loop.yaml
└── README.md
```

## Flow fixtures

All Mode-2, intent-only, no LLM (CLAUDE.md; memory: *no-llm-tests*). Stub
`host.oracle.converse`/`decide`/`task` via flow `host_handlers`; stub
`host.slidey.render`/`host.run` (render → fixture mp4 + canned
`chapters.json`) and `host.artifacts_dir` (canned handle). Stub oracle calls
**by per-invoke id** (memory: *oracle-stub-by-id*).

- **`happy_deck`** — full path, `medium=deck`; asserts `video_handle` bound,
  `done` shows the `media` element.
- **`happy_tour`** — `medium=tour`; asserts `record_tour.sh` (stub) ran and a
  chapter sidecar handle bound.
- **`brief_clarify_then_ok`** — `brief-gate` clarify → `intake` → ok.
- **`authoring_revise`** — `authoring` → revise(feedback) → re-author.
- **`refine_inline_then_accept`** — `review` → `refine feedback="logo too
  small on scene 1"` → `refining` (task stub edits scene) → `rendering` →
  `review` → `accept`. Asserts `iterations|length == 2`.
- **`refine_from_feedback_jsonl`** — seed a `feedback.jsonl` with two notes
  (one `kind: slidey`, one `kind: tour`); `review` drains them → `refining`
  edits both `source_ref`s. Asserts the batch flowed to the task.
- **`refine_budget_exhaust`** — `refine` × `refine_budget` → auto-stop.
- **`quit_mid_loop`** — `review` → `quit` → `@exit:abandoned`; partial
  `iterations` preserved.

## Tasks

```
## 1. Scaffold
- [ ] 1.1 app.yaml: world, hosts (converse, decide, task, slidey.render, run, artifacts_dir), root: intake
- [ ] 1.2 All room files, typed `extends: "base"` views, intent/transition skeletons
- [ ] 1.3 schemas/{brief,source}.json; stub prompts; templates/{mockup.html.tmpl,deck.starter.json}
- [ ] 1.4 scripts/record_tour.sh (wraps kitsoki-ui-demo recorder; emits chapters.json — slice 1 task 3.2)

## 2. Lock the graph
- [ ] 2.1 Probe intake→brief-gate (converse stub → brief; decide stub clarify/ok)
- [ ] 2.2 Probe authoring (task stub → source; accept/revise)
- [ ] 2.3 Probe rendering both media (deck via slidey stub, tour via run stub) → handle + chapters bound
- [ ] 2.4 Probe review (drain feedback.jsonl; accept/refine/rerender/quit)
- [ ] 2.5 Probe refining (inline + drained batch; budget auto-stop) → rendering
- [ ] 2.6 All flow fixtures pass: `kitsoki test flows stories/mockup-video/app.yaml`

## 3. Prompts + real wiring
- [ ] 3.1 brief_ready.md (concrete-brief gate) + brief schema
- [ ] 3.2 author_source.md (medium-branched: HTML+tour | deck), workspace write-jail
- [ ] 3.3 refine_source.md: feedback notes as BINDING directives + per-note compliance checklist;
          edit the source_ref target (HTML page | deck scene)
- [ ] 3.4 rendering wired to real host.slidey.render (deck) + record_tour.sh (tour); confirm a real
          mp4 + chapters.json emit and the media element renders in `kitsoki web`

## 4. Live + document
- [ ] 4.1 End-to-end: author a 3-scenario mockup video for a real proposal, render, flag a scene in
          /review (slice 2), refine it, watch the re-render improve
- [ ] 4.2 README.md: entry, exits, world contract (medium, workspace), host requirements
- [ ] 4.3 Migrate to docs/stories/mockup-video.md; cross-link from kitsoki-ui-demo/slidey skills;
          delete this proposal; update proposals/README.md
```

## Open questions

1. **Tour vs. deck as the default `medium`.** `deck` (slidey) is
   self-contained and dependency-lighter; `tour` (HTML + Playwright) is
   closer to "real UI" and reuses the demo recorder but needs a browser.
   *Lean: `tour` default (matches "static HTML pages … tour-based walkthrough
   video style"), `deck` opt-in.*
2. **Where does the authored source live?** A per-session `workspace` under
   `.artifacts/` (transient, never committed; CLAUDE.md) vs. a chosen project
   path. *Lean: `.artifacts/mockup-video/<session>/` by default; an export
   step copies the accepted source out if the operator wants to keep it.*
3. **Batch vs. per-flag refine** — deferred to epic cross-cutting Q3. *Lean:
   batch (one re-render per refine pass).*
4. **Scenario → scene granularity for `tour`.** One HTML page per scenario
   walked as N tour steps, or one page with step-targeted regions? *Lean:
   one page per scenario, steps target regions — keeps `source_ref.step_id`
   meaningful for refine.*

## Non-goals

- **Authoring the recorder or the renderer** — reuses `kitsoki-ui-demo`
  (Playwright) + slidey; builds neither.
- **Editing video in the browser** — refine edits *source*, then re-renders
  (epic non-goal: no in-browser trim/splice).
- **Producing production UI code** — these are *mockups* (static HTML / deck
  scenes), not shippable components.
- **Engine-level write sandboxing for the tasks** — the `workspace`
  write-jail in the prompt is the v1 guardrail; the durable allowlist is the
  `oracle-capability-model.md` epic.
```
