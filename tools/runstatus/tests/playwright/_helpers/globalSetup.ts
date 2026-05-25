/**
 * Playwright globalSetup: runs `pnpm build` once before the test suite
 * to ensure dist/index.html is fresh.
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

export default function globalSetup(): void {
  const distIndex = path.join(projectRoot, "dist", "index.html");

  // Skip build if dist already exists and is newer than src.
  // In CI or after a clean, always build.
  const forceRebuild = process.env.PW_FORCE_REBUILD === "1";

  if (forceRebuild || !fs.existsSync(distIndex)) {
    console.log("[globalSetup] Building dist/index.html via pnpm build…");
    execSync("pnpm build", {
      cwd: projectRoot,
      stdio: "inherit",
    });
  } else {
    console.log("[globalSetup] dist/index.html already exists — skipping build.");
  }
}
