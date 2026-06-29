/**
 * Act 1 of the kitsoki-github-agent demo — the GitHub side of the @kitsoki loop.
 *
 * It records the eight-scene GitHub-side storyboard (proposal §Tour storyboard,
 * Act 1): a bug-labelled issue mentioning @kitsoki, the kitsoki ack comment with a
 * …/run/<job-id> link, a feature-labelled issue routed to the design track, a PR
 * with red CI being auto-fixed, a merge-conflict rebase, a reviewer-comment
 * implement + parent-comment resolve, a low-confidence guidance request, and the
 * single rolling status comment edited to "done" (single voice, no flood).
 *
 * WHY A STATIC FIXTURE, NOT LIVE GITHUB: a kitsoki demo must be deterministic and
 * free — same input, same frames, no network, no auth, no real issue churn
 * (.agents/skills/kitsoki-ui-demo/SKILL.md → "Deterministic, no-LLM"). So Act 1
 * drives a faithful static replica of the thread (fixtures/gh-thread.html) over
 * file://, rendering what the host.gh.* path of slices #1–#3 produces. The worked
 * ticket is slidey-128 grid-cards narration-drift, so kitsoki improving slidey is
 * narrated on slidey's own repo (proposal §Case study).
 *
 * HOW IT'S NARRATED: the kitsoki tour overlay (__startTourWithSteps,
 * [data-testid=tour-*]) only exists inside the kitsoki SPA. On an external page we
 * narrate with the PORTABLE helpers — makeCaption + makeSpotlight from
 * _helpers/demo.ts — which inject plain DOM and work anywhere. Steps come from
 * src/tour/github-demo-manifest.ts.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test github-demo-issuepr --project=chromium
 * Record at watch-speed (the shippable clip — do NOT ship the WEB_CHAT_PACE=0 cut):
 *   pnpm exec playwright test github-demo-issuepr --project=chromium
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
import { cameraContext } from "./_helpers/camera.js";
import { installCurtain, liftCurtain, makeCaption, makeSpotlight } from "./_helpers/demo.js";
import { GITHUB_DEMO_STEPS } from "../../src/tour/github-demo-manifest.js";

const FIXTURE = path.join(repoRoot, "tools", "runstatus", "tests", "playwright", "fixtures", "gh-thread.html");
const CHAPTER_SOURCE = "tools/runstatus/src/tour/github-demo-manifest.ts";

// Output under tools/runstatus/.artifacts (gitignored) — the canonical
// github-demo-act1.mp4 + chapter sidecar feed the composite deck's Act-1 video
// scene (proposal §Net-new / composite path).
const ARTIFACT_DIR = path.join(repoRoot, "tools", "runstatus", ".artifacts", "github-demo-act1");
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

test("github-demo act1 issue+pr video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  try {
    // Curtain BEFORE the first goto so the file:// load doesn't flash by.
    await installCurtain(page, "GitHub · the @kitsoki loop");
    diag(`opening fixture ${FIXTURE}`);
    await page.goto(`file://${FIXTURE}`, { waitUntil: "load" });
    await dwell(page, SETTLE_MS);

    const caption = await makeCaption(page);
    const spotlight = await makeSpotlight(page);

    // Stage the opening frame, then lift the curtain onto the rendered thread.
    await caption("The GitHub side of @kitsoki", "A mention becomes a job becomes a PR — no App, no webhook, no LLM.", 0);
    await liftCurtain(page);
    await dwell(page, SETTLE_MS);

    for (const step of GITHUB_DEMO_STEPS) {
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
    await caption("Next: open it in kitsoki", "The run link jumps to the web viewer — that's act 2.", 3500);
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? (e.stack ?? e.message) : String(e)}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "github-demo-act1");
    if (mp4) writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[github-demo-issuepr] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
