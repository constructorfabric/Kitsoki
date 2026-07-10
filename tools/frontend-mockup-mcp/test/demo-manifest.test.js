import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";

import {
  loadManifest,
  resolveSlideyRoot,
  parseObjectLiteralTopKeys,
  extractSetStepTargets,
  matchToursToScenes,
  computeDwellOverrides
} from "../lib/demo-manifest.mjs";
import { cloneFixture, removeFixture, setFreshTimestamps, manifestPath } from "./helpers/fixture.mjs";

function withFixture(fn) {
  const dir = cloneFixture();
  setFreshTimestamps(dir);
  try {
    return fn(dir);
  } finally {
    removeFixture(dir);
  }
}

test("loadManifest resolves every relative path against the manifest's directory", () => {
  withFixture((dir) => {
    const manifest = loadManifest(manifestPath(dir));
    assert.equal(manifest.mockupAbs, path.join(dir, "mockup.html"));
    assert.equal(manifest.deckAbs, path.join(dir, "deck.slidey.json"));
    assert.equal(manifest.postersDirAbs, path.join(dir, "clips-real", "posters"));
    assert.equal(manifest.tours.length, 2);
    assert.equal(manifest.tours[0].tourAbs, path.join(dir, "tour-a.json"));
    assert.equal(manifest.tours[0].outAbs, path.join(dir, "clips-real", "tour-a.rrweb.json"));
    assert.equal(manifest.airSec, 1.5);
  });
});

test("loadManifest rejects an unsupported version", () => {
  withFixture((dir) => {
    const raw = JSON.parse(fs.readFileSync(manifestPath(dir), "utf8"));
    raw.version = 2;
    fs.writeFileSync(manifestPath(dir), JSON.stringify(raw));
    assert.throws(() => loadManifest(manifestPath(dir)), /unsupported demo manifest version/);
  });
});

test("resolveSlideyRoot: manifest `slidey` field wins, resolved relative to the manifest dir", () => {
  withFixture((dir) => {
    const manifest = loadManifest(manifestPath(dir));
    assert.equal(resolveSlideyRoot(manifest), path.join(dir, "fake-slidey-ok"));
  });
});

test("resolveSlideyRoot: falls back to SLIDEY_SRC env when the manifest omits `slidey`", () => {
  withFixture((dir) => {
    const raw = JSON.parse(fs.readFileSync(manifestPath(dir), "utf8"));
    delete raw.slidey;
    fs.writeFileSync(manifestPath(dir), JSON.stringify(raw));
    const manifest = loadManifest(manifestPath(dir));

    const prev = process.env.SLIDEY_SRC;
    process.env.SLIDEY_SRC = "/tmp/some-slidey-checkout";
    try {
      assert.equal(resolveSlideyRoot(manifest), "/tmp/some-slidey-checkout");
    } finally {
      if (prev === undefined) delete process.env.SLIDEY_SRC;
      else process.env.SLIDEY_SRC = prev;
    }
  });
});

test("resolveSlideyRoot: falls back to ~/code/slidey when neither manifest nor env is set", () => {
  withFixture((dir) => {
    const raw = JSON.parse(fs.readFileSync(manifestPath(dir), "utf8"));
    delete raw.slidey;
    fs.writeFileSync(manifestPath(dir), JSON.stringify(raw));
    const manifest = loadManifest(manifestPath(dir));

    const prev = process.env.SLIDEY_SRC;
    delete process.env.SLIDEY_SRC;
    try {
      assert.equal(resolveSlideyRoot(manifest), path.join(process.env.HOME || process.env.USERPROFILE, "code", "slidey"));
    } finally {
      if (prev !== undefined) process.env.SLIDEY_SRC = prev;
    }
  });
});

test("parseObjectLiteralTopKeys extracts only top-level keys of the states literal", () => {
  withFixture((dir) => {
    const html = fs.readFileSync(path.join(dir, "mockup.html"), "utf8");
    const keys = parseObjectLiteralTopKeys(html, "states");
    assert.deepEqual(keys, ["a", "b"]);
  });
});

test("parseObjectLiteralTopKeys returns null when the variable isn't declared", () => {
  const keys = parseObjectLiteralTopKeys("const somethingElse = { x: 1 };", "states");
  assert.equal(keys, null);
});

test("extractSetStepTargets pulls setStep('X') calls out of before[].eval", () => {
  const tourJson = {
    steps: [
      { id: "s1", before: [{ eval: "window.storyboard.setStep('open')" }] },
      { id: "s2", before: [{ eval: "someOtherCall()" }] },
      { id: "s3", before: [{ eval: "setStep(\"quoted-double\")" }] }
    ]
  };
  const targets = extractSetStepTargets(tourJson);
  assert.deepEqual(targets, [
    { stepId: "s1", target: "open" },
    { stepId: "s3", target: "quoted-double" }
  ]);
});

test("matchToursToScenes derives tour<->scene matches through the deckClipsRoot symlink", () => {
  withFixture((dir) => {
    const manifest = loadManifest(manifestPath(dir));
    const matches = matchToursToScenes(manifest);
    assert.equal(matches.length, 2);
    const byTour = new Map(matches.map((m) => [m.tour.tour, m.scene]));
    assert.equal(byTour.get("tour-a.json").title, "Scene A");
    assert.equal(byTour.get("tour-a.json").index, 1);
    assert.equal(byTour.get("tour-b.json").title, "Scene B");
    assert.equal(byTour.get("tour-b.json").index, 2);

    // Sanity: the symlink is really being traversed, not coincidentally
    // matching lexically -- the scene path goes through `clips/`, the tour
    // `out` goes through `clips-real/` directly.
    assert.ok(fs.lstatSync(path.join(dir, "clips")).isSymbolicLink());
  });
});

test("computeDwellOverrides: audioSec 16.4 + airSec 1.5 -> 17900ms; unmatched steps are absent", () => {
  const tourJson = {
    steps: [{ id: "a" }, { id: "extra" }]
  };
  const narration = [{ chapter: "a", audioSec: 16.4 }];
  const overrides = computeDwellOverrides(tourJson, narration, 1.5);
  assert.deepEqual(overrides, { a: 17900 });
  assert.equal("extra" in overrides, false);
});
