/**
 * surface-panels.spec.ts — reusable per-surface screenshot generator for review.
 *
 * Renders each decomposed surface (chat / trace / graph — the `?surface=` boot
 * targets the VS Code embed uses) at the REAL sizes + orientations it occupies in
 * VS Code, so we can review each panel as it will actually be presented (and catch
 * cut-off / overflow at narrow sidebar widths or short panel heights). This is the
 * Vue-layer companion to the full-editor `vscode-tour` electron video: same
 * surfaces, but captured headless in the browser at controlled viewports — fast,
 * deterministic, and re-runnable in every review.
 *
 * No LLM: spawns `kitsoki web` against the weather-report cassette flow, drives one
 * forecast turn over RPC (so trace + graph have real content), then screenshots
 * each surface at each SIZE into .artifacts/surface-panels/<surface>-<label>.png.
 * Point kitsoki-ui-qa --frames at that dir.
 *
 * Run:    make surface-panels
 *    ≡    (rebuild bin/kitsoki with the embedded SPA, then)
 *         pnpm -C tools/runstatus exec playwright test surface-panels --project=chromium
 *
 * NOTE: `kitsoki web` serves the go:embed'd SPA, so REBUILD the binary after any
 * change under tools/runstatus/src (the make target does this).
 */
import { test, chromium, type Browser, type Page } from "@playwright/test";
import fs from "fs";
import path from "path";
import { startWebServer, repoRoot, type WebServer } from "./_helpers/server.js";

const ADDR = "127.0.0.1:7763";
const STORY_DIR = path.join(repoRoot, "stories", "weather-report");
const FLOW = path.join(STORY_DIR, "flows", "tour.yaml");
const OUT_DIR = path.join(repoRoot, ".artifacts", "surface-panels");

/**
 * The sizes/orientations each surface is actually used at in VS Code. The chat is
 * an editor-area panel (wide); trace + graph dock in the "Kitsoki Surfaces"
 * activity-bar sidebar (narrow + tall) but are draggable to the bottom panel
 * (wide + short) — so we capture both orientations for those.
 */
const PANELS: Array<{ surface: "chat" | "trace" | "graph"; ready: string; sizes: Array<{ label: string; w: number; h: number }> }> = [
  {
    surface: "chat",
    ready: "chat-section",
    sizes: [{ label: "editor", w: 1000, h: 850 }],
  },
  {
    surface: "trace",
    ready: "trace-timeline",
    sizes: [
      // Activity-bar sidebar: one view full-height, and BOTH views stacked (each
      // gets ~half) — the real cut-off case. Plus the wide/short bottom panel.
      { label: "sidebar", w: 360, h: 850 },
      { label: "sidebar-stacked", w: 360, h: 420 },
      { label: "panel", w: 1100, h: 320 },
    ],
  },
  {
    surface: "graph",
    ready: "trace-diagram",
    sizes: [
      { label: "sidebar", w: 360, h: 850 },
      { label: "sidebar-stacked", w: 360, h: 420 },
      { label: "panel", w: 1100, h: 320 },
    ],
  },
];

let server: WebServer;

test.beforeAll(async () => {
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORY_DIR });
});
test.afterAll(() => server?.stop());

test("capture each surface at its VS Code sizes", async () => {
  test.setTimeout(180000);

  // Fresh output dir (drop stale PNGs).
  fs.mkdirSync(OUT_DIR, { recursive: true });
  for (const f of fs.readdirSync(OUT_DIR)) {
    if (f.endsWith(".png")) fs.rmSync(path.join(OUT_DIR, f));
  }

  // Drive a session to the `report` room over RPC (no LLM — the cassette replays
  // geocode + forecast), so trace + graph render real content the surfaces follow.
  const stories = await server.rpc<Array<{ path: string }>>("runstatus.stories.list", {});
  const storyPath = stories[0]?.path;
  if (!storyPath) throw new Error("no story to drive");
  const { session_id } = await server.rpc<{ session_id: string }>("runstatus.session.new", {
    story_path: storyPath,
  });
  await server.rpc("runstatus.session.submit", {
    session_id,
    intent: "forecast",
    slots: { location: "Tokyo" },
  });

  const browser: Browser = await chromium.launch({ headless: true });
  try {
    for (const panel of PANELS) {
      for (const size of panel.sizes) {
        const context = await browser.newContext({ viewport: { width: size.w, height: size.h } });
        const page: Page = await context.newPage();
        try {
          await page.goto(`${server.base}/?surface=${panel.surface}`);
          // The surface mounts, discovers the active session, hydrates, renders.
          await page.locator(`[data-testid="surface-${panel.surface}"]`).first().waitFor({ timeout: 20000 });
          await page
            .locator(`[data-testid="${panel.ready}"]`)
            .first()
            .waitFor({ timeout: 20000 })
            .catch(() => undefined);
          await page.waitForTimeout(600); // let the diagram/timeline settle
          const out = path.join(OUT_DIR, `${panel.surface}-${size.label}-${size.w}x${size.h}.png`);
          await page.screenshot({ path: out });
          // eslint-disable-next-line no-console
          console.log(`[surface-panels] ${path.relative(repoRoot, out)}`);
        } finally {
          await context.close();
        }
      }
    }
  } finally {
    await browser.close();
  }
  // eslint-disable-next-line no-console
  console.log(`[surface-panels] wrote PNGs to ${path.relative(repoRoot, OUT_DIR)}`);
});
