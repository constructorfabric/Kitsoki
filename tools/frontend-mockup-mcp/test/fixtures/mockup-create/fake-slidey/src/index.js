#!/usr/bin/env node
// Generic hermetic slidey stand-in for create-mockup.mjs's own test suite.
// Unlike test/fixtures/demo-base's fake-slidey-* fixtures (which hardcode
// chapter ids "a"/"b" to match ONE specific deck), this one reads whatever
// REAL deck/tour files create-mockup.mjs generated and echoes their own
// chapter/step ids back, so it can drive demo-doctor/record-tour
// hermetically against ANY scenario spec this suite throws at it -- proving
// create -> record-tour -> demo-doctor is a genuinely closed loop, not one
// wired to a fixed fixture shape.
import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const args = process.argv.slice(2);

if (args[0] === "capture" && args[1] === "--tours") {
  const tourSetPath = path.resolve(args[2]);
  const tourSet = JSON.parse(fs.readFileSync(tourSetPath, "utf8"));
  for (const tour of tourSet.tours) {
    const tourJson = JSON.parse(fs.readFileSync(path.resolve(tour.tour), "utf8"));
    const outAbs = path.resolve(tour.out);
    process.stdout.write(`[fake-slidey] capturing ${path.basename(tour.tour)} -> ${path.basename(outAbs)}\n`);
    fs.mkdirSync(path.dirname(outAbs), { recursive: true });
    fs.writeFileSync(outAbs, JSON.stringify({ events: [], generatedBy: "fake-slidey-mockup-create" }));
    const chapters = (tourJson.steps || []).map((step, index) => ({
      index,
      id: step.id,
      label: step.label || step.id,
      start_ms: index * 1000,
      end_ms: (index + 1) * 1000
    }));
    fs.writeFileSync(`${outAbs}.chapters.json`, JSON.stringify(chapters, null, 2));
  }
  process.stdout.write("capture ok\n");
  process.exit(0);
}

const deckPath = args[0];
const deck = JSON.parse(fs.readFileSync(path.resolve(deckPath), "utf8"));
const scenes = (deck.scenes || []).map((scene, index) => {
  if (scene.type !== "video") {
    return { index, type: scene.type, title: scene.title || "", durationSec: 3, flags: [] };
  }
  const narrationIn = Array.isArray(scene.narration) ? scene.narration : [];
  return {
    index,
    type: "video",
    title: scene.title || "",
    durationSec: Math.max(1, narrationIn.length) * 5,
    narration: narrationIn.map((n) => ({ chapter: n.chapter, text: n.text, words: 10, audioSec: 4.0 })),
    flags: []
  };
});
process.stdout.write(JSON.stringify({ spec: path.resolve(deckPath), scenes, flags: [] }));
process.exit(0);
