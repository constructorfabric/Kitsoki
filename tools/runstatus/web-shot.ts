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
 *
 * The settle logic mirrors the LIVE Playwright specs' _helpers (waitUntil
 * domcontentloaded + a settle beat for the SPA mount + RPC hydration), so a
 * web-shot looks like a tour/spec frame rather than a half-painted page.
 */
import { chromium } from "@playwright/test";

/** Fixed default viewport — the DEMO_VIEWPORT the skills' _helpers/demo.ts uses. */
const DEFAULT_VIEWPORT = { width: 1600, height: 900 } as const;

interface Args {
  url: string;
  out: string;
  viewport: { width: number; height: number };
  assertText: string[];
}

/** Parse `--key value` argv into the typed Args, with --viewport WxH. */
function parseArgs(argv: string[]): Args {
  const m = new Map<string, string>();
  const assertText: string[] = [];
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i];
    const v = argv[i + 1];
    if (k === "--assert-text" && v !== undefined) {
      assertText.push(v);
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
  return { url, out, viewport, assertText };
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
