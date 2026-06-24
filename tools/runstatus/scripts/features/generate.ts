/**
 * Feature-catalog codegen CLI. The catalog (features/*.yaml at the repo root)
 * is the single source of truth for feature content; this tool derives every
 * downstream artifact from it:
 *
 *   (default)         emit src/tour/generated/<id>.ts per feature with a tour,
 *                     plus features/feature.schema.json (editor validation).
 *   --check           validate everything, re-render in memory, byte-compare
 *                     against the committed files, and run the spec<->feature
 *                     bijection checks. Writes nothing; exit 1 on any problem.
 *   --index [--out D] emit the site/QA contract: features-index.json plus
 *                     qa/<id>.scenarios.yaml (default D=.artifacts/features).
 *   --print-demo ID   print "<specName>\t<artifactDir>\t<videoPath>" for make
 *                     recipes that resolve demo paths from the catalog.
 *
 * Emission is deterministic (no timestamps, fixed field order, JSON.stringify
 * strings) so --check is a trivial byte comparison and diffs stay reviewable.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { parse } from "yaml";
import { z } from "zod";
import { FeatureObjectSchema, FeatureSchema, validateCatalog, type Feature } from "./schema.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
// scripts/features → scripts → runstatus → tools → repo root
const repoRoot = path.resolve(__dirname, "../../../..");
const runstatusDir = path.resolve(__dirname, "../..");
const featuresDir = path.join(repoRoot, "features");
const generatedDir = path.join(runstatusDir, "src", "tour", "generated");
const jsonSchemaPath = path.join(featuresDir, "feature.schema.json");
const specsDir = path.join(runstatusDir, "tests", "playwright");

/** Fixed emission order — the TourStep declaration order in src/tour/types.ts. */
const STEP_FIELDS = [
  "id", "route", "target", "targetText", "title", "body",
  "placement", "kind", "advance", "advanceRoute", "waitForTarget", "dwellMs",
  "drive",
] as const;

interface Loaded {
  file: string; // repo-relative, e.g. features/agent-actions.yaml
  feature: Feature;
}

function fail(msgs: string[]): never {
  for (const m of msgs) console.error(`features: ${m}`);
  console.error(`features: ${msgs.length} problem(s)`);
  process.exit(1);
}

function loadCatalog(): Loaded[] {
  if (!fs.existsSync(featuresDir)) {
    fail([`no ${path.relative(repoRoot, featuresDir)}/ directory at the repo root`]);
  }
  const files = fs
    .readdirSync(featuresDir)
    .filter((f) => /\.ya?ml$/.test(f))
    .sort();
  if (files.length === 0) fail([`no *.yaml files under features/`]);

  const problems: string[] = [];
  const loaded: Loaded[] = [];
  for (const name of files) {
    const rel = path.join("features", name);
    let doc: unknown;
    try {
      doc = parse(fs.readFileSync(path.join(featuresDir, name), "utf8"));
    } catch (e) {
      problems.push(`${rel}: YAML parse error: ${e instanceof Error ? e.message : e}`);
      continue;
    }
    const res = FeatureSchema.safeParse(doc);
    if (!res.success) {
      for (const issue of res.error.issues) {
        problems.push(`${rel}: ${issue.path.join(".") || "(root)"}: ${issue.message}`);
      }
      continue;
    }
    loaded.push({ file: rel, feature: res.data });
  }
  problems.push(...validateCatalog(loaded, repoRoot));
  if (problems.length > 0) fail(problems);
  return loaded;
}

// ── Generated tour manifest ──────────────────────────────────────────────────

function renderManifest(l: Loaded): string {
  const f = l.feature;
  if (!f.tour) throw new Error(`renderManifest(${f.id}): no tour`);
  const lines: string[] = [
    `// Code generated from ${l.file} by scripts/features/generate.ts. DO NOT EDIT.`,
    `// Edit the YAML and run \`make features\` to regenerate.`,
    ``,
    `import { type TourStep } from "../types.js";`,
    ``,
    `// Re-export so a Playwright spec can import the step type alongside the array.`,
    `export type { TourStep };`,
    ``,
    `export const ${f.tour.export}: readonly TourStep[] = [`,
  ];
  for (const step of f.tour.steps) {
    lines.push(`  {`);
    for (const field of STEP_FIELDS) {
      const v = (step as Record<string, unknown>)[field];
      if (v === undefined) continue;
      lines.push(`    ${field}: ${JSON.stringify(v)},`);
    }
    lines.push(`  },`);
  }
  lines.push(`];`, ``);
  return lines.join("\n");
}

