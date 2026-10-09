import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { runDoctor } from "../scripts/demo-doctor.mjs";
import {
  cloneFixture,
  cloneRealAppFixture,
  removeFixture,
  setFreshTimestamps,
  setFreshTimestampsRealApp,
  touch,
  manifestPath,
  readManifestJSON,
  writeManifestJSON
} from "./helpers/fixture.mjs";

// Content mutations run BEFORE the freshness baseline is stamped, so
// incidental mtime changes from writing a mutated file (e.g. a rewritten
// tour or chapters JSON) never race the baseline's "old tour/mockup, fresh
// clip" ordering.
function withFixture(mutate, fn) {
  const dir = cloneFixture();
  if (mutate) mutate(dir);
  setFreshTimestamps(dir);
  try {
    return fn(dir);
  } finally {
    removeFixture(dir);
  }
}

// Freshness-specific mutations run AFTER the baseline, since they exist to
// deliberately break the baseline's ordering.
function withFreshBaselineThenMutate(mutate, fn) {
  const dir = cloneFixture();
  setFreshTimestamps(dir);
  if (mutate) mutate(dir);
  try {
    return fn(dir);
  } finally {
    removeFixture(dir);
  }
}

function checkByName(report, name) {
  const check = report.checks.find((c) => c.name === name);
  assert.ok(check, `expected a "${name}" check in ${JSON.stringify(report.checks.map((c) => c.name))}`);
  return check;
}

test("demo-doctor: clean fixture passes all five checks", () => {
  withFixture(null, (dir) => {
    const report = runDoctor(manifestPath(dir));
    assert.equal(report.ok, true, JSON.stringify(report, null, 2));
    assert.equal(report.checks.length, 5);
    for (const check of report.checks) assert.equal(check.ok, true, `${check.name}: ${check.detail}`);
    assert.deepEqual(
      report.checks.map((c) => c.name),
      ["states", "deck paths", "freshness", "chapters", "estimate"]
    );
  });
});

test("demo-doctor: clean real-app fixture (no mockup.html) passes all five checks", () => {
  const dir = cloneRealAppFixture();
  setFreshTimestampsRealApp(dir);
  try {
    const report = runDoctor(manifestPath(dir));
    assert.equal(report.ok, true, JSON.stringify(report, null, 2));
    for (const check of report.checks) assert.equal(check.ok, true, `${check.name}: ${check.detail}`);
  } finally {
    removeFixture(dir);
  }
});

test("demo-doctor: real-app fixture fails 'states' when a tour targets an undeclared state", () => {
  const dir = cloneRealAppFixture();
  const raw = readManifestJSON(dir);
  raw.states = ["a"]; // drop "b" — tour-b.json's setStep('b') is now undeclared
  writeManifestJSON(dir, raw);
  setFreshTimestampsRealApp(dir);
  try {
    const report = runDoctor(manifestPath(dir));
    const check = checkByName(report, "states");
    assert.equal(check.ok, false);
    assert.match(check.detail, /not a states key/);
  } finally {
    removeFixture(dir);
  }
});

test("demo-doctor: real-app fixture fails 'freshness' when a clip is older than a declared source", () => {
  const dir = cloneRealAppFixture();
  setFreshTimestampsRealApp(dir);
  touch(path.join(dir, "app-source.js"), 20_000); // source edited after the clip was captured
  try {
    const report = runDoctor(manifestPath(dir));
    const check = checkByName(report, "freshness");
    assert.equal(check.ok, false);
    assert.match(check.detail, /older than declared sources/);
  } finally {
    removeFixture(dir);
  }
});

test("demo-doctor check 1 (states): FAILs when a tour's setStep target isn't a states key", () => {
  withFixture(
    (dir) => {
      const tourPath = path.join(dir, "tour-a.json");
      const tour = JSON.parse(fs.readFileSync(tourPath, "utf8"));
      tour.steps[0].before[0].eval = "window.storyboard.setStep('not-a-real-state')";
      fs.writeFileSync(tourPath, JSON.stringify(tour));
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const states = checkByName(report, "states");
      assert.equal(states.ok, false);
      assert.match(states.detail, /not-a-real-state/);
      // Only the states check should fail from this mutation.
      const others = report.checks.filter((c) => c.name !== "states");
      for (const c of others) assert.equal(c.ok, true, `${c.name}: ${c.detail}`);
    }
  );
});

