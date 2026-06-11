/**
 * Mockup Video Studio · /review feedback-mode demo manifest.
 *
 * The step metadata (titles / bodies / targets / dwell) for the review-video
 * feature-spotlight demo (tests/playwright/review-video.spec.ts). Unlike the
 * agent-actions / onboarding tours, the /review SPA route is NOT one of the
 * tour overlay's known routes (home | interactive | any → /, /s/:id/chat,
 * /s/:id), so this manifest does NOT drive the live `__startTourWithSteps`
 * overlay. Instead the recording spec consumes it directly to caption + spotlight
 * each beat with the shared _helpers/demo.ts caption banner.
 *
 * Keeping the narration here (rather than inline in the spec) mirrors the golden
 * agent-actions pair: one declarative array a reviewer can read end-to-end, and
 * a spec that walks it. Every `target` is a data-testid the /review components
 * actually ship (ReviewPage / ChapterTimeline / FlagList / FlagDetail).
 *
 * Plain types + data only — no Vue / DOM imports (mirrors manifest.ts).
 */

/** One captioned/spotlit beat of the /review demo. */
export interface ReviewStep {
  /** Stable id; also the NN-<id>.png screenshot label. */
  id: string;
  /** data-testid to spotlight, or omit for a full-frame caption-only beat. */
  target?: string;
  /** Caption headline. */
  title: string;
  /** Caption sub-line — one sentence on what this shows and why it matters. */
  body: string;
  /** ms to hold the caption (PACE-scaled by the spec). */
  dwellMs?: number;
}

export const REVIEW_DEMO_STEPS: readonly ReviewStep[] = [
  {
    id: "review-entry",
    target: "media-review-link",
    title: "From the room: open the rendered walkthrough in review",
    body: "After rendering, the review room plays the walkthrough inline and offers one entry point — the 'Open in review' button on the video. That is how an operator reaches the feedback surface; clicking it carries this run's video handle straight to /review.",
    dwellMs: 6000,
  },
  {
    id: "review-open",
    target: "review-page",
    title: "Video review — feedback on a rendered walkthrough",
    body: "The /review surface opens on a session that rendered a real walkthrough video. Two columns: the player, chapter timeline and flags on the left; the selected flag's detail on the right.",
    dwellMs: 6000,
  },
  {
    id: "review-player",
    target: "rp-player",
    title: "The rendered walkthrough plays inline",
    body: "This is the actual MP4 the deterministic slidey producer rendered for this run, served through the run's ArtifactResolver by its journalled handle — no upload, no copy.",
    dwellMs: 6000,
  },
  {
    id: "review-timeline",
    target: "chapter-timeline",
    title: "A chapter timeline from the sidecar",
    body: "Each marker is one scene from the chapter sidecar the render emitted (source_ref kind=slidey). Click or drag the track to pick a moment; the selection is what you flag.",
    dwellMs: 6500,
  },
  {
    id: "review-flag",
    target: "ct-flag-btn",
    title: "Flag a moment",
    body: "Flagging the selected moment grabs a still at that timestamp — a REAL single-frame ffmpeg extraction (internal/video.Frame), recorded back through the run's substrate as its own artifact handle.",
    dwellMs: 5500,
  },
  {
    id: "review-still",
    target: "fd-still",
    title: "The captured still",
    body: "The flag's detail pane shows the grabbed frame — proof the frame seam ran end-to-end: video handle → ffmpeg grab at t → recorded PNG handle → served back into the panel.",
    dwellMs: 6000,
  },
  {
    id: "review-source",
    target: "fd-source",
    title: "Resolved source_ref + IDE deep-link",
    body: "The dominant chapter resolves the flag to the exact slidey scene that produced it, with a vscode:// deep-link straight to the spec. Feedback lands on the source, not a screenshot.",
    dwellMs: 6500,
  },
  {
    id: "review-instruction",
    target: "fd-instruction",
    title: "Write the refine instruction",
    body: "Type what should change at this moment. This becomes the structured feedback note's instruction — a binding directive the slice-3 refine arc will honour.",
    dwellMs: 6000,
  },
  {
    id: "review-send",
    target: "fd-send-refine",
    title: "Send to refine",
    body: "Dispatch the note via runstatus.feedback.add: it carries the source_ref, the time range, the captured frame handle, and the instruction. The panel captures and dispatches — it never edits a spec itself.",
    dwellMs: 6500,
  },
  {
    id: "review-flaglist",
    target: "flag-list",
    title: "Every flag, dispatched together",
    body: "Flags accumulate in the left column; a filled dot marks a dispatched note. 'Send all' fires every open note in one batch for the next refine cycle.",
    dwellMs: 6000,
  },
];
