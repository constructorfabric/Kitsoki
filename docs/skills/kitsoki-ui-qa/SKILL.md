---
name: kitsoki-ui-qa
description: Validate UI evidence (a screenshot for simple cases, a video for complex flows) against the bug or plan being verified plus usage scenarios — the inverse of kitsoki-ui-demo. Picks the evidence form by complexity, extracts deterministic frames, has a read-only `claude` vision agent judge each scenario against cited frames AND whether the evidence is complete for the stated bug/plan, adversarially re-checks every pass, and emits a gated qa-report.md + verdict.json. Use when asked to QA / review / validate / sign off on a demo, walkthrough, screenshot, or bug-fix proof, or to gate one in CI.
---

# Kitsoki UI demo QA

The **inverse** of [[kitsoki-ui-demo]]: that skill *produces* visual evidence;
this one *validates* it. Given **the bug or plan being verified**, a list of
**usage scenarios**, and the **evidence** (a screenshot for a simple case, a
video for a complex flow — or pre-extracted frames), it decides — with cited
evidence — whether the demo actually demonstrates each scenario, and exits
non-zero if a required one doesn't, so it can gate a release.

## Evidence is judged against the bug/plan — never in a vacuum

This skill **requires the bug or plan** as its `--feature` input (it is the
spec, not just background prose). The vision review answers two questions, not
one:

1. Does each scenario step appear, grounded in a cited frame? (the per-step
   verdict)
2. **Is the evidence complete and relevant for *this* bug/plan?** Evidence that
   is well-formed but doesn't actually exercise the changed behaviour — a video
   of an unrelated flow, a screenshot of the wrong state, a "before" that never
   shows the "after" the fix promises — is `unsupported`, even if every frame is
   crisp. A demo can be a perfectly good video and still be the wrong evidence.

So write the `--feature` file as the *actual* bug report or implementation plan
(what changed, what the user should now see), not a generic feature blurb. The
reviewer uses it to decide whether the screenshot/video proves the fix, not just
whether the UI rendered.

## Pick the evidence: screenshot vs video

Choose the *cheapest evidence that actually proves the change*, then QA that:

- **Simple, single-state cases → a Playwright screenshot.** If the bug/plan is
  fully verifiable from one (or a few) static frames — a badge label, a fixed
  layout, an element that should/shouldn't render, a color/spacing fix — capture
  a screenshot and QA it. No video needed; a screenshot is faster, smaller, and
  deterministic. Add a `page.screenshot({ path })` to a Playwright spec driving
  the relevant scene (the demo helpers in
  `tools/runstatus/tests/playwright/_helpers/demo.ts` stage scenes
  deterministically), or reuse the `NN-<scene>.png` captures the recorder
  already emits. Drop the PNG(s) into a dir and pass `--frames <dir>`.
- **Complex flows / multi-state transitions → a video.** If proving the
  bug/plan needs *motion* — a state badge advancing turn-by-turn, a streaming
  bubble appearing then resolving, a modal opening on an async event, an
  ordering/timing behaviour — a screenshot can't show it. Record a video with
  [[kitsoki-ui-demo]] and QA the video (frames are extracted deterministically).
  When in doubt about whether one frame can carry the claim, use a video.

**How to create the evidence** (and keep it relevant): see [[kitsoki-ui-demo]]
for the full recording pipeline (deterministic, no-LLM, MP4 + per-scene
`NN-<scene>.png`). Whichever form you pick, it **must exercise the feature being
implemented or the bug being fixed** — drive the specific room/intent/state the
bug/plan names, not a generic onboarding tour. Evidence that doesn't touch the
changed behaviour will be flagged `unsupported` by the review above.

> This is an **LLM-driven review tool by design** (it needs vision). It is *not*
> a no-LLM flow test and must never be wired into the automated test suite
> (CLAUDE.md, [[feedback_no_llm_tests]]). It uses the local `claude` CLI, so —
> like the engine's oracle — there's no API key and no per-call cost
> ([[project_oracle_uses_claude_cli]]). The two deterministic stages
> (`extract-frames.sh`, `report.sh`) are testable on their own without any LLM.

## Why it's reliable (read this first)

Video QA is unreliable when a model free-associates about UI it never saw. The
pipeline removes that failure mode structurally, not by hoping the model behaves:

1. **Deterministic evidence.** `extract-frames.sh` is pure ffmpeg — scene-change
   detection (the meaningful moments in a UI demo are state transitions) plus a
   periodic floor so static dwells aren't missed. Same video + flags → same
   frames + same `frames.json`. The frames are the *only* admissible evidence.
2. **Grounded verdicts.** Every `pass` MUST cite a frame filename and quote what
   is **literally visible**. A claim with no citable frame is `unsupported`
   (never silently `pass`); a frame that contradicts it is `fail`.
