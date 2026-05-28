#!/usr/bin/env node
// Build a single-file HTML artifact from a snapshot fixture.
//
//   node scripts/build-artifact.mjs <name>     # one: e.g. bugfix-recycle
//   node scripts/build-artifact.mjs --all      # all fixtures/*.snapshot.json
//
// Runs `vite build` first if dist/index.html is missing or older than any
// source file under src/.  Writes to <repo-root>/.artifacts/<name>.html and
// prints the absolute path.

import fs from "fs";
import path from "path";
import { spawnSync } from "child_process";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..");
const distIndex = path.join(root, "dist", "index.html");
const fixturesDir = path.join(root, "fixtures");

// Repo / worktree root — top of the surrounding git checkout.  Artifacts
// live there as <repo-root>/.artifacts/ so they're easy to find rather
// than buried under tools/runstatus.
const repoRoot = (() => {
  const res = spawnSync("git", ["rev-parse", "--show-toplevel"], { cwd: root, encoding: "utf-8" });
  if (res.status !== 0) throw new Error("git rev-parse failed: " + res.stderr);
  return res.stdout.trim();
})();
const outDir = path.join(repoRoot, ".artifacts");

function newestMtime(dir) {
  let newest = 0;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    const stat = entry.isDirectory() ? newestMtime(p) : fs.statSync(p).mtimeMs;
    if (stat > newest) newest = stat;
  }
  return newest;
}

function ensureDist() {
  const srcMtime = newestMtime(path.join(root, "src"));
  const distMtime = fs.existsSync(distIndex) ? fs.statSync(distIndex).mtimeMs : 0;
  if (distMtime >= srcMtime) return;

  console.log("[build-artifact] building dist/ via vite…");
  const vite = path.join(root, "node_modules", ".bin", "vite");
  const res = spawnSync(vite, ["build"], { cwd: root, stdio: "inherit" });
  if (res.status !== 0) {
    console.error("[build-artifact] vite build failed");
    process.exit(res.status ?? 1);
  }
}

function gitInfo() {
  const run = (args) => {
    const r = spawnSync("git", args, { cwd: root, encoding: "utf-8" });
    return r.status === 0 ? r.stdout.trim() : "";
  };
  return {
    commit: run(["rev-parse", "--short", "HEAD"]),
    branch: run(["rev-parse", "--abbrev-ref", "HEAD"]),
  };
}

// Parse Makefile phony targets so we know which names are fixture-managed.
const makefileTargets = (() => {
  const mk = path.join(fixturesDir, "Makefile");
  if (!fs.existsSync(mk)) return new Set();
  const targets = new Set();
  for (const line of fs.readFileSync(mk, "utf-8").split("\n")) {
    // ".PHONY: foo bar" lines list all managed targets
    const m = line.match(/^\.PHONY:\s*(.+)/);
    if (m) m[1].trim().split(/\s+/).forEach(t => targets.add(t));
  }
  // Remove meta-targets that aren't real fixture names
  targets.delete("all");
  return targets;
})();

