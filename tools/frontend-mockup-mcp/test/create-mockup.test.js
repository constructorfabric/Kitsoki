import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";
import process from "node:process";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

import { runCreateMockup } from "../scripts/create-mockup.mjs";
import { runDoctor } from "../scripts/demo-doctor.mjs";
import { runRecordTour } from "../scripts/record-tour.mjs";
import { parseObjectLiteralTopKeys } from "../lib/demo-manifest.mjs";

const FIXTURES_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), "fixtures", "mockup-create");
const SCENARIO_PATH = path.join(FIXTURES_DIR, "scenario.mockup.json");
const RENDERER_STUB = path.join(FIXTURES_DIR, "renderer.stub.js");
const FAKE_SLIDEY_DIR = path.join(FIXTURES_DIR, "fake-slidey");
const SCENARIO = JSON.parse(fs.readFileSync(SCENARIO_PATH, "utf8"));

function tempDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "create-mockup-test-"));
}

function generate(dir, opts = {}) {
  return runCreateMockup({
    scenarioPath: SCENARIO_PATH,
    outPath: path.join(dir, "mockup.html"),
    rendererPath: RENDERER_STUB,
    ...opts
  });
}

function extractScripts(html) {
  const re = /<script[^>]*>([\s\S]*?)<\/script>/g;
  const scripts = [];
  let m;
  while ((m = re.exec(html))) scripts.push(m[1]);
  return scripts;
}

/** Minimal in-memory DOM: only `getElementById(id).textContent/.innerHTML`
 * and `document.body.setAttribute(...)` -- exactly what mockup-template.mjs's
 * generated render() touches. No real browser, no jsdom dependency. */
function makeFakeDom() {
  const elements = new Map();
  function elFor(id) {
    if (!elements.has(id)) elements.set(id, { id, textContent: "", innerHTML: "" });
    return elements.get(id);
  }
  const body = { attrs: {}, setAttribute(k, v) { this.attrs[k] = v; } };
  const document = { getElementById: (id) => elFor(id), body };
  const window = { document };
  window.window = window;
  return { elements, document, window };
}

function runGeneratedScripts(html) {
  const { document, window } = makeFakeDom();
  const sandbox = { document, window, console };
  vm.createContext(sandbox);
  for (const script of extractScripts(html)) {
    vm.runInContext(script, sandbox);
  }
  return { document, window: sandbox.window };
}

test("create-mockup: generates a self-contained mockup with the portal root and all five zone testids", () => {
  const dir = tempDir();
  const summary = generate(dir);
  assert.equal(summary.ok, true);
  const html = fs.readFileSync(summary.mockup, "utf8");
  for (const testid of ["portal", "left-rail", "intake-strip", "graph-canvas", "inspector", "timeline"]) {
    assert.match(html, new RegExp(`data-testid="${testid}"`), `missing data-testid="${testid}"`);
  }
});

test("create-mockup: states block round-trips against the scenario's state keys", () => {
  const dir = tempDir();
  const summary = generate(dir);
  const html = fs.readFileSync(summary.mockup, "utf8");
  const keys = parseObjectLiteralTopKeys(html, "states");
  assert.deepEqual(keys.sort(), Object.keys(SCENARIO.states).sort());
  assert.deepEqual(summary.states.sort(), Object.keys(SCENARIO.states).sort());
});

test("create-mockup: every inline <script> is syntactically valid", () => {
  const dir = tempDir();
  const summary = generate(dir);
  const html = fs.readFileSync(summary.mockup, "utf8");
  const combined = extractScripts(html).join("\n;\n");
  const combinedPath = path.join(dir, "combined.check.js");
  fs.writeFileSync(combinedPath, combined);
  const res = spawnSync(process.execPath, ["--check", combinedPath], { encoding: "utf8" });
  assert.equal(res.status, 0, res.stderr);
});

