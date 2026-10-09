#!/usr/bin/env node
// render-demo.mjs — on-demand replay rendering for a demo node. The committed
// tour-driven Playwright spec is the durable source; the rrweb/MP4 replay it
// emits is an ignored, temporary render output.
//
//   node pog/render-demo.mjs demo-<feature-id>
//
// This is the well-known runner contract the POG portal's POST /api/render
// invokes (it only ever runs THIS file with a validated node id — never a
// command taken from catalog data). Progress goes to stderr; the final
// stdout line is JSON: {"ok":true,"paths":["<repo-relative replay path>"]}.
//
// What it does: resolve the feature's declared tour/capture spec, run it via
// Playwright in whichever tools/runstatus checkout has it installed, then
// bridge its produced replay back under this member root so the portal can
// serve it.
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, symlinkSync, writeFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));

function fail(msg) {
  console.error(`[render-demo] ${msg}`);
  console.log(JSON.stringify({ ok: false, error: msg }));
  process.exit(1);
}

const nodeId = process.argv[2] ?? "";
if (!/^demo-[a-z0-9-]+$/.test(nodeId)) fail(`usage: node pog/render-demo.mjs demo-<feature-id> (got ${JSON.stringify(nodeId)})`);
const featureId = nodeId.replace(/^demo-/, "");

// YAML parser: same candidate chain as pog/sync-product-site.mjs.
function loadYamlParser() {
  const candidates = [
    { pkg: join(ROOT, "tools/runstatus/package.json"), name: "yaml", parse: (m, s) => m.parse(s) },
    { pkg: join(ROOT, "../../tools/runstatus/package.json"), name: "yaml", parse: (m, s) => m.parse(s) },
    { pkg: "/Users/brad/code/POG/portal/package.json", name: "js-yaml", parse: (m, s) => m.load(s) },
  ];
  for (const c of candidates) {
    try {
      const mod = createRequire(c.pkg)(c.name);
      return (s) => c.parse(mod, s);
    } catch {
      /* try next */
    }
  }
  fail("no YAML parser found (need `yaml` in tools/runstatus or js-yaml in the POG portal)");
}
const parseYaml = loadYamlParser();

const spec = readdirSync(join(ROOT, "features"))
  .filter((f) => f.endsWith(".yaml"))
  .map((f) => parseYaml(readFileSync(join(ROOT, "features", f), "utf8")))
  .find((s) => s?.id === featureId);
if (!spec) fail(`no feature spec with id ${featureId} under features/`);

const declaredSpec = spec.demo?.rrwebSpec ?? spec.demo?.spec;
if (typeof declaredSpec !== "string" || !declaredSpec.startsWith("tests/playwright/") || !declaredSpec.endsWith(".spec.ts")) {
  fail(`demo ${featureId} has no supported tour/capture spec`);
}
const specName = declaredSpec.slice("tests/playwright/".length);

// Run in whichever runstatus checkout actually has the spec + node_modules:
// this member worktree first, then the main checkout it was cut from.
const runstatusDirs = [join(ROOT, "tools/runstatus"), resolve(ROOT, "../../tools/runstatus")];
let runstatusDir = "";
for (const dir of runstatusDirs) {
  if (existsSync(join(dir, "tests/playwright", specName)) && existsSync(join(dir, "node_modules"))) {
    runstatusDir = dir;
    break;
  }
}
if (!runstatusDir) fail(`no runnable capture spec for ${featureId} (${declaredSpec}) under ${runstatusDirs.join(" and ")}`);

// The source declares a single .artifacts output directory. After Playwright
// completes, discover the replay it emitted there instead of encoding a media
// format into the catalog contract.
const specPath = join(runstatusDir, "tests/playwright", specName);
const specText = readFileSync(specPath, "utf8");
const dirMatch = specText.match(/ARTIFACT_DIR = path\.join\(\s*repoRoot,\s*([^)]+)\)/);
const dirSegs = dirMatch ? [...dirMatch[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]) : [];
if (dirSegs[0] !== ".artifacts") fail(`${specName} doesn't declare an ARTIFACT_DIR under .artifacts`);
const artifactRel = dirSegs.join("/");
const specRepoRoot = resolve(runstatusDir, "../..");

