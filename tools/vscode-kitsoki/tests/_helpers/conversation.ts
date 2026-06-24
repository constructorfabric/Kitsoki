/**
 * conversation.ts — make a recorded conversation FOLLOWABLE.
 *
 * Kitsoki is a conversation engine, so its demo videos CONSTANTLY show a chat
 * transcript. The naive recording (type → fixed dwell → next turn) is jumpy and
 * unreadable: the chat component (`ChatTranscript.vue`) snaps the scroller to the
 * BOTTOM on every new message, so a reply taller than the viewport renders with
 * its OPENING lines already scrolled off — the viewer never sees where the
 * message starts. This helper reproduces how a person actually follows a chat:
 * the new operator input eases up to the top, you hold to read it, then the reply
 * eases DOWN through the viewport at a readable pace so every line passes
 * on-camera and someone can pause on any of it.
 *
 * It is the SAME technique the native web tour uses (`tools/runstatus/tests/
 * playwright/gears-prd-design.spec.ts` → `revealTurn`); this is the reusable,
 * frame-agnostic form so the VS Code extension demo (chat lives inside a webview
 * iframe) gets identical pacing. Everything is driven through a single transcript
 * scroller `Locator`, so it works whether the chat is the top page or an iframe.
 *
 * DISPLAY ONLY — this drives the camera (scroll position + dwell), never the
 * trace or the conversation content.
 */
import type { Locator } from '@playwright/test';

/** Pacing knobs (ms at PACE=1). Mirrors the native tour's constants. */
export interface RevealTiming {
  settleMs: number; // let the turn's rows render before we move the camera
  scrollUpMs: number; // ease the new input up to the top
  readInputMs: number; // hold on the input + the reply's opening
  readReplyMs: number; // hold on the finished reply before the next turn
}

export const DEFAULT_TIMING: RevealTiming = {
  settleMs: 1400,
  scrollUpMs: 1200,
  readInputMs: 1500,
  readReplyMs: 1700,
};

/**
 * Install once per transcript: neuter the component's instant
 * auto-scroll-to-bottom so WE own the camera, and attach an eased scroller +
 * measurement helpers to the scroller element. Idempotent (guarded by a flag on
 * the element), so it survives the SPA re-rendering the transcript.
 *
 * The override makes `el.scrollTop = …` (what the Vue watcher calls) a no-op,
 * while `__ease`/the measurement helpers drive the REAL prototype setter — so the
 * component can never yank the view to the bottom mid-reveal.
 */
export async function installConversationScroll(scroller: Locator): Promise<void> {
  await scroller.evaluate((el) => {
    const tag = el as unknown as { __revealInstalled?: boolean };
    if (tag.__revealInstalled) return;
    tag.__revealInstalled = true;

    const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop')!;
    const realGet = () => (desc.get as () => number).call(el);
    const realSet = (v: number) => (desc.set as (v: number) => void).call(el, v);

    // The component's `el.scrollTop = el.scrollHeight` becomes a no-op; the
    // camera is driven only through the helpers below (which bypass via realSet).
    Object.defineProperty(el, 'scrollTop', {
      configurable: true,
      get() {
        return realGet();
      },
      set() {
        /* ignored — natural scroll is driven via __ease */
      },
    });

    const api = el as unknown as {
      __ease: (to: number, ms: number) => Promise<void>;
      __lastRowTop: (role?: string) => number;
      __scrollMax: () => number;
    };
    api.__ease = (to: number, ms: number) =>
      new Promise<void>((res) => {
        const from = realGet();
        const max = el.scrollHeight - el.clientHeight;
        const target = Math.max(0, Math.min(to, max));
        if (ms <= 0 || Math.abs(target - from) < 2) {
          realSet(target);
          return res();
        }
        const t0 = performance.now();
        const tick = (now: number) => {
          const p = Math.min(1, (now - t0) / ms);
          const e = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2; // easeInOutQuad
          realSet(from + (target - from) * e);
          if (p < 1) requestAnimationFrame(tick);
          else res();
        };
        requestAnimationFrame(tick);
      });
    // Top (within the scroll range) of the last row of the given role, with a
    // little headroom so the avatar/role label isn't clipped against the edge.
    api.__lastRowTop = (role?: string) => {
      const sel = role ? `[data-testid="chat-row-${role}"]` : '.chat-row';
      const rows = el.querySelectorAll(sel);
      const last = rows[rows.length - 1] as HTMLElement | undefined;
      return last ? Math.max(0, last.offsetTop - 16) : el.scrollHeight;
    };
    api.__scrollMax = () => el.scrollHeight - el.clientHeight;
  });
}

