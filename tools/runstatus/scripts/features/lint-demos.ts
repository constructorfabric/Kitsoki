/**
 * demos:lint — the no-LLM + recording-consistency gate over the demo catalog.
 *
 * `features:check` (generate.ts) already validates the catalog↔spec bijection
 * and that the generated tour TS is fresh. THIS lint adds the per-spec
 * invariants that keep every catalog demo capture deterministic and
 * chapter-addressable — the things a byte-comparison of generated files can't
 * see:
 *
 *   1. camera   — the spec must route newContext through _helpers/camera, so
 *                 every replay/export shares the same 1600×900 capture canvas.
 *   2. chapters — the spec must emit a chapter sidecar (writeChapters), so the
 *                 rrweb viewer and any explicit legacy export can show a rail.
 *   3. no-LLM   — the spec must drive the server deterministically
 *                 (startWebServer / --flow / --harness), never a live model.
 *                 This is the cost guard AND what makes "the film is a CI test"
 *                 true (AGENTS.md: automated tests never use a real LLM).
 *
 * Run via `pnpm demos:lint`; also chained into `pnpm features:check` so it gates
 * in `make build` / `make test` / CI. Exit 1 with a per-spec problem list on any
 * violation; one OK line on success.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { parse } from "yaml";

const here = path.dirname(fileURLToPath(import.meta.url));
// scripts/features → scripts → runstatus → tools → repo root
const repoRoot = path.resolve(here, "../../../..");
const featuresDir = path.join(repoRoot, "features");
const specsDir = path.join(repoRoot, "tools", "runstatus", "tests", "playwright");

interface DemoBinding {
  feature: string; // features/<id>.yaml (repo-relative)
  id: string;
  spec: string; // as authored in YAML, e.g. tests/playwright/<name>.spec.ts
  external: boolean;
}

function loadDemos(): DemoBinding[] {
  if (!fs.existsSync(featuresDir)) {
    console.error(`demos:lint: no features/ directory at the repo root`);
    process.exit(1);
  }
  const files = fs.readdirSync(featuresDir).filter((f) => /\.ya?ml$/.test(f)).sort();
  const demos: DemoBinding[] = [];
  for (const f of files) {
    const doc = parse(fs.readFileSync(path.join(featuresDir, f), "utf8")) as
      | { id?: string; demo?: { spec?: string; external?: boolean } }
      | null;
    if (!doc?.demo?.spec) continue;
    demos.push({
      feature: `features/${f}`,
      id: doc.id ?? f.replace(/\.ya?ml$/, ""),
      spec: doc.demo.spec,
      external: doc.demo.external === true,
    });
  }
  return demos;
}

/** Each invariant: a label, a predicate over the spec source, and a fix hint. */
const CHECKS: { label: string; ok: (src: string) => boolean; hint: string }[] = [
  {
    label: "camera",
    ok: (s) => /_helpers\/camera(\.js)?["']/.test(s) && /cameraContext\s*\(/.test(s),
    hint: 'route newContext through cameraContext() — import { cameraContext } from "./_helpers/camera.js"',
  },
  {
    label: "chapters",
    ok: (s) => /writeChapters\s*\(/.test(s),
    hint: "emit a chapter sidecar — writeChapters(mp4, chapters.list())",
  },
  {
    label: "no-LLM",
    // Either a deterministic LIVE server (flow fixture / replay harness) or a
    // static offline SNAPSHOT (buildArtifact, no server at all). Both never call
    // a real model. A spec that spawns a server with neither would hit a live
    // harness — exactly what this guards.
    ok: (s) =>
      /startWebServer\s*\(/.test(s) ||
      /--flow/.test(s) ||
      /--harness/.test(s) ||
      /buildArtifact\s*\(/.test(s) ||
      /_helpers\/artifact(\.js)?["']/.test(s),
    hint:
      "drive the server deterministically (startWebServer / --flow / --harness) " +
      "or load a static snapshot (buildArtifact) — never a live model",
  },
];

function main(): void {
  const demos = loadDemos();
  const problems: string[] = [];
  let checked = 0;
  let skipped = 0;

  for (const d of demos) {
    const specName = d.spec.replace(/^tests\/playwright\//, "");
    const specPath = path.join(specsDir, specName);
    if (!fs.existsSync(specPath)) {
      // External demos are recorded outside this pipeline; tolerate a missing
      // local spec. A non-external demo pointing at a missing spec is a bug.
      if (d.external) {
        skipped++;
        continue;
      }
      problems.push(
        `${d.feature}: demo.spec "${d.spec}" not found at ${path.relative(repoRoot, specPath)}`,
      );
      continue;
    }
    const src = fs.readFileSync(specPath, "utf8");
    const rel = path.relative(repoRoot, specPath);
    // A deliberately-skipped stub (test.skip(true, …) / describe.skip) records
    // nothing yet — it exists only to satisfy the spec↔feature bijection.
    // Exempt it from the recording invariants; removing the skip re-engages them.
    if (/test\.skip\(\s*true\b/.test(src) || /\.describe\.skip\b/.test(src)) {
      skipped++;
      continue;
    }
    for (const c of CHECKS) {
      if (!c.ok(src)) problems.push(`${rel} [${d.id}]: ${c.label} — ${c.hint}`);
    }
    checked++;
  }

  if (problems.length > 0) {
    for (const p of problems) console.error(`demos:lint: ${p}`);
    console.error(`demos:lint: ${problems.length} problem(s) across ${demos.length} demo(s)`);
    process.exit(1);
  }
  console.log(
    `demos:lint: OK — ${checked} demo spec(s) pass camera/chapters/no-LLM` +
      (skipped ? ` (${skipped} skipped: external/stub)` : ""),
  );
}

main();
