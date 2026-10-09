// Shared demo-manifest primitives used by both demo-doctor.mjs and
// record-tour.mjs (and exposed to server.mjs's MCP tool wrappers).
//
// Implements contract §4 (manifest resolution) plus the tour<->scene
// derivation and slidey-shellout helpers both scripts need. See
// ~/code/POG/.context/mockup-demo-tooling-contract.md for the frozen
// interface this implements.

import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

export function readJSON(absPath) {
  return JSON.parse(fs.readFileSync(absPath, "utf8"));
}

function expandHome(value) {
  if (typeof value !== "string") return value;
  if (value === "~") return os.homedir();
  if (value.startsWith("~/")) return path.join(os.homedir(), value.slice(2));
  return value;
}

/**
 * Resolve as much of `p` as physically exists via fs.realpath, then append
 * any remaining (not-yet-created) trailing segments lexically. This lets us
 * compare "where does this asset really live" for paths that may not exist
 * yet (e.g. a tour's `out` clip before the first capture) the same way we
 * compare paths that do exist (a deck scene's rrweb path, reached through a
 * symlink).
 */
export function softRealpath(p) {
  try {
    return fs.realpathSync(p);
  } catch {
    const parent = path.dirname(p);
    if (parent === p) return p;
    return path.join(softRealpath(parent), path.basename(p));
  }
}

/**
 * Load a *.demo.json manifest (contract §4), resolving every relative path
 * against the manifest file's directory.
 */
export function loadManifest(manifestPath) {
  const absManifestPath = path.resolve(manifestPath);
  const manifestDir = path.dirname(absManifestPath);
  const raw = readJSON(absManifestPath);
  if (raw.version !== 1) {
    throw new Error(`unsupported demo manifest version: ${JSON.stringify(raw.version)} (expected 1)`);
  }
  if (!raw.mockup) throw new Error("manifest missing required field: mockup");
  if (!raw.deck) throw new Error("manifest missing required field: deck");

  const mockupAbs = path.resolve(manifestDir, raw.mockup);
  const deckAbs = path.resolve(manifestDir, raw.deck);
  const deckDir = path.dirname(deckAbs);
  const deckClipsRootAbs = raw.deckClipsRoot ? path.resolve(deckDir, raw.deckClipsRoot) : undefined;
  const postersDirAbs = raw.postersDir ? path.resolve(manifestDir, raw.postersDir) : undefined;

  const tours = (raw.tours || []).map((t) => ({
    tour: t.tour,
    out: t.out,
    tourAbs: path.resolve(manifestDir, t.tour),
    outAbs: path.resolve(manifestDir, t.out)
  }));

  return {
    raw,
    path: absManifestPath,
    dir: manifestDir,
    mockupAbs,
    deckAbs,
    deckDir,
    deckClipsRootAbs,
    postersDirAbs,
    tours,
    airSec: typeof raw.airSec === "number" ? raw.airSec : 0,
    target: raw.target,
    viewport: raw.viewport
  };
}

/**
 * slidey path resolution per contract §4: manifest `slidey` field (~
 * expanded), then SLIDEY_SRC env, then ~/code/slidey. A relative value
 * (after ~ expansion) resolves against the manifest's directory.
 */
export function resolveSlideyRoot(manifest) {
  let candidate = manifest.raw.slidey || process.env.SLIDEY_SRC || "~/code/slidey";
  candidate = expandHome(candidate);
  if (!path.isAbsolute(candidate)) candidate = path.resolve(manifest.dir, candidate);
  return candidate;
}

export function slideyIndexPath(manifest) {
  return path.join(resolveSlideyRoot(manifest), "src", "index.js");
}

/**
 * Pragmatically extract the top-level keys of a `const <varName> = { ... }`
 * object literal from raw JS/HTML source. Not a real JS parser: tracks
 * bracket depth and string/comment literals so nested objects/arrays don't
 * pollute the top-level key set, per the convention documented in contract
 * §5 check 1 (mockup must declare `const states = { key: {...}, ... }`).
 */