function buildRegenComment(baseName, snapshotPath) {
  const toolsRelative = "tools/runstatus"; // relative to repo root
  const fixtureRelative = `${toolsRelative}/fixtures/${baseName}.snapshot.json`;
  const htmlCmd = `cd ${toolsRelative} && node scripts/build-artifact.mjs ${baseName}`;

  if (makefileTargets.has(baseName)) {
    // Fixture managed by the Makefile — show the make target + artifact rebuild
    const makeCmd = `make -C ${toolsRelative}/fixtures ${baseName}`;
    return (
      `<!--\n` +
      `  REGENERATE THIS ARTIFACT\n` +
      `  snapshot : ${fixtureRelative}\n` +
      `  managed  : ${toolsRelative}/fixtures/Makefile\n` +
      `\n` +
      `  1. Refresh the snapshot:\n` +
      `       ${makeCmd}\n` +
      `\n` +
      `  2. Rebuild the HTML:\n` +
      `       ${htmlCmd}\n` +
      `-->`
    );
  }

  // Live / ad-hoc snapshot — extract session info from the JSON to give a
  // concrete export-status invocation.
  let sessionId = "";
  let appId = "";
  try {
    const snap = JSON.parse(fs.readFileSync(snapshotPath, "utf-8"));
    sessionId = snap?.session?.session_id ?? "";
    appId     = snap?.session?.app_id     ?? "";
  } catch { /* ignore */ }

  const exportCmd = sessionId
    ? (
      `go run ./cmd/kitsoki export-status \\\n` +
      `        --session ${sessionId} \\\n` +
      `        -o ${fixtureRelative}`
    )
    : (
      `go run ./cmd/kitsoki export-status \\\n` +
      `        --from-trace /path/to/session.jsonl \\\n` +
      `        --app /path/to/app.yaml \\\n` +
      `        -o ${fixtureRelative}`
    );

  const sessionLine = sessionId ? `  session  : ${sessionId}${appId ? `  (app: ${appId})` : ""}\n` : "";

  return (
    `<!--\n` +
    `  REGENERATE THIS ARTIFACT\n` +
    `  snapshot : ${fixtureRelative}\n` +
    `  source   : live session\n` +
    sessionLine +
    `\n` +
    `  1. Re-export the snapshot:\n` +
    `       ${exportCmd}\n` +
    `\n` +
    `  2. Rebuild the HTML:\n` +
    `       ${htmlCmd}\n` +
    `-->`
  );
}

function buildBanner(baseName) {
  const { commit, branch } = gitInfo();
  const builtAt = new Date().toISOString();
  const parts = [
    `fixture: ${baseName}`,
    commit ? `commit: ${commit}` : null,
    branch ? `branch: ${branch}` : null,
    `built: ${builtAt}`,
  ].filter(Boolean);

  return `<div id="kitsoki-artifact-banner" style="` +
    `position:fixed;top:0;left:0;right:0;z-index:9999;` +
    `background:#0f172a;border-bottom:1px solid #1e293b;` +
    `padding:0.25rem 0.75rem;font-family:ui-monospace,monospace;` +
    `font-size:0.7rem;color:#475569;display:flex;gap:1.5rem;` +
    `align-items:center;` +
    `">` +
    parts.map(p => {
      const [k, ...v] = p.split(": ");
      return `<span><span style="color:#334155">${k}:</span> <span style="color:#64748b">${v.join(": ")}</span></span>`;
    }).join("") +
    `</div>` +
    // push app content down so it doesn't hide under the banner
    `<style>#app{padding-top:1.75rem}</style>`;
}

function buildOne(snapshotPath) {
  const baseName = path.basename(snapshotPath, ".snapshot.json");
  const dist = fs.readFileSync(distIndex, "utf-8");
  const snap = fs.readFileSync(snapshotPath, "utf-8");
  const snapshotTag = `<script type="application/json" id="kitsoki-snapshot">${snap}</script>`;
  const banner = buildBanner(baseName);
  const regenComment = buildRegenComment(baseName, snapshotPath);

  let html = dist;
  // Inject regen comment at the very top of the file
  html = regenComment + "\n" + html;

  // Inject snapshot tag before first <script>
  const scriptIdx = html.indexOf("<script");
  html = scriptIdx === -1
    ? html.replace("</body>", `${snapshotTag}\n</body>`)
    : html.slice(0, scriptIdx) + snapshotTag + "\n" + html.slice(scriptIdx);

  // Inject banner as first child of <body>
  html = html.replace("<body>", `<body>\n${banner}`);

  fs.mkdirSync(outDir, { recursive: true });
  const out = path.join(outDir, `${baseName}.html`);
  fs.writeFileSync(out, html, "utf-8");
  console.log(out);
}

const args = process.argv.slice(2);
if (args.length === 0) {
  console.error("usage: build-artifact <name> | --all");
  process.exit(2);
}

ensureDist();

if (args[0] === "--all") {
  for (const f of fs.readdirSync(fixturesDir)) {
    if (f.endsWith(".snapshot.json")) buildOne(path.join(fixturesDir, f));
  }
} else {
  for (const name of args) {
    const snap = path.join(fixturesDir, `${name}.snapshot.json`);
    if (!fs.existsSync(snap)) {
      console.error(`no fixture: ${snap}`);
      process.exit(1);
    }
    buildOne(snap);
  }
}
