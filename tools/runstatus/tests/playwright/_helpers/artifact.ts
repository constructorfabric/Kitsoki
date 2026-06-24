/**
 * artifact.ts — helpers for Playwright artifact-mode tests.
 *
 * buildArtifact(snapshotPath) reads dist/index.html, splices the snapshot
 * JSON into it as a <script type="application/json" id="kitsoki-snapshot">
 * tag before the SPA's boot script, writes to a temp file, and returns a
 * file:// URL the test can navigate to.
 *
 * The SPA's main.ts reads the tag and assigns window.__KITSOKI_SNAPSHOT__
 * before mounting. source.ts → createDataSource() picks up SnapshotSource
 * when __KITSOKI_SNAPSHOT__ is defined.
 */
import fs from "fs";
import { fileURLToPath } from "url";
import path from "path";
import { execSync } from "child_process";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// __dirname = tools/runstatus/tests/playwright/_helpers
// projectRoot = tools/runstatus (3 levels up)
const projectRoot = path.resolve(__dirname, "../../..");

// Artifacts go in <repo-root>/.artifacts/ — top of the surrounding git
// checkout (which is the worktree root when running from a worktree).
const repoRoot = execSync("git rev-parse --show-toplevel", { cwd: projectRoot, encoding: "utf-8" }).trim();
const artifactsDir = path.join(repoRoot, ".artifacts");

/** Ensure the artifacts directory exists. */
function ensureArtifactsDir(): void {
  fs.mkdirSync(artifactsDir, { recursive: true });
}

/**
 * Inline agent-prompt sidecars into the snapshot so the artifact is fully
 * self-contained. Under file:// the browser blocks a relative fetch() of the
 * .txt sidecar, so usePromptLoader could never resolve prompt_file. We read
 * each referenced sidecar (relative to the snapshot's dir) and copy its text
 * into the matching inline attr, then drop the *_file ref. Unreadable sidecars
 * are left untouched. Mirrors scripts/build-artifact.mjs#inlinePromptSidecars.
 */
function inlinePromptSidecars(snapshotJson: string, snapshotPath: string): string {
  let snap: { events?: Array<{ attrs?: Record<string, unknown> }> };
  try {
    snap = JSON.parse(snapshotJson);
  } catch {
    return snapshotJson;
  }
  if (!Array.isArray(snap.events)) return snapshotJson;
  const baseDir = path.dirname(snapshotPath);
  const readSidecar = (rel: string): string | null => {
    try {
      return fs.readFileSync(path.resolve(baseDir, rel), "utf-8");
    } catch {
      return null;
    }
  };
  for (const e of snap.events) {
    const a = e && e.attrs;
    if (!a || typeof a !== "object") continue;
    if (typeof a.prompt_file === "string" && !a.prompt) {
      const txt = readSidecar(a.prompt_file);
      if (txt != null) { a.prompt = txt; delete a.prompt_file; }
    }
    if (typeof a.system_prompt_file === "string" && !a.system_prompt) {
      const txt = readSidecar(a.system_prompt_file);
      if (txt != null) { a.system_prompt = txt; delete a.system_prompt_file; }
    }
  }
  return JSON.stringify(snap);
}

/**
 * Build a single-file artifact HTML from dist/index.html with the given
 * snapshot JSON inlined. Returns a file:// URL.
 */
export function buildArtifact(snapshotPath: string): string {
  ensureArtifactsDir();

  const distIndex = path.join(projectRoot, "dist", "index.html");
  if (!fs.existsSync(distIndex)) {
    throw new Error(
      `dist/index.html not found — run pnpm build first (expected at ${distIndex})`
    );
  }

  const snapshotJson = inlinePromptSidecars(
    fs.readFileSync(snapshotPath, "utf-8"),
    snapshotPath
  );

  // Inline the snapshot as a <script type="application/json"> tag.
  // Insert it just before the first <script> tag in the HTML so it's
  // available when the SPA's main.ts runs.
  let html = fs.readFileSync(distIndex, "utf-8");

  const snapshotTag = `<script type="application/json" id="kitsoki-snapshot">${snapshotJson}</script>`;

  // Find the first <script> tag and insert before it.
  const scriptTagIndex = html.indexOf("<script");
  if (scriptTagIndex === -1) {
    // Fallback: append before </body>.
    html = html.replace("</body>", `${snapshotTag}\n</body>`);
  } else {
    html = html.slice(0, scriptTagIndex) + snapshotTag + "\n" + html.slice(scriptTagIndex);
  }

  // Write to a temp file named after the snapshot.
  const baseName = path.basename(snapshotPath, ".snapshot.json");
  const outPath = path.join(artifactsDir, `${baseName}.html`);
  fs.writeFileSync(outPath, html, "utf-8");

  return `file://${outPath}`;
}