function renderJsonSchema(): string {
  const js = z.toJSONSchema(FeatureObjectSchema);
  return JSON.stringify(js, null, 2) + "\n";
}

/** Map of output path (repo-relative) → content, for both write and check. */
function renderAll(catalog: Loaded[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const l of catalog) {
    if (!l.feature.tour) continue;
    const rel = path.relative(repoRoot, path.join(generatedDir, `${l.feature.id}.ts`));
    out.set(rel, renderManifest(l));
  }
  out.set(path.relative(repoRoot, jsonSchemaPath), renderJsonSchema());
  return out;
}

// ── Spec <-> feature bijection ───────────────────────────────────────────────

function checkBijection(catalog: Loaded[]): string[] {
  const problems: string[] = [];
  const byId = new Map(catalog.map((l) => [l.feature.id, l]));
  const exportToId = new Map(
    catalog.filter((l) => l.feature.tour).map((l) => [l.feature.tour!.export, l.feature.id]),
  );

  // Forward: every tour-bearing feature names a spec that exists and actually
  // uses its export symbol (catches a spec silently switching manifests).
  for (const l of catalog) {
    if (!l.feature.tour || !l.feature.demo) continue;
    const specPath = path.join(runstatusDir, l.feature.demo.spec);
    if (!fs.existsSync(specPath)) {
      problems.push(`${l.file}: demo.spec "${l.feature.demo.spec}" does not exist`);
      continue;
    }
    const src = fs.readFileSync(specPath, "utf8");
    if (!src.includes(l.feature.tour.export)) {
      problems.push(
        `${l.file}: spec ${l.feature.demo.spec} never references tour export "${l.feature.tour.export}"`,
      );
    }
  }

  // Reverse: every spec import of a generated manifest maps back to a feature,
  // and no spec still imports a legacy src/tour/*-manifest module whose content
  // has been migrated (same export now generated from YAML).
  for (const name of fs.readdirSync(specsDir).filter((f) => f.endsWith(".spec.ts"))) {
    const src = fs.readFileSync(path.join(specsDir, name), "utf8");
    for (const m of src.matchAll(/src\/tour\/generated\/([a-z0-9-]+)\.js/g)) {
      if (!byId.has(m[1])) {
        problems.push(`tests/playwright/${name}: imports generated/${m[1]}.js but no features/${m[1]}.yaml exists`);
      }
    }
    for (const m of src.matchAll(/src\/tour\/([a-z0-9-]+-manifest)\.js/g)) {
      const legacy = path.join(runstatusDir, "src", "tour", `${m[1]}.ts`);
      if (!fs.existsSync(legacy)) {
        problems.push(`tests/playwright/${name}: imports deleted legacy manifest src/tour/${m[1]}.ts`);
        continue;
      }
      const legacySrc = fs.readFileSync(legacy, "utf8");
      const exp = legacySrc.match(/export const ([A-Z][A-Z0-9_]*):/);
      if (exp && exportToId.has(exp[1])) {
        problems.push(
          `tests/playwright/${name}: still imports legacy src/tour/${m[1]}.ts — ` +
            `flip to src/tour/generated/${exportToId.get(exp[1])}.js and delete the legacy file`,
        );
      }
    }
  }

  // Orphans: generated files with no corresponding feature.
  if (fs.existsSync(generatedDir)) {
    for (const name of fs.readdirSync(generatedDir).filter((f) => f.endsWith(".ts"))) {
      const id = name.replace(/\.ts$/, "");
      if (!byId.has(id) || !byId.get(id)!.feature.tour) {
        problems.push(`src/tour/generated/${name}: orphan — no tour-bearing features/${id}.yaml`);
      }
    }
  }

  return problems;
}

