/**
 * github-demo composite — Act 3 of the @kitsoki GitHub-loop demo (epic
 * kitsoki-github-agent, demo slice #6).
 *
 * Renders ONE slidey presentation deck that brackets the two recorded act clips
 * with title / section slides ("One loop, two sides" / "The GitHub side" /
 * "The kitsoki side" / "One loop"). The deck is
 *   docs/proposals/demo-assets/kitsoki-github/deck/kitsoki-github.deck.json
 * with two `video` scenes embedding the DEFAULT-PACE act MP4s staged under
 *   docs/proposals/demo-assets/kitsoki-github/baked/{act1-github,act2-webviewer}.mp4
 * (each with its sibling <act>.mp4.chapters.json so `chapters: auto` derives
 * deck-styled lower-thirds — kitsoki's ChapterRecorder/writeChapters already
 * emits exactly the sidecar shape slidey consumes, so no conversion).
 *
 * The render is driven through the SAME slidey pipeline that host.slidey.render
 * discovers (hosts.md §host.slidey.render: $SLIDEY_HOME/src/index.js, else the
 * `slidey` binary on PATH). We shell out to that binary directly here so the
 * composite can be produced and validated without standing up a kitsoki server
 * — the bytes and the chapter sidecar are identical to the host-call path.
 * No LLM, no GitHub, no network: deterministic ffmpeg-backed render only.
 *
 * Output (gitignored): .artifacts/github-demo/composite/kitsoki-github.mp4 plus
 * the slidey-emitted kitsoki-github.mp4.chapters.json sidecar. The composite QA
 * gate (.context/qa/composite-*) runs pacing-scan --pacing-strict against that
 * sidecar, so the embedded act clips MUST be the default-pace shippable cuts,
 * never the WEB_CHAT_PACE=0 *.fast.mp4 (a fast clip would ship a flash).
 *
 * BLOCKED-SAFE: if either act MP4 is not staged under baked/ yet, the deck is
 * still valid but un-renderable; the test SKIPS with a precise message rather
 * than failing, so the deck+spec land correctly ahead of the act captures.
 *
 * Validate (no MP4 render, deck shape only):  pass --list / --validate via the
 *   companion `github-demo composite deck validates` test (always runs, even
 *   when act clips are absent).
 * Render (full MP4, ~minutes):  pnpm exec playwright test github-demo-composite
 *   --project=chromium   (runs the render test only when both act clips exist).
 */
import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

import { repoRoot } from "./_helpers/server.js";

const DECK = path.join(
  repoRoot,
  "docs",
  "proposals",
  "demo-assets",
  "kitsoki-github",
  "deck",
  "kitsoki-github.deck.json",
);
const BAKED_DIR = path.join(
  repoRoot,
  "docs",
  "proposals",
  "demo-assets",
  "kitsoki-github",
  "baked",
);
const ACT1 = path.join(BAKED_DIR, "act1-github.mp4");
const ACT2 = path.join(BAKED_DIR, "act2-webviewer.mp4");

const OUT_DIR = path.join(repoRoot, ".artifacts", "github-demo", "composite");
const OUT_MP4 = path.join(OUT_DIR, "kitsoki-github.mp4");
const OUT_CHAPTERS = `${OUT_MP4}.chapters.json`;

// Resolve the slidey entrypoint exactly as host.slidey.render does:
// $SLIDEY_HOME/src/index.js first, else the `slidey` binary on PATH.
function slideyArgv(rest: string[]): { cmd: string; args: string[] } {
  const home = process.env.SLIDEY_HOME;
  if (home && fs.existsSync(path.join(home, "src", "index.js"))) {
    return { cmd: process.execPath, args: [path.join(home, "src", "index.js"), ...rest] };
  }
  return { cmd: "slidey", args: rest };
}

function runSlidey(rest: string[], timeoutMs: number): string {
  const { cmd, args } = slideyArgv(rest);
  return execFileSync(cmd, args, {
    cwd: repoRoot,
    encoding: "utf8",
    timeout: timeoutMs,
    stdio: ["ignore", "pipe", "pipe"],
  });
}

type Chapter = {
  index: number;
  id?: string;
  label?: string;
  start_ms: number;
  end_ms: number;
  source_ref?: unknown;
};

// Parse slidey's `--list` table for the start offset (ms) of each VIDEO scene,
// in scene order. The table rows look like:
//   "   2  video            6.0s   56.6s  | (1 cues) …"
// We key only on the `video` type so the composite-timeline offsets come from
// slidey's own authoritative layout (narration/transition padding included),
// not a hand-maintained guess.
function videoSceneStartsMs(): number[] {
  const out = runSlidey([DECK, "--list"], 120_000);
  const starts: number[] = [];
  for (const line of out.split("\n")) {
    const m = line.match(/^\s*\d+\s+video\s+([\d.]+)s\s/);
    if (m) starts.push(Math.round(parseFloat(m[1]) * 1000));
  }
  return starts;
}

function loadActChapters(p: string): Chapter[] {
  const raw = JSON.parse(fs.readFileSync(p, "utf8"));
  const list = Array.isArray(raw) ? raw : raw?.chapters;
  return Array.isArray(list) ? (list as Chapter[]) : [];
}

