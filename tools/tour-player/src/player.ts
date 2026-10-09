/**
 * TourPlayer: the framework-free execution half of tour format v2. Extracted
 * from tools/runstatus/src/components/tour/TourOverlay.vue +
 * src/stores/tour.ts (the Vue reference implementation) — same anchoring,
 * spotlight, popover-placement, watchdog, and MutationObserver-driven SPA
 * resilience, with zero Vue/Pinia/DOM-framework dependency so it runs in
 * ANY page (kitsoki-family or third-party, e.g. pog-portal) and in headless
 * chromium the same way it runs for a live user.
 *
 * Non-goals: authoring (that's tools/browser-mcp's tour_* tools) and
 * network delivery (that's the P5 serving loop) — this class only executes
 * an already-built TourManifestV2 against the live DOM it's loaded into.
 */
import { AnchorResolutionError, resolveAnchor } from "./anchors.js";
import { effectivePolicy, type HealEvent, type TourManifestV2, type TourStepV2 } from "./types.js";

export interface TourPlayerOptions {
  /** Called whenever anchor resolution heals onto a secondary strategy. Never silent. */
  onHeal?: (heal: HealEvent) => void;
  /** Called on every lifecycle event (see TourPlayerEvent). */
  onEvent?: (event: TourPlayerEvent) => void;
  /** Returns the current route path; defaults to location.pathname. Used for step.route holds. */
  getRoute?: () => string;
  /** Grace window before an un-anchorable step is skipped (matches TourOverlay's ANCHOR_GRACE_MS). */
  anchorGraceMs?: number;
}

export type TourPlayerEvent =
  | { type: "started"; stepId: string }
  | { type: "step-shown"; stepId: string }
  | { type: "step-skipped"; stepId: string; reason: "watchdog" | "user" }
  | { type: "advanced"; fromStepId: string; toStepId: string | null }
  | { type: "act-pending-confirm"; stepId: string }
  | { type: "finished" }
  | { type: "aborted" };

const ANCHOR_RING_ID = "kitsoki-tour-ring";
const POPOVER_ID = "kitsoki-tour-popover";
const STYLE_ID = "kitsoki-tour-style";

const DEFAULT_CSS = `
#${ANCHOR_RING_ID} { position:fixed; z-index:2147483000; pointer-events:none; border:2px solid #38bdf8; border-radius:8px; box-shadow:0 0 0 3px rgba(56,189,248,.25),0 0 18px rgba(56,189,248,.4); transition:top .15s,left .15s,width .15s,height .15s; }
#${POPOVER_ID} { position:fixed; z-index:2147483001; width:320px; max-width:calc(100vw - 24px); background:#0d1b2a; border:1px solid #1e3a5f; border-radius:10px; box-shadow:0 12px 40px rgba(0,0,0,.6); color:#e2e8f0; padding:.85rem 1rem .7rem; font:14px/1.4 ui-sans-serif,system-ui,sans-serif; }
#${POPOVER_ID} h3 { margin:0 0 .35rem; font-size:.98rem; }
#${POPOVER_ID} p { margin:0; font-size:.82rem; line-height:1.5; color:#cbd5e1; }
#${POPOVER_ID} .kt-footer { display:flex; align-items:center; gap:.4rem; margin-top:.8rem; }
#${POPOVER_ID} .kt-spacer { flex:1; }
#${POPOVER_ID} button { border-radius:6px; padding:.32rem .7rem; font-size:.76rem; font-weight:600; cursor:pointer; font-family:inherit; background:none; border:1px solid #1e293b; color:#94a3b8; }
#${POPOVER_ID} button.kt-primary { background:#1d4ed8; border-color:#2563eb; color:#eef2ff; }
`;

export class TourPlayer {
  private manifest: TourManifestV2;
  private opts: Required<Pick<TourPlayerOptions, "anchorGraceMs">> & TourPlayerOptions;
  private index = 0;
  private active = false;
  private ringEl: HTMLDivElement | null = null;
  private popoverEl: HTMLDivElement | null = null;
  private clickCleanup: (() => void) | null = null;
  private watchdog: ReturnType<typeof setTimeout> | null = null;
  private observer: MutationObserver | null = null;
  private poll: ReturnType<typeof setInterval> | null = null;
  private actedStepId: string | null = null;

