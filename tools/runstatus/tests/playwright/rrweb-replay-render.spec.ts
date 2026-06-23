/**
 * rrweb-replay-render.spec.ts — the server-free render half.
 *
 * Second half of the rrweb demo-video method: take the event stream captured by
 * a *-rrweb-capture spec and RENDER it to an MP4 by replaying through an rrweb
 * Replayer while Playwright screen-records (renderReplayWithHolds /
 * renderReplayToMp4 in _helpers/rrweb-replay.ts).
 *
 * Defaults to the agent-actions capture. Parametrisable by env so the same spec
 * can render the diagram-showcase capture later:
 *   RRWEB_TARGET=agent-actions | diagram-showcase
 *
 * The viewport + deviceScaleFactor MUST match the capture spec's settings, or
 * the Replayer iframe is scaled / letterboxed. agent-actions captured at
 * 1600x900 (see agent-actions-rrweb-capture.spec.ts VIEWPORT). The capture
 * context did NOT set deviceScaleFactor, so it used the Playwright default of 1.
 *
 *   pnpm exec playwright test rrweb-replay-render --project=chromium
 */
import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import { repoRoot } from "./_helpers/server.js";
import { renderReplayToMp4, renderReplayWithHolds, type HoldChapter } from "./_helpers/rrweb-replay.js";

type TargetConfig = {
  /** Subdir under .artifacts/rrweb-eval/ holding <name>.rrweb.json. */
  dir: string;
  /** Base name of the events json + output mp4 stem. */
  name: string;
  viewport: { width: number; height: number };
  deviceScaleFactor: number;
};

const TARGETS: Record<string, TargetConfig> = {
  "agent-actions": {
    dir: "agent-actions",
    name: "agent-actions",
    // Matches agent-actions-rrweb-capture.spec.ts VIEWPORT; capture set no DSF
    // so the default (1) was used.
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 1,
  },
  "diagram-showcase": {
    dir: "diagram-showcase",
    name: "diagram-showcase",
    viewport: { width: 1600, height: 900 },
    deviceScaleFactor: 1,
  },
};

const targetKey = process.env.RRWEB_TARGET || "agent-actions";
const cfg = TARGETS[targetKey];
if (!cfg) {
  throw new Error(`unknown RRWEB_TARGET=${targetKey}; known: ${Object.keys(TARGETS).join(", ")}`);
}

const EVAL_DIR = path.join(repoRoot, ".artifacts", "rrweb-eval", cfg.dir);
const EVENTS_JSON = path.join(EVAL_DIR, `${cfg.name}.rrweb.json`);

// RRWEB_HOLDS=1 switches to the G2-fix render: drive the Replayer chapter by
// chapter (pause→hold→advance) so each tour step's view holds for its manifest
// dwellMs regardless of the recorder dropping frames during passive holds. The
// hold chapters (settled per-step seek points + manifest dwells) are read from
// <dir>/holds-chapters.json, a deterministic function of the capture timeline +
// the tour manifest dwellMs (built once, committed beside the capture).
const USE_HOLDS = process.env.RRWEB_HOLDS === "1";
const HOLDS_JSON = path.join(EVAL_DIR, "holds-chapters.json");

test(`rrweb replay-render → mp4 (${targetKey}${USE_HOLDS ? ", holds" : ""})`, async () => {
  test.setTimeout(600000);
  expect(fs.existsSync(EVENTS_JSON), `events json must exist: ${EVENTS_JSON}`).toBe(true);

  const result = USE_HOLDS
    ? await (async () => {
        expect(fs.existsSync(HOLDS_JSON), `holds chapters must exist: ${HOLDS_JSON}`).toBe(true);
        const chapters = JSON.parse(fs.readFileSync(HOLDS_JSON, "utf-8")) as HoldChapter[];
        return renderReplayWithHolds({
          eventsJsonPath: EVENTS_JSON,
          viewport: cfg.viewport,
          deviceScaleFactor: cfg.deviceScaleFactor,
          outDir: EVAL_DIR,
          name: `${cfg.name}-rrweb`,
          chapters,
          fps: 2,
        });
      })()
    : await renderReplayToMp4({
        eventsJsonPath: EVENTS_JSON,
        viewport: cfg.viewport,
        deviceScaleFactor: cfg.deviceScaleFactor,
        outDir: EVAL_DIR,
        name: `${cfg.name}-rrweb`,
      });

  console.log(
    `[rrweb-replay-render:${targetKey}] events=${result.eventCount} totalTimeMs=${result.totalTimeMs} mp4=${result.mp4Path} frames=${result.framesDir}`,
  );

  expect(result.mp4Path, "mp4 must be produced").toBeTruthy();
  expect(fs.statSync(result.mp4Path!).size, "mp4 must be >0 bytes").toBeGreaterThan(0);

  const frames = fs.readdirSync(result.framesDir).filter((f) => f.endsWith(".png"));
  expect(frames.length, "at least a few frames must extract").toBeGreaterThan(2);
});
