<template>
  <Teleport to="body">
    <div v-if="tour.active && tour.currentStep" class="tour" data-testid="tour-overlay">
      <!-- Holding state: the step's target/state isn't ready yet (mid route
           transition, or waiting on a turn to settle). Deliberately NON-blocking
           — a small pill with Skip, no backdrop — so the tour can never freeze
           the UI. A watchdog auto-skips a step that never anchors. -->
      <div v-if="!ready" class="tour__prep" data-testid="tour-loading">
        <span class="tour__prep-spinner">↻</span>
        <span>Setting up the next step…</span>
        <button
          class="tour__btn tour__btn--ghost"
          data-testid="tour-skip"
          @click="tour.skip()"
        >Skip tour</button>
      </div>

      <template v-else>
        <!-- Spotlight: keep the page fully visible. The ring highlights the
             target without dimming or covering the rest of the UI. Anchorless
             narration is just the popover. -->
        <template v-if="hole">
          <div class="tour__ring" :style="ringStyle"></div>
        </template>

        <!-- Popover -->
        <div
          ref="popoverEl"
          class="tour__popover"
          data-testid="tour-popover"
          :style="popStyle"
        >
          <div class="tour__progress">Step {{ tour.stepIndex + 1 }} of {{ total }}</div>
          <h3 class="tour__title" data-testid="tour-title">{{ tour.currentStep.title }}</h3>
          <p class="tour__body" data-testid="tour-body">{{ tour.currentStep.body }}</p>

          <p v-if="tour.currentStep.kind === 'action'" class="tour__cue">
            ↳ Click the highlighted control to continue.
          </p>

          <div class="tour__footer">
            <button
              class="tour__btn tour__btn--ghost"
              data-testid="tour-skip"
              @click="tour.skip()"
            >Skip tour</button>
            <span class="tour__spacer"></span>
            <button
              class="tour__btn tour__btn--ghost"
              data-testid="tour-back"
              :disabled="tour.isFirst"
              @click="tour.prev()"
            >Back</button>
            <button
              v-if="tour.currentStep.kind === 'explain'"
              class="tour__btn tour__btn--primary"
              data-testid="tour-next"
              @click="tour.next()"
            >{{ tour.isLast ? "Done" : "Next" }}</button>
          </div>
        </div>
      </template>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { useTourStore } from "../../stores/tour.js";
import { type TourRoute, type TourStep } from "../../tour/manifest.js";

const tour = useTourStore();
const route = useRoute();

const total = computed(() => tour.steps.length);

// ── Anchoring state ──────────────────────────────────────────────────────────
interface Rect { top: number; left: number; width: number; height: number }
const anchorRect = ref<Rect | null>(null);
const targetEl = ref<HTMLElement | null>(null);
const popoverEl = ref<HTMLElement | null>(null);
const popStyle = ref<Record<string, string>>({});

/** Map the current hash route to a TourRoute kind. */
const currentRouteKind = computed<TourRoute>(() => {
  const p = route.path;
  if (p === "/") return "home";
  if (p.endsWith("/chat")) return "interactive";
  return "any";
});

function routeKindMatches(want: TourRoute | undefined): boolean {
  if (!want) return false;
  return want === currentRouteKind.value;
}

function selOf(testid: string): string {
  return `[data-testid="${testid}"]`;
}

/** Is the current step ready to render (route + waitFor gates satisfied)? */
const ready = computed<boolean>(() => {
  // eslint-disable-next-line @typescript-eslint/no-unused-expressions
  tour.envTick; // reactive dep: re-evaluate DOM presence on env changes
  const s = tour.currentStep;
  if (!s) return false;
  if (!(s.route === "any" || s.route === currentRouteKind.value)) return false;
  if (s.waitForTarget && !document.querySelector(selOf(s.waitForTarget))) return false;
  return true;
});

function resolveTargetEl(s: TourStep): HTMLElement | null {
  if (!s.target) return null;
  const all = Array.from(
    document.querySelectorAll<HTMLElement>(selOf(s.target))
  ).filter((el) => el.offsetParent !== null || el.getClientRects().length > 0);
  if (all.length === 0) return null;
  if (!s.targetText) return all[0];
  const needle = s.targetText.toLowerCase();
  const hit = all.find((el) => {
    const scope = el.closest('[data-testid="story-card"]') ?? el;
    return (scope.textContent ?? "").toLowerCase().includes(needle);
  });
  return hit ?? all[0];
}

function recompute(): void {
  const s = tour.currentStep;
  if (!s || !tour.active || !ready.value || !s.target) {
    anchorRect.value = null;
    targetEl.value = null;
    return;
  }
  const el = resolveTargetEl(s);
  if (!el) {
    anchorRect.value = null;
    targetEl.value = null;
    return;
  }
  let r = el.getBoundingClientRect();
  if (r.top < 0 || r.bottom > window.innerHeight || r.left < 0 || r.right > window.innerWidth) {
    el.scrollIntoView({ block: "center", inline: "center" });
    r = el.getBoundingClientRect();
  }
  targetEl.value = el;
  anchorRect.value = { top: r.top, left: r.left, width: r.width, height: r.height };
}

