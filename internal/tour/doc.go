// Package tour implements the `kitsoki tour` subcommand: a deterministic,
// no-LLM demo-video renderer that drives the embedded web UI from the binary —
// no Node, pnpm, or Playwright (kitsoki-as-dependency epic, slice 2).
//
// # The flow
//
//	feature/manifest YAML ──► TourManifest (steps, each with optional drive[])
//	                              │
//	in-process server.NewMulti ◄─┘   (no-LLM posture: flow fixture + cassette,
//	    on a localhost-only HTTP                   reusing cmd/kitsoki/web.go)
//	    listener the browser hits
//	                              │
//	chromedp headless Chrome ─────┘
//	    navigate → home, inject window.__startTourWithSteps(JSON), then for each
//	    step execute its drive[] actions over CDP Runtime.evaluate:
//	      type-and-send → fill composer + click send
//	      click-intent  → click intent-btn-<intent>
//	      wait-state    → poll the current-state testid until it matches
//	      reveal-turn   → ease the last turn to the top, hold, ease through reply
//	      dwell-ms      → hold on the frame (pace-scaled)
//	                              │
//	Page.startScreencast ─────────┘
//	    frames stream in → PNGs on disk → ffmpeg stitches an H.264 MP4
//	                              │
//	output ◄──────────────────────┘
//	    <out>/<videoBase>.mp4
//	    <out>/<videoBase>.mp4.chapters.json  (internal/video.WriteChapters;
//	                                          one Chapter per step, source_ref
//	                                          kind="tour", step_id=<step.id>)
//	    <out>/<videoBase>.mp4.steps.json     (one StepShot per PNG: a deterministic
//	                                          PNG → spec-location reference + the
//	                                          assertions the render enforced —
//	                                          see steps.go)
//	    <out>/NN-<step-id>.png               (per-step poster frames)
//
// # Drive actions are the manifest's self-driving data
//
// The Playwright specs (e.g. agent-actions-video.spec.ts) carry the imperative
// driving (type "prd", click core__prd__start, wait
// core.prd.idle, paced reveal) in TypeScript the binary cannot read. The
// optional ordered drive[] list on each TourStep captures that as data, so the
// SAME manifest renders the SAME demo from the binary. DriveAction mirrors the
// TS DriveAction (tools/runstatus/src/tour/types.ts) field-for-field.
//
// # Non-goals
//
//   - Replacing the Playwright harness — the subcommand is ADDITIVE; the
//     existing agent-actions golden spec stays as the JS-side reference.
//   - Any real LLM — every render is no-LLM via a flow fixture + cassette
//     (CLAUDE.md). A render that needs an LLM is a bug in the fixture.
//   - Re-encoding / trimming beyond the single frames→MP4 stitch (that is
//     internal/video's job for single-frame extraction; the stitch lives here).
package tour
