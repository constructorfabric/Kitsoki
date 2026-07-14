# Demo storyboards

A **storyboard** (`*.storyboard.yaml`) is the single planning source for a
tour-driven demo video. It declares — before any capture — what the demo
promises (`goal`), how it runs deterministically (`binding`), and every scene's
narrative purpose, narration, drive actions, dwell, and observable claims. The
plan is a validated artifact, not prose: `kitsoki storyboard validate` lints it,
and the capture-facing formats are derived from it instead of hand-kept in
parallel.

Machine contract: [`schemas/storyboard/v1/storyboard.schema.json`](../../schemas/storyboard/v1/storyboard.schema.json)
(documentation mirror; the authoritative validator is `internal/storyboard`).
Worked example: [`.agents/skills/kitsoki-ui-demo/templates/storyboard.example.yaml`](../../.agents/skills/kitsoki-ui-demo/templates/storyboard.example.yaml)
(kept lint-clean against the repo by a test).

## Why a storyboard

Before this mechanism, a demo's plan was scattered across four places that
nothing kept in sync:

| Artifact | Role | Kept honest by |
|---|---|---|
| `.context/*-demo-plan.md` | intent, free-form prose | nobody |
| tour manifest (`TourStep[]`) | capture drive plan | `tour.LoadTourManifest` |
| ui-qa `scenarios.yaml` | what QA must observe | the vision gate |
| `<video>.chapters.json` | what was actually captured | the recorder |

The storyboard is upstream of all four: the prose plan becomes `render`
markdown, the tour manifest and QA scenarios are `emit`-ted from it, and the
chapter sidecar is `check`-ed against it after capture.

## Lifecycle

```bash
# 1. PLAN — author the storyboard (drafts live in .context/storyboards/;
#    promote it beside the capture spec when the demo is committed).
cp .agents/skills/kitsoki-ui-demo/templates/storyboard.example.yaml \
   .context/storyboards/my-demo.storyboard.yaml

# 2. VALIDATE — structural + semantic lint (run from the repo root).
go run ./cmd/kitsoki storyboard validate .context/storyboards/my-demo.storyboard.yaml

# 3. REVIEW — render the consistent human-readable plan.
go run ./cmd/kitsoki storyboard render .context/storyboards/my-demo.storyboard.yaml \
  --out .context/storyboards/my-demo.plan.md

# 4. EMIT — derive the capture-ready artifacts.
go run ./cmd/kitsoki storyboard emit tour .context/storyboards/my-demo.storyboard.yaml \
  --out .artifacts/my-demo/tour.yaml        # kitsoki tour --manifest / Playwright specs
go run ./cmd/kitsoki storyboard emit qa .context/storyboards/my-demo.storyboard.yaml \
  --out .artifacts/my-demo/scenarios.yaml   # kitsoki-ui-qa scripts/qa.sh --scenarios

# 5. CAPTURE — per .agents/skills/kitsoki-ui-demo/SKILL.md (rrweb / mp4 / kitsoki tour).

# 6. CHECK — diff the recording against the plan via its chapter sidecar.
go run ./cmd/kitsoki storyboard check .context/storyboards/my-demo.storyboard.yaml \
  --chapters .artifacts/my-demo/my-demo.mp4
```

## What validation enforces

**Errors** (exit 1): unknown YAML keys (strict parse), malformed/duplicate
kebab ids, a scene missing `purpose` / `narration` / `expect` / `dwellMs`,
invalid `drive` actions (the same rules the tour renderer enforces —
`tour.DriveAction.Validate`), invalid route/placement/kind/advance enums,
binding paths that don't exist under `--root`, a `scenario:` id missing from
`tools/product-journey/scenarios.json` or a disallowed transport.

**Warnings** (`--strict` promotes to failures):

- `dwellMs` below the narration **reading budget** (~280 ms/word over
  title+narration, 1200 ms floor — the same floor as the rrweb pacing scan).
- Estimated total screen time below **25 s** (the recorders' `MIN_DEMO_SECONDS`
  gate, which down-names short output `<name>.SHORT-<n>s.mp4`). A deliberate
  clip sets `minTotalMs`.
- Raw `__` internal intent names in viewer-facing text (word intents
  naturally).
- No `binding` — the deterministic no-LLM posture is unplanned.
- A scene with no anchor, no drive, and no screen description (it shows nothing
  specific), or `kind: action` with nothing that advances it.

## Design decisions

- **The drive vocabulary is the tour's, verbatim.** `scenes[].drive` reuses
  `internal/tour.DriveAction` (`type-and-send`, `click-intent`, `wait-state`,
  `reveal-turn`, `dwell-ms`), so the plan and the renderer can never disagree
  about what an action means, and `emit tour` output loads through
  `tour.LoadTourManifest` by construction (covered by a round-trip test).
- **`expect` is the QA contract.** Each claim must be observable in a captured
  frame; `emit qa` turns them into a kitsoki-ui-qa scenarios file, so the
  vision gate verifies exactly what the plan promised — no privately-owned
  case lists (see the scenario-ownership rule in `AGENTS.md`).
- **The chapter sidecar is the drift seam.** Scene ids double as chapter ids;
  `check` reads the producer-agnostic `<video>.chapters.json`
  (`internal/video.Chapter`) that both `kitsoki tour` and the Playwright
  `ChapterRecorder` emit, and flags missing scenes, reordered scenes, and
  windows under 80 % of the planned dwell.
- **A storyboard does not replace the product-journey run bundle.** When a
  demo claims coverage of a catalog scenario, `scenario:` references it and
  the run bundle stays the source of truth for evidence contracts and quality
  gates; the storyboard plans the *film*, not the QA pipeline.

## Where storyboards live

- **Planning drafts / one-off demos:** `.context/storyboards/<demo>.storyboard.yaml`
  (transient, uncommitted).
- **Committed demos:** beside the capture spec they drive (e.g. under
  `tools/runstatus/tests/playwright/` or the feature's docs), so the plan
  reviews and evolves with the spec.
