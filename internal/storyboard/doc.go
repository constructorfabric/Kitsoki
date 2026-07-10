// Package storyboard is the planning contract for a tour-driven demo video:
// one *.storyboard.yaml that declares, before any capture, what the demo
// promises (goal), how it runs deterministically (binding), and every scene's
// narrative purpose, narration, drive actions, dwell, and observable claims.
//
// It sits upstream of three existing per-scene formats and is the single
// source they derive from or are checked against:
//
//   - tour manifests ([kitsoki/internal/tour.TourStep]) — emitted via
//     [Storyboard.EmitTourYAML]; the output loads through
//     tour.LoadTourManifest, so an emitted plan is capture-ready by
//     construction.
//   - kitsoki-ui-qa scenarios ({id, title, required, steps}) — emitted via
//     [Storyboard.EmitQAScenariosYAML] from each scene's expect claims, so
//     the vision gate verifies exactly what the plan promised.
//   - the chapter sidecar ([kitsoki/internal/video.Chapter]) — checked after
//     capture via [CheckChapters], so a recorded video is diffed against the
//     plan (missing scenes, reordered scenes, under-dwelled windows).
//
// Validation ([Storyboard.Validate]) is two-layered: strict YAML decoding
// rejects unknown keys (typo safety), and semantic lint enforces the demo
// discipline the capture skills prescribe — unique kebab ids, a narrative
// purpose and at least one observable claim per scene, dwell at or above the
// narration reading budget (the same 1200 ms floor as the rrweb pacing scan),
// total screen time at or above the MIN_DEMO_SECONDS gate, no raw `__` intent
// names in viewer-facing text, and existing binding/scenario references.
//
// Non-goals: this package does not drive a browser, render video, or replace
// the product-journey run bundle. A storyboard that claims coverage of a
// catalog scenario must reference it (scenario:) — the run bundle stays the
// source of truth for evidence contracts and quality gates.
//
// The human-readable form of the same plan is [Storyboard.RenderMarkdown];
// the machine contract is mirrored (documentation-only) at
// schemas/storyboard/v1/storyboard.schema.json. The CLI surface is
// `kitsoki storyboard` (cmd/kitsoki/storyboard.go); the narrative doc is
// docs/media/storyboard.md.
package storyboard