export function parseObjectLiteralTopKeys(source, varName) {
  const declRe = new RegExp(`(?:const|let|var)\\s+${varName}\\s*=\\s*\\{`);
  const m = declRe.exec(source);
  if (!m) return null;

  const n = source.length;
  let i = m.index + m[0].length;
  let depth = 1;
  let expectKey = true;
  const keys = [];

  while (i < n && depth > 0) {
    const ch = source[i];

    if (ch === '"' || ch === "'" || ch === "`") {
      const quote = ch;
      const start = i;
      i += 1;
      let str = "";
      while (i < n && source[i] !== quote) {
        if (source[i] === "\\") {
          str += source[i + 1];
          i += 2;
          continue;
        }
        str += source[i];
        i += 1;
      }
      i += 1; // closing quote
      if (depth === 1 && expectKey) {
        keys.push(str);
        expectKey = false;
      }
      void start;
      continue;
    }

    if (ch === "/" && source[i + 1] === "/") {
      while (i < n && source[i] !== "\n") i += 1;
      continue;
    }
    if (ch === "/" && source[i + 1] === "*") {
      i += 2;
      while (i < n && !(source[i] === "*" && source[i + 1] === "/")) i += 1;
      i += 2;
      continue;
    }

    if (ch === "{" || ch === "[" || ch === "(") {
      depth += 1;
      i += 1;
      continue;
    }
    if (ch === "}" || ch === "]" || ch === ")") {
      depth -= 1;
      i += 1;
      continue;
    }
    if (ch === "," && depth === 1) {
      expectKey = true;
      i += 1;
      continue;
    }

    if (depth === 1 && expectKey && /[A-Za-z_$]/.test(ch)) {
      let j = i;
      while (j < n && /[A-Za-z0-9_$]/.test(source[j])) j += 1;
      const ident = source.slice(i, j);
      let k = j;
      while (k < n && /\s/.test(source[k])) k += 1;
      if (source[k] === ":") {
        keys.push(ident);
        expectKey = false;
      }
      i = j;
      continue;
    }

    i += 1;
  }

  return keys;
}

/**
 * Every `window.storyboard.setStep('X')` (or bare `setStep('X')`) call found
 * in a tour's `steps[].before[].eval` strings, per contract §5 check 1.
 */
export function extractSetStepTargets(tourJson) {
  const re = /setStep\(\s*['"]([^'"]+)['"]\s*\)/g;
  const results = [];
  for (const step of tourJson.steps || []) {
    for (const before of step.before || []) {
      if (typeof before?.eval !== "string") continue;
      re.lastIndex = 0;
      let match;
      while ((match = re.exec(before.eval))) {
        results.push({ stepId: step.id, target: match[1] });
      }
    }
  }
  return results;
}

/**
 * Deck video scenes (contract §5 check 2 domain), each annotated with its
 * position in the full `scenes` array (matches slidey's --estimate --json
 * `index` field).
 */
export function deckVideoScenes(deckJson) {
  return (deckJson.scenes || [])
    .map((scene, index) => ({ ...scene, index }))
    .filter((scene) => scene.type === "video" && scene.rrweb);
}

/**
 * Lexical containment check for contract §5 check 2: does the scene's rrweb
 * path stay inside the deck's folder once normalized (WITHOUT following
 * symlinks)? A path that only escapes via a symlink component still passes
 * ("symlink traversal allowed"); a literal `../` escape fails.
 */
export function sceneRrwebEscapesDeckFolder(deckDir, rawPath) {
  const resolved = path.resolve(deckDir, rawPath);
  const rel = path.relative(deckDir, resolved);
  return rel.startsWith("..") || path.isAbsolute(rel);
}

/**
 * Derive tour<->scene matches: a deck scene matches a tour when the scene's
 * rrweb path (resolved via the deck's folder, following symlinks) realpaths
 * to the tour's `out` (contract §4). Tours or scenes with no counterpart are
 * simply absent from the result.
 */
export function matchToursToScenes(manifest) {
  const deckJson = readJSON(manifest.deckAbs);
  const scenes = deckVideoScenes(deckJson);
  const sceneReal = scenes.map((scene) => ({
    scene,
    real: softRealpath(path.resolve(manifest.deckDir, scene.rrweb))
  }));

  const matches = [];
  for (const tour of manifest.tours) {
    const tourReal = softRealpath(tour.outAbs);
    const found = sceneReal.find((entry) => entry.real === tourReal);
    if (found) matches.push({ tour, scene: found.scene });
  }
  return matches;
}

