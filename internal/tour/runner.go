package tour

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// startTour injects the steps and starts the overlay via the SPA's
// window.__startTourWithSteps hook (the same entrypoint every Playwright video
// spec uses), then waits for the overlay to mount.
func startTour(ctx context.Context, steps []TourStep) error {
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return fmt.Errorf("marshal steps: %w", err)
	}
	// __startTourWithSteps takes a JSON STRING (it JSON.parses internally), so
	// pass the steps document as a JS string literal: json-encode it again.
	stepsArg, err := json.Marshal(string(stepsJSON))
	if err != nil {
		return fmt.Errorf("encode steps arg: %w", err)
	}
	js := fmt.Sprintf(`(() => {
  if (typeof window.__startTourWithSteps !== 'function') return false;
  window.__startTourWithSteps(%s);
  return true;
})()`, string(stepsArg))
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return fmt.Errorf("start tour: %w", err)
	}
	if !ok {
		return fmt.Errorf("__startTourWithSteps hook not present (SPA unbuilt?)")
	}
	// Wait for the overlay to appear (8s, matching the spec).
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return chromedp.Run(tctx, chromedp.WaitVisible(`[data-testid="tour-overlay"]`, chromedp.ByQuery))
}

// walkSteps drives the tour step-by-step, the Go port of the the agent-actions
// spec loop: per step it (1) honors the route guard, (2) executes the step's
// drive[] actions (the self-driving data), (3) waits for waitForTarget, (4)
// asserts the popover title, (5) opens the chapter + holds + captures a PNG, then
// (6) advances (Next for explain; click target for action). Returns the per-step
// PNG paths.
func walkSteps(ctx context.Context, cfg Config, exec *executor, chapters *chapterRecorder, cap *screencastCapturer) ([]StepShot, error) {
	var shots []StepShot
	pngIdx := 0
	for i, step := range cfg.Manifest.Steps {
		// (1) Route guard — skip a step whose route we are not on (the overlay
		// holds it too). "any" always runs.
		routeKind, err := currentRouteKind(ctx)
		if err != nil {
			return shots, err
		}
		if step.Route != "any" && step.Route != routeKind {
			continue
		}

		// Interactive beats drive + reveal their whole conversation, so open the
		// chapter NOW so its window spans the conversation (the tour spec pattern).
		if step.Route == "interactive" {
			chapters.open(step.ID, step.Title)
		}

		// (2) Self-driving actions (the data form of the spec's pre-step setup).
		if err := exec.run(step.Drive); err != nil {
			return shots, fmt.Errorf("step %q drive: %w", step.ID, err)
		}

		// (3) DOM-presence precondition.
		if step.WaitForTarget != "" {
			wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := chromedp.Run(wctx, chromedp.WaitVisible(targetSelector(step.WaitForTarget), chromedp.ByQuery))
			cancel()
			if err != nil {
				return shots, fmt.Errorf("step %q waitForTarget %q: %w", step.ID, step.WaitForTarget, err)
			}
		}

		// (4) Anti-drift: the popover must show THIS step's title.
		if err := assertTitle(ctx, step.Title); err != nil {
			return shots, fmt.Errorf("step %q: %w", step.ID, err)
		}

		// (5) Non-interactive steps open their chapter, hold, and capture here.
		// Interactive steps already opened theirs and revealed each turn.
		if step.Route != "interactive" {
			chapters.open(step.ID, step.Title)
			dwellMs := step.DwellMs
			if dwellMs == 0 {
				dwellMs = 3000
			}
			if err := exec.dwell(dwellMs); err != nil {
				return shots, err
			}
		}
		pngIdx++
		png, err := capturePNG(ctx, cfg.OutDir, pngIdx, step.ID)
		if err == nil {
			// Record the deterministic spec reference for this capture: every
			// precondition above (drive wait-states, waitForTarget, assertTitle)
			// has passed, so the screenshot provably depicts spec step i.
			shots = append(shots, cfg.Manifest.stepShot(pngIdx, i, filepath.Base(png), step))
		}

		// (6) Advance.
		if err := advance(ctx, exec, step); err != nil {
			return shots, fmt.Errorf("step %q advance: %w", step.ID, err)
		}
	}
	return shots, nil
}

// advance moves past a step: explain → click Next; action → click the target.
func advance(ctx context.Context, exec *executor, step TourStep) error {
	if step.Kind == "explain" {
		js := `(() => { const b = document.querySelector('[data-testid="tour-next"]'); if (!b) return false; b.click(); return true; })()`
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("tour-next not present")
		}
		return exec.dwell(700)
	}
	// action: DOM-dispatch the click on the target so it fires through the
	// overlay backdrop (spec pattern), then settle. route-match steps wait for
	// the URL to change.
	if step.Target != "" {
		js := fmt.Sprintf(`(() => { const t = document.querySelector(%q); if (!t) return false; t.scrollIntoView({block:'center'}); t.click(); return true; })()`, targetSelector(step.Target))
		var ok bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("target %q not present", step.Target)
		}
	}
	if step.Advance == "route-match" {
		if err := waitRouteChange(ctx, step.AdvanceRoute, 15*time.Second); err != nil {
			return err
		}
	}
	return exec.dwell(1000)
}

// currentRouteKind classifies the SPA hash route the same way the spec does:
// "interactive" on /chat, "any" on the bare observer (/s/<uuid>), else "home".
func currentRouteKind(ctx context.Context) (string, error) {
	var href string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.href`, &href)); err != nil {
		return "", err
	}
	switch {
	case strings.Contains(href, "/chat"):
		return "interactive", nil
	case observerHashRE.MatchString(href):
		return "any", nil
	default:
		return "home", nil
	}
}

// waitRouteChange waits until the hash route matches the advance target. An
// "interactive" advance lands on /chat; "any" on the bare observer.
func waitRouteChange(ctx context.Context, advanceRoute string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		kind, err := currentRouteKind(ctx)
		if err != nil {
			return err
		}
		if advanceRoute == "" || kind == advanceRoute || (advanceRoute == "interactive" && kind == "interactive") {
			return nil
		}
		if advanceRoute == "any" && (kind == "any" || kind == "interactive") {
			return nil
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("route never became %q", advanceRoute)
}

// assertTitle reads the tour-title testid and verifies it equals want, with a
// short retry to let the spotlight animation settle (spec uses toHaveText with a
// timeout). A drift onto a LATER step's title is tolerated by the caller's loop
// structure (we re-read each iteration), so here we only retry the match.
func assertTitle(ctx context.Context, want string) error {
	deadline := time.Now().Add(12 * time.Second)
	js := `(() => { const el = document.querySelector('[data-testid="tour-title"]'); return el ? (el.textContent||'').trim() : ''; })()`
	var got string
	for time.Now().Before(deadline) {
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &got)); err != nil {
			return err
		}
		if got == want {
			return nil
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("tour title never became %q (last %q)", want, got)
}

// capturePNG screenshots the page to <outDir>/NN-<id>.png, matching the spec's
// numbered makeShot output the contact-sheet scripts consume.
func capturePNG(ctx context.Context, outDir string, idx int, id string) (string, error) {
	var buf []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return "", err
	}
	name := filepath.Join(outDir, fmt.Sprintf("%02d-%s.png", idx, id))
	if err := writeFileAtomic(name, buf); err != nil {
		return "", err
	}
	return name, nil
}

// targetSelector resolves a testid to its [data-testid="..."] selector.
func targetSelector(testid string) string {
	return fmt.Sprintf(`[data-testid="%s"]`, testid)
}
