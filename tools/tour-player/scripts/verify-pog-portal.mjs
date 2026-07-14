#!/usr/bin/env node
// P4 acceptance: proves tour-player works standalone against a real,
// unrelated third-party SPA — not just kitsoki-family apps
// (.context/2026-07-12-browser-mcp-tour-implementation-brief.md's "design
// nothing that blocks generalization" note). Starts pog-portal's own dev
// server (a separate personal repo at ~/code/pog/portal, read-only here —
// this script only runs its `npm run dev`, never touches its git state),
// injects the built dist/tour-player.iife.js via a real headless browser,
// discovers real anchors already on the page (no hardcoded kitsoki-shaped
// assumptions about pog-portal's DOM), and drives a live 2-step tour.
//
// No LLM anywhere in this script. Requires dist/tour-player.iife.js
// (`npm run build`) and a cached Playwright chromium
// (KITSOKI_TOUR_PLAYER_CHROMIUM env var, or the default ms-playwright
// cache path this script probes).
import { chromium } from "playwright-core";
import { spawn } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import os from "node:os";

const here = path.dirname(fileURLToPath(import.meta.url));
const bundlePath = path.join(here, "..", "dist", "tour-player.iife.js");
const pogPortalDir = process.env.POG_PORTAL_DIR || path.join(os.homedir(), "code", "pog", "portal");
const PORT = 5183;
const url = `http://localhost:${PORT}/`;

function assertOk(cond, message) {
  if (!cond) throw new Error(`VERIFY FAIL: ${message}`);
  console.log(`ok - ${message}`);
}

async function resolveChromiumPath() {
  if (process.env.KITSOKI_TOUR_PLAYER_CHROMIUM) return process.env.KITSOKI_TOUR_PLAYER_CHROMIUM;
  const { readdirSync } = await import("node:fs");
  const cacheDir = path.join(os.homedir(), "Library", "Caches", "ms-playwright");
  if (!existsSync(cacheDir)) return null;
  const candidates = readdirSync(cacheDir).filter((n) => n.startsWith("chromium-"));
  for (const dir of candidates.sort().reverse()) {
    const macPath = path.join(cacheDir, dir, "chrome-mac-arm64", "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing");
    if (existsSync(macPath)) return macPath;
    const linuxPath = path.join(cacheDir, dir, "chrome-linux", "chrome");
    if (existsSync(linuxPath)) return linuxPath;
  }
  return null;
}

// page.evaluate() has NO default timeout in Playwright (unlike click/goto):
// if pog-portal's execution context gets torn down mid-call (an SPA route
// change, a live SSE reconnect) the CDP round trip can hang forever instead
// of erroring. Every evaluate/click below goes through this so a stuck call
// fails loudly in bounded time rather than hanging the whole script.
async function withTimeout(promise, ms, label) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`timed out after ${ms}ms: ${label}`)), ms);
  });
  try {
    return await Promise.race([promise, timeout]);
  } finally {
    clearTimeout(timer);
  }
}

async function waitForServer(targetUrl, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(targetUrl);
      if (res.ok || res.status < 500) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(`pog-portal dev server did not come up at ${targetUrl} within ${timeoutMs}ms`);
}

