/**
 * Unit tests for the guided-tour Pinia store. No DOM observation, no server, no
 * LLM — the store's job is purely position + the localStorage completion gate +
 * the trigger-matching in advanceFromEnv. The overlay (not tested here) owns all
 * route/state/DOM observation and calls back into advanceFromEnv.
 */

import { describe, it, expect, beforeEach } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useTourStore } from "../../src/stores/tour.js";
import { TOUR_STEPS } from "../../src/tour/manifest.js";

const LS_KEY = "kitsoki.tour.completed.v2";

function snapshotMode(on: boolean): void {
  const g = globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown };
  if (on) g.__KITSOKI_SNAPSHOT__ = {};
  else delete g.__KITSOKI_SNAPSHOT__;
}

/** happy-dom defaults navigator.webdriver to true; the store treats that as
 *  automation and skips auto-start. Tests drive it explicitly. */
function automated(on: boolean): void {
  Object.defineProperty(navigator, "webdriver", { value: on, configurable: true });
}

describe("tour store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    localStorage.clear();
    snapshotMode(false);
    automated(false);
  });

  it("start() activates at the first step", () => {
    const tour = useTourStore();
    expect(tour.active).toBe(false);
    tour.start();
    expect(tour.active).toBe(true);
    expect(tour.stepIndex).toBe(0);
    expect(tour.currentStep?.id).toBe(TOUR_STEPS[0].id);
    expect(tour.isFirst).toBe(true);
  });

  it("next() walks to the end then finishes + persists", () => {
    const tour = useTourStore();
    tour.start();
    for (let i = 0; i < TOUR_STEPS.length - 1; i++) tour.next();
    expect(tour.isLast).toBe(true);
    expect(tour.active).toBe(true);
    tour.next(); // off the end → finish
    expect(tour.active).toBe(false);
    expect(tour.completed).toBe(true);
    expect(localStorage.getItem(LS_KEY)).toBe("1");
  });

  it("prev() never goes below the first step", () => {
    const tour = useTourStore();
    tour.start();
    tour.prev();
    expect(tour.stepIndex).toBe(0);
    tour.next();
    tour.prev();
    expect(tour.stepIndex).toBe(0);
  });

  it("goTo() jumps to a step by id", () => {
    const tour = useTourStore();
    tour.start();
    tour.goTo("home-start");
    expect(tour.currentStep?.id).toBe("home-start");
    tour.goTo("does-not-exist");
    expect(tour.currentStep?.id).toBe("home-start"); // unchanged
  });

  it("skip() closes and persists completion", () => {
    const tour = useTourStore();
    tour.start();
    tour.skip();
    expect(tour.active).toBe(false);
    expect(tour.completed).toBe(true);
    expect(localStorage.getItem(LS_KEY)).toBe("1");
  });

  it("maybeAutoStart() starts on first login but not once completed", () => {
    const first = useTourStore();
    first.maybeAutoStart();
    expect(first.active).toBe(true);
    first.finish();

    // A fresh store instance now reads the persisted gate and stays quiet.
    setActivePinia(createPinia());
    const second = useTourStore();
    expect(second.completed).toBe(true);
    second.maybeAutoStart();
    expect(second.active).toBe(false);
  });

  it("maybeAutoStart() stays quiet for an already-onboarded project", () => {
    const tour = useTourStore();
    tour.maybeAutoStart({ projectOnboarded: true });
    expect(tour.active).toBe(false);
    expect(tour.completed).toBe(false);
  });

  it("start(true) replays even after completion", () => {
    localStorage.setItem(LS_KEY, "1");
    const tour = useTourStore();
    tour.start(); // no-op: already completed
    expect(tour.active).toBe(false);
    tour.start(true); // replay
    expect(tour.active).toBe(true);
    expect(tour.stepIndex).toBe(0);
  });

  it("automation (navigator.webdriver) suppresses auto-start but not explicit start", () => {
    automated(true);
    const tour = useTourStore();
    tour.maybeAutoStart();
    expect(tour.active).toBe(false); // never auto-pop under Playwright/Selenium
    tour.start(true); // explicit opt-in (the tour-video spec) still works
    expect(tour.active).toBe(true);
  });

  it("snapshot mode suppresses start + auto-start", () => {
    snapshotMode(true);
    const tour = useTourStore();
    tour.start();
    expect(tour.active).toBe(false);
    tour.maybeAutoStart();
    expect(tour.active).toBe(false);
  });

  it("advanceFromEnv only advances on the current step's matching trigger", () => {
    const tour = useTourStore();
    tour.start();
    // Step 0 is an 'explain' (advance: 'next') step — env triggers are ignored.
    tour.advanceFromEnv("route");
    tour.advanceFromEnv("click");
    expect(tour.stepIndex).toBe(0);

    // home-start advances on route-match.
    tour.goTo("home-start");
    const idx = tour.stepIndex;
    tour.advanceFromEnv("click"); // wrong trigger → ignored
    expect(tour.stepIndex).toBe(idx);
    tour.advanceFromEnv("route"); // correct → advance
    expect(tour.stepIndex).toBe(idx + 1);
  });
});