  constructor(manifest: TourManifestV2, opts: TourPlayerOptions = {}) {
    this.manifest = manifest;
    this.opts = { anchorGraceMs: 6000, ...opts };
  }

  private emit(event: TourPlayerEvent): void {
    this.opts.onEvent?.(event);
  }

  private get currentStep(): TourStepV2 | undefined {
    return this.manifest.steps[this.index];
  }

  private ensureStyle(): void {
    if (document.getElementById(STYLE_ID)) return;
    const style = document.createElement("style");
    style.id = STYLE_ID;
    style.textContent = DEFAULT_CSS;
    document.head.appendChild(style);
  }

  start(): void {
    this.ensureStyle();
    this.active = true;
    this.index = 0;
    // The observer watches document.body for SPA re-renders (a route
    // change, a re-mounted target) so the player re-anchors without a
    // manual nudge — Reactour-style resilience. Its own ring/popover
    // inserts are ALSO childList mutations of document.body, so the
    // callback goes through scheduleRefresh (rAF-coalesced), and refresh()
    // itself only WRITES to the DOM when something actually changed
    // (renderRing/renderPopover below) — otherwise this is a one-mutation
    // feedback loop that starves the page's own event loop.
    this.observer = new MutationObserver(() => this.scheduleRefresh());
    this.observer.observe(document.body, { childList: true, subtree: true });
    this.poll = setInterval(() => this.refresh(), 200);
    window.addEventListener("scroll", this.onScrollResize, true);
    window.addEventListener("resize", this.onScrollResize);
    const first = this.currentStep;
    if (first) this.emit({ type: "started", stepId: first.id });
    this.refresh();
  }

  private refreshScheduled = false;
  private scheduleRefresh(): void {
    if (this.refreshScheduled) return;
    this.refreshScheduled = true;
    requestAnimationFrame(() => {
      this.refreshScheduled = false;
      this.refresh();
    });
  }

  private onScrollResize = (): void => {
    if (this.active) this.scheduleRefresh();
  };

  next(): void {
    const from = this.currentStep;
    if (this.index >= this.manifest.steps.length - 1) {
      this.finish();
      return;
    }
    this.index += 1;
    this.emit({ type: "advanced", fromStepId: from?.id ?? "", toStepId: this.currentStep?.id ?? null });
    this.refresh();
  }

  prev(): void {
    if (this.index > 0) {
      this.index -= 1;
      this.refresh();
    }
  }

  goTo(id: string): void {
    const i = this.manifest.steps.findIndex((s) => s.id === id);
    if (i >= 0) {
      this.index = i;
      this.refresh();
    }
  }

  skip(): void {
    this.finish();
  }

  abort(): void {
    this.teardownDom();
    this.stopLifecycle();
    this.active = false;
    this.emit({ type: "aborted" });
  }

  private finish(): void {
    this.teardownDom();
    this.stopLifecycle();
    this.active = false;
    this.emit({ type: "finished" });
  }

  private stopLifecycle(): void {
    this.observer?.disconnect();
    this.observer = null;
    if (this.poll !== null) clearInterval(this.poll);
    this.poll = null;
    this.clearWatchdog();
    window.removeEventListener("scroll", this.onScrollResize, true);
    window.removeEventListener("resize", this.onScrollResize);
    this.clickCleanup?.();
    this.clickCleanup = null;
  }

  private clearWatchdog(): void {
    if (this.watchdog !== null) {
      clearTimeout(this.watchdog);
      this.watchdog = null;
    }
  }

  private teardownDom(): void {
    this.ringEl?.remove();
    this.popoverEl?.remove();
    this.ringEl = null;
    this.popoverEl = null;
  }

  private routeMatches(step: TourStepV2): boolean {
    if (!step.route) return true;
    const current = this.opts.getRoute ? this.opts.getRoute() : window.location.pathname;
    return step.route === "any" || step.route === current;
  }

