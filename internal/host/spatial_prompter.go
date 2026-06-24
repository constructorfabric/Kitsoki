// Package host — spatial-input forwarding seam (docs/tui/spatial-handoff.md).
//
// Sibling to operator_prompter.go. Where OperatorPrompter forwards a
// multiple-choice question to the live operator surface and blocks for the
// answer, SpatialPrompter forwards a "point at the screen" request and blocks
// for a VisualAmbient bundle — the frame the operator was looking at, the pixel
// they pointed at, and the element under it.
//
// The terminal can't render pixels, so the TUI implementation does not draw a
// picker: it prints an OSC 8 clickable link to a transient, one-time-token
// `/point` web window (the slice-2 SpatialPicker rendered chrome-less), shows a
// blocking "pointing… ↗ open in browser" status, and blocks the turn until the
// browser POSTs the bundle back through the return endpoint — exactly the
// operator-ask "block the turn on operator input" pattern, where the "answer"
// is a spatial bundle instead of an option label.
//
// Interactivity is detected by the PRESENCE of a prompter in context, mirroring
// OperatorPrompterFrom: the TUI/web run loop installs one via
// WithSpatialPrompter; non-interactive callers (cassette replay, flow fixtures,
// headless) install none and AskSpatial is never reached — a spatial ambient
// simply isn't requested, and the turn proceeds text-only.
package host

import "context"

// SpatialRequest is the "point at something" ask forwarded to the operator
// surface. It carries the context the window needs to display the right frame:
// which media/route to open on, and an optional timestamp within it. All fields
// are optional — an empty request opens the window on the live run view (the
// proposal's open-question-4 lean).
type SpatialRequest struct {
	// Prompt is the human-readable reason the agent wants a point (e.g. "point
	// at the control you mean"). Shown in the terminal status and the window.
	Prompt string
	// MediaHandle is the source media the window should display, as an artifact
	// handle or path. Empty opens the window on the live run view.
	MediaHandle string
	// Route is the UI route/URL to open the window on. Empty opens the run view.
	Route string
	// TMs is the timestamp within the media to seek to, in milliseconds.
	TMs int
}

// SpatialPrompter forwards a "point at the screen" request to the live operator
// surface and blocks until the operator submits a bundle (or the wait is
// cancelled / times out / no browser is reachable).
//
// A returned error means no usable bundle was obtained (operator abandoned, the
// surface couldn't reach a browser, ctx done) — the caller degrades to "spatial
// input unavailable, proceed text-only" rather than hard-blocking forever, the
// same posture an unanswered operator question takes.
//
// Concurrency contract mirrors OperatorPrompter.Ask: AskSpatial is called from
// the turn goroutine and MUST honour ctx cancellation (the whole turn blocks
// inside it).
type SpatialPrompter interface {
	AskSpatial(ctx context.Context, sessionID string, req SpatialRequest) (VisualAmbient, error)
}

// spatialPrompterKey is the context key for a per-session SpatialPrompter.
type spatialPrompterKey struct{}

// WithSpatialPrompter returns a child context carrying prompter. The TUI/web run
// loop installs one so the spatial-ambient request can reach a live operator. A
// nil prompter is a no-op — returns ctx unchanged so the headless "no spatial
// input" posture is the default for callers that never set one.
func WithSpatialPrompter(ctx context.Context, prompter SpatialPrompter) context.Context {
	if prompter == nil {
		return ctx
	}
	return context.WithValue(ctx, spatialPrompterKey{}, prompter)
}

// SpatialPrompterFrom returns the prompter installed in ctx and whether one was
// present. The boolean is the interactivity signal: present ⇒ a spatial ambient
// can be requested; absent ⇒ the turn proceeds text-only.
func SpatialPrompterFrom(ctx context.Context) (SpatialPrompter, bool) {
	p, ok := ctx.Value(spatialPrompterKey{}).(SpatialPrompter)
	return p, ok && p != nil
}
