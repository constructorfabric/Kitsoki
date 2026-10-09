import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";

import { runRecordTour } from "../scripts/record-tour.mjs";
import {
  cloneFixture,
  removeFixture,
  setFreshTimestamps,
  manifestPath,
  readManifestJSON,
  writeManifestJSON
} from "./helpers/fixture.mjs";

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

test("record-tour: happy path computes dwellOverrides, captures via the tour-set, and passes the doctor gate", () => {
  withFixture(null, (dir) => {
    const summary = runRecordTour(manifestPath(dir));

    assert.equal(summary.ok, true);
    assert.equal(summary.doctor.ok, true, JSON.stringify(summary.doctor, null, 2));

    // dwellOverrides math: audioSec 16.4 + airSec 1.5 -> 17900; the "extra"
    // step has no matching chapter so it's absent, not zeroed.
    assert.deepEqual(summary.dwellOverrides["tour-a.json"], { a: 17900 });
    assert.deepEqual(summary.dwellOverrides["tour-b.json"], { b: 11500 });

    // The generated tour-set landed next to postersDir's parent and carries
    // the same overrides plus the shared target/viewport.
    const tourSet = JSON.parse(fs.readFileSync(summary.tourSet, "utf8"));
    assert.equal(summary.tourSet, path.join(dir, "clips-real", "tour-set.generated.json"));
    assert.equal(tourSet.tours.length, 2);
    const tourA = tourSet.tours.find((t) => t.out.endsWith("tour-a.rrweb.json"));
    assert.deepEqual(tourA.dwellOverrides, { a: 17900 });
    assert.equal(tourA.format, "rrweb");
    assert.equal(tourSet.target.addr, "127.0.0.1:7799");

    // The fake capture command actually ran, once, with the generated tour-set.
    const invocations = JSON.parse(fs.readFileSync(path.join(dir, "clips-real", "capture.invocations.json"), "utf8"));
    assert.equal(invocations.tourSet, summary.tourSet);
    assert.equal(invocations.tours.length, 2);
  });
});

test("record-tour: fails loudly (throws) when the re-run estimate reports flags after capture", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-flags";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      assert.throws(() => runRecordTour(manifestPath(dir)), /flag/);
    }
  );
});

test("record-tour: propagates a demo-doctor failure after an otherwise-successful capture", () => {
  withFixture(
    (dir) => {
      // Leaves the estimate/capture loop untouched (fake-slidey-ok) but
      // breaks a doctor-only invariant, so the throw can only come from
      // step 4 (the doctor gate), not the earlier estimate/flag checks.
      const tourPath = path.join(dir, "tour-a.json");
      const tour = JSON.parse(fs.readFileSync(tourPath, "utf8"));
      tour.steps[0].before[0].eval = "window.storyboard.setStep('not-a-real-state')";
      fs.writeFileSync(tourPath, JSON.stringify(tour));
    },
    (dir) => {
      try {
        runRecordTour(manifestPath(dir));
        assert.fail("expected runRecordTour to throw");
      } catch (err) {
        assert.match(err.message, /demo-doctor failed/);
        assert.ok(err.doctorReport, "expected the thrown error to carry a doctorReport");
        assert.equal(err.doctorReport.ok, false);
        const states = err.doctorReport.checks.find((c) => c.name === "states");
        assert.match(states.detail, /not-a-real-state/);
      }
    }
  );
});

test("record-tour: fails loudly when slidey --estimate --json is unavailable", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-badjson";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      assert.throws(() => runRecordTour(manifestPath(dir)), /unavailable/);
    }
  );
});
