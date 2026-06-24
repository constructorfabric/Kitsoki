#!/usr/bin/env node
/**
 * Stage the ALLOWLISTED repo docs into the site's gitignored src/guide/ tree.
 *
 * Allowlist-copy (not srcDir-over-docs/, not symlinks) is a structural
 * guarantee: internal material (docs/proposals/, docs/competitive-analysis/,
 * .agents/skills/, ...) can never leak onto the published site because it is
 * never staged. Markdown links that escape the allowlist are rewritten to
 * GitHub blob/raw URLs so they stay alive instead of going dead — and VitePress
 * runs with ignoreDeadLinks:false, so a missed rewrite FAILS the build rather
 * than publishing a broken link.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { expandManifest } from "./manifest.mjs";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const siteDir = path.resolve(__dirname, "..");
const repoRoot = path.resolve(siteDir, "../..");
const srcDir = path.join(siteDir, "src");
const guideDir = path.join(srcDir, "guide");

const { repoUrl, branch, sections } = expandManifest(siteDir, repoRoot);

/** Flat [repoPath -> sitePath] map over every expanded manifest entry. */
function expand() {
  const map = new Map();
  for (const section of sections) {
    for (const e of section.entries) map.set(e.from, e.to);
  }
  return map;
}

/** Rewrite one markdown link target found in `repoFile` (repo-relative). */
function rewriteTarget(target, repoFile, siteFile, map) {
  // Leave external/anchor/absolute targets alone.
  if (/^(https?:|mailto:|#|\/)/.test(target)) return target;
  const [p, anchor = ""] = target.split(/(#.*)$/, 2);
  if (p === "") return target;
  const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(repoFile), p));
  if (map.has(resolved)) {
    const dest = map.get(resolved);
    let rel = path.posix.relative(path.posix.dirname(siteFile), dest);
    if (!rel.startsWith(".")) rel = "./" + rel;
    return rel + anchor;
  }
  // Escapes the allowlist — point at GitHub so the reference stays alive.
  if (!fs.existsSync(path.join(repoRoot, resolved))) {
    // Path doesn't exist in the repo either; keep as-is and let the dead-link
    // check surface it (it is broken at the source).
    return target;
  }
  return `${repoUrl}/blob/${branch}/${resolved}${anchor}`;
}

function rewriteLinks(content, repoFile, siteFile, map) {
  // Inline links + images: [text](target) / ![alt](target). Images that escape
  // the allowlist use the raw URL so they still render as images.
  return content.replace(/(!?)\[([^\]]*)\]\(([^)\s]+)\)/g, (m, bang, text, target) => {
    let out = rewriteTarget(target, repoFile, siteFile, map);
    if (bang === "!" && out.startsWith(`${repoUrl}/blob/`)) {
      out = out.replace("/blob/", "/raw/");
    }
    return `${bang}[${text}](${out})`;
  });
}

fs.rmSync(guideDir, { recursive: true, force: true });
const map = expand();
let staged = 0;
for (const [from, to] of map) {
  const content = fs.readFileSync(path.join(repoRoot, from), "utf8");
  const out = path.join(srcDir, to);
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, rewriteLinks(content, from, to, map));
  staged++;
}
console.log(`stage-docs: staged ${staged} doc(s) -> ${path.relative(repoRoot, guideDir)}`);
