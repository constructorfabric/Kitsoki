#!/usr/bin/env node
/**
 * Prepare the writable VitePress runtime root under .temp/site.
 *
 * tools/site is source. VitePress/Vite 5 writes temporary timestamp modules
 * beside config and dynamic-route files, and the site pipeline stages generated
 * docs/media before serving. Keep all of that churn in KITSOKI_SITE_ROOT and
 * copy source-controlled site files there so VitePress never resolves the
 * runtime project through a protected checkout or a stale symlink realpath.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const sourceSiteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.join(__dirname, ".."));
const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.join(sourceSiteDir, "../.."));
const workspace = path.resolve(process.env.KITSOKI_SITE_ROOT ?? path.join(repoRoot, ".temp", "site"));

function rel(from, to) {
  return path.relative(from, to) || ".";
}

function ensureDir(dir) {
  fs.mkdirSync(dir, { recursive: true });
}

function copySource(src, dest) {
  ensureDir(path.dirname(dest));
  fs.copyFileSync(src, dest);
}

function mirrorSourceTree(src, dest, skip) {
  ensureDir(dest);
  for (const name of fs.readdirSync(src).sort()) {
    if (skip.has(name)) continue;
    const from = path.join(src, name);
    const to = path.join(dest, name);
    const stat = fs.lstatSync(from);
    if (stat.isDirectory()) {
      mirrorSourceTree(from, to, new Set());
    } else if (stat.isFile() || stat.isSymbolicLink()) {
      copySource(from, to);
    }
  }
}

ensureDir(workspace);
for (const name of [
  ".vitepress",
  "src",
  "scripts",
  "cache",
  "vite-cache",
  "package.json",
  "pnpm-lock.yaml",
  "pnpm-workspace.yaml",
]) {
  fs.rmSync(path.join(workspace, name), { recursive: true, force: true });
}

for (const name of ["package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml"]) {
  copySource(path.join(sourceSiteDir, name), path.join(workspace, name));
}

const vitepressDir = path.join(workspace, ".vitepress");
ensureDir(vitepressDir);
copySource(path.join(sourceSiteDir, ".vitepress", "config.ts"), path.join(vitepressDir, "config.ts"));
mirrorSourceTree(path.join(sourceSiteDir, ".vitepress", "data"), path.join(vitepressDir, "data"), new Set());
mirrorSourceTree(path.join(sourceSiteDir, ".vitepress", "theme"), path.join(vitepressDir, "theme"), new Set());
ensureDir(path.join(vitepressDir, "gen"));
copySource(path.join(sourceSiteDir, "scripts", "manifest.mjs"), path.join(workspace, "scripts", "manifest.mjs"));

mirrorSourceTree(path.join(sourceSiteDir, "src"), path.join(workspace, "src"), new Set(["guide"]));
for (const generated of ["media", "deck-viewers", "decks"]) {
  fs.rmSync(path.join(workspace, "src", "public", generated), { recursive: true, force: true });
}

console.log(`site-workspace: prepared ${rel(repoRoot, workspace)} from ${rel(repoRoot, sourceSiteDir)}`);
