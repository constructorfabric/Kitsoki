/**
 * External-target PRD → Design video — gears-rust edition.
 *
 * Records the gears-rust POC: dev-story pointed at a FOREIGN repo
 * (constructorfabric/gears-rust), driving a gear's PRD → Design walk so the
 * docs publish into the gears-rust checkout as gears-sdlc PRD.md / DESIGN.md.
 * The whole video is TOUR-DRIVEN (src/tour/gears-prd-design-manifest.ts via
 * window.__startTourWithSteps) and stays in the MAIN CHAT: home story library
 * → new session → the chat → author a PRD by talking it through → watch it
 * publish into the gears tree → continue into the design intake → author the
 * gears-sdlc DESIGN that publishes alongside it.
 *
 * THE CONVERSATION IS THE DEMO. Every pipeline turn is driven THROUGH THE PAGE
 * (composer fills + intent-button clicks), not via off-camera RPC — so each
 * turn renders into the chat transcript the spotlight then frames. (An
 * RPC-driven turn advances server state but never renders in the driving
 * page's transcript, which is why this spec clicks.)
 *
 * No LLM: stubs from stories/gears-rust/flows/prd_to_design_full.yaml (the
 * full single-session walk, also a `test flows` fixture).
 *
 * Record:  pnpm exec playwright test gears-prd-design --project=chromium
 * Fast:    WEB_CHAT_PACE=0 pnpm exec playwright test gears-prd-design --project=chromium
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page, type Locator } from "@playwright/test";
import path from "path";
import {
  startWebServer,
  repoRoot,
  makeShot,
  waitForState,
  prepareVideoDir,
  saveVideoAsMp4,
  dwell,
  cinematicGoto,
  SETTLE_MS,
  type WebServer,
} from "./_helpers/server.js";
import { DEMO_VIEWPORT, captureDiagnostics } from "./_helpers/demo.js";
import { GEARS_PRD_DESIGN_TOUR_STEPS, type TourStep } from "../../src/tour/gears-prd-design-manifest.js";

// Port distinct from all other specs (7740–7758 taken).
const ADDR = "127.0.0.1:7759";
const STORY_DIR = path.join(repoRoot, "stories", "gears-rust");
const FLOW = path.join(STORY_DIR, "flows", "prd_to_design_full.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "gears-prd-design");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "video");

let server: WebServer;
test.beforeAll(async () => {
  prepareVideoDir(VIDEO_DIR);
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

async function resolveTarget(page: Page, step: TourStep): Promise<Locator> {
  return page.getByTestId(step.target!).first();
}

test("external-target PRD → Design — gears-rust", async () => {
  test.setTimeout(300000);
  const browser: Browser = await chromium.launch({ headless: true });
  const context: BrowserContext = await browser.newContext({
    viewport: { ...DEMO_VIEWPORT },
    recordVideo: { dir: VIDEO_DIR, size: { ...DEMO_VIEWPORT } },
  });
  const page: Page = await context.newPage();
  const video = page.video();
  const shot = makeShot(ARTIFACT_DIR);
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  // ── Page-driving helpers: drive every turn THROUGH THE PAGE so the chat
  // transcript renders it. DOM-dispatch the click so it fires through the
  // tour overlay regardless of paint order (the overlay backdrop is otherwise
  // a hit-test wall over controls below the spotlight). ───────────────────────
  const clickIntent = async (intent: string) => {
    const btn = page.getByTestId(`intent-btn-${intent}`).first();
    await expect(btn).toBeVisible({ timeout: 15000 });
    await btn.scrollIntoViewIfNeeded().catch(() => undefined);
    await btn.evaluate((el) => (el as HTMLElement).click());
  };
  const typeAndSend = async (text: string) => {
    const input = page.getByTestId("composer-input").first();
    await expect(input).toBeVisible({ timeout: 15000 });
    await input.fill(text);
    await dwell(page, 200); // let v-model settle so composer-send enables
    await page.getByTestId("composer-send").first().evaluate((el) => (el as HTMLElement).click());
  };
  const scrollChatToLatest = async () => {
    await page
      .getByTestId("chat-transcript")
      .first()
      .evaluate((el) => {
        el.scrollTop = el.scrollHeight;
      })
      .catch(() => undefined);
    await dwell(page, SETTLE_MS);
  };

  try {
    // ── 1. Open the home story library and start the tour ON it ──────────────
    mark("navigating home");
    await cinematicGoto(page, `${server.base}/#/`, { waitForTestId: "home-view" });

    await page.evaluate((stepsJson: string) => {
      (window as unknown as { __startTourWithSteps?: (s: string) => void })
        .__startTourWithSteps?.(stepsJson);
    }, JSON.stringify(GEARS_PRD_DESIGN_TOUR_STEPS));
    await expect(page.getByTestId("tour-overlay")).toBeVisible({ timeout: 8000 });

    // ── 2. Walk the GEARS_PRD_DESIGN_TOUR_STEPS ──────────────────────────────
    for (const step of GEARS_PRD_DESIGN_TOUR_STEPS) {
      mark(`step ${step.id}`);
      const currentUrl = page.url();
      const currentRouteKind = currentUrl.includes("/chat")
        ? "interactive"
        : currentUrl.match(/#\/s\/[0-9a-f-]{36}$/)
          ? "any"
          : "home";
      if (step.route !== "any" && step.route !== currentRouteKind) {
        mark(`  route-skip (${currentRouteKind})`);
        continue;
      }

      // ── Pre-step setup: drive the pipeline THROUGH THE PAGE so this step's
      // chat content exists and is on-screen before the spotlight lands. ──────
      if (step.id === "gr-prd-discovery") {
        // `main` is a SEMANTIC room (no intent buttons); the deterministic
        // router matches the typed verb "prd" → go_prd with no LLM, exactly as
        // an operator would type it. From prd.idle on, rooms expose composers
        // and intent buttons, so the rest is driven by composer + clicks.
        await waitForState(page, "core.main", 15000);
        await typeAndSend("prd");
        await waitForState(page, "core.prd.idle", 12000);
        await typeAndSend("I want a multi-tenant notes-service gear for the platform");
        await waitForState(page, "core.prd.idle", 12000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-prd-clarify") {
        await clickIntent("core__prd__start");
        await waitForState(page, "core.prd.search", 20000);
        await clickIntent("core__prd__confirm");
        await waitForState(page, "core.prd.clarifying", 20000);
        // Round 1: answer the questions, then submit. clarifying has no submit
        // BUTTON — the verbs live in prose, caught by the deterministic router
        // ("submit" is a submit_answers example).
        await typeAndSend("Platform users; the metric is notes-saved-per-session");
        await waitForState(page, "core.prd.clarifying", 12000);
        await typeAndSend("submit");
        await waitForState(page, "core.prd.brief", 20000);
        // Round 2: the brief's `clarify` loops back for another round,
        // preserving the accumulated transcript.
        await clickIntent("core__prd__clarify");
        await waitForState(page, "core.prd.clarifying", 20000);
        await typeAndSend("Tenant isolation is mandatory; admins see only aggregate metrics");
        await waitForState(page, "core.prd.clarifying", 12000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-prd-draft") {
        await typeAndSend("submit");
        await waitForState(page, "core.prd.brief", 20000);
        await clickIntent("core__prd__confirm");
        await waitForState(page, "core.prd.references", 20000);
        await clickIntent("core__prd__confirm");
        await waitForState(page, "core.prd.drafting", 20000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-published") {
        await clickIntent("core__prd__accept");
        await waitForState(page, "core.prd_published", 20000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-design-intake") {
        // prd_published uses a prose `list:` (no choice buttons) → semantic
        // room; "continue" is a continue-intent example matched by the router.
        await typeAndSend("continue");
        await waitForState(page, "core.design", 20000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-design-refine") {
        await typeAndSend("Realize the notes-service PRD as a gears-sdlc DESIGN");
        await waitForState(page, "core.design_search", 20000);
        await clickIntent("core__confirm");
        await waitForState(page, "core.design_refine", 20000);
        // Re-run the refiner on the brief — the design's refine loop. The
        // `refine` choice fires `discuss` (intent-btn-core__discuss); the
        // refiner reworks the brief and the gaps update, then `ready` checks it.
        await clickIntent("core__discuss");
        await waitForState(page, "core.design_refine", 12000);
        await scrollChatToLatest();
      }
      if (step.id === "gr-design-done") {
        // `ready` re-enters design_refine and arms the brief judge; once it
        // returns `continue`, advance_brief appears as a choice the operator
        // clicks (the UI does not auto-advance the way a flow turn does).
        await clickIntent("core__ready");
        await expect(page.getByTestId("intent-btn-core__advance_brief").first()).toBeVisible({ timeout: 20000 });
        await clickIntent("core__advance_brief");
        await waitForState(page, "core.design_draft", 20000);
        await clickIntent("core__accept");
        await waitForState(page, "core.design_done", 20000);
        await scrollChatToLatest();
      }

      if (step.waitForTarget) {
        await expect(page.getByTestId(step.waitForTarget).first()).toBeVisible({ timeout: 15000 });
      }

      // Anti-drift assertion: the popover must show THIS step's title.
      const titleEl = page.getByTestId("tour-title");
      const actualTitle = await titleEl.textContent({ timeout: 8000 }).catch(() => "");
      if (actualTitle !== step.title) {
        const remaining = GEARS_PRD_DESIGN_TOUR_STEPS.slice(
          GEARS_PRD_DESIGN_TOUR_STEPS.indexOf(step) + 1
        );
        if (remaining.some((s) => s.title === actualTitle)) {
          mark(`  drift-skip: overlay on "${actualTitle}"`);
          continue;
        }
      }
      await expect(titleEl).toHaveText(step.title, { timeout: 12000 });

      await dwell(page, step.dwellMs ?? 3000);
      await shot(page, step.id);

      if (step.kind === "explain") {
        await page.getByTestId("tour-next").click();
        await dwell(page, 700);
      } else {
        const target = await resolveTarget(page, step);
        await target.scrollIntoViewIfNeeded().catch(() => undefined);
        if (step.advance === "route-match") {
          await target.click();
          await page.waitForTimeout(300);
          if (step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });
          } else if (step.advanceRoute === "any") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}$/, { timeout: 15000 });
          }
          await dwell(page, 1000);
        } else {
          await target.evaluate((el) => (el as HTMLElement).click());
          await dwell(page, 1000);
        }
      }
    }

    // The final step's "Done" closes the tour.
    await expect(page.getByTestId("tour-overlay")).toHaveCount(0, { timeout: 5000 });
  } catch (err) {
    onThrow(err);
    throw err;
  } finally {
    await context.close();
    await saveVideoAsMp4(video, ARTIFACT_DIR, "gears-prd-design");
    await browser.close();
  }
});
