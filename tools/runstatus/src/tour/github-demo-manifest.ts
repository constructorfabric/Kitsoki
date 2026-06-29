/**
 * Tour manifest for Act 1 of the kitsoki-github-agent demo — the GitHub side of
 * the @kitsoki loop, narrated on constructorfabric/Kitsoki.
 *
 * Like the gh-issue-review manifest it bridges, a kitsoki live TourStep carries
 * route/kind/advance because the overlay drives the kitsoki SPA. None of that
 * applies on an external file:// page: there is no kitsoki router, no intent
 * buttons, nothing to advance. So each step is a purpose-built beat — an element
 * to spotlight (by data-testid on the static gh-thread.html fixture) plus the
 * caption to show — and the github-demo-issuepr spec walks it with the portable
 * makeCaption + makeSpotlight helpers (plain DOM, works on any page).
 *
 * Eight beats, one per storyboard scene (proposal §Tour storyboard, Act 1):
 * mention ingress → ack + run link → label→story dispatch → CI-watch/fix →
 * rebase-on-conflict → comment-driven implement + parent resolve → guidance
 * request → rolling single-voice status edited to "done". Each title/body narrates
 * WHAT it proves and WHICH slice (#1–#3) it demonstrates.
 *
 * Hand-authored to match the gh-issue-review-manifest.ts voice; the features/ yaml
 * registration (proposal task 4.1) is a separate Land task, not a capture blocker.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test github-demo-issuepr --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test github-demo-issuepr --project=chromium
 */

/** One narrated beat on the external GitHub fixture page. */
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

export const GITHUB_DEMO_STEPS: readonly ReviewStep[] = [
  {
    id: "gh1-bug-mention",
    target: "gh-issue-bug",
    title: "A bug-labelled issue mentions @kitsoki",
    body: "slidey-128: grid-cards narration drifts off-screen. The @kitsoki mention on a bug-labelled issue is the ingress — that's all it takes to start a job (slice #1).",
    dwellMs: 5500,
  },
  {
    id: "gh2-ack-link",
    target: "gh-ack-comment",
    title: "kitsoki acks with one run link",
    body: "One reply: picked up the mention, dispatched the slidey-bugfix story, and posted a …/run/job-7f3a91c2 link. One mention, one job, one link (slices #1, #2).",
    dwellMs: 6000,
  },
  {
    id: "gh3-feature",
    target: "gh-issue-feature",
    title: "A feature issue routes to the design track",
    body: "The same loop reads the label: a feature-labelled issue dispatches the PRD→design story, not a straight bugfix. Label → story dispatch (slice #2).",
    dwellMs: 5500,
  },
  {
    id: "gh4-ci-fix",
    target: "gh-pr-ci-fix",
    title: "It watches CI and pushes a fix",
    body: "On the PR, render-tests went red. kitsoki reads the failure and pushes a fix to the same branch — CI-watch plus auto-fix (slice #3).",
    dwellMs: 6000,
  },
  {
    id: "gh5-rebase",
    target: "gh-pr-rebase",
    title: "A merge conflict, rebased away",
    body: "When main moved and the branch conflicted, kitsoki replayed the fix on top and reported 'rebased onto main' — the branch is mergeable again (slice #3).",
    dwellMs: 5500,
  },
  {
    id: "gh6-resolve",
    target: "gh-review-resolve",
    title: "Reviewer asks; kitsoki implements and resolves",
    body: "A reviewer wants the clamp to read the theme safe-area. kitsoki implements the change and resolves the parent comment — comment-driven implement (slice #3).",
    dwellMs: 6000,
  },
  {
    id: "gh7-guidance",
    target: "gh-guidance-request",
    title: "Low confidence — it asks first",
    body: "The retro theme has no safe-area inset. Rather than guess, kitsoki posts a guidance-request comment and holds the run. If in doubt, ask (slices #1, #3).",
    dwellMs: 6000,
  },
  {
    id: "gh8-done",
    target: "gh-status-done",
    title: "One rolling status, edited to done",
    body: "A single status comment, edited in place six times to 'done' — single voice, no comment flood. The whole job tracked on one thread (slice #1).",
    dwellMs: 6000,
  },
] as const;
