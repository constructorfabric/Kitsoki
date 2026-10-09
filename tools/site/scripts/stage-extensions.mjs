#!/usr/bin/env node
/**
 * Build-time extension-library staging.
 *
 * The product site must not hand-maintain the extension/package inventory. It
 * shells out to the kitsoki docs indexer, writes the generated JSON into the
 * VitePress gen/ workspace, and validates the minimal contract the library
 * pages rely on. CI runs this through make site, so a broken docs index breaks
 * the site before publication.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { spawnSync } from "child_process";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const sourceSiteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.join(__dirname, ".."));
const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.join(sourceSiteDir, "../.."));
const runtimeSiteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? sourceSiteDir);
const outDir = path.join(runtimeSiteDir, ".vitepress", "gen");
const outFile = path.join(outDir, "extensions-index.json");

fs.mkdirSync(outDir, { recursive: true });

const res = spawnSync(
  "go",
  ["run", "./cmd/kitsoki", "docs", "index", "--root", ".", "--json-out", outFile],
  { cwd: repoRoot, stdio: "inherit", env: process.env },
);
if (res.status !== 0) process.exit(res.status ?? 1);

function fail(message) {
  console.error(`stage-extensions: ${message}`);
  process.exit(1);
}

if (!fs.existsSync(outFile)) fail(`indexer did not write ${path.relative(repoRoot, outFile)}`);
const index = JSON.parse(fs.readFileSync(outFile, "utf8"));
if (index.schema !== "kitsoki.extensions-index/v1") fail(`unexpected schema ${index.schema}`);
if (!Array.isArray(index.packages)) fail("packages must be an array");
if (!Array.isArray(index.stories)) fail("stories must be an array");
if (!Array.isArray(index.components)) fail("components must be an array");
if (!Array.isArray(index.docs)) fail("docs must be an array");
for (const pkg of index.packages) {
  if (!pkg.id || !pkg.title || !pkg.path) fail(`package missing id/title/path: ${JSON.stringify(pkg)}`);
}
for (const story of index.stories) {
  if (!story.id || !story.title || !story.path) fail(`story missing id/title/path: ${JSON.stringify(story)}`);
}
for (const component of index.components) {
  if (!component.id || !component.kind || !component.title) {
    fail(`component missing id/kind/title: ${JSON.stringify(component)}`);
  }
  if (component.publish && !["true", "false", "summary", "full"].includes(String(component.publish))) {
    fail(`component ${component.kind}:${component.id} has unsupported publish value ${component.publish}`);
  }
}
for (const doc of index.docs) {
  if (!doc.id || !doc.owner || !doc.title) fail(`doc missing id/owner/title: ${JSON.stringify(doc)}`);
  if (!["true", "false", "summary", "full"].includes(String(doc.publish))) {
    fail(`doc ${doc.id} has unsupported publish value ${doc.publish}`);
  }
}
console.log(`stage-extensions: ${index.packages.length} package(s), ${index.stories.length} story/stories, ${index.components.length} component(s), ${index.docs.length} doc node(s) -> ${path.relative(repoRoot, outFile)}`);
