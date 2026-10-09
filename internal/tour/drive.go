package tour

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// awaitPromise is a chromedp.EvaluateOption that makes Runtime.evaluate await a
// returned promise before resolving — needed for the async ease() tween, whose
// expression returns a Promise we must block on. chromedp's Evaluate does not
// set this by default for a nil result.
func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// executor runs a step's DriveAction list against the live page over CDP. It is
// the Go port of the Playwright spec helpers (e.g. agent-actions-video.spec.ts:
// typeAndSend, clickIntent, waitForState, revealTurn, ease, dwell). Every gesture is a
// Runtime.evaluate (chromedp.Evaluate) so it avoids browser hit-test flake from
// the tour popover or transient layout shifts — the same reason the spec
// DOM-dispatches `el.click()` rather than a hit-test click.
//
// pace scales every dwell/reveal duration: 0 = instant (fast deterministic
// runs, the WEB_CHAT_PACE=0 posture), 1 = watch speed.
type executor struct {
	ctx  context.Context
	pace float64
}

func newExecutor(ctx context.Context, pace float64) *executor {
	if pace < 0 {
		pace = 0
	}
	return &executor{ctx: ctx, pace: pace}
}

// run executes the step's drive actions in order.
func (e *executor) run(actions []DriveAction) error {
	for i, a := range actions {
		if err := e.one(a); err != nil {
			return fmt.Errorf("drive[%d] %s: %w", i, a.Type, err)
		}
	}
	return nil
}

func (e *executor) one(a DriveAction) error {
	switch a.Type {
	case DriveTypeAndSend:
		return e.typeAndSend(a.Text)
	case DriveClickIntent:
		return e.clickIntent(a.Intent)
	case DriveWaitState:
		return e.waitForState(a.State, 20*time.Second)
	case DriveRevealTurn:
		return e.revealTurn()
	case DriveDwellMs:
		return e.dwell(a.Ms)
	default:
		return fmt.Errorf("unknown drive type %q", a.Type)
	}
}

// dwell holds on the current frame for ms, pace-scaled. The single pacing
// primitive (server.ts dwell). A non-positive scaled duration is a no-op.
func (e *executor) dwell(ms int) error {
	scaled := time.Duration(float64(ms)*e.pace) * time.Millisecond
	if scaled <= 0 {
		return nil
	}
	select {
	case <-time.After(scaled):
		return nil
	case <-e.ctx.Done():
		return e.ctx.Err()
	}
}

// typeAndSend fills the composer with text and clicks send (the tour spec
// typeAndSend). The fill dispatches an input event so the Vue v-model updates
// and the send button enables, then a short settle, then the send click.
//
// A room that shows a choice/form widget hides the primary composer but still
// exposes the free-text "text floor" beneath it (InputBar.vue, data-testid
// text-floor-*) — the text-only contract says every room is drivable by typing
// (docs/architecture/transports.md §7). So the gestures fall back to the floor
// when the primary composer is absent: composer-input → text-floor-input,
// composer-send → text-floor-send. Both route through the same sendRaw semantic
// off-ramp, so the keystrokes are faithful either way.
func (e *executor) typeAndSend(text string) error {
	fill := fmt.Sprintf(`(() => {
  const input = document.querySelector('[data-testid="composer-input"]')
             || document.querySelector('[data-testid="text-floor-input"]');
  if (!input) return false;
  input.focus();
  input.value = %q;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
})()`, text)
	var ok bool
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(fill, &ok)); err != nil {
		return fmt.Errorf("fill composer: %w", err)
	}
	if !ok {
		return fmt.Errorf("composer-input not found (no composer or text floor)")
	}
	// Let v-model settle so the send button enables (spec waits 200ms here).
	if err := e.sleepRaw(200 * time.Millisecond); err != nil {
		return err
	}
	send := `(() => {
  const btn = document.querySelector('[data-testid="composer-send"]')
           || document.querySelector('[data-testid="text-floor-send"]');
  if (!btn) return false;
  btn.click();
  return true;
})()`
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(send, &ok)); err != nil {
		return fmt.Errorf("click send: %w", err)
	}
	if !ok {
		return fmt.Errorf("composer-send not found (no composer or text floor)")
	}
	return nil
}

// clickIntent clicks the intent-btn-<intent> control (the tour spec clickIntent).
// It scrolls the button into view first, then DOM-dispatches the click.
func (e *executor) clickIntent(intent string) error {
	js := fmt.Sprintf(`(() => {
  const btn = document.querySelector('[data-testid="intent-btn-%s"]');
  if (!btn) return false;
  btn.scrollIntoView({ block: 'center' });
  btn.click();
  return true;
})()`, intent)
	var ok bool
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return fmt.Errorf("click intent %q: %w", intent, err)
	}
	if !ok {
		return fmt.Errorf("intent button %q not found", intent)
	}
	return nil
}

