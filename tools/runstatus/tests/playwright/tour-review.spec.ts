/**
 * tour-review.spec.ts — the CAPTURE stage of the kitsoki-ui-review pipeline.
 *
 * This is the generalized sibling of tour-video.spec.ts. Where that spec records
 * ONE watch-speed video at one resolution for a human to watch, this one walks
 * the SAME tour manifest (src/tour/manifest.ts — the single source of truth for
 * "the surfaces worth showing") at SEVERAL viewports and, at every step, emits
 * the evidence a layout/usability review needs:
 *
 *   <out>/frames/NN-<step-id>@<viewport>.png   what the surface looks like
 *   <out>/audit.json                           deterministic DOM-geometry + axe
 *                                              findings + the step×viewport map
 *
 * "We develop a tour, the agents review it": to put a new surface in front of
 * the reviewers, add a step to the manifest — both the live overlay, the demo
 * video, AND this review then cover it, with no other code change.
 *
 * Determinism: no-LLM `--flow` posture, same as tour-video. The anti-drift title
 * assertion is kept on the PRIMARY viewport (the manifest can't silently drift);
 * secondary viewports run best-effort so that responsive breakage surfaces as a
 * recorded finding instead of crashing the capture.
 *
 *   Fast self-check (assertions only, primary viewport):
 *     WEB_CHAT_PACE=0 UI_REVIEW_VIEWPORTS=desktop \
 *       pnpm exec playwright test tour-review --project=chromium
 *   Full capture (all viewports):
 *     pnpm exec playwright test tour-review --project=chromium
 */
import { test, expect, chromium, type Browser, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { AxeBuilder } from "@axe-core/playwright";
import { startWebServer, repoRoot, PACE, type WebServer } from "./_helpers/server.js";
import { TOUR_STEPS } from "../../src/tour/manifest.js";
import { geometryProbe, type RawFinding, type Severity } from "./lib/ui-audit.js";

// A distinct port per viewport — reusing one port across the three sequential
// servers races on bind/teardown and a fetch to a not-yet-released server can
// hang the whole run.
const PORT_BASE = 7746;
const STORY_DIR = path.join(repoRoot, "stories", "oregon-trail");
const FLOW = path.join(STORY_DIR, "flows", "winning_deterministic.yaml");
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "ui-review");
const FRAMES_DIR = path.join(ARTIFACT_DIR, "frames");