3. **Adversarial re-check (interpretive ÷ deterministic).** A second `claude`
   pass plays skeptic: it re-reads each `pass` step's cited frame and emits only a
   small **list of downgrades** (which step, to `fail`/`unsupported`, and what the
   frame really shows). `qa-review.sh` then **applies them deterministically** — it
   can only *lower* a status, never raise one (the downgrade-only invariant is
   enforced in code, not by trusting the model), and recomputes every
   scenario/`overall`/summary itself. The tiny delta output (vs. re-emitting the
   whole multi-KB verdict) is what makes this pass robust. Model output is parsed
   with a brace-matching JSON extractor that tolerates stray prose / ``` fences.
   The result is recorded as `adversary: {status, downgrades_applied}` on the
   verdict. (`--no-adversary` to skip.)
4. **Authoritative gate.** `report.sh` recomputes pass/fail from the per-scenario
   status in `verdict.json` (it does *not* trust the model's own `overall`) and
   sets the exit code. Under `--strict` it additionally blocks if the adversarial
   pass was supposed to run but did not complete (`adversary.status != "ok"`), so
   a silent adversary flake can never pass a strict gate.

## Prerequisites

`ffmpeg`, `jq`, and the `claude` CLI on PATH (all already present in this repo's
dev env). No `make build` needed — this consumes an existing video/frames.

## The loop

1. **Give it the bug/plan + what the evidence should show.** Copy the templates
   and edit — `--feature` is the *actual bug report or implementation plan*, not
   a generic blurb (see "judged against the bug/plan" above):
   ```bash
   D=docs/skills/kitsoki-ui-qa
   cp $D/templates/feature.example.md   .context/qa-feature.md   # ← the bug or plan
   cp $D/templates/scenarios.example.yaml .context/qa-scenarios.yaml
   ```
   Scenarios are **observable claims** ("the state badge advances", "three story
   cards are listed") — not internal behaviour the camera can't see. Mark a
   scenario `required: false` to keep it non-blocking.

   Then pick the evidence form (see "Pick the evidence" above): a **screenshot**
   for a simple, single-state case; a **video** for a complex/multi-state flow.

2. **Run the QA gate** (one shot: extract → contact sheet → review → report).
   For a **screenshot** (or any pre-captured PNG set), pass the frames dir
   directly — the positional path is only used to name the output dir:
   ```bash
   docs/skills/kitsoki-ui-qa/scripts/qa.sh .artifacts/fix-badge/badge.png \
     --frames   .artifacts/fix-badge \
     --feature   .context/qa-feature.md \
     --scenarios .context/qa-scenarios.yaml --strict
   ```
   For a **video**:
   ```bash
   docs/skills/kitsoki-ui-qa/scripts/qa.sh \
     .artifacts/multi-story/multi-story.mp4 \
     --feature   .context/qa-feature.md \
     --scenarios .context/qa-scenarios.yaml
   echo "gate exit: $?"          # 0 pass · 1 blocking failure · 2 pipeline error
   ```
   Artifacts land in `.artifacts/ui-qa/<video-stem>/`
   ([[feedback_artifacts_dir]]): `frames/`, `frames.json`, `contact-sheet.png`,
   `verdict.json`, `qa-report.md`.

3. **Prefer ground-truth frames when you have them.** The recorder already emits
   labeled per-scene `NN-<scene>.png`. Point `--frames` at that dir to QA those
   exact captures instead of re-extracting (highest fidelity, skips ffmpeg):
   ```bash
   docs/skills/kitsoki-ui-qa/scripts/qa.sh .artifacts/multi-story/multi-story.mp4 \
     --frames .artifacts/multi-story --feature .context/qa-feature.md \
     --scenarios .context/qa-scenarios.yaml --strict
   ```

4. **Read `qa-report.md`.** Per-scenario table with the cited evidence frame for
   each step. Open the cited PNGs (or `contact-sheet.png`) to confirm. If a
   scenario is `unsupported`, the demo didn't cover it — usually a gap in the
   *demo*, occasionally a vague scenario step to sharpen.

## The tools (`scripts/`)

| Script | Does | LLM? |
|---|---|---|
| `qa.sh <video> --feature F --scenarios S [--frames D] [--out D] [--model M] [--max-frames N] [--no-adversary] [--strict]` | One-shot wrapper; exit code is the gate | via review |
| `extract-frames.sh <video> <out-dir> [--scene TH] [--interval S] [--dedup MS] [--max N] [--width W]` | Deterministic scene-change + periodic-floor frames + `frames.json` | no |
| `qa-review.sh --frames D --feature F --scenarios S --out V [--model M] [--no-adversary]` | Read-only vision agent → evidence-cited `verdict.json` + adversarial re-check | **yes** |
| `report.sh <verdict.json> [--out report.md] [--strict]` | `verdict.json` → `qa-report.md`; recomputes the gate exit code | no |

Defaults: review model `claude-opus-4-8` (override `--model claude-sonnet-4-6`
for faster/cheaper); `--max-frames 48`; `--strict` makes every scenario blocking.

## verdict.json shape

```json
{ "overall":"pass|fail",
  "summary":{"scenarios_total":0,"passed":0,"failed":0,"unsupported":0},
  "frames_reviewed":["0001-0ms.png"],
  "scenarios":[
    {"id":"drive","title":"…","required":true,"status":"pass|fail|unsupported",
     "steps":[{"text":"…","status":"pass|fail|unsupported",
               "evidence":[{"frame":"0007-5200ms.png","observation":"<literal>"}],
               "confidence":0.0}]}]}
```

## Pointers

- The recorder this inverts: [[kitsoki-ui-demo]] (`docs/skills/kitsoki-ui-demo/`)
  — its `NN-<scene>.png` output is the ideal `--frames` input here, and its
  `contact-sheet.sh` is reused for the storyboard.
- Oracle = local `claude` CLI: `internal/host/oracle_runner.go`.

## Maintenance

Exposed to Claude Code via a symlink (skills under `docs/` aren't auto-discovered):

```
ln -s "$(pwd)/docs/skills/kitsoki-ui-qa" ~/.claude/skills/kitsoki-ui-qa
```