function positionPopover(): void {
  const pop = popoverEl.value;
  if (!pop) return;
  const pw = pop.offsetWidth;
  const ph = pop.offsetHeight;
  const a = anchorRect.value;
  const gap = 14;
  let top: number;
  let left: number;
  if (!a) {
    // Anchorless: centered by default, but honor placement so a no-dim narration
    // popover can sit off to the side (e.g. "right") instead of over the content.
    const m = 16;
    const placement = tour.currentStep?.placement ?? "center";
    top = (window.innerHeight - ph) / 2;
    left = (window.innerWidth - pw) / 2;
    if (placement === "right") left = window.innerWidth - pw - m;
    else if (placement === "left") left = m;
    else if (placement === "top") top = m;
    else if (placement === "bottom") top = window.innerHeight - ph - m;
  } else {
    const placement = tour.currentStep?.placement ?? "bottom";
    switch (placement) {
      case "top":
        top = a.top - ph - gap;
        left = a.left + a.width / 2 - pw / 2;
        break;
      case "left":
        top = a.top + a.height / 2 - ph / 2;
        left = a.left - pw - gap;
        break;
      case "right":
        top = a.top + a.height / 2 - ph / 2;
        left = a.left + a.width + gap;
        break;
      case "center":
        top = (window.innerHeight - ph) / 2;
        left = (window.innerWidth - pw) / 2;
        break;
      case "bottom":
      default:
        top = a.top + a.height + gap;
        left = a.left + a.width / 2 - pw / 2;
        break;
    }
  }
  const m = 12;
  left = Math.max(m, Math.min(left, window.innerWidth - pw - m));
  top = Math.max(m, Math.min(top, window.innerHeight - ph - m));
  popStyle.value = { top: `${top}px`, left: `${left}px` };
}

async function refresh(): Promise<void> {
  recompute();
  await nextTick();
  positionPopover();
}

// ── Spotlight geometry ───────────────────────────────────────────────────────
const hole = computed<Rect | null>(() => {
  const a = anchorRect.value;
  if (!a) return null;
  const p = 6;
  return {
    top: Math.max(0, a.top - p),
    left: Math.max(0, a.left - p),
    width: a.width + 2 * p,
    height: a.height + 2 * p,
  };
});

const ringStyle = computed<Record<string, string>>(() => {
  const h = hole.value;
  if (!h) return { display: "none" };
  const style: Record<string, string> = {
    top: `${h.top}px`,
    left: `${h.left}px`,
    width: `${h.width}px`,
    height: `${h.height}px`,
  };
  return style;
});

// ── Advancement: route / state / click ───────────────────────────────────────
let clickCleanup: (() => void) | null = null;

function bindClickTarget(): void {
  clickCleanup?.();
  clickCleanup = null;
  const s = tour.currentStep;
  const el = targetEl.value;
  if (!s || s.advance !== "click-target" || !el) return;
  const handler = (): void => tour.advanceFromEnv("click");
  el.addEventListener("click", handler, { capture: true, once: true });
  clickCleanup = () => el.removeEventListener("click", handler, { capture: true });
}

watch(
  () => route.path,
  () => {
    tour.noteEnvChange();
    const s = tour.currentStep;
    if (s?.advance === "route-match" && routeKindMatches(s.advanceRoute)) {
      tour.advanceFromEnv("route");
    }
    void refresh();
  }
);

watch(
  () => tour.stepIndex,
  () => {
    anchorRect.value = null;
    targetEl.value = null;
    syncWatchdog();
    void refresh().then(bindClickTarget);
  }
);

watch([ready, () => tour.envTick, () => tour.active], () => {
  syncWatchdog();
  void refresh().then(bindClickTarget);
});

watch(targetEl, bindClickTarget);

// ── Watchdog: never let a step block forever ─────────────────────────────────
// If a step can't anchor within a grace window (its target/state never arrives —
// e.g. a non-deterministic session that doesn't reach the scripted state), skip
// it. If that exhausts the tour, finish() fires and the overlay disappears.
const ANCHOR_GRACE_MS = 6000;
let watchdog: number | null = null;

function clearWatchdog(): void {
  if (watchdog !== null) {
    window.clearTimeout(watchdog);
    watchdog = null;
  }
}

function syncWatchdog(): void {
  clearWatchdog();
  if (!tour.active || ready.value) return; // anchored or inactive → no timer
  watchdog = window.setTimeout(() => {
    if (tour.active && !ready.value) tour.next(); // skip the un-anchorable step
  }, ANCHOR_GRACE_MS);
}

// ── Lifecycle: observers, poll backstop, keyboard, scroll/resize ─────────────
let observer: MutationObserver | null = null;
let poll: number | null = null;

function onScrollResize(): void {
  void refresh();
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === "Escape" && tour.active) tour.skip();
}

