/**
 * rrweb-replay-viewport-assert.spec.ts — the viewport-match invariant guard.
 *
 * The render helpers force `transform:none` on the rrweb player wrapper to
 * defeat rrweb's fit-scale; that is ONLY clip-safe when the render viewport/DSF
 * equals the capture's, otherwise the reconstructed UI silently clips to the
 * top-left. assertViewportMatchesCapture() (in _helpers/rrweb-replay.ts) reads
 * the capture viewport/DSF that writeEvents persisted into the
 * <events>.capture.json sidecar and FAILS LOUDLY on any mismatch.
 *
 * This is a deterministic, no-browser unit spec: it constructs an events file +
 * a capture sidecar by hand, then asserts the guard throws on each axis of
 * mismatch (width / height / DSF), does NOT throw on an exact match, and is a
 * no-op when no sidecar exists (back-compat with captures written without it).
 *
 *   pnpm exec playwright test rrweb-replay-viewport-assert --project=chromium
 */
import { test, expect } from "@playwright/test";
import path from "path";
import fs from "fs";
import os from "os";
import {
  writeEvents,
  assertViewportMatchesCapture,
  captureSidecarPath,
  type RrwebEvent,
} from "./_helpers/rrweb-replay.js";

const TMP = path.join(os.tmpdir(), `rrweb-vp-assert-${process.pid}`);
const EVENTS_JSON = path.join(TMP, "fixture.rrweb.json");
const EVENTS: RrwebEvent[] = [{ type: 4 }, { type: 2 }];
const CAPTURE = { width: 1600, height: 900, deviceScaleFactor: 1 };

test.beforeAll(() => fs.mkdirSync(TMP, { recursive: true }));
test.afterAll(() => fs.rmSync(TMP, { recursive: true, force: true }));

test("viewport-match guard: throws on every axis of mismatch, passes on exact match", () => {
  // writeEvents persists the capture viewport/DSF into the sidecar.
  writeEvents(EVENTS, EVENTS_JSON, CAPTURE);
  expect(fs.existsSync(captureSidecarPath(EVENTS_JSON)), "sidecar must be written").toBe(true);

  // Exact match: no throw.
  expect(() => assertViewportMatchesCapture(EVENTS_JSON, { ...CAPTURE })).not.toThrow();

  // Width mismatch: throws, and the message names the mismatch + the invariant.
  expect(() =>
    assertViewportMatchesCapture(EVENTS_JSON, { ...CAPTURE, width: 1280 }),
  ).toThrow(/width capture=1600 render=1280/);
  expect(() =>
    assertViewportMatchesCapture(EVENTS_JSON, { ...CAPTURE, width: 1280 }),
  ).toThrow(/transform:none/);

  // Height mismatch: throws.
  expect(() =>
    assertViewportMatchesCapture(EVENTS_JSON, { ...CAPTURE, height: 800 }),
  ).toThrow(/height capture=900 render=800/);

  // DSF mismatch: throws.
  expect(() =>
    assertViewportMatchesCapture(EVENTS_JSON, { ...CAPTURE, deviceScaleFactor: 2 }),
  ).toThrow(/deviceScaleFactor capture=1 render=2/);
});

test("viewport-match guard: no sidecar → no-op (back-compat)", () => {
  const noSidecar = path.join(TMP, "no-sidecar.rrweb.json");
  fs.writeFileSync(noSidecar, JSON.stringify(EVENTS));
  // No sidecar written → nothing to compare → must not throw even on a wildly
  // different render viewport.
  expect(() =>
    assertViewportMatchesCapture(noSidecar, { width: 1, height: 1, deviceScaleFactor: 4 }),
  ).not.toThrow();
});