// waitForState polls the interactive view's current-state testid until it
// equals state, mirroring the spec's waitForState (a DOM read, not an RPC). The
// state is the deterministic, no-LLM settle point a flow turn reaches.
func (e *executor) waitForState(state string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	js := `(() => {
  const el = document.querySelector('[data-testid="current-state"]');
  return el ? (el.textContent || '').trim() : '';
})()`
	for time.Now().Before(deadline) {
		var cur string
		if err := chromedp.Run(e.ctx, chromedp.Evaluate(js, &cur)); err != nil {
			return fmt.Errorf("read current-state: %w", err)
		}
		if cur == state {
			return nil
		}
		if err := e.sleepRaw(300 * time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("wait-state %q timed out after %s", state, timeout)
}

// revealTurn eases the last operator input to the top of the chat, holds, then
// eases down through the reply — the per-turn reading rhythm (the tour spec
// revealTurn). It installs a scroll-control shim on first use (neutering the
// chat's instant auto-scroll-to-bottom) exactly as the spec does, then drives
// the tween in-page. Durations are pace-scaled.
func (e *executor) revealTurn() error {
	if err := e.ensureScrollControl(); err != nil {
		return err
	}
	// Let the turn's rows render (spec dwells SETTLE_MS = 1400 here).
	if err := e.dwell(1400); err != nil {
		return err
	}
	// 1. Ease the new operator input to the top; hold.
	var top float64
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(`window.__lastUserTop ? window.__lastUserTop() : 0`, &top)); err != nil {
		return err
	}
	if err := e.ease(top, e.paced(1200)); err != nil {
		return err
	}
	if err := e.dwell(1300); err != nil {
		return err
	}
	// 2. Ease down through the reply; hold. Duration tracks the scroll span.
	var max float64
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(`window.__scrollMax ? window.__scrollMax() : 0`, &max)); err != nil {
		return err
	}
	span := max - top
	if span < 0 {
		span = 0
	}
	downMs := int(span * 3)
	if downMs < 700 {
		downMs = 700
	}
	if downMs > 3000 {
		downMs = 3000
	}
	if err := e.ease(max, e.paced(downMs)); err != nil {
		return err
	}
	return e.dwell(1500)
}

// paced scales ms by pace (rounded), the spec's `paced` helper.
func (e *executor) paced(ms int) int { return int(float64(ms) * e.pace) }

// ease drives an in-page easeInOutQuad scroll tween over ms on the
// chat-transcript, blocking until it resolves (spec ease + __ease). A
// non-positive ms snaps instantly.
func (e *executor) ease(to float64, ms int) error {
	js := fmt.Sprintf(`(async () => {
  if (window.__ease) { await window.__ease(%f, %d); }
})()`, to, ms)
	return chromedp.Run(e.ctx, chromedp.Evaluate(js, nil, awaitPromise))
}

// ensureScrollControl installs the spec's scroll-control shim once: it neuters
// the chat component's instant auto-scroll-to-bottom and exposes __ease /
// __lastUserTop / __scrollMax for revealTurn to drive. Idempotent.
func (e *executor) ensureScrollControl() error {
	js := `(() => {
  const el = document.querySelector('[data-testid="chat-transcript"]');
  if (!el) return false;
  if (el.__nat) return true;
  el.__nat = true;
  const desc = Object.getOwnPropertyDescriptor(Element.prototype, 'scrollTop');
  const realGet = () => desc.get.call(el);
  const realSet = (v) => desc.set.call(el, v);
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get() { return realGet(); },
    set() { /* ignored — natural scroll is driven via __ease */ },
  });
  window.__ease = (to, ms) => new Promise((res) => {
    const from = realGet();
    const max = el.scrollHeight - el.clientHeight;
    const target = Math.max(0, Math.min(to, max));
    if (ms <= 0 || Math.abs(target - from) < 2) { realSet(target); return res(); }
    const t0 = performance.now();
    const tick = (now) => {
      const p = Math.min(1, (now - t0) / ms);
      const eased = p < 0.5 ? 2 * p * p : 1 - Math.pow(-2 * p + 2, 2) / 2;
      realSet(from + (target - from) * eased);
      if (p < 1) requestAnimationFrame(tick); else res();
    };
    requestAnimationFrame(tick);
  });
  window.__lastUserTop = () => {
    const rows = el.querySelectorAll('[data-testid="chat-row-user"]');
    const last = rows[rows.length - 1];
    return last ? Math.max(0, last.offsetTop - 16) : el.scrollHeight;
  };
  window.__scrollMax = () => el.scrollHeight - el.clientHeight;
  return true;
})()`
	var ok bool
	if err := chromedp.Run(e.ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return fmt.Errorf("install scroll control: %w", err)
	}
	return nil
}

// sleepRaw is an UN-paced sleep (for fixed protocol settles like the v-model
// beat), context-aware.
func (e *executor) sleepRaw(d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-e.ctx.Done():
		return e.ctx.Err()
	}
}