onMounted(() => {
  observer = new MutationObserver(() => tour.noteEnvChange());
  observer.observe(document.body, { childList: true, subtree: true });
  poll = window.setInterval(() => {
    if (tour.active) void refresh();
  }, 200);
  window.addEventListener("scroll", onScrollResize, true);
  window.addEventListener("resize", onScrollResize);
  window.addEventListener("keydown", onKeydown);
  // Dev/test convenience: deterministic kickoff regardless of localStorage.
  type TourWindow = typeof window & {
    __startTour?: () => void;
    __startTourWithSteps?: (stepsJson: string) => void;
    __tourGoTo?: (id: string) => void;
    __tourSkip?: () => void;
  };
  const win = window as TourWindow;
  win.__startTour = () => tour.start(true);
  // Allows the trace-features video spec to inject a custom step array:
  //   window.__startTourWithSteps(JSON.stringify(TRACE_TOUR_STEPS))
  win.__startTourWithSteps = (stepsJson: string) => {
    try {
      const parsed = JSON.parse(stepsJson) as TourStep[];
      tour.startWithSteps(parsed, true);
    } catch {
      // malformed JSON — fall back to the default onboarding tour
      tour.start(true);
    }
  };
  // Test hook: re-sync the overlay to a step by id when the video spec detects
  // the overlay's internal anchoring has drifted ahead of the driven step.
  win.__tourGoTo = (id: string) => tour.goTo(id);
  // Test hook: dismiss the overlay (e.g. the VS Code recorder clears it before
  // an out-of-webview editor beat so the popover doesn't cover the editor frame).
  win.__tourSkip = () => tour.skip();
  syncWatchdog();
  void refresh();
});

onUnmounted(() => {
  observer?.disconnect();
  if (poll !== null) window.clearInterval(poll);
  clearWatchdog();
  window.removeEventListener("scroll", onScrollResize, true);
  window.removeEventListener("resize", onScrollResize);
  window.removeEventListener("keydown", onKeydown);
  clickCleanup?.();
});
</script>

<style scoped>
.tour {
  position: fixed;
  inset: 0;
  z-index: 1500;
  pointer-events: none;
}

.tour__ring {
  position: fixed;
  border: 2px solid var(--k-fg-accent, #38bdf8);
  border-radius: 8px;
  box-shadow: 0 0 0 3px rgba(56, 189, 248, 0.25), 0 0 18px rgba(56, 189, 248, 0.4);
  pointer-events: none; /* let action-step clicks reach the real control */
}

/* Non-blocking holding pill: bottom-center, interactive only on itself so the
   rest of the UI stays fully usable while a step prepares. */
.tour__prep {
  position: fixed;
  bottom: 1.1rem;
  left: 50%;
  transform: translateX(-50%);
  z-index: 1600;
  display: flex;
  align-items: center;
  gap: 0.55rem;
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e3a5f);
  border-radius: 999px;
  padding: 0.4rem 0.5rem 0.4rem 0.85rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.78rem;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  pointer-events: auto;
}
.tour__prep-spinner {
  display: inline-block;
  animation: tour-spin 1.1s linear infinite;
  color: var(--k-fg-accent, #38bdf8);
}
@keyframes tour-spin {
  to { transform: rotate(360deg); }
}

.tour__popover {
  position: fixed;
  z-index: 1600;
  width: 320px;
  max-width: calc(100vw - 24px);
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e3a5f);
  border-radius: 10px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.6);
  color: var(--k-fg, #e2e8f0);
  padding: 0.85rem 1rem 0.7rem;
  pointer-events: auto;
}

.tour__progress {
  font-size: 0.62rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--k-fg-muted, #64748b);
  margin-bottom: 0.25rem;
}

.tour__title {
  margin: 0 0 0.35rem;
  font-size: 0.98rem;
  color: var(--k-fg, #f1f5f9);
}

.tour__body {
  margin: 0;
  font-size: 0.82rem;
  line-height: 1.5;
  color: var(--k-fg, #cbd5e1);
}

.tour__cue {
  margin: 0.55rem 0 0;
  font-size: 0.74rem;
  color: var(--k-fg-accent, #7dd3fc);
}

.tour__footer {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  margin-top: 0.8rem;
}
.tour__spacer {
  flex: 1;
}

.tour__btn {
  border-radius: 6px;
  padding: 0.32rem 0.7rem;
  font-size: 0.76rem;
  font-weight: 600;
  cursor: pointer;
  font-family: inherit;
}
.tour__btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
.tour__btn--ghost {
  background: none;
  border: 1px solid var(--k-border, #1e293b);
  color: var(--k-fg-muted, #94a3b8);
}
.tour__btn--ghost:hover:not(:disabled) {
  background: var(--k-bg-hover, #15233a);
  color: var(--k-fg, #cbd5e1);
}
.tour__btn--primary {
  background: var(--k-button-bg, #1d4ed8);
  border: 1px solid var(--k-border-focus, #2563eb);
  color: var(--k-button-fg, #eef2ff);
}
.tour__btn--primary:hover {
  background: var(--k-button-hover-bg, #2563eb);
}
</style>
