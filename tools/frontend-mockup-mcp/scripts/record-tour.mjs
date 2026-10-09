#!/usr/bin/env node
// record-tour: contract §6 (~/code/POG/.context/mockup-demo-tooling-contract.md).
//
//   node scripts/record-tour.mjs <manifest.demo.json>
//
// The closed loop:
//   1. per-cue estimate -> dwellOverrides for tour steps matching a chapter
//      of their matched deck scene.
//   2. generate a tour-set (contract §2) and run `slidey capture --tours`.
//   3. re-run estimate --json; fail loudly on any flag.
//   4. run demo-doctor; propagate its failure.

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import {
  loadManifest,
  readJSON,
  matchToursToScenes,
  computeDwellOverrides,
  runSlideyEstimate,
  runSlideyCapture,
  flagsFromEstimate
} from "../lib/demo-manifest.mjs";
import { runDoctor } from "./demo-doctor.mjs";

/** Directory the generated tour-set (and, by convention, the clips it
 * captures into) lives in: the parent of postersDir, or the manifest's own
 * directory when no postersDir is configured. */
function clipsDirFor(manifest) {
  return manifest.postersDirAbs ? path.dirname(manifest.postersDirAbs) : manifest.dir;
}

function buildTourSet(manifest, dwellByTour) {
  const tours = manifest.tours.map((tour) => {
    const tourJson = readJSON(tour.tourAbs);
    const dwellOverrides = dwellByTour[tour.tour] || {};
    return {
      tour: tour.tourAbs,
      out: tour.outAbs,
      format: "rrweb",
      pace: typeof tourJson.pace === "number" ? tourJson.pace : 1,
      ...(manifest.postersDirAbs ? { postersDir: manifest.postersDirAbs } : {}),
      ...(Object.keys(dwellOverrides).length ? { dwellOverrides } : {})
    };
  });
  return {
    target: manifest.target,
    ...(manifest.viewport ? { viewport: manifest.viewport } : {}),
    ...(manifest.raw.deviceScaleFactor ? { deviceScaleFactor: manifest.raw.deviceScaleFactor } : {}),
    tours
  };
}

/** Run the full record-tour closed loop. Throws on any step failure; the
 * thrown Error carries a `.doctorReport` when the failure is a doctor gate
 * failure so callers (CLI/MCP) can surface the structured report. */
export function runRecordTour(manifestPath) {
  const manifest = loadManifest(manifestPath);

  // 1. Initial estimate -> per-cue audioSec -> dwellOverrides.
  const initial = runSlideyEstimate(manifest);
  if (!initial.ok) {
    throw new Error(`record-tour: ${initial.message}`);
  }
  const matches = matchToursToScenes(manifest);
  const estimateSceneByIndex = new Map((initial.data.scenes || []).map((s) => [s.index, s]));

  const dwellByTour = {};
  for (const { tour, scene } of matches) {
    const estimateScene = estimateSceneByIndex.get(scene.index);
    if (!estimateScene) continue;
    const tourJson = readJSON(tour.tourAbs);
    dwellByTour[tour.tour] = computeDwellOverrides(tourJson, estimateScene.narration, manifest.airSec);
  }

  // 2. Generate the tour-set and capture.
  const clipsDir = clipsDirFor(manifest);
  fs.mkdirSync(clipsDir, { recursive: true });
  const tourSetPath = path.join(clipsDir, "tour-set.generated.json");
  const tourSet = buildTourSet(manifest, dwellByTour);
  fs.writeFileSync(tourSetPath, `${JSON.stringify(tourSet, null, 2)}\n`);

  runSlideyCapture(manifest, tourSetPath);

  // 3. Re-estimate; fail loudly on any flag.
  const final = runSlideyEstimate(manifest);
  if (!final.ok) {
    throw new Error(`record-tour: ${final.message}`);
  }
  const flags = flagsFromEstimate(final.data);
  if (flags.length) {
    throw new Error(`record-tour: slidey --estimate --json reported ${flags.length} flag(s) after capture: ${flags.join("; ")}`);
  }

  // 4. demo-doctor gate.
  const doctorReport = runDoctor(manifestPath);
  if (!doctorReport.ok) {
    const err = new Error("record-tour: demo-doctor failed after capture");
    err.doctorReport = doctorReport;
    throw err;
  }

  return {
    ok: true,
    manifest: manifest.path,
    tourSet: tourSetPath,
    dwellOverrides: dwellByTour,
    finalEstimate: { scenes: final.data.scenes.length, flags: 0 },
    doctor: doctorReport
  };
}

function main() {
  const manifestArg = process.argv[2];
  if (!manifestArg) {
    console.error("usage: record-tour.mjs <manifest.demo.json>");
    process.exit(2);
  }
  try {
    const summary = runRecordTour(path.resolve(manifestArg));
    console.log(JSON.stringify(summary, null, 2));
    process.exit(0);
  } catch (err) {
    if (err.doctorReport) {
      console.error(JSON.stringify(err.doctorReport, null, 2));
    } else {
      console.error(`record-tour: ${err.stack || err.message}`);
    }
    process.exit(1);
  }
}

if (import.meta.url === `file://${process.argv[1]}`) main();
