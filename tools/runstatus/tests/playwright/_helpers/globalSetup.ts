/**
 * Playwright globalSetup: builds the runstatus SPA when needed and stages the
 * same bundle into the Go embed asset used by live `kitsoki web` specs.
 */
import { execSync } from "child_process";
import { fileURLToPath } from "url";
import path from "path";
import fs from "fs";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// __dirname = tools/runstatus/tests/playwright/_helpers
// projectRoot = tools/runstatus (3 levels up)
const projectRoot = path.resolve(__dirname, "../../..");
const repoRoot = path.resolve(projectRoot, "../..");
const tempRoot = process.env.KITSOKI_TEMP_ROOT ?? path.join(repoRoot, ".temp");
const distIndex = path.join(tempRoot, "runstatus", "dist", "index.html");
const embedIndex = path.join(repoRoot, "internal", "runstatus", "web", "assets", "index.html");
const buildInputs = [
  path.join(projectRoot, "src"),
  path.join(projectRoot, "package.json"),
  path.join(projectRoot, "pnpm-lock.yaml"),
  path.join(projectRoot, "tsconfig.json"),
  path.join(projectRoot, "vite.config.ts"),
];

export default function globalSetup(): void {
  const forceRebuild = process.env.PW_FORCE_REBUILD === "1";

  if (forceRebuild || needsBuild(distIndex, buildInputs)) {
    console.log("[globalSetup] Building runstatus SPA via pnpm build...");
    execSync("pnpm build", {
      cwd: projectRoot,
      stdio: "inherit",
    });
  } else {
    console.log("[globalSetup] runstatus SPA build is fresh.");
  }

  stageEmbed(distIndex, embedIndex, forceRebuild);
}

function stageEmbed(from: string, to: string, force: boolean): void {
  if (!fs.existsSync(from)) {
    throw new Error(`runstatus build output missing: ${from}`);
  }
  if (!force && fs.existsSync(to) && fs.statSync(to).mtimeMs >= fs.statSync(from).mtimeMs) {
    console.log("[globalSetup] Go embed asset is fresh.");
    return;
  }
  fs.mkdirSync(path.dirname(to), { recursive: true });
  fs.copyFileSync(from, to);
  console.log(`[globalSetup] Staged runstatus SPA -> ${path.relative(repoRoot, to)}`);
}

function needsBuild(output: string, inputs: string[]): boolean {
  if (!fs.existsSync(output)) return true;
  const outputMtime = fs.statSync(output).mtimeMs;
  return inputs.some((input) => latestMtime(input) > outputMtime);
}

function latestMtime(target: string): number {
  if (!fs.existsSync(target)) return 0;
  const stat = fs.statSync(target);
  if (!stat.isDirectory()) return stat.mtimeMs;

  let latest = stat.mtimeMs;
  for (const entry of fs.readdirSync(target, { withFileTypes: true })) {
    if (entry.name === "node_modules" || entry.name === "dist" || entry.name === "test-results") {
      continue;
    }
    latest = Math.max(latest, latestMtime(path.join(target, entry.name)));
  }
  return latest;
}
