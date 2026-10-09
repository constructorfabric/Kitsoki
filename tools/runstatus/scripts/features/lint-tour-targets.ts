/**
 * tour-targets:lint — a fast, deterministic drift check between the feature
 * catalog's tour steps and the SPA's real `data-testid` attributes.
 *
 * Every `target` / `waitForTarget` in a features/<id>.yaml tour step is a
 * literal that the live tour overlay resolves via
 * `document.querySelector('[data-testid="<literal>"]')`
 * (tools/runstatus/src/components/tour/TourOverlay.vue, `selOf()`). If a UI
 * commit renames or removes that data-testid without updating the tour step,
 * the step silently stalls (`waitForTarget` never satisfied) — usually only
 * caught later, expensively, by a failed demo recording. This check catches
 * it at PR time, for free: no browser, no recording, no LLM.
 *
 * Two kinds of intentional non-matches are tolerated, not flagged:
 *   1. Dynamic testids — a component binds `:data-testid="`prefix-${expr}`"`,
 *      so no static string in the source will ever equal the full literal a
 *      tour step waits for (e.g. `intent-btn-launch` against
 *      InputBar.vue's `` `intent-btn-${item.Intent}` ``). Any literal tour
 *      target that starts with a known dynamic prefix and has a non-empty
 *      remainder counts as resolved.
 *   2. Allowlisted literals — testid-lint.allowlist.txt, one id per line with
 *      a mandatory trailing `# comment` (a TODO(demo-repair) note is the
 *      expected shape for a currently-broken target a demo-repair pass owns).
 *
 * Run via `pnpm tour-targets:lint`; chained into `pnpm features:check` so it
 * gates `make features-check` / `make web` (part of `make build`) / `make
 * test` (scripts/run-tests.sh Suite 3, when pnpm/node_modules are present).
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
import { parse } from "yaml";

const here = path.dirname(fileURLToPath(import.meta.url));
// scripts/features → scripts → runstatus → tools → repo root
const repoRoot = path.resolve(here, "../../../..");
const runstatusDir = path.resolve(here, "../..");
const featuresDir = path.join(repoRoot, "features");
const srcDir = path.join(runstatusDir, "src");
const allowlistPath = path.join(here, "tour-targets-lint.allowlist.txt");

interface TourTarget {
  feature: string; // features/<id>.yaml, repo-relative
  stepId: string;
  field: "target" | "waitForTarget";
  testid: string;
}

function collectTourTargets(): TourTarget[] {
  if (!fs.existsSync(featuresDir)) {
    console.error(`tour-targets:lint: no features/ directory at the repo root`);
    process.exit(1);
  }
  const out: TourTarget[] = [];
  for (const name of fs.readdirSync(featuresDir).filter((f) => /\.ya?ml$/.test(f)).sort()) {
    const rel = path.join("features", name);
    const doc = parse(fs.readFileSync(path.join(featuresDir, name), "utf8")) as
      | { tour?: { steps?: { id: string; target?: string; waitForTarget?: string }[] } }
      | null;
    for (const step of doc?.tour?.steps ?? []) {
      if (step.target) out.push({ feature: rel, stepId: step.id, field: "target", testid: step.target });
      if (step.waitForTarget) {
        out.push({ feature: rel, stepId: step.id, field: "waitForTarget", testid: step.waitForTarget });
      }
    }
  }
  return out;
}

/** Every source file under src/ except the codegen output itself (which only
 * ever repeats catalog literals back — checking it would be circular). */
function sourceFiles(): string[] {
  const out: string[] = [];
  const generatedDir = path.join(srcDir, "tour", "generated");
  const walk = (dir: string) => {
    for (const dirent of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, dirent.name);
      if (dirent.isDirectory()) {
        if (p === generatedDir) continue;
        if (dirent.name === "node_modules") continue;
        walk(p);
      } else if (/\.(vue|ts|tsx)$/.test(dirent.name)) {
        out.push(p);
      }
    }
  };
  walk(srcDir);
  return out;
}

