// Test helper: clone the hermetic demo-base fixture into a throwaway temp
// dir per test so mutations (stale mtimes, broken JSON, etc.) never leak
// between tests or depend on execution order.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const BASE_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "fixtures", "demo-base");

// fs.cpSync rewrites relative symlink targets to absolute paths pointing at
// the SOURCE tree (observed on Node 22.20), which would silently point the
// clone's `clips` symlink back at the read-only fixture dir instead of the
// clone's own `clips-real`. Walk and copy by hand so symlinks keep their
// literal (relative) target string.
function copyTree(src, dest) {
  const stat = fs.lstatSync(src);
  if (stat.isSymbolicLink()) {
    fs.symlinkSync(fs.readlinkSync(src), dest);
    return;
  }
  if (stat.isDirectory()) {
    fs.mkdirSync(dest, { recursive: true });
    for (const entry of fs.readdirSync(src)) {
      copyTree(path.join(src, entry), path.join(dest, entry));
    }
    return;
  }
  fs.copyFileSync(src, dest);
}

export function cloneFixture(prefix = "demo-fixture-") {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  for (const entry of fs.readdirSync(BASE_DIR)) {
    copyTree(path.join(BASE_DIR, entry), path.join(tmp, entry));
  }
  return tmp;
}

export function removeFixture(dir) {
  fs.rmSync(dir, { recursive: true, force: true });
}

export function touch(filePath, msOffsetFromNow) {
  const t = new Date(Date.now() + msOffsetFromNow);
  fs.utimesSync(filePath, t, t);
}

/** Base fixture's freshness invariants, made explicit and deterministic:
 * mockup + tours are "old", clips are "fresh" (newer than both). Call right
 * after cloneFixture() so every test starts from a known-good baseline. */
export function setFreshTimestamps(dir) {
  touch(path.join(dir, "mockup.html"), -10_000);
  touch(path.join(dir, "tour-a.json"), -10_000);
  touch(path.join(dir, "tour-b.json"), -10_000);
  touch(path.join(dir, "clips-real", "tour-a.rrweb.json"), 0);
  touch(path.join(dir, "clips-real", "tour-a.rrweb.json.chapters.json"), 0);
  touch(path.join(dir, "clips-real", "tour-b.rrweb.json"), 0);
  touch(path.join(dir, "clips-real", "tour-b.rrweb.json.chapters.json"), 0);
}

export function manifestPath(dir) {
  return path.join(dir, "manifest.demo.json");
}

export function readManifestJSON(dir) {
  return JSON.parse(fs.readFileSync(manifestPath(dir), "utf8"));
}

export function writeManifestJSON(dir, manifest) {
  fs.writeFileSync(manifestPath(dir), `${JSON.stringify(manifest, null, 2)}\n`);
}