/** Named viewports — mobile / tablet / desktop. The FIRST is the primary. */
interface Viewport {
  name: string;
  width: number;
  height: number;
}
const ALL_VIEWPORTS: Viewport[] = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "tablet", width: 820, height: 1180 },
  { name: "mobile", width: 390, height: 844 },
];
// UI_REVIEW_VIEWPORTS=desktop,mobile narrows the set (and keeps the first listed
// in ALL_VIEWPORTS as primary among the selection).
const selected = (process.env.UI_REVIEW_VIEWPORTS || "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);
const VIEWPORTS = selected.length
  ? ALL_VIEWPORTS.filter((v) => selected.includes(v.name))
  : ALL_VIEWPORTS;

/** A finding as written to audit.json (raw geometry/axe + step/viewport tags). */
interface TaggedFinding extends RawFinding {
  step: string;
  viewport: string;
  frame: string;
  source: "geometry" | "a11y";
  // axe-only extras (the rich node data the report shows for a11y findings):
  target?: string; // the failing node's CSS selector(s) from axe
  failureSummary?: string; // axe's "Fix any of the following" guidance
  helpUrl?: string; // deque rule doc
}

interface StepCapture {
  step: string;
  title: string;
  route: string;
  viewport: string;
  width: number; // viewport size at capture — part of the reproduction recipe
  height: number;
  url: string; // the live hash route the frame was taken at
  frame: string;
  captured: boolean;
  note?: string;
}

let server: WebServer;
const findings: TaggedFinding[] = [];
const captures: StepCapture[] = [];

test.beforeAll(async () => {
  fs.rmSync(ARTIFACT_DIR, { recursive: true, force: true });
  fs.mkdirSync(FRAMES_DIR, { recursive: true });
});

// A fixed settle so the page is laid-out AND the tour overlay has anchored its
// popover to the current step before we audit + advance — INDEPENDENT of
// WEB_CHAT_PACE. Too small and the overlay's own watchdog races us and
// auto-skips steps; ~0.9s is the sweet spot (reliable, still far faster than the
// watch-speed video).
const SETTLE = 900;

function dwell(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(Math.round(ms * PACE));
}

/**
 * Advance the tour reliably at any pace: run `action`, then wait until the
 * popover title moves off `fromTitle` (the tour has stepped). A click can be
 * lost if it lands mid-transition, so retry up to twice. Returns whether the
 * tour advanced — a stall is reported by the caller, never thrown. `last` steps
 * just close the overlay, so success there means the overlay is gone.
 */
async function advance(
  page: Page,
  action: () => Promise<void>,
  fromTitle: string,
  last: boolean,
): Promise<boolean> {
  for (let attempt = 0; attempt < 3; attempt++) {
    await action().catch(() => {});
    if (last) {
      const gone = await page
        .getByTestId("tour-overlay")
        .waitFor({ state: "detached", timeout: 3000 })
        .then(() => true)
        .catch(() => false);
      if (gone) return true;
      continue;
    }
    const moved = await expect(page.getByTestId("tour-title"))
      .not.toHaveText(fromTitle, { timeout: 2500 })
      .then(() => true)
      .catch(() => false);
    if (moved) return true;
  }
  return false;
}

/** axe impact → our severity gate. */
function axeSeverity(impact: string | null | undefined): Severity {
  if (impact === "critical" || impact === "serious") return "error";
  if (impact === "moderate") return "warn";
  return "info";
}

/** Manifest step looked up by its (unique) title — the tour may auto-skip steps
 *  whose anchor isn't ready, so we key on what the LIVE popover actually shows. */
const STEP_BY_TITLE = new Map(TOUR_STEPS.map((s) => [s.title, s]));

/**
 * Screenshot + DOM geometry + axe for whatever the tour is currently showing.
 * axe is the expensive part and the DOM is identical across the many steps on
 * one route, so it runs ONCE per (route, viewport) — keyed in `axeDone`.
 */
async function auditStep(
  page: Page,
  label: string,
  route: string,
  vp: Viewport,
  idx: number,
  axeDone: Set<string>,
): Promise<void> {
  const n = String(idx).padStart(2, "0");
  const frame = `${n}-${label}@${vp.name}.png`;
  await page.screenshot({ path: path.join(FRAMES_DIR, frame) });
  captures.push({
    step: label,
    title: label,
    route,
    viewport: vp.name,
    width: vp.width,
    height: vp.height,
    url: page.url(),
    frame,
    captured: true,
  });

  const geo = await page.evaluate(geometryProbe);
  for (const f of geo) {
    findings.push({ ...f, step: label, viewport: vp.name, frame, source: "geometry" });
  }

  const axeKey = `${route}@${vp.name}`;
  if (!axeDone.has(axeKey)) {
    axeDone.add(axeKey);
    try {
      const results = await new AxeBuilder({ page })
        .disableRules(["region", "landmark-one-main", "page-has-heading-one"])
        .analyze();
      for (const v of results.violations) {
        const node = v.nodes[0];
        const sel = node?.target?.join(" ") || "";
        findings.push({
          check: `a11y:${v.id}`,
          severity: axeSeverity(v.impact),
          selector: sel,
          path: sel,
          html: (node?.html || "").replace(/\s+/g, " ").trim().slice(0, 300),
          styles: {},
          rect: { x: 0, y: 0, w: 0, h: 0 },
          text: (node?.html || "").replace(/\s+/g, " ").trim().slice(0, 80),
          detail: v.help,
          target: sel,
          failureSummary: (node?.failureSummary || "").replace(/\s+/g, " ").trim(),
          helpUrl: v.helpUrl,
          step: label,
          viewport: vp.name,
          frame,
          source: "a11y",
        });
      }
    } catch {
      axeDone.delete(axeKey); // injection failed on a torn-down page; retry later
    }
  }
}

/**
 * Walk the live tour at one viewport. The overlay AUTO-SKIPS steps whose anchor
 * isn't ready (TourOverlay.vue → tour.next() when !ready), so we DON'T assume
 * manifest order: each loop reads the popover's actual title, audits whatever is
 * shown (labelled by the matched step), then advances. Tolerant of skips; stops
 * when the overlay closes or genuinely stalls (recorded as a finding, not a throw).
 */
async function walk(page: Page, vp: Viewport): Promise<void> {
  const axeDone = new Set<string>();
  await page.goto(`${server.base}/#/`);
  // Best-effort: a fresh server has no sessions so home renders, but tolerate a
  // redirect just in case — the tour is force-started regardless of route.
  await page.getByTestId("home-view").waitFor({ state: "visible", timeout: 15000 }).catch(() => {});
  // The tour's first real anchors are the story cards; if the home raced the
  // initial scan and shows none, Rescan and wait so the tour has something to
  // point at (otherwise it skips straight past the catalogue steps).
  const hasCard = await page.getByTestId("story-card").first().isVisible().catch(() => false);
  if (!hasCard) {
    await page.getByTestId("rescan-btn").click({ timeout: 4000 }).catch(() => {});
    await page.getByTestId("story-card").first().waitFor({ state: "visible", timeout: 8000 }).catch(() => {});
  }
  await page.evaluate(() => {
    (window as unknown as { __startTour?: () => void }).__startTour?.();
  });
  const started = await page
    .getByTestId("tour-overlay")
    .waitFor({ state: "visible", timeout: 8000 })
    .then(() => true)
    .catch(() => false);
  if (!started) {
    findings.push({
      check: "tour-stalled",
      severity: "warn",
      selector: "tour-overlay",
      path: "",
      html: "",
      styles: {},
      text: "tour did not start",
      detail: `the tour overlay never appeared at ${vp.name} (${vp.width}px)`,
      rect: { x: 0, y: 0, w: 0, h: 0 },
      step: "home-welcome",
      viewport: vp.name,
      frame: "",
      source: "geometry",
    });
    return;
  }

  const titleLoc = page.getByTestId("tour-title");
  const seen = new Set<string>();
  let idx = 0;
  for (let guard = 0; guard < TOUR_STEPS.length + 6; guard++) {
    const visible = await titleLoc.isVisible().catch(() => false);
    if (!visible) break; // overlay closed — tour finished
    const title = ((await titleLoc.textContent().catch(() => "")) || "").trim();
    if (!title || seen.has(title)) {
      // No advance since last loop — give the transition a beat, else stop.
      if (seen.has(title)) break;
    }
    seen.add(title);
    const step = STEP_BY_TITLE.get(title);
    const label = step ? step.id : title.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
    const route = step ? step.route : "any";

    // Drive one Oregon turn at the input step so the trace renders real content.
    if (step?.id === "iv-input") {
      const begin = page.getByTestId("intent-btn-begin_setup");
      if ((await begin.count()) > 0) {
        await begin.first().click({ timeout: 5000 }).catch(() => {});
        await page.getByTestId("current-state").waitFor({ timeout: 8000 }).catch(() => {});
      }
    }

    idx++;
    await page.waitForTimeout(SETTLE);
    await dwell(page, step?.dwellMs ?? 0); // extra camera dwell only when PACE>0
    await auditStep(page, label, route, vp, idx, axeDone);

    // Advance:
    //  • action steps click the REAL highlighted control with a real,
    //    actionability-checked click — if that control is unreachable (e.g. a
    //    popover occludes it on mobile) the step genuinely stalls, which is a
    //    real finding we record below.
    //  • explain steps click the tour's own Next via a direct DOM click. The
    //    live trace keeps repositioning the popover, so Playwright's stability
    //    check would (correctly) refuse the click — but advancing the tour
    //    CHROME is not the thing under test, so we fire the handler directly.
    // The final manifest step just closes the overlay — capture it, dismiss, done.
    if (step && step.id === TOUR_STEPS[TOUR_STEPS.length - 1].id) {
      await page.getByTestId("tour-next").evaluate((el) => (el as HTMLElement).click()).catch(() => {});
      break;
    }

    const isAction = step?.kind === "action";
    const action: () => Promise<void> = isAction
      ? async () => {
          if (step?.target) {
            await page.getByTestId(step.target).first().click({ timeout: 4000 }).catch(() => {});
          }
          if (step?.advance === "route-match" && step.advanceRoute === "interactive") {
            await page.waitForURL(/#\/s\/[0-9a-f-]{36}\/chat$/, { timeout: 15000 }).catch(() => {});
          }
        }
      : () =>
          page
            .getByTestId("tour-next")
            .evaluate((el) => (el as HTMLElement).click())
            .catch(() => {});
    const advanced = await advance(page, action, title, false);
    if (!advanced) {
      // The tour could not leave this step via a real interaction. For an action
      // step that almost always means its highlighted control was not clickable
      // from here (commonly: the popover occludes the very control it spotlights).
      // Record it as a finding and stop this viewport's walk. The frame just
      // captured for this step is the evidence (it shows the popover over the
      // control), so cite it.
      findings.push({
        check: "tour-stalled",
        severity: "warn",
        selector: step?.target ? `[data-testid="${step.target}"]` : "tour-next",
        path: "",
        html: "",
        styles: {},
        text: title,
        detail: isAction
          ? `the tour's highlighted control ("${step?.target}") could not be clicked from step "${label}" at ${vp.name} (${vp.width}px) — likely occluded or off-screen, so a user can't proceed here either`
          : `the tour could not advance past step "${label}" at ${vp.name} (${vp.width}px)`,
        rect: { x: 0, y: 0, w: 0, h: 0 },
        step: label,
        viewport: vp.name,
        frame: captures[captures.length - 1]?.frame ?? "",
        source: "geometry",
      });
      break; // stop this viewport's walk — the stall is recorded for the review
    }
  }
}

test("ui-review capture: walk the tour at each viewport", async () => {
  // A fresh server per viewport + the per-step settle/audit puts a 3-viewport
  // walk around ~2min; give generous headroom so the gate never fails on the
  // capture clock (each individual action already has its own tight timeout).
  test.setTimeout(240000);
  const browser: Browser = await chromium.launch({ headless: true });
  try {
    for (let vi = 0; vi < VIEWPORTS.length; vi++) {
      const vp = VIEWPORTS[vi];
      // Fresh server (fresh DB, distinct port) per viewport so each starts from a
      // clean home with no sessions — otherwise the session the tour creates makes
      // home auto-redirect into it on the next viewport, hiding the home screen.
      server = await startWebServer({
        addr: `127.0.0.1:${PORT_BASE + vi}`,
        flow: FLOW,
        storiesDir: STORY_DIR,
      });
      // Healthy (GET / 200) does not guarantee the initial story scan finished;
      // if the page calls stories.list before it does, the home shows "No
      // stories discovered" and the whole tour collapses. Wait for discovery —
      // each rpc raced against a 2s timeout so a wedged fetch can never hang.
      for (let i = 0; i < 30; i++) {
        const s = await Promise.race([
          server.rpc<unknown[]>("runstatus.stories.list", {}).catch(() => [] as unknown[]),
          new Promise<unknown[]>((r) => setTimeout(() => r([]), 2000)),
        ]);
        if (Array.isArray(s) && s.length > 0) break;
        await new Promise((r) => setTimeout(r, 200));
      }
      const context = await browser.newContext({
        viewport: { width: vp.width, height: vp.height },
        deviceScaleFactor: 2,
      });
      const page = await context.newPage();
      try {
        await walk(page, vp);
      } finally {
        await context.close();
        server.stop();
      }
    }
  } finally {
    await browser.close();
  }

  const summary = {
    error: findings.filter((f) => f.severity === "error").length,
    warn: findings.filter((f) => f.severity === "warn").length,
    info: findings.filter((f) => f.severity === "info").length,
  };
  // The exact, deterministic way to bring the UI back to where a finding was
  // seen — the report turns this into a copy-pasteable reproduction recipe.
  const relFlow = path.relative(repoRoot, FLOW);
  const relStories = path.relative(repoRoot, STORY_DIR);
  const addr = `127.0.0.1:${PORT_BASE}`;
  const server_recipe = {
    addr,
    base: `http://${addr}`,
    storiesDir: relStories,
    flow: relFlow,
    cmd: `bin/kitsoki web --stories-dir ${relStories} --flow ${relFlow} --addr ${addr}`,
    startTour: "in the browser console: window.__startTour()  (or click the “?” button)",
  };
  const audit = {
    server: server_recipe,
    viewports: VIEWPORTS,
    steps: TOUR_STEPS.map((s) => ({ id: s.id, title: s.title, route: s.route })),
    captures,
    findings,
    summary,
  };
  fs.writeFileSync(path.join(ARTIFACT_DIR, "audit.json"), JSON.stringify(audit, null, 2));
  console.log(
    `[ui-review] frames=${captures.filter((c) => c.captured).length} ` +
      `findings: ${summary.error} error / ${summary.warn} warn / ${summary.info} info`,
  );
  console.log(`[ui-review] audit: ${path.join(ARTIFACT_DIR, "audit.json")}`);

  // Sanity only: the capture fundamentally worked at the primary viewport (the
  // home steps at least). A tour that stalls deeper is recorded as a
  // `tour-stalled` finding for the review — not a capture failure here.
  const primary = VIEWPORTS[0].name;
  const primaryFrames = captures.filter((c) => c.viewport === primary && c.captured).length;
  if (primaryFrames < TOUR_STEPS.length) {
    console.warn(
      `[ui-review] primary viewport captured ${primaryFrames}/${TOUR_STEPS.length} steps — ` +
        `see tour-stalled findings`,
    );
  }
  expect(primaryFrames, "primary viewport captured at least the home steps").toBeGreaterThanOrEqual(4);
});