async function main() {
  if (!existsSync(bundlePath)) throw new Error(`missing ${bundlePath} — run npm run build first`);
  if (!existsSync(pogPortalDir)) {
    console.log(`SKIP: pog-portal not found at ${pogPortalDir} (set POG_PORTAL_DIR to override)`);
    return;
  }
  const chromiumPath = await resolveChromiumPath();
  if (!chromiumPath) {
    console.log("SKIP: no cached Playwright chromium found (set KITSOKI_TOUR_PLAYER_CHROMIUM)");
    return;
  }

  console.log(`starting pog-portal dev server in ${pogPortalDir} ...`);
  const devServer = spawn("npm", ["run", "dev"], { cwd: pogPortalDir, stdio: ["ignore", "pipe", "pipe"] });
  let serverOutput = "";
  devServer.stdout.on("data", (c) => (serverOutput += c));
  devServer.stderr.on("data", (c) => (serverOutput += c));

  try {
    await waitForServer(url);

    const browser = await chromium.launch({ executablePath: chromiumPath, headless: true });
    const page = await browser.newPage();
    page.setDefaultTimeout(15000);
    await withTimeout(page.goto(url, { waitUntil: "domcontentloaded" }), 30000, "page.goto");
    // pog-portal keeps a live /rpc SSE connection open, so "networkidle"
    // never fires; wait for hydration to settle a bounded amount instead.
    await page.waitForTimeout(2000);

    const bundleSrc = readFileSync(bundlePath, "utf8");
    await withTimeout(page.addScriptTag({ content: bundleSrc }), 10000, "addScriptTag");

    const hasPlayer = await withTimeout(
      page.evaluate(() => typeof window.KitsokiTourPlayer?.TourPlayer === "function"),
      10000,
      "check TourPlayer global"
    );
    assertOk(hasPlayer, "tour-player IIFE loads into pog-portal's own page and exposes TourPlayer");

    // Discover real anchors already on the page — no hardcoded assumption
    // about pog-portal's DOM, proving generalization rather than a
    // kitsoki-shaped fixture in disguise. Only unique testids qualify: a
    // duplicate would make resolveAnchor legitimately (and correctly) fail.
    const testids = await withTimeout(
      page.evaluate(() => {
        const counts = new Map();
        for (const el of document.querySelectorAll("[data-testid]")) {
          const id = el.getAttribute("data-testid");
          if (id) counts.set(id, (counts.get(id) || 0) + 1);
        }
        return Array.from(counts.entries())
          .filter(([, count]) => count === 1)
          .map(([id]) => id);
      }),
      10000,
      "discover testids"
    );
    assertOk(testids.length >= 2, `pog-portal's live DOM exposes at least 2 unique data-testid anchors (found ${testids.length})`);
    const [anchorA, anchorB] = testids;
    console.log(`  using anchors: ${anchorA}, ${anchorB}`);

    const tourStarted = await withTimeout(
      page.evaluate(
        ({ anchorA, anchorB }) => {
          const tour = {
            version: 2,
            id: "pog-portal-verify",
            steps: [
              { id: "s1", kind: "highlight", target: { testid: anchorA }, popover: { title: "Step 1", body: anchorA, side: "bottom" } },
              { id: "s2", kind: "highlight", target: { testid: anchorB }, popover: { title: "Step 2", body: anchorB, side: "bottom" } }
            ]
          };
          window.__verifyPlayer = new window.KitsokiTourPlayer.TourPlayer(tour);
          window.__verifyPlayer.start();
          return true;
        },
        { anchorA, anchorB }
      ),
      10000,
      "start tour"
    );
    assertOk(tourStarted, "TourPlayer.start() runs against pog-portal's real page without throwing");

    // The player polls at 200ms and mutation-observes before it first
    // anchors; give it one poll cycle rather than asserting the instant
    // start() returns.
    await page.waitForTimeout(400);

    const step1Title = await withTimeout(
      page.evaluate(() => document.getElementById("kitsoki-tour-popover")?.querySelector("h3")?.textContent),
      10000,
      "read step1 title"
    );
    assertOk(step1Title === "Step 1", `step 1's popover renders on the real page (title=${step1Title})`);

    const ringVisible = await withTimeout(
      page.evaluate(() => {
        const ring = document.getElementById("kitsoki-tour-ring");
        return !!ring && ring.style.width !== "" && ring.style.top !== "";
      }),
      10000,
      "read ring geometry"
    );
    assertOk(ringVisible, "the spotlight ring positions against pog-portal's real anchor geometry");

    await withTimeout(page.click('[data-kt-action="next"]'), 10000, "click next");
    await page.waitForTimeout(400);
    const step2Title = await withTimeout(
      page.evaluate(() => document.getElementById("kitsoki-tour-popover")?.querySelector("h3")?.textContent),
      10000,
      "read step2 title"
    );
    assertOk(step2Title === "Step 2", `Next advances to step 2 on the real page (title=${step2Title})`);

    await withTimeout(page.click('[data-kt-action="next"]'), 10000, "click done");
    const finished = await withTimeout(
      page.evaluate(() => document.getElementById("kitsoki-tour-popover") === null),
      10000,
      "read finished state"
    );
    assertOk(finished, "Done finishes the tour and tears down the popover on the real page");

    await browser.close();
    console.log(`\ntour-player pog-portal verification: PASSED (anchors used: ${anchorA}, ${anchorB}; no LLM calls made)`);
  } finally {
    devServer.kill();
  }
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