test("demo-doctor check 2 (deck paths): FAILs on a literal ../ escape", () => {
  withFixture(
    (dir) => {
      const deckPath = path.join(dir, "deck.slidey.json");
      const deck = JSON.parse(fs.readFileSync(deckPath, "utf8"));
      deck.scenes[1].rrweb = "../outside/escaped.rrweb.json";
      fs.writeFileSync(deckPath, JSON.stringify(deck));
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const deckPaths = checkByName(report, "deck paths");
      assert.equal(deckPaths.ok, false);
      assert.match(deckPaths.detail, /escapes deck folder/);
    }
  );
});

test("demo-doctor check 2 (deck paths): symlink traversal through deckClipsRoot is allowed", () => {
  withFixture(null, (dir) => {
    const report = runDoctor(manifestPath(dir));
    const deckPaths = checkByName(report, "deck paths");
    assert.equal(deckPaths.ok, true, deckPaths.detail);
  });
});

test("demo-doctor check 3 (freshness): FAILs when a clip is older than its tour", () => {
  withFreshBaselineThenMutate(
    (dir) => {
      touch(path.join(dir, "clips-real", "tour-a.rrweb.json"), -100_000);
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const freshness = checkByName(report, "freshness");
      assert.equal(freshness.ok, false);
      assert.match(freshness.detail, /older than the tour file/);
    }
  );
});

test("demo-doctor check 3 (freshness): FAILs when a clip is older than the mockup", () => {
  withFreshBaselineThenMutate(
    (dir) => {
      touch(path.join(dir, "mockup.html"), 100_000);
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const freshness = checkByName(report, "freshness");
      assert.equal(freshness.ok, false);
      assert.match(freshness.detail, /older than the mockup/);
    }
  );
});

test("demo-doctor check 4 (chapters): FAILs when the sidecar order doesn't match scene narration", () => {
  withFixture(
    (dir) => {
      const chaptersPath = path.join(dir, "clips-real", "tour-a.rrweb.json.chapters.json");
      fs.writeFileSync(
        chaptersPath,
        JSON.stringify([{ index: 0, id: "not-a", label: "Wrong chapter id", start_ms: 0, end_ms: 100 }])
      );
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const chapters = checkByName(report, "chapters");
      assert.equal(chapters.ok, false);
      assert.match(chapters.detail, /not-a/);
    }
  );
});

test("demo-doctor check 4 (chapters): tours with no matched scene are skipped, not failed", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.tours.push({ tour: "tour-a.json", out: "clips-real/unmatched.rrweb.json" });
      writeManifestJSON(dir, manifest);
      fs.writeFileSync(path.join(dir, "clips-real", "unmatched.rrweb.json"), "{}");
      touch(path.join(dir, "clips-real", "unmatched.rrweb.json"), 0);
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      const chapters = checkByName(report, "chapters");
      assert.equal(chapters.ok, true, chapters.detail);
    }
  );
});

test("demo-doctor check 5 (estimate): FAILs when slidey --estimate --json reports flags", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-flags";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const estimate = checkByName(report, "estimate");
      assert.equal(estimate.ok, false);
      assert.match(estimate.detail, /flag/);
    }
  );
});

test("demo-doctor check 5 (estimate): FAILs with a clear message when --json is unsupported", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-badjson";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      const report = runDoctor(manifestPath(dir));
      assert.equal(report.ok, false);
      const estimate = checkByName(report, "estimate");
      assert.equal(estimate.ok, false);
      assert.match(estimate.detail, /slidey --estimate --json unavailable/);
    }
  );
});

test("demo-doctor --json CLI report matches the library report shape", () => {
  withFixture(null, (dir) => {
    const report = runDoctor(manifestPath(dir));
    assert.deepEqual(Object.keys(report).sort(), ["checks", "manifest", "ok"]);
  });
});
