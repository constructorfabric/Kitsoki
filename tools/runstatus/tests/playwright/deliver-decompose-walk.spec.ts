/**
 * deliver-decompose-walk.spec.ts — the WEB proof of the decompose-vs-direct
 * chain (proposal docs/proposals/deliver-canonical-decomposition.md, task B4
 * 5.1): dev-story's `design_done` room hands off to `deliver` via `go_deliver`,
 * which decomposes the published proposal into briefs, lints/reviews them, and
 * fans `fleet` over the result — driven against a REAL `kitsoki web` server in
 * the deterministic no-LLM posture (`--flow
 * stories/dev-story/flows/design_to_decompose_to_impl.yaml`).
 *
 * This is the web sibling of the flow-only proof
 * (`go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows
 * 'flows/design_to_decompose_to_impl.yaml'`) — same fixture, driven through the
 * actual UI: home story library → new session → the compound intents fired as
 * on-screen buttons (`intent-btn-<name>`, the `bf__accept`-style idiom from
 * dev-story-bugfix-video.spec.ts), asserting the resulting `current-state`
 * after each turn. The fixture seeds `design_file` via `initial_world` (the
 * `slidey_decomposition.yaml` precedent: a mid-graph `initial_state` needs no
 * slot-bearing intent to reach it), so a brand-new session lands directly on
 * `design_done` with nothing left to type.
 *
 * `--flow` stubs every `host.*` call (decomposer/reviewer agent responses,
 * fleet's integrate/verify/cleanup execs) — this proves the CONVERSATION / ROOM
 * WALK renders and drives correctly, not that any artifact got written to disk.
 *
 * Run:
 *   pnpm exec playwright test deliver-decompose-walk --project=chromium
 */
import { test, expect, type Page } from "@playwright/test";
import path from "path";
import { startWebServer, repoRoot, demoAddr, type WebServer } from "./_helpers/server.js";

const ADDR = demoAddr(7763);
const STORY_DIR = path.join(repoRoot, "stories");
const FLOW = path.join(
  repoRoot,
  "stories",
  "dev-story",
  "flows",
  "design_to_decompose_to_impl.yaml",
);

let server: WebServer;

test.beforeAll(async () => {
  // one-shot: the fixture's decompose → lint → review chain auto-advances
  // through synthetic emit/decision gates in a single turn, matching the
  // flow-test harness's default (staged, kitsoki web's own default, would
  // stall at the first no-operator-input gate — see
  // internal/testrunner/flows.go's execution-modes doc).
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR, mode: "one-shot" });
});

test.afterAll(() => server?.stop());

/** Assert the drive view's current-state reaches `state`. */
async function expectState(page: Page, state: string): Promise<void> {
  await expect(page.getByTestId("current-state")).toHaveText(state, { timeout: 15000 });
}

/**
 * Click a slotless intent's on-camera button and assert the resulting state.
 *
 * Fleet re-enters the SAME `deliver.fleet.ship.configure` leaf room once per
 * brief (dispatch → ship.configure → ship.tail.verify → dispatch → …), so a
 * click whose expected state repeats the room the fixture is ALREADY sitting
 * in cannot be confirmed by text equality alone — that assertion would
 * trivially pass on the STALE pre-click text before the click's own turn (and
 * fleet's async re-dispatch to the next brief) has even run, letting the walk
 * race ahead of the backend. Capture the pre-click text and first require it
 * to CHANGE AWAY (proving a real turn — possibly through fleet's transient
 * dispatch/tail states — actually happened) before waiting for the expected
 * state to settle.
 */
async function driveButton(page: Page, intent: string, expectStateName: string): Promise<void> {
  const before = (await page.getByTestId("current-state").textContent().catch(() => "")) ?? "";
  const btn = page.getByTestId(`intent-btn-${intent}`).first();
  await expect(btn).toBeVisible({ timeout: 15000 });
  await btn.click();
  await expect(page.getByTestId("current-state")).not.toHaveText(before, { timeout: 15000 });
  await expectState(page, expectStateName);
}

test("web: design_done → go_deliver → deliver decompose/lint/review → fleet fan-out → landing", async ({
  page,
}) => {
  test.setTimeout(60000);

  // ── Home → new session ────────────────────────────────────────────────────
  await page.goto(`${server.base}/#/`);
  await expect(page.getByTestId("home-view")).toBeVisible({ timeout: 15000 });

  const devStoryCard = page
    .locator('[data-testid="story-card"][data-story-path$="/dev-story/app.yaml"]')
    .first();
  await expect(devStoryCard).toBeVisible({ timeout: 8000 });
  await devStoryCard.getByTestId("new-session-btn").click();

  await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 });

  // The fixture's initial_state (design_done) + initial_world (design_file)
  // are seeded with no slot-bearing intent required, so the fresh session
  // lands directly here — nothing to type.
  await expectState(page, "design_done");

  // ── design_done → deliver.configure (decompose-vs-direct: decompose arc) ──
  await driveButton(page, "go_deliver", "deliver.configure");

  // ── deliver.configure → deliver.fleet.load (decompose → lint → review) ────
  await driveButton(page, "deliver__start", "deliver.fleet.load");

  // ── deliver.fleet.load → deliver.fleet.ship.configure (fan-out begins) ────
  await driveButton(page, "deliver__fleet__start", "deliver.fleet.ship.configure");

  // ── First brief ships (merge lock held; one more brief queued) ────────────
  await driveButton(
    page,
    "deliver__fleet__ship__integrate_existing",
    "deliver.fleet.ship.configure",
  );

  // ── Second brief ships → fleet summary → @exit:done → landing ─────────────
  await driveButton(page, "deliver__fleet__ship__integrate_existing", "landing");
});
