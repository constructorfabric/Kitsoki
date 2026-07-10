/**
 * dev-story "bug triage → autonomous bugfix pipeline" feature-tour video demo.
 *
 * Drives the dev-story tour against a real `kitsoki web` server in the
 * deterministic no-LLM posture (--flow tour_triage_to_bugfix.yaml; the flow's
 * host_handlers stub every host.* call, so NO host cassette is needed) and
 * records a video + per-scene screenshots to .artifacts/dev-story-bugfix/.
 *
 * Like the golden agent-actions-video.spec.ts, this spec runs ONLY the
 * DEV_STORY_BUGFIX_TOUR_STEPS from src/tour/generated/dev-story-bugfix.ts via
 * window.__startTourWithSteps. The tour drives the whole video: it opens on the
 * home story library and its route-match action step navigates home → new
 * session → the drive view, then the explain beats narrate the triage + pipeline
 * walk while the spec drives the matching intents between beats.
 *
 * Driving mechanics (the load-bearing part):
 *   - Slotless intents (go_ticket_search, go_bugfix, bf__accept ×7) are driven
 *     on-camera by clicking intent-btn-<name>; the resulting current-state /
 *     state-badge is the hard signal the turn landed.
 *   - Slotted intents (search_tickets, pick_ticket) are driven via the composer:
 *     composer-select → composer-input → composer-send.
 * The drivable surface (current-state, state-badge, composer-*, intent-btn-*) is
 * the InteractiveView ("Drive (chat)"), which is also where the Observe link
 * lives — so the tour drives there and points out the read-only observer.
 *
 * Validate fast (no dwells):
 *   WEB_CHAT_PACE=0 pnpm exec playwright test dev-story-bugfix-video --project=chromium
 * Record at watch-speed:
 *   pnpm exec playwright test dev-story-bugfix-video --project=chromium
 *
 * NOTE: the harness suppresses Playwright stdout, so per-step progress and any
 * failure context is also written to .artifacts/dev-story-bugfix/ERROR.txt.
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import {
  startWebServer,
  repoRoot,
  makeShot,
  prepareVideoDir,
  saveVideoAsMp4,
  ChapterRecorder,
  writeChapters,
  demoAddr,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { cameraContext } from "./_helpers/camera.js";
import { DEV_STORY_BUGFIX_TOUR_STEPS, type TourStep } from "../../src/tour/generated/dev-story-bugfix.js";

// The feature-catalog source of truth for this spec's tour steps: each step
// becomes a chapter (source_ref kind=tour) in the MP4's sidecar.
const CHAPTER_SOURCE = "features/dev-story-bugfix.yaml";

// 7760 — confirmed free; distinct from every other spec's port so parallel
// runs never race on the same bind.
const ADDR = demoAddr(7760);
const STORY_DIR = path.join(repoRoot, "stories", "dev-story");
const FLOW = path.join(STORY_DIR, "flows", "tour_triage_to_bugfix.yaml");
// No host cassette: the flow's host_handlers stub every host.* call along the
// path (triage search + transition, git/fs/inbox, the agent judge verdicts).
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "dev-story-bugfix");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");
const ERROR_TXT = path.join(ARTIFACT_DIR, "ERROR.txt");

let server: WebServer;

function diag(msg: string): void {
  const line = `[${new Date().toISOString()}] ${msg}\n`;
  try {
    fs.appendFileSync(ERROR_TXT, line);
  } catch {
    /* best-effort */
  }
}

test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  fs.writeFileSync(ERROR_TXT, "");
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

/**
 * Drive a slotless intent by clicking its on-camera button, then assert the
 * resulting state. The button click fires the turn AND (the tour having spotlit
 * it as an explain step) is purely a real control — the overlay does not gate
 * it. A beat before so the cursor's intent reads on camera.
 */
async function driveButton(page: Page, intent: string, expectStateName: string): Promise<void> {
  diag(`driveButton ${intent} → ${expectStateName}`);
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await dwell(page, 500);
  // Dispatch the DOM click directly: the tour overlay paints a backdrop while
  // its popover narrates this control, so a hit-test click can be intercepted.
  // The element's own @click still fires the turn.
  await btn.evaluate((el) => (el as HTMLElement).click());
  await expectState(page, expectStateName);
  await dwell(page, 600);
}

/**
 * Idempotently advance the bugfix pipeline one room by clicking bf__accept.
 * Reads current-state first: clicks only when at `fromState`; if the run has
 * already drifted to `toState` (the overlay auto-advanced and a prior beat
 * accepted), it no-ops. This decouples the on-camera pipeline walk from any
 * overlay step drift so exactly one accept lands per room.
 */
