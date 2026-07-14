#!/usr/bin/env node
/**
 * check-download-links.mjs — liveness gate for the download page.
 *
 * tools/site/src/download.md links `releases/latest/download/<asset>` URLs
 * (plus a checksums.txt). Those 404 for as long as no tagged release exists —
 * silently, for weeks, with zero build signal (the site build never touches
 * the network). This HEAD-requests every such link and fails the build on
 * any non-2xx response, so a broken download page can never ship unnoticed.
 *
 * Usage: node scripts/check-download-links.mjs [--file <download.md>]
 *                                               [--timeout-ms N] [--retries N]
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(siteDir, "../..");

function argValue(name, fallback) {
  const i = process.argv.indexOf(name);
  return i >= 0 ? process.argv[i + 1] : fallback;
}

function rel(p) {
  return path.relative(repoRoot, p) || ".";
}

const file = path.resolve(repoRoot, argValue("--file", path.join(siteDir, "src", "download.md")));
const timeoutMs = Number(argValue("--timeout-ms", "15000"));
const retries = Number(argValue("--retries", "2"));

if (!fs.existsSync(file)) {
  console.error(`check-download-links: ${rel(file)} missing`);
  process.exit(2);
}

const md = fs.readFileSync(file, "utf8");
// Markdown link targets pointing at a GitHub Releases download asset, e.g.
// https://github.com/<org>/<repo>/releases/latest/download/<asset>
const LINK_RE = /\]\((https:\/\/github\.com\/[^)]+\/releases\/latest\/download\/[^)]+)\)/g;
const urls = [...new Set([...md.matchAll(LINK_RE)].map((m) => m[1]))];

if (urls.length === 0) {
  console.error(
    `check-download-links: no releases/latest/download/... links found in ${rel(file)} — ` +
      `is the page still publishing prebuilt binaries via GitHub Releases?`,
  );
  process.exit(2);
}

async function headOnce(url) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    // HEAD is enough to prove the asset resolves without downloading it; a
    // ranged GET is the fallback for any redirect target that mishandles HEAD.
    let res = await fetch(url, { method: "HEAD", redirect: "follow", signal: controller.signal });
    if (!res.ok) {
      res = await fetch(url, {
        method: "GET",
        redirect: "follow",
        signal: controller.signal,
        headers: { Range: "bytes=0-0" },
      });
    }
    return { status: res.status, ok: res.ok };
  } finally {
    clearTimeout(timer);
  }
}

async function checkOne(url) {
  let lastErr;
  for (let attempt = 0; attempt <= retries; attempt++) {
    try {
      const { status, ok } = await headOnce(url);
      if (ok) return { url, status, ok: true };
      lastErr = `HTTP ${status}`;
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
    }
  }
  return { url, status: 0, ok: false, error: lastErr };
}

const results = await Promise.all(urls.map(checkOne));
for (const r of results) {
  console.log(`check-download-links: ${r.ok ? "OK  " : "FAIL"} ${r.ok ? r.status : r.error} ${r.url}`);
}

const problems = results.filter((r) => !r.ok);
if (problems.length > 0) {
  console.error(`check-download-links: ${problems.length} of ${results.length} link(s) failed — ${rel(file)} would 404 for visitors`);
  process.exit(1);
}
console.log(`check-download-links: OK — ${results.length} download link(s) live`);
