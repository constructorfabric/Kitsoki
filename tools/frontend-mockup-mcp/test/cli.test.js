import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { cloneFixture, removeFixture, setFreshTimestamps, manifestPath, readManifestJSON, writeManifestJSON } from "./helpers/fixture.mjs";

const TOOL_ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..");
const DOCTOR_CLI = path.join(TOOL_ROOT, "scripts", "demo-doctor.mjs");
const RECORD_TOUR_CLI = path.join(TOOL_ROOT, "scripts", "record-tour.mjs");
const CREATE_MOCKUP_CLI = path.join(TOOL_ROOT, "scripts", "create-mockup.mjs");
const MOCKUP_CREATE_FIXTURES = path.join(path.dirname(fileURLToPath(import.meta.url)), "fixtures", "mockup-create");

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

test("demo-doctor CLI: human output exits 0 with one ok/FAIL line per check on a clean fixture", () => {
  withFixture(null, (dir) => {
    const res = spawnSync(process.execPath, [DOCTOR_CLI, manifestPath(dir)], { encoding: "utf8" });
    assert.equal(res.status, 0, res.stdout + res.stderr);
    const lines = res.stdout.trim().split("\n");
    assert.equal(lines.length, 5);
    for (const line of lines) assert.match(line, /^ok {2}/);
  });
});

test("demo-doctor CLI: --json exits non-zero and emits a machine report on failure", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-flags";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      const res = spawnSync(process.execPath, [DOCTOR_CLI, manifestPath(dir), "--json"], { encoding: "utf8" });
      assert.equal(res.status, 1, res.stdout + res.stderr);
      const report = JSON.parse(res.stdout);
      assert.equal(report.ok, false);
      assert.ok(report.checks.some((c) => c.name === "estimate" && !c.ok));
    }
  );
});

test("record-tour CLI: exits 0 and prints a JSON summary on a clean fixture", () => {
  withFixture(null, (dir) => {
    const res = spawnSync(process.execPath, [RECORD_TOUR_CLI, manifestPath(dir)], { encoding: "utf8" });
    assert.equal(res.status, 0, res.stdout + res.stderr);
    const summary = JSON.parse(res.stdout);
    assert.equal(summary.ok, true);
    assert.equal(summary.doctor.ok, true);
  });
});

test("record-tour CLI: exits non-zero when the loop fails", () => {
  withFixture(
    (dir) => {
      const manifest = readManifestJSON(dir);
      manifest.slidey = "fake-slidey-badjson";
      writeManifestJSON(dir, manifest);
    },
    (dir) => {
      const res = spawnSync(process.execPath, [RECORD_TOUR_CLI, manifestPath(dir)], { encoding: "utf8" });
      assert.equal(res.status, 1);
      assert.match(res.stderr, /unavailable/);
    }
  );
});

test("record-tour CLI: streams the capture child's output through to stderr instead of swallowing it, while stdout stays pure JSON", () => {
  withFixture(null, (dir) => {
    const res = spawnSync(process.execPath, [RECORD_TOUR_CLI, manifestPath(dir)], { encoding: "utf8" });
    assert.equal(res.status, 0, res.stdout + res.stderr);
    // Progress from the nested fake-slidey capture child reached this
    // process's stderr (proving it wasn't silently discarded)...
    assert.match(res.stderr, /\[fake-slidey\] capturing .*tour-a\.json -> .*tour-a\.rrweb\.json/);
    assert.match(res.stderr, /\[fake-slidey\] capturing .*tour-b\.json -> .*tour-b\.rrweb\.json/);
    // ...while stdout is still exactly the final JSON summary, unpolluted.
    const summary = JSON.parse(res.stdout);
    assert.equal(summary.ok, true);
  });
});

test("create-mockup CLI: exits 0 and prints a JSON summary with --manifest", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "create-mockup-cli-"));
  const res = spawnSync(
    process.execPath,
    [
      CREATE_MOCKUP_CLI,
      path.join(MOCKUP_CREATE_FIXTURES, "scenario.mockup.json"),
      path.join(dir, "mockup.html"),
      "--manifest",
      "--renderer",
      path.join(MOCKUP_CREATE_FIXTURES, "renderer.stub.js")
    ],
    { encoding: "utf8" }
  );
  assert.equal(res.status, 0, res.stdout + res.stderr);
  const summary = JSON.parse(res.stdout);
  assert.equal(summary.ok, true);
  assert.ok(fs.existsSync(summary.mockup));
  assert.ok(fs.existsSync(summary.manifest));
  assert.ok(fs.existsSync(summary.deck));
});

test("create-mockup CLI: exits non-zero with a clear message on missing arguments", () => {
  const res = spawnSync(process.execPath, [CREATE_MOCKUP_CLI], { encoding: "utf8" });
  assert.equal(res.status, 2);
  assert.match(res.stderr, /usage: create-mockup\.mjs/);
});
