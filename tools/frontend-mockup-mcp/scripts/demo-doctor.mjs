#!/usr/bin/env node
// demo-doctor: contract §5 (~/code/POG/.context/mockup-demo-tooling-contract.md).
//
//   node scripts/demo-doctor.mjs <manifest.demo.json> [--json]
//
// Runs five checks against a demo manifest and exits 0 iff all pass.

import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import {
  loadManifest,
  readJSON,
  parseObjectLiteralTopKeys,
  extractSetStepTargets,
  deckVideoScenes,
  sceneRrwebEscapesDeckFolder,
  matchToursToScenes,
  runSlideyEstimate,
  flagsFromEstimate
} from "../lib/demo-manifest.mjs";

function pass(name, detail) {
  return { name, ok: true, detail };
}
function failCheck(name, detail) {
  return { name, ok: false, detail };
}

function checkStates(manifest) {
  const html = fs.readFileSync(manifest.mockupAbs, "utf8");
  const stateKeys = parseObjectLiteralTopKeys(html, "states");
  if (!stateKeys) {
    return failCheck("states", `could not find "const states = { ... }" in ${manifest.mockupAbs}`);
  }
  const problems = [];
  for (const tour of manifest.tours) {
    let tourJson;
    try {
      tourJson = readJSON(tour.tourAbs);
    } catch (err) {
      problems.push(`${tour.tour}: could not read tour file (${err.message})`);
      continue;
    }
    for (const { stepId, target } of extractSetStepTargets(tourJson)) {
      if (!stateKeys.includes(target)) {
        problems.push(`${tour.tour}: step "${stepId}" calls setStep('${target}'), not a states key`);
      }
    }
  }
  if (problems.length) return failCheck("states", problems.join("; "));
  return pass("states", `${stateKeys.length} state key(s) [${stateKeys.join(", ")}] validated across ${manifest.tours.length} tour(s)`);
}

function checkDeckPaths(manifest) {
  let deckJson;
  try {
    deckJson = readJSON(manifest.deckAbs);
  } catch (err) {
    return failCheck("deck paths", `could not read deck ${manifest.deckAbs} (${err.message})`);
  }
  const scenes = deckVideoScenes(deckJson);
  const problems = [];
  for (const scene of scenes) {
    if (sceneRrwebEscapesDeckFolder(manifest.deckDir, scene.rrweb)) {
      problems.push(`scene[${scene.index}] "${scene.title || ""}" rrweb path escapes deck folder: ${scene.rrweb}`);
    }
  }
  if (problems.length) return failCheck("deck paths", problems.join("; "));
  return pass("deck paths", `${scenes.length} video scene rrweb path(s) resolve inside ${manifest.deckDir}`);
}

function checkFreshness(manifest) {
  let mockupMtime;
  try {
    mockupMtime = fs.statSync(manifest.mockupAbs).mtimeMs;
  } catch (err) {
    return failCheck("freshness", `could not stat mockup ${manifest.mockupAbs} (${err.message})`);
  }
  const problems = [];
  for (const tour of manifest.tours) {
    if (!fs.existsSync(tour.outAbs)) {
      problems.push(`${tour.tour}: clip missing at ${tour.out}`);
      continue;
    }
    if (!fs.existsSync(tour.tourAbs)) {
      problems.push(`${tour.tour}: tour file missing at ${tour.tourAbs}`);
      continue;
    }
    const clipMtime = fs.statSync(tour.outAbs).mtimeMs;
    const tourMtime = fs.statSync(tour.tourAbs).mtimeMs;
    if (clipMtime < tourMtime) problems.push(`${tour.tour}: clip ${tour.out} is older than the tour file`);
    if (clipMtime < mockupMtime) problems.push(`${tour.tour}: clip ${tour.out} is older than the mockup`);
  }
  if (problems.length) return failCheck("freshness", problems.join("; "));
  return pass("freshness", `${manifest.tours.length} clip(s) newer than their tour and the mockup`);
}

function checkChapters(manifest, matches) {
  if (!matches.length) {
    return pass("chapters", "no matched scene/tour pairs to check");
  }
  const problems = [];
  for (const { tour, scene } of matches) {
    const chaptersPath = `${tour.outAbs}.chapters.json`;
    if (!fs.existsSync(chaptersPath)) {
      problems.push(`${tour.tour}: missing chapters sidecar ${chaptersPath}`);
      continue;
    }
    let chapters;
    try {
      chapters = readJSON(chaptersPath);
    } catch (err) {
      problems.push(`${tour.tour}: could not read chapters sidecar (${err.message})`);
      continue;
    }
    const chapterIds = chapters.map((c) => c.id);
    const sceneChapterIds = (scene.narration || []).map((n) => n.chapter).filter((c) => c != null);
    if (JSON.stringify(chapterIds) !== JSON.stringify(sceneChapterIds)) {
      problems.push(
        `${tour.tour}: chapters [${chapterIds.join(", ")}] != scene "${scene.title || scene.index}" narration [${sceneChapterIds.join(", ")}]`
      );
    }
  }
  if (problems.length) return failCheck("chapters", problems.join("; "));
  return pass("chapters", `${matches.length} matched scene/tour pair(s) agree on chapter order`);
}

function checkEstimate(manifest) {
  const estimate = runSlideyEstimate(manifest);
  if (!estimate.ok) {
    return failCheck("estimate", estimate.message);
  }
  const flags = flagsFromEstimate(estimate.data);
  if (flags.length) {
    return failCheck("estimate", `slidey --estimate --json reported ${flags.length} flag(s): ${flags.join("; ")}`);
  }
  return pass("estimate", `slidey --estimate --json reported zero flags across ${estimate.data.scenes.length} scene(s)`);
}

/** Run all five checks against a manifest. Never throws for check-level
 * problems (those are surfaced as a failing check); only throws if the
 * manifest itself cannot be loaded at all. */
export function runDoctor(manifestPath) {
  const manifest = loadManifest(manifestPath);

  const checks = [checkStates(manifest), checkDeckPaths(manifest), checkFreshness(manifest)];

  let matches = [];
  let matchError;
  try {
    matches = matchToursToScenes(manifest);
  } catch (err) {
    matchError = err;
  }
  if (matchError) {
    checks.push(failCheck("chapters", `could not derive tour<->scene matches: ${matchError.message}`));
  } else {
    checks.push(checkChapters(manifest, matches));
  }

  checks.push(checkEstimate(manifest));

  return { ok: checks.every((c) => c.ok), manifest: manifest.path, checks };
}

function printHuman(report) {
  for (const check of report.checks) {
    console.log(`${check.ok ? "ok  " : "FAIL"}  ${check.name}: ${check.detail}`);
  }
}

function main() {
  const args = process.argv.slice(2);
  const jsonFlag = args.includes("--json");
  const manifestArg = args.find((a) => !a.startsWith("--"));
  if (!manifestArg) {
    console.error("usage: demo-doctor.mjs <manifest.demo.json> [--json]");
    process.exit(2);
  }

  let report;
  try {
    report = runDoctor(path.resolve(manifestArg));
  } catch (err) {
    console.error(`demo-doctor: ${err.stack || err.message}`);
    process.exit(2);
  }

  if (jsonFlag) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    printHuman(report);
  }
  process.exit(report.ok ? 0 : 1);
}

if (import.meta.url === `file://${process.argv[1]}`) main();
