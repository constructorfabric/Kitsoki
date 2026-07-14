#!/usr/bin/env node
// Builds tour-player as a single ESM artifact (for bundlers / <script
// type="module">) and a single IIFE artifact (for a plain <script> tag,
// e.g. injecting the player into a third-party page via
// page.addScriptTag) — Driver.js-class budget: zero runtime deps, one file
// each. Also runs `tsc --emitDeclarationOnly` for dist/*.d.ts.
import { build } from "esbuild";
import { execSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.join(here, "..");
const entry = path.join(root, "src", "index.ts");

await build({
  entryPoints: [entry],
  bundle: true,
  format: "esm",
  outfile: path.join(root, "dist", "tour-player.esm.js"),
  target: "es2020",
  minify: true
});

await build({
  entryPoints: [entry],
  bundle: true,
  format: "iife",
  globalName: "KitsokiTourPlayer",
  outfile: path.join(root, "dist", "tour-player.iife.js"),
  target: "es2020",
  minify: true
});

execSync("npx tsc --emitDeclarationOnly", { cwd: root, stdio: "inherit" });

console.log("tour-player build: dist/tour-player.esm.js + dist/tour-player.iife.js + .d.ts");