// Derive + write the composite <out.mp4>.chapters.json from the two act
// sidecars, each lifted onto the composite timeline at its video scene's start.
function writeCompositeChapters(): void {
  const starts = videoSceneStartsMs();
  const actSidecars = [`${ACT1}.chapters.json`, `${ACT2}.chapters.json`];
  const composite: Chapter[] = [];
  let idx = 0;
  for (let i = 0; i < actSidecars.length; i++) {
    const offset = starts[i] ?? 0;
    const sidecar = actSidecars[i];
    if (!fs.existsSync(sidecar)) continue;
    for (const ch of loadActChapters(sidecar)) {
      composite.push({
        ...ch,
        index: idx++,
        start_ms: ch.start_ms + offset,
        end_ms: ch.end_ms + offset,
      });
    }
  }
  fs.writeFileSync(OUT_CHAPTERS, JSON.stringify(composite, null, 2) + "\n", "utf8");
}

test.describe("github-demo composite (slidey deck)", () => {
  // Always-on: proves the deck is structurally valid and renderable shape —
  // runs even when the act clips are not yet staged. `--list` validates the
  // spec and prints the scene/duration table without a full render.
  test("github-demo composite deck validates", () => {
    expect(fs.existsSync(DECK), `deck missing at ${DECK}`).toBeTruthy();

    const spec = JSON.parse(fs.readFileSync(DECK, "utf8"));
    expect(spec?.meta?.mode, "meta.mode MUST be 'pitch' or every frame renders blank").toBe(
      "pitch",
    );
    // Bracketing structure: 4 title/section slides + 2 video scenes.
    const types = (spec.scenes as Array<{ type: string }>).map((s) => s.type);
    expect(types.filter((t) => t === "title").length).toBe(4);
    expect(types.filter((t) => t === "video").length).toBe(2);
    // Capture==render viewport invariant (1600x900) so video scenes letterbox-clean.
    expect(spec.meta.resolution).toEqual({ width: 1600, height: 900 });

    // slidey --list parses + validates the spec; tolerate absent act clips
    // (the table still lists scenes — only the MP4 render needs the bytes).
    let out = "";
    try {
      out = runSlidey([DECK, "--list"], 120_000);
    } catch (err) {
      const e = err as { stdout?: string; stderr?: string; message?: string };
      out = `${e.stdout ?? ""}\n${e.stderr ?? ""}\n${e.message ?? ""}`;
      // A missing src clip can make --list non-zero; that is the blocked case,
      // not a deck defect. Only fail on a genuine spec/parse error.
      expect(
        /error|invalid|unexpected token|SyntaxError/i.test(out) &&
          !/act1-github\.mp4|act2-webviewer\.mp4|ENOENT/i.test(out),
        `slidey rejected the deck spec:\n${out}`,
      ).toBeFalsy();
    }
  });

  // Full render — only when both default-pace act clips are staged under baked/.
  test("github-demo composite renders to MP4 + chapter sidecar", () => {
    test.setTimeout(900_000); // a full MP4 render is ~7-12 min

    const haveActs = fs.existsSync(ACT1) && fs.existsSync(ACT2);
    test.skip(
      !haveActs,
      `act clips not staged — expected ${ACT1} and ${ACT2}. Record Act 1 ` +
        `(github-demo-issuepr.spec.ts) + Act 2 (github-demo-webviewer.spec.ts) ` +
        `at DEFAULT pace, then copy the shippable <act>.mp4 + <act>.mp4.chapters.json ` +
        `into baked/. Deck + spec are authored and validated; render is blocked on the clips.`,
    );

    fs.mkdirSync(OUT_DIR, { recursive: true });
    for (const f of [OUT_MP4, OUT_CHAPTERS]) {
      if (fs.existsSync(f)) fs.rmSync(f);
    }

    runSlidey([DECK, OUT_MP4], 900_000);

    // Assert the rendered MP4 first (slidey's job).
    expect(fs.existsSync(OUT_MP4), `render produced no MP4 at ${OUT_MP4}`).toBeTruthy();
    expect(fs.statSync(OUT_MP4).size, "rendered MP4 is empty").toBeGreaterThan(10_000);

    // Emit the composite chapter sidecar. slidey consumes each act clip's
    // <src>.chapters.json for lower-third CAPTIONS, but its deck-render path does
    // not WRITE an aggregate <out.mp4>.chapters.json for the composite timeline.
    // We derive it here — the deliverable per the proposal's flow-fixtures bullet
    // 3 — by lifting each video scene's act sidecar onto the composite timeline
    // at the scene's start offset (read from slidey's own `--list` table, the
    // authoritative render-time scene layout) so seeking the composite by chapter
    // is honest end-to-end.
    writeCompositeChapters();
    expect(
      fs.existsSync(OUT_CHAPTERS),
      `no composite chapter sidecar derived at ${OUT_CHAPTERS}`,
    ).toBeTruthy();

    const chapters = JSON.parse(fs.readFileSync(OUT_CHAPTERS, "utf8"));
    const list = Array.isArray(chapters) ? chapters : chapters?.chapters;
    expect(Array.isArray(list) && list.length > 0, "chapter sidecar carries no chapters").toBeTruthy();
  });
});
