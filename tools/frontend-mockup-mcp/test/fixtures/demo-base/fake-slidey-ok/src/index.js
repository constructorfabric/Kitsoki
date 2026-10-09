#!/usr/bin/env node
// Hermetic slidey stand-in for tests. Understands exactly two invocations:
//   <index.js> <deck> --estimate --json
//   <index.js> capture --tours <tour-set.json>
// The estimate output is canned to match test/fixtures/demo-base's deck
// (chapter "a" audioSec 16.4, chapter "b" audioSec 10.0 -- the same numbers
// the implementation contract's own example uses, so
// ceil((16.4 + 1.5) * 1000) === 17900 is exercised end to end).
import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const args = process.argv.slice(2);

if (args[0] === "capture" && args[1] === "--tours") {
  const tourSetPath = path.resolve(args[2]);
  const tourSet = JSON.parse(fs.readFileSync(tourSetPath, "utf8"));
  const invocations = [];
  for (const tour of tourSet.tours) {
    const outAbs = path.resolve(tour.out);
    // Progress marker: proves the record-tour progress-passthrough fix
    // actually streams this child's output through to whoever spawned
    // record-tour.mjs, instead of it being silently swallowed until exit.
    process.stdout.write(`[fake-slidey] capturing ${tour.tour} -> ${tour.out}\n`);
    fs.mkdirSync(path.dirname(outAbs), { recursive: true });
    fs.writeFileSync(outAbs, JSON.stringify({ events: [], generatedBy: "fake-slidey-ok" }));
    fs.writeFileSync(`${outAbs}.chapters.json`, JSON.stringify(chaptersFor(outAbs), null, 2));
    invocations.push({ tour: tour.tour, out: tour.out, dwellOverrides: tour.dwellOverrides || {} });
  }
  fs.writeFileSync(
    path.join(path.dirname(tourSetPath), "capture.invocations.json"),
    JSON.stringify({ tourSet: tourSetPath, target: tourSet.target, tours: invocations }, null, 2)
  );
  process.stdout.write("capture ok\n");
  process.exit(0);
}

const deckPath = args[0];
const estimate = {
  spec: path.resolve(deckPath),
  scenes: [
    { index: 0, type: "title", title: "Intro", durationSec: 3, flags: [] },
    {
      index: 1,
      type: "video",
      title: "Scene A",
      durationSec: 20,
      narration: [{ chapter: "a", text: "State A narration.", words: 24, audioSec: 16.4 }],
      flags: []
    },
    {
      index: 2,
      type: "video",
      title: "Scene B",
      durationSec: 14,
      narration: [{ chapter: "b", text: "State B narration.", words: 16, audioSec: 10.0 }],
      flags: []
    }
  ],
  flags: []
};
process.stdout.write(JSON.stringify(estimate));
process.exit(0);

function chaptersFor(outAbs) {
  const base = path.basename(outAbs);
  if (base.includes("tour-a")) return [{ index: 0, id: "a", label: "State A", start_ms: 0, end_ms: 17900 }];
  if (base.includes("tour-b")) return [{ index: 0, id: "b", label: "State B", start_ms: 0, end_ms: 11500 }];
  return [];
}
