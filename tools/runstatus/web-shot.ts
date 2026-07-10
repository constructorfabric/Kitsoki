/**
 * web-shot.ts — the maintained browser->PNG helper for the kitsoki web SPA.
 *
 * Promoted from the one-off snap.ts into a thin, maintained CLI that the Go
 * `internal/webshot` seam (and `kitsoki web-shot`) shells: launch headless
 * Chromium at a FIXED viewport, navigate to a served URL, wait for the SPA to
 * settle, and write one PNG. It is deliberately dumb — it shoots whatever the
 * URL renders. The DETERMINISM (no-LLM) is the SERVER's: the caller boots
 * `kitsoki web --flow/--host-cassette`, so this helper never needs to know about
 * harnesses or agents. This is the web twin of `kitsoki shot`.
 *
 * Usage:
 *   tsx web-shot.ts --url <u> --out <p> [--viewport WxH]
 *
 *   --url       the served SPA URL to screenshot (e.g. http://127.0.0.1:PORT/#/)
 *   --out       output PNG path
 *   --viewport  WxH, default 1600x900 (the skills' DEMO_VIEWPORT). The capture
 *               viewport equals the render viewport — the rrweb invariant.
 *   --assert-text <text>  Optional, repeatable. Fails if the settled page text
 *               does not contain the string before screenshot.
 *   --semantic-out <path> Optional. Writes window.__kitsokiVisual.observe() as
 *               compact JSON when the app helper is installed.
 *   --rrweb-out <path> Optional. Writes window.__kitsokiVisual.recording() as a
 *               Slidey-compatible rrweb envelope when available.
 *
 * The settle logic mirrors the LIVE Playwright specs' _helpers (waitUntil
 * domcontentloaded + a settle beat for the SPA mount + RPC hydration), so a
 * web-shot looks like a tour/spec frame rather than a half-painted page.
 */
import { chromium } from "@playwright/test";
import { writeFile } from "node:fs/promises";

/** Fixed default viewport — the DEMO_VIEWPORT the skills' _helpers/demo.ts uses. */
const DEFAULT_VIEWPORT = { width: 1600, height: 900 } as const;

interface Args {
  url: string;
  out: string;
  viewport: { width: number; height: number };
  assertText: string[];
  semanticOut?: string;
  rrwebOut?: string;
  action?: string;
  actionHandle?: string;
  point?: { x: number; y: number };
  button?: "left" | "right" | "middle";
  modifiers: Array<"Alt" | "Control" | "Meta" | "Shift">;
}

/** Parse `--key value` argv into the typed Args, with --viewport WxH. */
function parseArgs(argv: string[]): Args {
  const m = new Map<string, string>();
  const assertText: string[] = [];
  const modifiers: Args["modifiers"] = [];
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i];
    const v = argv[i + 1];
    if (k === "--assert-text" && v !== undefined) {
      assertText.push(v);
      continue;
    }
    if (k === "--modifier" && v !== undefined) {
      if (v === "Alt" || v === "Control" || v === "Meta" || v === "Shift") {
        modifiers.push(v);
      } else {
        throw new Error(`web-shot.ts: unsupported --modifier ${JSON.stringify(v)}`);
      }
      continue;
    }
    if (k?.startsWith("--") && v !== undefined) m.set(k.slice(2), v);
  }
  const url = m.get("url");
  const out = m.get("out");
  if (!url || !out) {
    throw new Error("web-shot.ts: --url and --out are required (usage: --url <u> --out <p> [--viewport WxH])");
  }
  let viewport = { ...DEFAULT_VIEWPORT };
  const vp = m.get("viewport");
  if (vp) {
    const match = /^(\d+)x(\d+)$/.exec(vp.trim());
    if (!match) throw new Error(`web-shot.ts: --viewport must be WxH (got ${vp})`);
    viewport = { width: Number(match[1]), height: Number(match[2]) };
  }
  let point: Args["point"];
  const pointArg = m.get("point");
  if (pointArg) {
    const match = /^(-?\d+),(-?\d+)$/.exec(pointArg.trim());
    if (!match) throw new Error(`web-shot.ts: --point must be x,y (got ${pointArg})`);
    point = { x: Number(match[1]), y: Number(match[2]) };
  }
  const buttonArg = m.get("button");
  const button =
    buttonArg === "right" || buttonArg === "middle" || buttonArg === "left"
      ? buttonArg
      : buttonArg
        ? (() => {
            throw new Error(`web-shot.ts: --button must be left, right, or middle (got ${buttonArg})`);
          })()
        : undefined;
  return {
    url,
    out,
    viewport,
    assertText,
    semanticOut: m.get("semantic-out"),
    rrwebOut: m.get("rrweb-out"),
    action: m.get("action"),
    actionHandle: m.get("action-handle"),
    point,
    button,
    modifiers,
  };
}