// Run with a generated minimal config instead of tools/runstatus's own:
// the default config's globalSetup rebuilds the SPA and stages the Go embed
// asset, and its outputDir/html reporter write INSIDE tools/runstatus — all
// of which fail when the checkout is locked read-only (the main tree's
// clobber protection). The captures only need the prebuilt bin/kitsoki, so
// every write goes under .artifacts instead. Plain object export — no
// defineConfig import, so the config resolves from .artifacts too.
const renderDir = join(specRepoRoot, ".artifacts", "render-demo");
mkdirSync(renderDir, { recursive: true });
const configPath = join(renderDir, "playwright.render.config.mjs");
writeFileSync(
  configPath,
  `export default {
  testDir: ${JSON.stringify(join(runstatusDir, "tests/playwright"))},
  workers: 1,
  // Capture specs start a real local app in beforeAll; their test-level
  // timeout does not cover that setup phase.
  timeout: 300000,
  use: { actionTimeout: 15000, navigationTimeout: 15000 },
  expect: { timeout: 5000 },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
  outputDir: ${JSON.stringify(join(renderDir, "test-results"))},
  reporter: [["list"]],
};
`,
);

console.error(`[render-demo] ${nodeId}: running ${specName} in ${runstatusDir} (writes under ${artifactRel})`);
const run = spawnSync("pnpm", ["exec", "playwright", "test", "-c", configPath, specName, "--project=chromium"], {
  cwd: runstatusDir,
  stdio: ["ignore", 2, 2], // playwright chatter goes to stderr; stdout stays clean for the JSON contract
  timeout: 15 * 60 * 1000,
  // Default the story server to go-run mode: it compiles current source into
  // GOCACHE (no writes into a possibly locked checkout) instead of trusting a
  // prebuilt bin/kitsoki that may predate the spec. Callers can override.
  env: { ...process.env, KITSOKI_WEB_GO_RUN: process.env.KITSOKI_WEB_GO_RUN ?? "1" },
});
if (run.status !== 0) fail(`playwright run failed (exit ${run.status ?? "signal"}) — see log above`);

const artifactAbs = join(specRepoRoot, artifactRel);
const replayFiles = existsSync(artifactAbs)
  ? readdirSync(artifactAbs, { recursive: true })
      .map((file) => String(file))
      .filter((file) => /(?:\.rrweb\.json|\.(?:mp4|webm))$/i.test(file))
      .map((file) => join(artifactAbs, file))
  : [];
const producedAbs = replayFiles.sort((a, b) => statSync(a).mtimeMs - statSync(b).mtimeMs).pop();
if (!producedAbs) fail(`spec passed but did not produce a replay under ${artifactRel}`);
const producedRel = producedAbs.slice(specRepoRoot.length + 1);

// Bridge the artifact dir back under this member root (gitignored symlink,
// same pattern as .artifacts/site-media and .artifacts/demo-specs) so the
// portal's evidence route — which resolves paths against the member root —
// can serve the recording.
if (specRepoRoot !== ROOT && !existsSync(join(ROOT, producedRel))) {
  // Link the shallowest missing directory along the artifact path (a real
  // .artifacts/rrweb-eval dir may already exist here — then link one level
  // deeper, the spec's own leaf dir).
  const producedSegs = producedRel.split("/");
  for (let depth = 1; depth < producedSegs.length; depth += 1) {
    const rel = producedSegs.slice(0, depth + 1).join("/");
    if (existsSync(join(ROOT, rel))) continue;
    mkdirSync(dirname(join(ROOT, rel)), { recursive: true });
    symlinkSync(join(specRepoRoot, rel), join(ROOT, rel));
    console.error(`[render-demo] linked ${rel} -> ${specRepoRoot}/${rel}`);
    break;
  }
  // Every directory level already exists as a real dir here (e.g. this
  // member root has its own old .artifacts/rrweb-eval runs) — link the
  // produced file itself.
  if (!existsSync(join(ROOT, producedRel))) {
    mkdirSync(dirname(join(ROOT, producedRel)), { recursive: true });
    symlinkSync(join(specRepoRoot, producedRel), join(ROOT, producedRel));
    console.error(`[render-demo] linked ${producedRel} -> ${specRepoRoot}/${producedRel}`);
  }
  if (!existsSync(join(ROOT, producedRel))) fail(`could not bridge ${producedRel} under the member root`);
}

console.error(`[render-demo] done: ${producedRel}`);
console.log(JSON.stringify({ ok: true, paths: [producedRel] }));