/** Smoothly ease the transcript to an absolute scrollTop over `ms`. */
export async function easeScroll(scroller: Locator, to: number, ms: number): Promise<void> {
  await scroller.evaluate(
    (el, [to, ms]) =>
      (el as unknown as { __ease: (a: number, b: number) => Promise<void> }).__ease(to, ms),
    [to, ms] as [number, number],
  );
}

/** Scroll offset of the last row of `role` (default: any row). */
export async function lastRowTop(scroller: Locator, role?: 'user' | 'agent'): Promise<number> {
  return scroller.evaluate(
    (el, r) => (el as unknown as { __lastRowTop: (role?: string) => number }).__lastRowTop(r),
    role,
  );
}

/** Maximum scrollTop of the transcript (scrollHeight − clientHeight). */
export async function scrollMax(scroller: Locator): Promise<number> {
  return scroller.evaluate(
    (el) => (el as unknown as { __scrollMax: () => number }).__scrollMax(),
  );
}

/**
 * Wait until the newest agent bubble has rendered REAL content — not the bare
 * live/streaming placeholder ("…"/". ."). Easing the camera through a reply
 * while it is still the placeholder captures an empty-looking bubble (a QA
 * conversation-legibility failure). Polls briefly and falls through on timeout
 * (a turn that legitimately stays a working indicator, e.g. a suspended one that
 * never emits text, must not hang the recording).
 */
export async function waitForAgentContent(scroller: Locator, timeoutMs = 8000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const ready = await scroller
      .evaluate((el) => {
        const rows = el.querySelectorAll('[data-testid="chat-row-agent"], .chat-row--agent');
        const last = rows[rows.length - 1] as HTMLElement | undefined;
        if (!last) return false;
        // Strip the role label + avatar + the dots/ellipsis the placeholder uses;
        // anything substantive left means the reply has rendered.
        const stripped = (last.innerText || '')
          .toLowerCase()
          .replace(/agent|you/g, '')
          .replace(/[.…·•\s]/g, '');
        return stripped.length >= 3;
      })
      .catch(() => false);
    if (ready) return;
    await new Promise((r) => setTimeout(r, 200));
  }
}

export interface RevealDeps {
  /** Resolve the transcript scroller Locator (re-resolved each turn; the SPA may
   *  replace the node). */
  scroller: () => Locator;
  /** Sleep `ms` (already pace-scaled by the caller's dwell). */
  dwell: (ms: number) => Promise<void>;
  /** Scale a base ms by the record pace (identity at PACE=1, 0 in fast mode). */
  paced: (ms: number) => number;
  /** Optional: capture a labeled frame at the `-in` / `-out` beats. */
  shot?: (label: string) => Promise<void>;
  timing?: Partial<RevealTiming>;
}

/**
 * Drive ONE conversational turn, then reveal it the way a reader follows it:
 * settle → ease the new operator input to the top → hold → ease DOWN through the
 * reply at a readable pace → hold on the finished reply. `settle` waits for the
 * turn's effect (e.g. a state transition) before the camera moves.
 *
 * No-op camera work in fast mode (paced → 0): `action` + `settle` still run so
 * the assertions gate, but the eases collapse to instant.
 */
export async function revealTurn(
  deps: RevealDeps,
  action: () => Promise<void>,
  settle: () => Promise<void>,
  label: string,
): Promise<void> {
  const t = { ...DEFAULT_TIMING, ...(deps.timing ?? {}) };
  await action();
  await settle();
  // Fast mode (paced → 0): run the action + settle so the assertions still gate,
  // but skip ALL camera work so the fast run stays a pure, instant assert-gate.
  if (deps.paced(1000) === 0) return;
  await deps.dwell(deps.paced(t.settleMs)); // rows render
  // Don't ease through a reply that is still the bare live placeholder ("…") —
  // wait for its real content first so the camera never captures an empty bubble.
  await waitForAgentContent(deps.scroller());
  await installConversationScroll(deps.scroller());

  // 1. New operator input → ease it to the top of the chat; hold to read it.
  const top = await lastRowTop(deps.scroller(), 'user');
  await easeScroll(deps.scroller(), top, deps.paced(t.scrollUpMs));
  await deps.dwell(deps.paced(t.readInputMs));
  if (deps.shot) await deps.shot(`${label}-in`);

  // 2. Ease DOWN through the reply (duration tracks the distance so a long reply
  //    scrolls slowly + readably); hold on the finished reply. No-op when the
  //    reply already fits under the input with no overflow.
  const max = await scrollMax(deps.scroller());
  const span = Math.max(0, max - top);
  await easeScroll(deps.scroller(), max, deps.paced(Math.min(3200, Math.max(700, Math.round(span * 3)))));
  await deps.dwell(deps.paced(t.readReplyMs));
  if (deps.shot) await deps.shot(`${label}-out`);
}