  private refresh(): void {
    if (!this.active) return;
    const step = this.currentStep;
    if (!step) return;
    if (!this.routeMatches(step)) {
      this.syncWatchdog(false);
      return;
    }

    if (!step.target) {
      this.clearWatchdog();
      this.renderPopover(step, null);
      this.noteShown(step.id);
      return;
    }

    let resolved;
    try {
      resolved = resolveAnchor(step.target, step.id);
    } catch (err) {
      if (err instanceof AnchorResolutionError) {
        this.syncWatchdog(false);
        return;
      }
      throw err;
    }
    this.clearWatchdog();
    if (resolved.heal) this.opts.onHeal?.(resolved.heal);

    const rect = this.scrollAndMeasure(resolved.el, step);
    this.renderRing(rect);
    this.renderPopover(step, rect);
    this.bindAdvance(step, resolved.el);
    this.maybePerformAct(step, resolved.el);
    this.noteShown(step.id);
  }

  private lastShownStepId: string | null = null;
  private noteShown(stepId: string): void {
    if (this.lastShownStepId === stepId) return;
    this.lastShownStepId = stepId;
    this.emit({ type: "step-shown", stepId });
  }

  private syncWatchdog(anchored: boolean): void {
    this.clearWatchdog();
    if (anchored) return;
    this.watchdog = setTimeout(() => {
      const step = this.currentStep;
      if (this.active && step) {
        this.emit({ type: "step-skipped", stepId: step.id, reason: "watchdog" });
        this.next();
      }
    }, this.opts.anchorGraceMs);
  }

  private scrollAndMeasure(el: HTMLElement, step: TourStepV2): DOMRect {
    let r = el.getBoundingClientRect();
    const offscreen = r.top < 0 || r.bottom > window.innerHeight || r.left < 0 || r.right > window.innerWidth;
    if (offscreen) {
      if (step.viewport) {
        const container = document.querySelector(`[data-testid="${step.viewport}"]`);
        container?.scrollTo?.({ top: (container as HTMLElement).scrollTop + r.top - window.innerHeight / 2 });
      }
      el.scrollIntoView({ block: "center", inline: "center" });
      r = el.getBoundingClientRect();
    }
    return r;
  }

  private lastRingKey = "";
  private renderRing(rect: DOMRect): void {
    if (!this.ringEl) {
      this.ringEl = document.createElement("div");
      this.ringEl.id = ANCHOR_RING_ID;
      document.body.appendChild(this.ringEl);
    }
    const p = 6;
    const top = Math.max(0, rect.top - p);
    const left = Math.max(0, rect.left - p);
    const width = rect.width + 2 * p;
    const height = rect.height + 2 * p;
    // Idempotent write: the ring is a child of document.body, so an
    // unconditional style write here is itself a childList/attribute
    // mutation that would re-trigger the MutationObserver in start() on
    // every refresh, forever — see start()'s comment. Only touch the DOM
    // when the geometry actually changed.
    const key = `${top}|${left}|${width}|${height}`;
    if (key === this.lastRingKey) return;
    this.lastRingKey = key;
    Object.assign(this.ringEl.style, { top: `${top}px`, left: `${left}px`, width: `${width}px`, height: `${height}px` });
  }

  private lastPopoverStepId: string | null = null;
  private renderPopover(step: TourStepV2, anchorRect: DOMRect | null): void {
    if (!this.popoverEl) {
      this.popoverEl = document.createElement("div");
      this.popoverEl.id = POPOVER_ID;
      document.body.appendChild(this.popoverEl);
    }
    if (!anchorRect && this.ringEl) {
      this.ringEl.remove();
      this.ringEl = null;
      this.lastRingKey = "";
    }

    // Same idempotency concern as renderRing: only rebuild innerHTML (and
    // rebind its buttons) when the STEP changed, not on every poll/observer
    // tick — an unconditional innerHTML write is a childList mutation of
    // document.body's subtree that would otherwise loop forever.
    if (this.lastPopoverStepId !== step.id) {
      this.lastPopoverStepId = step.id;
      const total = this.manifest.steps.length;
      const policy = effectivePolicy(step);
      const showConfirm = step.kind === "act" && policy === "confirm";
      this.popoverEl.innerHTML = `
        <div style="font-size:.62rem;text-transform:uppercase;letter-spacing:.06em;color:#64748b;margin-bottom:.25rem">Step ${this.index + 1} of ${total}</div>
        <h3>${escapeHtml(step.popover?.title ?? "")}</h3>
        <p>${escapeHtml(step.popover?.body ?? "")}</p>
        <div class="kt-footer">
          <button data-kt-action="skip">Skip tour</button>
          <span class="kt-spacer"></span>
          <button data-kt-action="back" ${this.index === 0 ? "disabled" : ""}>Back</button>
          ${step.kind === "highlight" ? `<button class="kt-primary" data-kt-action="next">${this.index === total - 1 ? "Done" : "Next"}</button>` : ""}
          ${showConfirm ? `<button class="kt-primary" data-kt-action="confirm-act">Confirm</button>` : ""}
        </div>
      `;
      this.popoverEl.querySelector('[data-kt-action="skip"]')?.addEventListener("click", () => this.skip(), { once: true });
      this.popoverEl.querySelector('[data-kt-action="back"]')?.addEventListener("click", () => this.prev(), { once: true });
      this.popoverEl.querySelector('[data-kt-action="next"]')?.addEventListener("click", () => this.next(), { once: true });
      this.popoverEl.querySelector('[data-kt-action="confirm-act"]')?.addEventListener(
        "click",
        () => {
          this.performAct(step);
          this.next();
        },
        { once: true }
      );
    }

    this.positionPopover(step, anchorRect);
  }

