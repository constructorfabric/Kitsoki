#!/usr/bin/env node
/*
 * Capture a title-slide poster PNG for each per-phase GitHub-agent slidey deck.
 *
 * Opens each self-contained `deck.html` in headless Chromium (the exact
 * artifact a viewer opens at <public-base-url>/run/<job-id>/assets/deck.html)
 * and screenshots the first painted slide. Deterministic and offline: it reads
 * the already-bundled local HTML rather than the live URL.
 *
 * Usage: node capture-deck-posters.mjs [media-root]
 */
import { chromium } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const MEDIA_ROOT = process.argv[2] || ".artifacts/github-agent-live";
const CASES = ["bug-issue", "feature-issue", "guidance", "guidance-resume", "pr-status"];
const WIDTH = 1600;
const HEIGHT = 900;

const browser = await chromium.launch();
try {
  const ctx = await browser.newContext({ viewport: { width: WIDTH, height: HEIGHT }, deviceScaleFactor: 1 });
  for (const slug of CASES) {
    const html = path.resolve(MEDIA_ROOT, slug, "deck.html");
    if (!fs.existsSync(html)) {
      console.error(`skip ${slug}: missing ${html}`);
      continue;
    }
    const page = await ctx.newPage();
    await page.goto(`file://${html}`, { waitUntil: "load", timeout: 60000 });
    // Let the first slide paint (title card + fonts) before the shot.
    await page.waitForTimeout(2500);
    const out = path.resolve(MEDIA_ROOT, slug, "deck-poster.png");
    await page.screenshot({ path: out });
    await page.close();
    console.log(`wrote ${out}`);
  }
} finally {
  await browser.close();
}