test("create-mockup: setStep switches per-zone content (DOM-lite, no browser)", () => {
  const dir = tempDir();
  const summary = generate(dir);
  const html = fs.readFileSync(summary.mockup, "utf8");
  const { document, window } = runGeneratedScripts(html);

  // Initial render is the scenario's first declared state ("start").
  assert.equal(document.getElementById("decision-card").textContent, "Can we start?");
  assert.equal(document.getElementById("graph-title").textContent, "Graph A");
  assert.equal(document.getElementById("intake-field-0").textContent, "Decision A");
  assert.equal(document.body.attrs["data-state"], "start");
  assert.match(document.getElementById("timeline-steps").innerHTML, /Step1/);

  assert.equal(typeof window.storyboard?.setStep, "function");
  window.storyboard.setStep("done");

  assert.equal(document.getElementById("decision-card").textContent, "Done decision");
  assert.equal(document.getElementById("graph-title").textContent, "Graph C");
  assert.equal(document.getElementById("intake-field-0").textContent, "Decision C");
  assert.equal(document.body.attrs["data-state"], "done");
  assert.match(document.getElementById("timeline-steps").innerHTML, /StepC/);
  assert.match(document.getElementById("evidence-list").innerHTML, /evidence C/);
});

test("create-mockup: --manifest co-emits a manifest/tours/deck that pass demo-doctor's structural checks immediately", () => {
  const dir = tempDir();
  const summary = generate(dir, { manifest: true });
  assert.ok(summary.manifest && fs.existsSync(summary.manifest));
  assert.ok(summary.deck && fs.existsSync(summary.deck));
  assert.equal(summary.tours.length, 2, "expected one tour per state group (alpha, beta)");
  for (const tour of summary.tours) assert.ok(fs.existsSync(tour.tour));

  // No captures have run yet, so freshness/chapters/estimate aren't
  // meaningful -- but states (check 1) and deck paths (check 2) require
  // nothing but the generated files and must already pass.
  const report = runDoctor(summary.manifest);
  const byName = new Map(report.checks.map((c) => [c.name, c]));
  assert.equal(byName.get("states").ok, true, byName.get("states").detail);
  assert.equal(byName.get("deck paths").ok, true, byName.get("deck paths").detail);
});

test("create-mockup: create -> record-tour -> demo-doctor is a closed loop (hermetic fake slidey)", () => {
  const dir = tempDir();
  const summary = generate(dir, { manifest: true });

  const manifestJson = JSON.parse(fs.readFileSync(summary.manifest, "utf8"));
  manifestJson.slidey = FAKE_SLIDEY_DIR;
  fs.writeFileSync(summary.manifest, JSON.stringify(manifestJson, null, 2));

  const result = runRecordTour(summary.manifest);
  assert.equal(result.ok, true);
  assert.equal(result.doctor.ok, true, JSON.stringify(result.doctor, null, 2));
  for (const check of result.doctor.checks) assert.equal(check.ok, true, `${check.name}: ${check.detail}`);

  // demo-doctor run standalone afterwards agrees (clips are now on disk).
  const report = runDoctor(summary.manifest);
  assert.equal(report.ok, true, JSON.stringify(report, null, 2));
});

test("create-mockup: throws a clear error when a graph projection is declared but neither --renderer nor a slidey checkout is available", () => {
  const dir = tempDir();
  const prevSlideySrc = process.env.SLIDEY_SRC;
  process.env.SLIDEY_SRC = fs.mkdtempSync(path.join(os.tmpdir(), "no-slidey-here-"));
  try {
    assert.throws(
      () => runCreateMockup({ scenarioPath: SCENARIO_PATH, outPath: path.join(dir, "mockup.html") }),
      /no slidey checkout is resolvable/
    );
  } finally {
    if (prevSlideySrc === undefined) delete process.env.SLIDEY_SRC;
    else process.env.SLIDEY_SRC = prevSlideySrc;
  }
});
