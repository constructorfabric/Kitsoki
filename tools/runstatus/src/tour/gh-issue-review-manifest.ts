/**
 * Tour manifest for the GitHub-review act of the cross-site gh-issues demo —
 * the FIRST kitsoki tour that narrates a site other than kitsoki.
 *
 * A kitsoki TourStep carries route/kind/advance because the live overlay drives
 * the kitsoki SPA. None of that applies on an external page: there is no kitsoki
 * router, no intent buttons, nothing to advance. So this act uses a purpose-built
 * step — an element to spotlight (by data-testid on the static GitHub-issue
 * fixture) plus the caption to show — and the gh-issue-review-video spec walks it
 * with the portable makeCaption + makeSpotlight helpers (which inject plain DOM
 * and work on any page). See .agents/skills/kitsoki-ui-demo/SKILL.md and
 * tools/runstatus/tests/playwright/fixtures/gh-issue-review.html.
 *
 * The narrative bridges act 1 (file a bug in the kitsoki web UI) to act 3 (triage
 * it back in kitsoki): it shows that the filed bug is a real GitHub issue on
 * constructorfabric/Kitsoki, carrying the labels, the uploaded evidence, and the
 * kitsoki body-metadata block that host.gh.ticket.create writes (slice #1/#2 of
 * the github-issues-tracker epic).
 */

/** One narrated beat on the external GitHub page. */
export interface ReviewStep {
  /** Stable id; also the per-scene screenshot label. */
  id: string;
  /** data-testid on the fixture to spotlight (omit to narrate the whole page). */
  target?: string;
  /** Caption headline. */
  title: string;
  /** Caption sub-line — one sentence on why it matters. */
  body: string;
  /** ms the video dwells on this beat (PACE-scaled by the spec). */
  dwellMs?: number;
}

export const GH_ISSUE_REVIEW_STEPS: readonly ReviewStep[] = [
  {
    id: "ghr-repo",
    target: "gh-repo-breadcrumb",
    title: "It opened on the canonical repo",
    body: "constructorfabric/Kitsoki — pinned via the ticket_repo world key, even when you're filing from a personal fork.",
    dwellMs: 5000,
  },
  {
    id: "ghr-title",
    target: "gh-issue-title",
    title: "Your bug is now a real GitHub issue",
    body: "The Report-bug modal from act 1 filed issue #3 through host.gh.ticket.create — not an in-repo issues/bugs/*.md file.",
    dwellMs: 5500,
  },
  {
    id: "ghr-labels",
    target: "gh-labels",
    title: "Triaged by labels, automatically",
    body: "create() mapped the bug-format axes onto labels: severity P2, comp:web, target:kitsoki.",
    dwellMs: 5000,
  },
  {
    id: "ghr-body",
    target: "gh-body",
    title: "The repro, rendered as Markdown",
    body: "What happened and the steps to reproduce — exactly what you typed into the review modal.",
    dwellMs: 5000,
  },
  {
    id: "ghr-artifacts",
    target: "gh-artifacts",
    title: "Your evidence, uploaded to the issue",
    body: "screenshot.png, the scrubbed har.json and the replayable session.rrweb.json — captured in-browser, uploaded to GitHub.",
    dwellMs: 6000,
  },
  {
    id: "ghr-meta",
    target: "gh-meta-block",
    title: "The kitsoki metadata block",
    body: "GitHub has no custom fields, so trace_ref / kitsoki_rev / filed_by ride in a fenced ```kitsoki block that get() parses back out.",
    dwellMs: 6000,
  },
  {
    id: "ghr-triage",
    target: "gh-triage-comment",
    title: "A maintainer picks it up — back in kitsoki",
    body: "Reproduced from the uploaded replay, then triaged from the kitsoki web UI. That's act 3.",
    dwellMs: 6000,
  },
] as const;