  private positionPopover(step: TourStepV2, a: DOMRect | null): void {
    const pop = this.popoverEl;
    if (!pop) return;
    const pw = pop.offsetWidth;
    const ph = pop.offsetHeight;
    const gap = 14;
    const m = 16;
    let top: number;
    let left: number;
    const placement = step.popover?.side ?? (a ? "bottom" : "center");
    if (!a) {
      top = (window.innerHeight - ph) / 2;
      left = (window.innerWidth - pw) / 2;
      if (placement === "right") left = window.innerWidth - pw - m;
      else if (placement === "left") left = m;
      else if (placement === "top") top = m;
      else if (placement === "bottom") top = window.innerHeight - ph - m;
    } else {
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
      }
    }
    const clampM = 12;
    left = Math.max(clampM, Math.min(left, window.innerWidth - pw - clampM));
    top = Math.max(clampM, Math.min(top, window.innerHeight - ph - clampM));
    // Same idempotency rule as renderRing/renderPopover: skip the write
    // when position hasn't moved, so a settled popover stops mutating
    // document.body every poll/observer tick.
    const key = `${top}|${left}`;
    if (key === this.lastPopoverPositionKey) return;
    this.lastPopoverPositionKey = key;
    pop.style.top = `${top}px`;
    pop.style.left = `${left}px`;
  }
  private lastPopoverPositionKey = "";

  private bindAdvance(step: TourStepV2, el: HTMLElement): void {
    if (this.boundAdvanceStepId === step.id) return;
    this.boundAdvanceStepId = step.id;
    this.clickCleanup?.();
    this.clickCleanup = null;
    if (step.kind !== "gate" || step.advanceOn?.event !== "click") return;
    const handler = (): void => this.next();
    el.addEventListener("click", handler, { capture: true, once: true });
    this.clickCleanup = () => el.removeEventListener("click", handler, { capture: true });
  }
  private boundAdvanceStepId: string | null = null;

  private maybePerformAct(step: TourStepV2, el: HTMLElement): void {
    if (step.kind !== "act" || this.actedStepId === step.id) return;
    const policy = effectivePolicy(step);
    if (policy === "confirm") {
      this.emit({ type: "act-pending-confirm", stepId: step.id });
      return; // waits for the popover's Confirm button; not yet "acted"
    }
    this.actedStepId = step.id;
    // watch/auto both perform immediately; "watch" additionally pulses the
    // element so a human observer sees what happened (auto is silent/trusted).
    if (policy === "watch") el.classList.add("kitsoki-tour-acting");
    this.performActOnEl(step, el);
  }

  private performAct(step: TourStepV2): void {
    if (!step.target) return;
    const resolved = resolveAnchor(step.target, step.id);
    this.performActOnEl(step, resolved.el);
  }

  private performActOnEl(step: TourStepV2, el: HTMLElement): void {
    if (!step.act) return;
    switch (step.act.kind) {
      case "click":
        el.click();
        break;
      case "fill":
        if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
          el.value = step.act.value ?? "";
          el.dispatchEvent(new Event("input", { bubbles: true }));
        }
        break;
      case "scroll":
        el.scrollIntoView({ block: "center" });
        break;
      case "press":
        el.dispatchEvent(new KeyboardEvent("keydown", { key: step.act.value, bubbles: true }));
        break;
    }
  }
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c] as string);
}
