/**
 * kitsoki-tour-adapter.cjs — a slidey tour adapter for the kitsoki web SPA.
 *
 * DEPENDENCY INJECTION at the slidey tour-engine boundary: slidey core defines
 * the adapter interface (slidey/tour-adapter) and stays ZERO-dependency on
 * kitsoki; this module — which lives in the kitsoki repo — supplies the
 * kitsoki-specific drive that slidey's closed verb set cannot express. slidey
 * loads it by PATH (`--adapter ./.../kitsoki-tour-adapter.cjs` or the tour
 * spec's `"adapter"` field), never by import.
 *
 * It mirrors, verb-for-verb, the PROVEN Act-2 drive that previously forced a
 * forked Playwright harness:
 *   tools/runstatus/tests/playwright/github-demo-act2-rrweb-capture.spec.ts
 *   tools/runstatus/tests/playwright/github-demo-webviewer.spec.ts
 *
 * The three kitsoki seams slidey's `dom` adapter cannot express:
 *   1. composer prose → slot-intent routing   → actions.composeAndSend
 *   2. a bare-verb submit seam                 → actions.submitIntent
 *      (window.__kitsokiSubmitIntent(name, slots))
 *   3. state-gated advance                     → advancers['state-match']
 *      (wait until [data-testid=current-state] reaches step.advanceState)
 *
 * Plus ergonomic verbs used by the tour's drive:[] lists — clickIntent,
 * waitState, revealTurn — that reproduce the manifest's click-intent /
 * wait-state / reveal-turn motion on-camera.
 *
 * Every verb/advancer receives (page, args|step, ctx) where ctx is slidey's
 * shared adapter ctx { base, pace, mode, timeout, resolve, adapter }. NO real
 * LLM, NO real GitHub: the adapter only drives the puppeteer page; the target is
 * a no-LLM kitsoki REPLAY server (see act2-webviewer.tour.json).
 */

'use strict';

// A handful of BARE NAVIGATION VERBS the slidey pm_idea recording routes as free
// text to slot-less PRD pipeline intents. The rooms that receive them render the
// STRUCTURED composer (no text floor for the verb), so the proven drive submits
// the mapped intent through the live submit seam instead of typing the word.
// (Mirrors github-demo-act2-rrweb-capture.spec.ts VERB_INTENTS.)
const VERB_INTENTS = {
  ready: 'core__prd__start',
  confirm: 'core__prd__confirm',
  submit: 'core__prd__submit_answers',
};

const STATE_POLL_MS = 300;

/** Pace-aware sleep (ctx.pace is slidey's dwell multiplier). */
function nap(ctx, ms) {
  const p = ctx && Number.isFinite(ctx.pace) ? ctx.pace : 1;
  return new Promise((r) => setTimeout(r, Math.max(0, Math.round(ms * p))));
}

/**
 * Type prose into the kitsoki composer (or the conversational text floor) and
 * send it. Bare navigation verbs (ready/confirm/submit) are re-routed to the
 * structured submit seam — exactly as the forked spec does — so a verb typed at
 * a structured-composer room still lands its mapped intent. Real on-camera
 * typing (rrweb records the keystrokes) when paced; an instant value-set when
 * pace is 0.
 *
 * @param {object} page  puppeteer page
 * @param {{text:string}} args
 * @param {object} ctx   slidey adapter ctx
 */
