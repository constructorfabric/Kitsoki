import { defineStore } from "pinia";
import { computed, ref } from "vue";
import { TOUR_STEPS, type TourStep } from "../tour/manifest.js";

// Re-export TourStep so consumers (e.g. TourOverlay) can import from one place.
export type { TourStep };

/**
 * The guided-onboarding tour store. App-global (mounted once in App.vue), so its
 * position survives Vue Router navigation — that is what lets one tour walk from
 * the home screen into a live session and back.
 *
 * Like the meta store, it stays render-free: it never touches the route, the run
 * store, or the DOM. The overlay (src/components/tour/TourOverlay.vue) owns all
 * of that observation and calls back into `advanceFromEnv` when a route / state /
 * click matches the current step's trigger. That keeps this store trivially
 * unit-testable against the manifest alone (see tests/unit/tour-store.test.ts).
 *
 * The step content itself lives in the shared, Vue-free manifest
 * (src/tour/manifest.ts) so the Playwright video demo can import the same steps.
 */

/** localStorage gate. Bump the suffix to re-surface the tour after a redesign. */
const LS_KEY = "kitsoki.tour.completed.v2";

/** Snapshot/artifact mode has no live server — the tour can't drive anything. */
function isSnapshot(): boolean {
  return (
    (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
      .__KITSOKI_SNAPSHOT__ !== undefined
  );
}

/**
 * Under browser automation (Playwright/Selenium) the tour must never auto-pop:
 * its popover can still intercept the clicks other UI specs make. The
 * tour-video spec opts in explicitly via window.__startTour / start(true).
 */
function isAutomated(): boolean {
  return typeof navigator !== "undefined" && navigator.webdriver === true;
}

function readCompleted(): boolean {
  try {
    return localStorage.getItem(LS_KEY) === "1";
  } catch {
    return false; // localStorage can throw (private mode); treat as not-done.
  }
}

function writeCompleted(): void {
  try {
    localStorage.setItem(LS_KEY, "1");
  } catch {
    /* best-effort — a non-persisted gate just re-offers the tour next load. */
  }
}

export const useTourStore = defineStore("tour", () => {
  // ---- state ----
  const active = ref(false);
  const stepIndex = ref(0);
  // Seeded from localStorage: true once the user has finished or skipped once.
  const completed = ref<boolean>(readCompleted());
  // Bumped by the overlay whenever the DOM / route / state changes, so the
  // overlay's own anchoring computeds re-run. The store doesn't read it; it is
  // a shared reactive nudge owned here so it survives navigation.
  const envTick = ref(0);

  // Active step array — defaults to the onboarding manifest but can be replaced
  // by startWithSteps() for a dedicated feature-spotlight tour (e.g. the
  // trace-introspection video spec).
  const steps = ref<readonly TourStep[]>(TOUR_STEPS);

  // ---- getters ----
  const currentStep = computed<TourStep | undefined>(
    () => steps.value[stepIndex.value]
  );
  const isFirst = computed(() => stepIndex.value === 0);
  const isLast = computed(() => stepIndex.value === steps.value.length - 1);

  // ---- actions ----

  /** Start the tour from the top. `replay` re-runs it even once completed. */
  function start(replay = false): void {
    if (isSnapshot()) return;
    if (completed.value && !replay) return;
    stepIndex.value = 0;
    active.value = true;
  }

  /** First-login auto-start: skip if completed, in snapshot, under automation, or in an already-onboarded project. */
  function maybeAutoStart(opts: { projectOnboarded?: boolean } = {}): void {
    if (opts.projectOnboarded || completed.value || isSnapshot() || isAutomated()) return;
    start();
  }

  function next(): void {
    if (isLast.value) {
      finish();
      return;
    }
    stepIndex.value += 1;
  }

  function prev(): void {
    if (!isFirst.value) stepIndex.value -= 1;
  }

  /** Jump to a step by id (no-op if unknown). */
  function goTo(id: string): void {
    const i = steps.value.findIndex((s) => s.id === id);
    if (i >= 0) stepIndex.value = i;
  }

  /**
   * Start a feature-spotlight tour with a custom step array. Used by the
   * trace-features video spec via window.__startTourWithSteps. The default
   * onboarding manifest (TOUR_STEPS) is restored automatically when the overlay
   * finishes or is skipped.
   */
  function startWithSteps(customSteps: readonly TourStep[], replay = false): void {
    // In snapshot mode the auto-start / "?"-replay flows stay suppressed (a
    // shared static artifact shouldn't pop a tour at a reader), but an EXPLICIT
    // programmatic kickoff (replay=true, e.g. the trace-introspection video
    // spec's window.__startTourWithSteps) is allowed: it's a deliberate driver,
    // not an unprompted onboarding nag.
    if (isSnapshot() && !replay) return;
    if (completed.value && !replay) return;
    steps.value = customSteps;
    stepIndex.value = 0;
    active.value = true;
  }

  /** Reached the end — close and remember. */
  function finish(): void {
    active.value = false;
    completed.value = true;
    writeCompleted();
    // Restore the default manifest so a subsequent replay() call from the "?"
    // button always returns the onboarding tour, not a feature-spotlight tour.
    steps.value = TOUR_STEPS;
  }

  /** Dismiss early — same persistence as finishing (don't nag). */
  function skip(): void {
    finish();
  }

  /**
   * Called by the overlay when an environment change matches the current step's
   * advance trigger. Ignores mismatches so a stray route/click for an 'explain'
   * (next-driven) step never skips it.
   */
  function advanceFromEnv(reason: "route" | "click"): void {
    const s = currentStep.value;
    if (!active.value || !s) return;
    const matches =
      (reason === "route" && s.advance === "route-match") ||
      (reason === "click" && s.advance === "click-target");
    if (matches) next();
  }

  /** Overlay hook: nudge anchoring computeds after a DOM/route/state change. */
  function noteEnvChange(): void {
    envTick.value += 1;
  }

  return {
    // state
    active,
    stepIndex,
    completed,
    envTick,
    steps,
    // getters
    currentStep,
    isFirst,
    isLast,
    // actions
    start,
    startWithSteps,
    maybeAutoStart,
    next,
    prev,
    goTo,
    finish,
    skip,
    advanceFromEnv,
    noteEnvChange,
  };
});
