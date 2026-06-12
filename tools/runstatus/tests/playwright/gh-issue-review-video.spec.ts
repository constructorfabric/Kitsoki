/**
 * GitHub-review act of the cross-site gh-issues demo — the FIRST kitsoki tour
 * video that drives a site OTHER than kitsoki.
 *
 * It records the middle act of the bug→review→triage story: the bug filed from
 * the kitsoki web UI (act 1, report-bug-video) is now a real GitHub issue on
 * constructorfabric/Kitsoki, and a maintainer reviews it before picking it up in
 * the kitsoki dev-story (act 3, dev-story-bugfix-video). The orchestrator
 * (scripts/record-gh-issues-demo.sh) concatenates the three MP4s with ffmpeg.
 *
 * WHY A STATIC FIXTURE, NOT LIVE GITHUB: a kitsoki demo must be deterministic
 * and free — same input, same frames, no network, no auth, no real issue churn
 * (docs/skills/kitsoki-ui-demo/SKILL.md → "Deterministic, no-LLM"). So act 2
 * drives a faithful static replica of the issue (fixtures/gh-issue-review.html)
 * over file://, rendering exactly what host.gh.ticket.create produces.
 *
 * HOW IT'S NARRATED: the kitsoki tour overlay (__startTourWithSteps,
 * [data-testid=tour-*]) only exists inside the kitsoki SPA. On an external page
 * we narrate with the PORTABLE helpers — makeCaption + makeSpotlight from
 * _helpers/demo.ts — which inject plain DOM and work anywhere. The steps come
 * from src/tour/gh-issue-review-manifest.ts.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test gh-issue-review-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test gh-issue-review-video --project=chromium
 */
import { test, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  dwell,
  SETTLE_MS,
} from "./_helpers/server.js";
import { installCurtain, liftCurtain, makeCaption, makeSpotlight } from "./_helpers/demo.js";
import { GH_ISSUE_REVIEW_STEPS } from "../../src/tour/gh-issue-review-manifest.js";

const FIXTURE = path.join(repoRoot, "tools", "runstatus", "tests", "playwright", "fixtures", "gh-issue-review.html");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/gh-issue-review-manifest.ts";

const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "gh-issue-review");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

function diag(msg: string): void {
  try {
    fs.appendFileSync(ERROR_TXT, `[${new Date().toISOString()}] ${msg}\n`);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(() => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_TXT, "");
});

test("gh-issue-review cross-site act video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 2,
    recordVideo: { dir: VIDEO_DIR, size: { width: 1600, height: 900 } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    // Curtain BEFORE the first goto so the file:// load doesn't flash by.
    await installCurtain(page, "GitHub · the bug is now a real issue");
    diag(`opening fixture ${FIXTURE}`);
    await page.goto(`file://${FIXTURE}`, { waitUntil: "load" });
    await dwell(page, SETTLE_MS);

    const caption = await makeCaption(page);
    const spotlight = await makeSpotlight(page);

    // Stage the opening frame, then lift the curtain onto the rendered issue.
    await caption("Reviewing the GitHub issue", "Filed from the kitsoki web UI in act 1.", 0);
    await liftCurtain(page);
    await dwell(page, SETTLE_MS);

    for (const step of GH_ISSUE_REVIEW_STEPS) {
      diag(`step ${step.id}`);
      const sel = step.target ? `[data-testid="${step.target}"]` : null;
      // makeSpotlight scrolls the target into view, measures, and frames it
      // atomically — no spec-side scroll race.
      await spotlight(sel);
      chapters.open(step.id, step.title, CHAPTER_SOURCE);
      await caption(step.title, step.body, step.dwellMs ?? 5000);
      await shot(page, step.id);
    }

    // Lift the spotlight and hold a closing frame.
    await spotlight(null);
    await caption("Next: triage it back in kitsoki", "host.gh.ticket picks the issue up in the dev-story.", 3500);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? (e.stack ?? e.message) : String(e)}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "gh-issue-review-demo");
    if (mp4) writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[gh-issue-review-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