// Matches `data-testid="literal"` and `data-testid='literal'` (static Vue
// attribute / plain HTML / JSX).
const STATIC_RE = /data-testid=["']([^"'{}`$]+)["']/g;
// Matches Vue's bound form: `:data-testid="<expr>"`, where <expr> is itself a
// JS/TS expression — most commonly a template literal `` `prefix-${x}` `` or a
// string concatenation `'prefix-' + x`.
const BOUND_RE = /:data-testid="([^"]+)"/g;
// Pulls the literal prefix out of a template literal expression, up to its
// first `${`. A template literal with no leading literal text (interpolation
// first) yields an empty prefix, which matches nothing (never satisfies a
// tour target) — allowlist the specific tour target if that's a false
// positive in practice (a cross-component prop-forwarded testid is the usual
// cause; not resolvable without real dataflow analysis).
const TEMPLATE_PREFIX_RE = /`([^`$]*)\$\{/;
// Pulls the literal prefix out of a leading string-concatenation expression,
// e.g. `'agent-actions-mode-' + m` -> "agent-actions-mode-".
const CONCAT_PREFIX_RE = /^["']([^"']*)["']\s*\+/;

function collectTestids(files: string[]): { static: Set<string>; dynamicPrefixes: Set<string> } {
  const staticIds = new Set<string>();
  const dynamicPrefixes = new Set<string>();
  for (const f of files) {
    const src = fs.readFileSync(f, "utf8");
    for (const m of src.matchAll(STATIC_RE)) staticIds.add(m[1]);
    for (const m of src.matchAll(BOUND_RE)) {
      const expr = m[1];
      if (/^["'][^"'{}`$]+["']$/.test(expr)) {
        // :data-testid="'literal'" — a bound attribute that's actually static.
        staticIds.add(expr.slice(1, -1));
        continue;
      }
      const tmpl = expr.match(TEMPLATE_PREFIX_RE);
      if (tmpl && tmpl[1]) {
        dynamicPrefixes.add(tmpl[1]);
        continue;
      }
      const concat = expr.match(CONCAT_PREFIX_RE);
      if (concat && concat[1]) {
        dynamicPrefixes.add(concat[1]);
        continue;
      }
      // Anything else (a bare identifier, a computed ref, a ternary, a
      // template literal/concat with no static prefix) can't be resolved
      // statically; it neither adds a static id nor a usable prefix, so it
      // simply can't clear an unmatched literal — allowlist the specific
      // tour target if that's a false positive in practice.
    }
  }
  return { static: staticIds, dynamicPrefixes };
}

/** id → trailing comment (kept only for the "OK" summary; not load-bearing). */
function loadAllowlist(): Map<string, string> {
  const allowed = new Map<string, string>();
  if (!fs.existsSync(allowlistPath)) return allowed;
  for (const rawLine of fs.readFileSync(allowlistPath, "utf8").split("\n")) {
    const line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    const hashIdx = line.indexOf("#");
    const id = (hashIdx >= 0 ? line.slice(0, hashIdx) : line).trim();
    const comment = hashIdx >= 0 ? line.slice(hashIdx + 1).trim() : "";
    if (id) allowed.set(id, comment);
  }
  return allowed;
}

function main(): void {
  const targets = collectTourTargets();
  const files = sourceFiles();
  const { static: staticIds, dynamicPrefixes } = collectTestids(files);
  const allowlist = loadAllowlist();

  const problems: string[] = [];
  const seen = new Set<string>(); // dedupe identical (testid) across steps for the resolved tally
  let resolvedCount = 0;
  let allowlistedCount = 0;
  const usedAllowlist = new Set<string>();

  for (const t of targets) {
    seen.add(t.testid);
    if (staticIds.has(t.testid)) {
      resolvedCount++;
      continue;
    }
    if ([...dynamicPrefixes].some((p) => t.testid.startsWith(p) && t.testid.length > p.length)) {
      resolvedCount++;
      continue;
    }
    if (allowlist.has(t.testid)) {
      allowlistedCount++;
      usedAllowlist.add(t.testid);
      continue;
    }
    problems.push(
      `${t.feature} [${t.stepId}] ${t.field}: "${t.testid}" has no matching data-testid under tools/runstatus/src/ ` +
        `(not staged/renamed UI, or allowlist it in ${path.relative(repoRoot, allowlistPath)} with a reason)`,
    );
  }

  const staleAllowlist = [...allowlist.keys()].filter((id) => !usedAllowlist.has(id));
  for (const id of staleAllowlist) {
    problems.push(
      `${path.relative(repoRoot, allowlistPath)}: "${id}" is allowlisted but no tour step references it — remove the stale entry`,
    );
  }

  if (problems.length > 0) {
    for (const p of problems) console.error(`tour-targets:lint: ${p}`);
    console.error(`tour-targets:lint: ${problems.length} problem(s) across ${targets.length} tour target(s)`);
    process.exit(1);
  }
  console.log(
    `tour-targets:lint: OK — ${targets.length} tour target(s) checked, ${seen.size} distinct ` +
      `(${resolvedCount} resolved${allowlistedCount ? `, ${allowlistedCount} allowlisted` : ""})`,
  );
}

main();