async function performAction(page: import("@playwright/test").Page, args: Args): Promise<void> {
  if (!args.action) return;
  const action = args.action.trim();
  const button =
    args.button ?? (action === "contextmenu" || action === "right_click" ? "right" : "left");
  const clickOptions = { button };
  for (const mod of args.modifiers) await page.keyboard.down(mod);
  try {
    if (args.point) {
      await page.mouse.click(args.point.x, args.point.y, clickOptions);
      await page.waitForTimeout(500);
      return;
    }
    if (!args.actionHandle) {
      throw new Error("web-shot.ts: browser action requires --action-handle or --point");
    }
    const handle = args.actionHandle;
    if (handle === "testid:meta-report-bug") {
      await clickReportBugAction(page);
      await page.waitForTimeout(500);
      return;
    }
    const bbox = await page.evaluate((h) => {
      const helper = (window as Window & { __kitsokiVisual?: { observe: () => any } }).__kitsokiVisual;
      const actions = helper?.observe?.()?.actions ?? [];
      const found = actions.find((a: any) => a?.handle === h);
      return found?.bbox ?? null;
    }, handle);
    if (!bbox) {
      throw new Error(`web-shot.ts: no semantic action found for handle ${JSON.stringify(handle)}`);
    }
    await page.mouse.click(
      Math.round(bbox.x + bbox.width / 2),
      Math.round(bbox.y + bbox.height / 2),
      clickOptions
    );
    await page.waitForTimeout(500);
  } finally {
    for (const mod of [...args.modifiers].reverse()) await page.keyboard.up(mod);
  }
}

async function clickReportBugAction(page: import("@playwright/test").Page): Promise<void> {
  const reportBug = page.locator('[data-testid="meta-report-bug"]');
  if (!(await reportBug.isVisible().catch(() => false))) {
    const metaButton = page.locator('[data-testid="meta-button"]');
    if (!(await metaButton.isVisible().catch(() => false))) {
      throw new Error('web-shot.ts: cannot open Report bug; "testid:meta-button" is not visible');
    }
    await metaButton.click();
    await reportBug.waitFor({ state: "visible", timeout: 5000 });
  }
  await reportBug.evaluate((el) => {
    if (!(el instanceof HTMLElement)) throw new Error('web-shot.ts: "testid:meta-report-bug" is not an HTMLElement');
    el.click();
  });
  await Promise.race([
    page.locator('[data-testid="bug-modal"]').waitFor({ state: "visible", timeout: 5000 }),
    page.locator('[data-testid="bug-report-toast"]').waitFor({ state: "visible", timeout: 5000 }),
    reportBug.waitFor({ state: "hidden", timeout: 5000 }),
  ]).catch(() => undefined);
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));

  const browser = await chromium.launch({ headless: true, args: ["--no-sandbox", "--hide-scrollbars"] });
  try {
    const page = await browser.newPage();
    // Capture viewport == render viewport (the rrweb invariant): the page must
    // render at exactly the size we screenshot, or the PNG is not the SPA a
    // human sees at this viewport.
    await page.setViewportSize(args.viewport);

    // domcontentloaded (not networkidle): the SPA opens long-lived SSE streams
    // that never go idle, so networkidle would hang. The settle beat below
    // covers the Vue mount + initial RPC hydration — the same posture the LIVE
    // _helpers specs use (cinematicGoto: goto then settle).
    await page.goto(args.url, { timeout: 30000, waitUntil: "domcontentloaded" });
    // SPA mount + hash-route resolve + first RPC paint. A fixed settle (not a
    // selector wait) keeps the helper surface-agnostic: it shoots home,
    // session, editor — whatever the URL routes to — without per-surface anchors.
    await page.waitForTimeout(2000);
    for (const text of args.assertText) {
      const bodyText = await page.locator("body").innerText({ timeout: 5000 });
      if (!bodyText.includes(text)) {
        throw new Error(`web-shot.ts: expected settled page text to contain ${JSON.stringify(text)}`);
      }
    }

    await performAction(page, args);

    if (args.semanticOut) {
      const semantic = await page.evaluate(() => {
        const helper = (window as Window & { __kitsokiVisual?: { observe: () => unknown } }).__kitsokiVisual;
        return helper ? helper.observe() : { ok: false, error: "window.__kitsokiVisual is not installed" };
      });
      await writeFile(args.semanticOut, `${JSON.stringify(semantic, null, 2)}\n`, "utf8");
      process.stderr.write(`web-shot: wrote ${args.semanticOut} semantic observation\n`);
    }
    if (args.rrwebOut) {
      const rrweb = await page.evaluate(() => {
        const helper = (window as Window & { __kitsokiVisual?: { recording: () => unknown } }).__kitsokiVisual;
        return helper ? helper.recording() : { schemaVersion: 1, source: "kitsoki-visual-record", events: [] };
      });
      await writeFile(args.rrwebOut, `${JSON.stringify(rrweb)}\n`, "utf8");
      process.stderr.write(`web-shot: wrote ${args.rrwebOut} rrweb envelope\n`);
    }

    await page.screenshot({ path: args.out, fullPage: false });
    process.stderr.write(`web-shot: wrote ${args.out} (${args.viewport.width}x${args.viewport.height})\n`);
  } finally {
    await browser.close();
  }
}

main().catch((e) => {
  process.stderr.write(`${e instanceof Error ? e.stack ?? e.message : String(e)}\n`);
  process.exit(1);
});
