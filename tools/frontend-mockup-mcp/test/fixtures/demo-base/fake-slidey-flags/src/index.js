#!/usr/bin/env node
// Hermetic slidey stand-in that always reports a non-empty flags list, for
// exercising demo-doctor's "estimate" check FAIL path.
import path from "node:path";
import process from "node:process";

const deckPath = process.argv[2];
const estimate = {
  spec: path.resolve(deckPath),
  scenes: [
    {
      index: 1,
      type: "video",
      title: "Scene A",
      durationSec: 5,
      narration: [{ chapter: "a", text: "State A narration.", words: 40, audioSec: 30 }],
      flags: ["narration overruns scene by 25.0s"]
    }
  ],
  flags: []
};
process.stdout.write(JSON.stringify(estimate));
process.exit(0);