// ── Site/QA index ────────────────────────────────────────────────────────────

function specName(spec: string): string {
  return path.basename(spec).replace(/\.spec\.ts$/, "");
}

/** Empty for desktop (the back-compat primary keeps `<base>.mp4`); `--<id>`
 *  otherwise. Keep in lockstep with profileSuffix() in
 *  tests/playwright/_helpers/camera.ts. */
function profileSuffix(profile: string): string {
  return profile === "desktop" ? "" : `--${profile}`;
}

/**
 * The index demo entry. The video/chapters PATHS are derived here (never
 * authored in YAML) so the catalog owns the contract. Each declared profile gets
 * a `variants` entry at its suffixed path; `video`/`chapters` stay the desktop
 * primary for every existing consumer (stage-media, the site data join).
 */
function buildDemoIndex(d: NonNullable<Feature["demo"]>) {
  const dir = path.join(".artifacts", d.artifactDir);
  const profiles = d.profiles ?? ["desktop"];
  const variantFor = (p: string) => {
    const s = profileSuffix(p);
    return {
      video: path.join(dir, `${d.videoBase}${s}.mp4`),
      chapters: path.join(dir, `${d.videoBase}${s}.mp4.chapters.json`),
    };
  };
  const variants = Object.fromEntries(profiles.map((p) => [p, variantFor(p)]));
  // Desktop is the back-compat primary; fall back to the first declared profile
  // only if a demo ever omits desktop.
  const primary = variants.desktop ?? variantFor(profiles[0]);
  return {
    spec: d.spec ? path.join("tools/runstatus", d.spec) : null,
    specName: d.spec ? specName(d.spec) : null,
    renderer: d.renderer ?? "playwright",
    artifactDir: dir,
    video: primary.video,
    chapters: primary.chapters,
    profiles,
    variants,
    posterStep: d.posterStep ?? null,
    screenshotPattern: "NN-<stepId>.png",
    story: d.story ?? null,
    flow: d.flow ?? null,
    hostCassette: d.hostCassette ?? null,
    external: d.external ?? false,
  };
}

function renderIndex(catalog: Loaded[]): string {
  const features = catalog.map((l) => {
    const f = l.feature;
    const demo = f.demo ? buildDemoIndex(f.demo) : null;
    return {
      id: f.id,
      kind: f.kind,
      title: f.title,
      tagline: f.tagline,
      summary: f.summary,
      narrative: f.narrative ?? null,
      promo: f.promo ?? null,
      docs: f.docs ?? [],
      related: f.related ?? [],
      demo,
      sections: f.sections ?? null,
      tour: f.tour ?? null,
      qa: f.qa
        ? { scenariosFile: `.artifacts/features/qa/${f.id}.scenarios.yaml`, scenarios: f.qa.scenarios }
        : null,
    };
  });
  return JSON.stringify({ schemaVersion: 1, features }, null, 2) + "\n";
}

/**
 * ui-qa "--feature" markdown: the bug/plan spec the vision judge grounds its
 * verdict in. Derived from the catalog so the judged claim set and the promo
 * copy can never diverge.
 */
function renderFeatureMd(f: Feature): string {
  const lines: string[] = [
    `<!-- Generated from features/${f.id}.yaml by scripts/features/generate.ts. DO NOT EDIT. -->`,
    ``,
    `# ${f.title}`,
    ``,
    f.summary,
    ``,
  ];
  if (f.tour) {
    lines.push(`## What the demo video walks through`, ``);
    for (const s of f.tour.steps) lines.push(`- **${s.title}** — ${s.body}`);
    lines.push(``);
  }
  return lines.join("\n");
}

