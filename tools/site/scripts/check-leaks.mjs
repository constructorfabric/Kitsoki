#!/usr/bin/env node
/**
 * Belt-and-braces leak check over a built site dist: fail if any page
 * references repo paths that must never publish (internal docs), and — for the
 * embedded variant — if any MP4 slipped into the payload destined for the
 * go:embed binary.
 *
 * Usage: node scripts/check-leaks.mjs <dist-dir> [--embedded]
 */
import * as fs from "fs";
import * as path from "path";

const dist = process.argv[2];
const embedded = process.argv.includes("--embedded");
if (!dist || !fs.existsSync(dist)) {
  console.error(`check-leaks: dist dir missing: ${dist}`);
  process.exit(2);
}

// Internal doc trees that must never be LINKED site-relatively. Prose mentions
// of these paths (tour narration describing repo behavior) are fine, as are
// GitHub blob URLs (the stage-docs escape hatch for out-of-allowlist targets)
// — the leak signal is an href that tries to serve them from THIS site.
const FORBIDDEN_HREF =
  /href="(?!https?:)[^"]*docs\/(proposals|competitive-analysis|skills|case-studies)\/[^"]*"/;

const problems = [];
function walk(dir) {
  for (const name of fs.readdirSync(dir)) {
    const p = path.join(dir, name);
    const st = fs.statSync(p);
    if (st.isDirectory()) {
      walk(p);
    } else if (embedded && name.endsWith(".mp4")) {
      problems.push(`embedded payload contains a video: ${path.relative(dist, p)}`);
    } else if (name.endsWith(".html")) {
      const content = fs.readFileSync(p, "utf8");
      const m = content.match(FORBIDDEN_HREF);
      if (m) problems.push(`${path.relative(dist, p)}: forbidden internal-doc link ${m[0]}`);
    }
  }
}
walk(dist);

if (problems.length > 0) {
  for (const p of problems) console.error(`check-leaks: ${p}`);
  process.exit(1);
}
console.log(`check-leaks: ${dist} clean`);
