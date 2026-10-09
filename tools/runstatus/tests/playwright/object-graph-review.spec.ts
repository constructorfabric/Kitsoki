/**
 * object-graph-review.spec.ts — CAPTURE stage of a local, persona-lensed
 * kitsoki-ui-review pass over the object-graph viewer
 * (docs/proposals/project-object-graph/ui-declutter-and-diff-mode.md, slice 5).
 *
 * Same audit.json/frames shape tour-review.spec.ts (the onboarding-tour
 * capture) produces, so the existing review.sh/report.sh stages — which read
 * that shape generically, not tied to any one tour — work unmodified. This
 * spec exists because the object-graph route is a standalone SPA view (no
 * chat/story session), not a step in the onboarding tour's chat-driven
 * manifest — TOUR_STEPS/tour-review.spec.ts's capture is fundamentally
 * "walk this chat tour", which doesn't fit a bare route with no session.
 *
 * Steps captured: the catalog "as-is", and diff mode ("Proposed" and "Diff"
 * tabs) against this proposal's own overlay — the ui-declutter-and-diff-mode
 * work reviewing itself.
 *
 * Output: .artifacts/ui-review-object-graph/{frames/,audit.json} — a
 * separate artifact dir from the onboarding tour's .artifacts/ui-review/ so
 * the two reviews never clobber each other.
 *
 *   pnpm exec playwright test object-graph-review --project=chromium
 */
import { test, chromium, type Browser, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { AxeBuilder } from "@axe-core/playwright";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";
import { geometryProbe, type RawFinding, type Severity } from "./lib/ui-audit.js";

const PORT = 7747;
const ARTIFACT_DIR = path.join(repoRoot, ".artifacts", "ui-review-object-graph");
const FRAMES_DIR = path.join(ARTIFACT_DIR, "frames");
const OVERLAY = "docs/proposals/project-object-graph/seed-objects.overlay-ui-declutter.yaml";

interface Viewport {
  name: string;
  width: number;
  height: number;
}
const ALL_VIEWPORTS: Viewport[] = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "mobile", width: 390, height: 844 },
];
const selected = (process.env.UI_REVIEW_VIEWPORTS || "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);
const VIEWPORTS = selected.length ? ALL_VIEWPORTS.filter((v) => selected.includes(v.name)) : ALL_VIEWPORTS;

// One "step" per surface this review covers, in capture order — mirrors
// TOUR_STEPS's {id, title, route} shape so audit.json stays generic.
interface Step {
  id: string;
  title: string;
  route: string;
  hash: string;
  mode?: "asis" | "proposed" | "diff";
}
const STEPS: Step[] = [
  { id: "catalog-asis", title: "Catalog — as-is", route: "/graph", hash: "#/graph" },
  {
    id: "catalog-diff",
    title: "Catalog — diff mode",
    route: "/graph?overlay=...",
    hash: `#/graph?overlay=${encodeURIComponent(OVERLAY)}`,
    mode: "diff",
  },
];

interface TaggedFinding extends RawFinding {
  step: string;
  viewport: string;
  frame: string;
  source: "geometry" | "a11y";
  target?: string;
  failureSummary?: string;
  helpUrl?: string;
}
interface StepCapture {
  step: string;
  title: string;
  route: string;
  viewport: string;
  width: number;
  height: number;
  url: string;
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

function axeSeverity(impact: string | null | undefined): Severity {
  if (impact === "critical" || impact === "serious") return "error";
  if (impact === "moderate") return "warn";
  return "info";
}

async function auditStep(page: Page, step: Step, vp: Viewport, idx: number): Promise<void> {
  const n = String(idx).padStart(2, "0");
  const frame = `${n}-${step.id}@${vp.name}.png`;
  await page.screenshot({ path: path.join(FRAMES_DIR, frame) });
  captures.push({
    step: step.id,
    title: step.title,
    route: step.route,
    viewport: vp.name,
    width: vp.width,
    height: vp.height,
    url: page.url(),
    frame,
    captured: true,
  });

  const geo = await page.evaluate(geometryProbe);
  for (const f of geo) findings.push({ ...f, step: step.id, viewport: vp.name, frame, source: "geometry" });

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
        step: step.id,
        viewport: vp.name,
        frame,
        source: "a11y",
      });
    }
  } catch {
    // best-effort, same as tour-review.spec.ts
  }
}

async function walk(page: Page, vp: Viewport): Promise<void> {
  let idx = 0;
  for (const step of STEPS) {
    idx += 1;
    await page.goto(`${server.base}/${step.hash}`, { waitUntil: "load" });
    const ok = await page
      .getByTestId("objectgraph-catalog")
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!ok) {
      captures.push({
        step: step.id,
        title: step.title,
        route: step.route,
        viewport: vp.name,
        width: vp.width,
        height: vp.height,
        url: page.url(),
        frame: "",
        captured: false,
        note: "objectgraph-catalog did not become visible",
      });
      continue;
    }
    if (step.mode === "diff") {
      await page.click('[data-testid="objectgraph-mode-diff"]').catch(() => {});
    }
    await page.waitForTimeout(600);
    await auditStep(page, step, vp, idx);
  }
}

test("object-graph viewer — capture frames + deterministic audit", async () => {
  test.setTimeout(60_000);
  server = await startWebServer({ addr: `127.0.0.1:${PORT}` });
  let browser: Browser | undefined;
  try {
    browser = await chromium.launch();
    for (const vp of VIEWPORTS) {
      const context = await browser.newContext({ viewport: { width: vp.width, height: vp.height } });
      const page = await context.newPage();
      try {
        await walk(page, vp);
      } finally {
        await context.close();
      }
    }
  } finally {
    await browser?.close();
    server.stop();
  }

  const summary = {
    error: findings.filter((f) => f.severity === "error").length,
    warn: findings.filter((f) => f.severity === "warn").length,
    info: findings.filter((f) => f.severity === "info").length,
  };
  const audit = {
    server: {
      addr: `127.0.0.1:${PORT}`,
      base: `http://127.0.0.1:${PORT}`,
      cmd: `bin/kitsoki web --stories-dir stories --addr 127.0.0.1:${PORT}`,
    },
    viewports: VIEWPORTS,
    steps: STEPS.map((s) => ({ id: s.id, title: s.title, route: s.route })),
    captures,
    findings,
    summary,
  };
  fs.writeFileSync(path.join(ARTIFACT_DIR, "audit.json"), JSON.stringify(audit, null, 2));
  console.log(
    `[object-graph-review] frames=${captures.filter((c) => c.captured).length} ` +
      `findings: ${summary.error} error / ${summary.warn} warn / ${summary.info} info`,
  );
  console.log(`[object-graph-review] audit: ${path.join(ARTIFACT_DIR, "audit.json")}`);
});