async function composeAndSend(page, { text }, ctx) {
  const verbIntent = VERB_INTENTS[String(text).trim().toLowerCase()];
  if (verbIntent) {
    return submitIntent(page, { name: verbIntent }, ctx);
  }

  const focused = await page.evaluate(() => {
    const input =
      document.querySelector('[data-testid="composer-input"]') ||
      document.querySelector('[data-testid="text-floor-input"]');
    if (!input) return false;
    input.focus();
    input.value = '';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  });
  if (!focused) throw new Error('composer-input not found (no composer or text floor)');

  const paced = (ctx && Number.isFinite(ctx.pace) ? ctx.pace : 1) > 0;
  if (paced) {
    await page.keyboard.type(String(text), { delay: 14 });
  } else {
    await page.evaluate((t) => {
      const input =
        document.querySelector('[data-testid="composer-input"]') ||
        document.querySelector('[data-testid="text-floor-input"]');
      if (input) {
        input.value = t;
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
    }, String(text));
  }
  await nap(ctx, 600);

  const sent = await page.evaluate(() => {
    const btn =
      document.querySelector('[data-testid="composer-send"]') ||
      document.querySelector('[data-testid="text-floor-send"]');
    if (!btn) return false;
    btn.click();
    return true;
  });
  if (!sent) throw new Error('composer-send not found');
}

/**
 * The bare-verb submit seam: window.__kitsokiSubmitIntent(name, slots). Used for
 * navigation verbs the composer can't carry (and as the re-route target for the
 * VERB_INTENTS prose above).
 *
 * @param {object} page
 * @param {{name:string, slots?:object}} args
 * @param {object} ctx
 */
async function submitIntent(page, { name, slots }, ctx) {
  const ok = await page.evaluate(
    async (n, s) => {
      if (typeof window.__kitsokiSubmitIntent !== 'function') return false;
      await window.__kitsokiSubmitIntent(n, s || undefined);
      return true;
    },
    name,
    slots || null,
  );
  if (!ok) throw new Error(`__kitsokiSubmitIntent unavailable (cannot submit ${name})`);
}

/**
 * Click a rendered intent button ([data-testid=intent-btn-<id>]). Scrolls it
 * into view first so the click is recorded as real motion.
 */
async function clickIntent(page, { intent }, ctx) {
  const ok = await page.evaluate((id) => {
    const btn = document.querySelector(`[data-testid="intent-btn-${id}"]`);
    if (!btn) return false;
    btn.scrollIntoView({ block: 'center' });
    btn.click();
    return true;
  }, intent);
  if (!ok) throw new Error(`intent button ${intent} not found`);
}

/**
 * Poll [data-testid=current-state] until it reads `state` AND the matching view
 * has settled (landing shows the go_prd intent; non-landing has navigated past
 * it) AND the composer is not pending. This is the proven state-gate from the
 * fork's waitForState — exposed both as a drive verb (waitState) and as the
 * `state-match` advancer.
 *
 * @param {object} page
 * @param {string} state
 * @param {object} ctx
 */
async function waitForStateValue(page, state, ctx) {
  const timeout = (ctx && ctx.timeout) || 15000;
  const deadline = Date.now() + Math.max(timeout, 20000);
  let cur = '';
  while (Date.now() < deadline) {
    const probe = await page.evaluate(() => {
      const badge = document.querySelector('[data-testid="current-state"]');
      const cur = badge ? (badge.textContent || '').trim() : '';
      const onLanding = !!document.querySelector('[data-testid="intent-btn-core__go_prd"]');
      const pending = !!document.querySelector(
        '[data-testid="composer-input"][disabled], [data-testid="text-floor-input"][disabled]',
      );
      return { cur, onLanding, pending };
    });
    cur = probe.cur;
    const viewSettled = state === 'core.landing' ? probe.onLanding : !probe.onLanding;
    if (cur === state && viewSettled && !probe.pending) return;
    await nap(ctx, STATE_POLL_MS);
  }
  throw new Error(`wait-state ${state} timed out (last "${cur}")`);
}

/** Drive verb form of the state gate: { waitState: { state } }. */
async function waitState(page, { state }, ctx) {
  return waitForStateValue(page, state, ctx);
}

/**
 * Gently scroll the transcript so the just-landed turn is on-camera. A simpler,
 * deterministic cousin of the fork's revealTurn (no scrollTop monkeypatch is
 * needed — slidey records whatever scroll we drive). Best-effort.
 */
async function revealTurn(page, _args, ctx) {
  await page
    .evaluate(() => {
      const el = document.querySelector('[data-testid="chat-transcript"]');
      if (!el) return;
      const rows = el.querySelectorAll('[data-testid="chat-row-user"]');
      const last = rows[rows.length - 1];
      const top = last ? Math.max(0, last.offsetTop - 16) : el.scrollHeight;
      el.scrollTo({ top, behavior: 'smooth' });
    })
    .catch(() => {});
  await nap(ctx, 900);
  await page
    .evaluate(() => {
      const el = document.querySelector('[data-testid="chat-transcript"]');
      if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
    })
    .catch(() => {});
  await nap(ctx, 900);
}

module.exports = {
  name: 'kitsoki',

  /**
   * Post-ready, pre-recording: confirm the SPA mounted and the submit seam is
   * live. No helper script to stage (the SPA exposes __kitsokiSubmitIntent on
   * mount); this is the ready gate the fork got from waitForTestId("home-view").
   */
  async init(page, _tour, ctx) {
    await page.waitForSelector('[data-testid="home-view"]', {
      visible: true,
      timeout: (ctx && ctx.timeout) || 15000,
    });
  },

  actions: {
    composeAndSend,
    submitIntent,
    clickIntent,
    waitState,
    revealTurn,
  },

  advancers: {
    // state-gated advance: wait until current-state reaches step.advanceState.
    'state-match': (page, step, ctx) => waitForStateValue(page, step.advanceState, ctx),
  },
};