/**
 * dwellMs = ceil((audioSec + airSec) * 1000) for every tour step whose id
 * matches a chapter id in `narration` (the ESTIMATE OUTPUT's narration,
 * which carries audioSec — the deck file's own narration entries do not).
 * Steps without a matching cue are simply absent from the result (contract
 * §6 step 1: "steps without a matching cue keep their authored dwell").
 */
export function computeDwellOverrides(tourJson, narration, airSec) {
  const byChapter = new Map((narration || []).filter((n) => n.chapter != null).map((n) => [n.chapter, n]));
  const overrides = {};
  for (const step of tourJson.steps || []) {
    const cue = byChapter.get(step.id);
    if (cue && typeof cue.audioSec === "number") {
      overrides[step.id] = Math.ceil((cue.audioSec + airSec) * 1000);
    }
  }
  return overrides;
}

function collectFlags(estimateData) {
  const flags = [...(estimateData.flags || [])];
  for (const scene of estimateData.scenes || []) flags.push(...(scene.flags || []));
  return flags;
}

export function flagsFromEstimate(estimateData) {
  return collectFlags(estimateData);
}

/**
 * Run `node <slidey>/src/index.js <deck> --estimate --json` and parse its
 * output. Any failure to run, non-zero exit, or output that doesn't parse
 * as the documented shape is reported as `unavailable` (contract §5 check
 * 5: "if that slidey invocation errors because --json is unsupported ...
 * report check 5 as FAIL with a clear message").
 */
export function runSlideyEstimate(manifest) {
  const indexJs = slideyIndexPath(manifest);
  const res = spawnSync(process.execPath, [indexJs, manifest.deckAbs, "--estimate", "--json"], {
    cwd: manifest.dir,
    encoding: "utf8"
  });
  if (res.error) {
    return { ok: false, unavailable: true, message: `slidey --estimate --json unavailable: ${res.error.message}` };
  }
  if (res.status !== 0) {
    const detail = (res.stderr || res.stdout || "").trim().slice(0, 400);
    return {
      ok: false,
      unavailable: true,
      message: `slidey --estimate --json unavailable: exit ${res.status}${detail ? `: ${detail}` : ""}`
    };
  }
  let data;
  try {
    data = JSON.parse(res.stdout);
  } catch (err) {
    return {
      ok: false,
      unavailable: true,
      message: `slidey --estimate --json unavailable: could not parse JSON output (${err.message})`
    };
  }
  if (!data || !Array.isArray(data.scenes) || !Array.isArray(data.flags)) {
    return { ok: false, unavailable: true, message: "slidey --estimate --json unavailable: unexpected output shape" };
  }
  return { ok: true, data };
}

/**
 * Run `node <slidey>/src/index.js capture --tours <tourSetPath>`. Throws on
 * any failure; callers decide how to surface that.
 *
 * A real multi-tour capture can run for minutes (browser launch + one
 * capture per tour). Earlier versions used `spawnSync(..., {encoding:
 * "utf8"})`, which buffers the child's entire stdout/stderr and hands it
 * back only after the process exits -- so a healthy long-running capture
 * and a silently hung one looked IDENTICAL to whoever was watching
 * (nothing printed either way) until the final summary or a timeout. This
 * streams the child's stdout AND stderr straight through to THIS process's
 * stderr (fd 2, not fd 1) as they're written: `stdio: ["ignore", 2, 2]`
 * duplicates the current process's real stderr fd for both of the child's
 * output streams, so a caller watching stderr sees live progress, while
 * this process's own stdout stays reserved for the final JSON summary
 * (record-tour.mjs's `console.log(JSON.stringify(summary))`) -- callers
 * that spawn record-tour.mjs as a subprocess can keep parsing stdout as
 * pure JSON even though the nested slidey capture output flowed through.
 */
export function runSlideyCapture(manifest, tourSetPath) {
  const indexJs = slideyIndexPath(manifest);
  const res = spawnSync(process.execPath, [indexJs, "capture", "--tours", tourSetPath], {
    cwd: manifest.dir,
    stdio: ["ignore", 2, 2]
  });
  if (res.error || res.status !== 0) {
    throw new Error(
      `slidey capture --tours failed: ${res.error?.message || `exit ${res.status}`} ` +
        `(see the streamed capture output on stderr above for detail)`
    );
  }
  return { status: res.status ?? 0 };
}
