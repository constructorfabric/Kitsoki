import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const scriptPath = path.join(path.dirname(fileURLToPath(import.meta.url)), "prepare-workspace.mjs");

function writeFile(file, text = "") {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, text);
}

function seedSource(sourceRoot) {
  writeFile(path.join(sourceRoot, "package.json"), "{\"name\":\"site-fixture\",\"type\":\"module\"}\n");
  writeFile(path.join(sourceRoot, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n");
  writeFile(path.join(sourceRoot, "pnpm-workspace.yaml"), "packages: []\n");
  writeFile(path.join(sourceRoot, ".vitepress", "config.ts"), "export default {}\n");
  writeFile(path.join(sourceRoot, ".vitepress", "data", "features.ts"), "export {}\n");
  writeFile(path.join(sourceRoot, ".vitepress", "theme", "index.ts"), "export default {}\n");
  writeFile(path.join(sourceRoot, "scripts", "manifest.mjs"), "export default {}\n");
  writeFile(path.join(sourceRoot, "src", "index.md"), "# Home\n");
  writeFile(path.join(sourceRoot, "src", "guide", "generated.md"), "# Generated\n");
  writeFile(path.join(sourceRoot, "src", "public", "media", "stale.txt"), "old media\n");
  writeFile(path.join(sourceRoot, "src", "public", "deck-viewers", "stale.txt"), "old viewer\n");
  writeFile(path.join(sourceRoot, "src", "public", "decks", "stale.txt"), "old deck\n");
}

function assertRuntimeCopy(workspace, file) {
  const runtimePath = path.join(workspace, file);
  assert.equal(fs.lstatSync(runtimePath).isSymbolicLink(), false, `${file} should be copied`);
  assert.ok(fs.realpathSync(runtimePath).startsWith(fs.realpathSync(workspace) + path.sep));
}

test("prepare-workspace copies project metadata and clears stale dev caches", () => {
  const tempRoot = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-site-workspace-"));
  const repoRoot = path.join(tempRoot, "repo");
  const sourceRoot = path.join(repoRoot, "tools", "site");
  const workspace = path.join(repoRoot, ".temp", "site");
  seedSource(sourceRoot);

  fs.mkdirSync(workspace, { recursive: true });
  fs.mkdirSync(path.join(workspace, "cache"), { recursive: true });
  fs.mkdirSync(path.join(workspace, "vite-cache"), { recursive: true });
  writeFile(path.join(workspace, "cache", "stale.js"), "from a previous checkout\n");
  writeFile(path.join(workspace, "vite-cache", "stale.js"), "from a previous checkout\n");
  fs.symlinkSync(path.relative(workspace, path.join(sourceRoot, "package.json")), path.join(workspace, "package.json"));

  const result = spawnSync(process.execPath, [scriptPath], {
    cwd: repoRoot,
    env: {
      ...process.env,
      KITSOKI_REPO_ROOT: repoRoot,
      KITSOKI_SITE_SOURCE_ROOT: sourceRoot,
      KITSOKI_SITE_ROOT: workspace,
    },
    encoding: "utf8",
  });

  assert.equal(result.status, 0, result.stderr || result.stdout);
  assert.match(result.stdout, /site-workspace: prepared \.temp\/site from tools\/site/);
  assert.equal(fs.existsSync(path.join(workspace, "cache")), false);
  assert.equal(fs.existsSync(path.join(workspace, "vite-cache")), false);

  for (const file of [
    "package.json",
    "pnpm-lock.yaml",
    "pnpm-workspace.yaml",
    ".vitepress/config.ts",
    ".vitepress/theme/index.ts",
    ".vitepress/data/features.ts",
    "scripts/manifest.mjs",
    "src/index.md",
  ]) {
    assertRuntimeCopy(workspace, file);
  }

  assert.equal(fs.existsSync(path.join(workspace, "src", "guide", "generated.md")), false);
  assert.equal(fs.existsSync(path.join(workspace, "src", "public", "media", "stale.txt")), false);
  assert.equal(fs.existsSync(path.join(workspace, "src", "public", "deck-viewers", "stale.txt")), false);
  assert.equal(fs.existsSync(path.join(workspace, "src", "public", "decks", "stale.txt")), false);
});