async function driveAccept(page: Page, fromState: string, toState: string): Promise<void> {
  const cur = (await page.getByTestId("current-state").textContent())?.trim() ?? "";
  diag(`driveAccept ${fromState}→${toState} (current=${cur})`);
  if (cur === toState) {
    diag(`  already at ${toState}; skip`);
    return;
  }
  if (cur !== fromState) {
    // Be tolerant: wait briefly for the expected fromState in case a prior turn
    // is still settling.
    await expect(page.getByTestId("current-state")).toHaveText(fromState, { timeout: 15000 });
  }
  const btn = page.getByTestId("intent-btn-bf__accept").first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await dwell(page, 500);
  await btn.evaluate((el) => (el as HTMLElement).click());
  await expectState(page, toState);
  await dwell(page, 600);
}

/**
 * Drive a slotted (text-slot) intent through the legacy composer the triage
 * room renders: an optional composer-select (present only when >1 text intent),
 * a composer-input textarea, and composer-send. We set the select (if any),
 * fill the textarea (firing input so v-model picks it up), and submit the
 * composer form. DOM-level so the tour popover never intercepts.
 */
async function driveComposer(
  page: Page,
  intent: string,
  value: string,
  expectStateName: string,
): Promise<void> {
  diag(`driveComposer ${intent}="${value}" → ${expectStateName}`);
  const select = page.getByTestId("composer-select");
  if ((await select.count()) > 0) {
    await select.evaluate((el, v) => {
      const sel = el as HTMLSelectElement;
      sel.value = v;
      sel.dispatchEvent(new Event("change", { bubbles: true }));
    }, intent).catch(() => undefined);
    await dwell(page, 300);
  }
  const input = page.getByTestId("composer-input").first();
  await expect(input).toBeVisible({ timeout: 15000 });
  await input.evaluate((el, v) => {
    const ta = el as HTMLTextAreaElement;
    ta.value = v;
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
  await dwell(page, 600);
  // Submit via the form so the @submit.prevent handler fires regardless of
  // popover placement.
  const form = page.getByTestId("composer").first();
  await form.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, 600);
}

/**
 * Drive a slotted intent rendered as a typed-view choice param-form
 * (`form.input-bar__choice-param-form[data-intent="<intent>"]`). When several
 * forms share the same data-intent (e.g. pick_ticket's row-number vs by-id
 * variants), `placeholderMatch` disambiguates by the input's placeholder text.
 * The Send button is disabled until the input has a value, so we fire the input
 * event first (enabling it) and then submit the form. DOM-level so the tour
 * popover never intercepts.
 */
async function driveParamForm(
  page: Page,
  intent: string,
  value: string,
  expectStateName: string,
  placeholderMatch?: string,
): Promise<void> {
  diag(`driveParamForm ${intent}="${value}" → ${expectStateName}`);
  let form = page.locator(`form.input-bar__choice-param-form[data-intent="${intent}"]`);
  if (placeholderMatch) {
    // The param composer is a wrapping <textarea> now (not a single-line input).
    form = form.filter({ has: page.locator(`textarea[placeholder*="${placeholderMatch}" i]`) });
  }
  const f = form.first();
  await expect(f).toBeVisible({ timeout: 15000 });
  const input = f.locator("textarea").first();
  await input.evaluate((el, v) => {
    const t = el as HTMLTextAreaElement;
    t.value = v;
    t.dispatchEvent(new Event("input", { bubbles: true }));
  }, value);
  await dwell(page, 600);
  await f.evaluate((el) => (el as HTMLFormElement).requestSubmit());
  await expectState(page, expectStateName);
  await dwell(page, 600);
}

/**
 * Drive the turn associated with a given manifest step (performed AFTER its
 * narration screenshot, BEFORE advancing to the next step). Steps with no entry
 * are pure narration. Each entry asserts the resulting current-state — the hard
 * signal from the flow fixture's verified path.
 */
async function driveForStep(page: Page, stepId: string, sessionId: string): Promise<void> {
  switch (stepId) {
    case "ds-triage-open":
      // `main` is a semantic-routing room: no intent buttons, only a free-text
      // composer routed by the semantic router (non-deterministic without an
      // LLM). Drive the verified explicit intent through THIS view's own store
      // path via the __kitsokiSubmitIntent test hook, so the chat + InputBar
      // re-render reactively (an out-of-band RPC would leave the view stale).
      diag("submit go_ticket_search via page store hook");
      await page.evaluate(async () => {
        await (window as unknown as {
          __kitsokiSubmitIntent?: (n: string, s?: Record<string, unknown>) => Promise<void>;
        }).__kitsokiSubmitIntent?.("go_ticket_search", {});
      });
      await expectState(page, "ticket_search");
      await dwell(page, 600);
      break;
    case "ds-triage-pick":
      // No search detour: ticket_search's on_enter already fetched the queue,
      // so we pick straight from it. pick_ticket renders as choice param-forms
      // (a row-number variant + a by-id variant); pick the by-id form (the flow
      // fixture supplies the ticket id) and submit it. The pick arc self-targets
      // ticket_search, so the room stays put with AA-13268 marked ✓ Ready.
      await driveParamForm(page, "pick_ticket", "AA-13268", "ticket_search", "ticket id");
      break;
    case "ds-handoff":
      await driveButton(page, "go_bugfix", "bf.reproducing");
      break;
    case "ds-bf-reproducing":
      await driveAccept(page, "bf.reproducing", "bf.proposing");
      break;
    case "ds-bf-proposing":
      await driveAccept(page, "bf.proposing", "bf.implementing");
      break;
    case "ds-bf-implementing":
      await driveAccept(page, "bf.implementing", "bf.testing");
      break;
    case "ds-bf-testing":
      await driveAccept(page, "bf.testing", "bf.reviewing");
      break;
    case "ds-bf-reviewing":
      await driveAccept(page, "bf.reviewing", "bf.validating");
      break;
    case "ds-bf-validating":
      await driveAccept(page, "bf.validating", "bf.done");
      break;
    case "ds-bf-done":
      await driveAccept(page, "bf.done", "pr.open_pr");
      break;
    default:
      break;
  }
}

test("dev-story triage → autonomous bugfix feature-tour video", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext(
    cameraContext({ recordVideoDir: VIDEO_DIR }),
  );
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);

  // Accumulate per-step time windows for the chapter sidecar. The clock starts
  // now so windows line up with the recorded MP4 timeline.
  const chapters = new ChapterRecorder();

  let sessionId = "";

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    diag("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(DEV_STORY_BUGFIX_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk DEV_STORY_BUGFIX_TOUR_STEPS ──────────────────────────────────
    for (const step of DEV_STORY_BUGFIX_TOUR_STEPS) {
      diag(`step ${step.id}`);
      // Mirror the overlay's route-guard. The intro home step is "home"; once we
      // click New session we're on /chat ("interactive"); all driving steps are
      // route "any" so they show on the drive view.
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        diag(`  route-skip (${currentRouteKind})`);
        continue;
      }

      // Honor DOM-presence preconditions.
      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // If the overlay has drifted ahead of this loop step (its internal
      // anchoring can auto-advance when a target/route settles), re-sync it back
      // to THIS step so the popover narrates the room we're about to drive. The
      // tour store exposes goTo via the same window surface as the start hook.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = (await titleEl.textContent({ timeout: 8000 }).catch(() => "")) ?? "";
      if (actualTitle !== step.title) {
        diag(`  re-sync overlay from "${actualTitle}" → "${step.title}"`);
        await page.evaluate((id: string) => {
          (window as unknown as { __tourGoTo?: (s: string) => void }).__tourGoTo?.(id);
        }, step.id);
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      // This step's spotlight is settled and on-screen — open its chapter
      // (auto-closes the prior one) so the dwell below becomes its window.
      chapters.open(step.id, step.title, CHAPTER_SOURCE);

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "action") {
        // The only action step is the intro "New session" navigation.
        const target = page.getByTestId(step.target!).first();
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        await target.click();
        await page.waitForTimeout(300);
        if (step.advanceRoute === "interactive") {
          await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          const m = page.url().match(/\/s\/([0-9a-f-]{36})\/chat$/);
          if (m) {
            sessionId = m[1];
            diag(`session ${sessionId}`);
          }
        }
        await dwell(page, 1000);
      } else {
        // explain step: drive the associated turn (if any) on-camera AFTER its
        // narration screenshot. Driving is state-guarded + idempotent so it is
        // correct regardless of any overlay drift. Then advance the overlay.
        await driveForStep(page, step.id, sessionId);
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      }
    }

    // The final ds-done step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (e) {
    diag(`FAILED: ${e instanceof Error ? e.stack ?? e.message : String(e)}`);
    diag(`--- server log ---\n${server?.log?.() ?? ""}`);
    throw e;
  } finally {
    await context.close();
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, "dev-story-bugfix-demo");
    // Emit the producer-agnostic chapter sidecar beside the MP4: each tour
    // step → one chapter with source_ref kind=tour.
    writeChapters(mp4, chapters.list());
    await browser.close();
  }

  const pngs = fs.readdirSync(ARTIFACT_DIR).filter((f) => f.endsWith(".png"));
  console.log(`[dev-story-bugfix-video] screenshots (${pngs.length}) in ${ARTIFACT_DIR}`);
});