/** ui-qa scenarios file (.agents/skills/kitsoki-ui-qa contract): feature + scenarios. */
function renderScenarios(f: Feature): string {
  const lines: string[] = [
    `# Generated from features/${f.id}.yaml by scripts/features/generate.ts. DO NOT EDIT.`,
    `# Each step is an OBSERVABLE claim judged frame-by-frame by the gated ui-qa pipeline.`,
    ``,
    `feature: ${JSON.stringify(f.title)}`,
    ``,
    `scenarios:`,
  ];
  for (const s of f.qa!.scenarios) {
    lines.push(`  - id: ${JSON.stringify(s.id)}`);
    lines.push(`    title: ${JSON.stringify(s.title)}`);
    lines.push(`    required: ${s.required}`);
    lines.push(`    steps:`);
    for (const step of s.steps) lines.push(`      - ${JSON.stringify(step)}`);
    lines.push(``);
  }
  return lines.join("\n");
}

// ── Modes ────────────────────────────────────────────────────────────────────

function modeWrite(catalog: Loaded[]): void {
  fs.mkdirSync(generatedDir, { recursive: true });
  let wrote = 0;
  for (const [rel, content] of renderAll(catalog)) {
    const abs = path.join(repoRoot, rel);
    if (fs.existsSync(abs) && fs.readFileSync(abs, "utf8") === content) continue;
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, content);
    console.log(`features: wrote ${rel}`);
    wrote++;
  }
  const problems = checkBijection(catalog);
  if (problems.length > 0) fail(problems);
  console.log(`features: ${catalog.length} feature(s) OK, ${wrote} file(s) updated`);
}

function modeCheck(catalog: Loaded[]): void {
  const problems: string[] = [];
  for (const [rel, content] of renderAll(catalog)) {
    const abs = path.join(repoRoot, rel);
    if (!fs.existsSync(abs)) {
      problems.push(`${rel}: missing — run: make features`);
    } else if (fs.readFileSync(abs, "utf8") !== content) {
      problems.push(`${rel}: stale — run: make features`);
    }
  }
  problems.push(...checkBijection(catalog));
  if (problems.length > 0) fail(problems);
  console.log(`features: ${catalog.length} feature(s) OK — generated files fresh`);
}

function modeIndex(catalog: Loaded[], outDir: string): void {
  fs.mkdirSync(path.join(outDir, "qa"), { recursive: true });
  const indexPath = path.join(outDir, "features-index.json");
  fs.writeFileSync(indexPath, renderIndex(catalog));
  console.log(`features: wrote ${path.relative(repoRoot, indexPath)}`);
  for (const l of catalog) {
    if (!l.feature.qa) continue;
    const p = path.join(outDir, "qa", `${l.feature.id}.scenarios.yaml`);
    fs.writeFileSync(p, renderScenarios(l.feature));
    const md = path.join(outDir, "qa", `${l.feature.id}.feature.md`);
    fs.writeFileSync(md, renderFeatureMd(l.feature));
    console.log(`features: wrote ${path.relative(repoRoot, p)} (+ feature.md)`);
  }
}

function modePrintDemo(catalog: Loaded[], id: string): void {
  const l = catalog.find((c) => c.feature.id === id);
  if (!l) fail([`no feature "${id}" in the catalog`]);
  const d = l.feature.demo;
  if (!d) fail([`feature "${id}" has no demo binding`]);
  if (!d.spec) fail([`feature "${id}" is stitched, not recorded — use: make render-tour`]);
  const dir = path.join(".artifacts", d.artifactDir);
  process.stdout.write(`${specName(d.spec)}\t${dir}\t${path.join(dir, `${d.videoBase}.mp4`)}\n`);
}

const args = process.argv.slice(2);
const catalog = loadCatalog();
if (args[0] === "--check") {
  modeCheck(catalog);
} else if (args[0] === "--index") {
  const outFlag = args.indexOf("--out");
  const outDir = outFlag >= 0 ? path.resolve(args[outFlag + 1]) : path.join(repoRoot, ".artifacts", "features");
  modeIndex(catalog, outDir);
} else if (args[0] === "--print-demo") {
  if (!args[1]) fail([`--print-demo needs a feature id`]);
  modePrintDemo(catalog, args[1]);
} else if (args.length === 0) {
  modeWrite(catalog);
} else {
  fail([`unknown arguments: ${args.join(" ")}`]);
}
