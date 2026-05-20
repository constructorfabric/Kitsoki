// Package orchestrator — RoomEnterSink hook
//
// The transcript pane should show the room's banner the moment we
// arrive in a new room, BEFORE the room's on_enter chain starts
// firing tool-call breadcrumbs (oracle / Bash / Grep / Read). The
// banner is part of the room's view: block — and the view doesn't
// render until the turn completes — so without this hook the user
// sees "→ in-room: core__continue" followed by a long stream of
// tool calls, and only later (when the turn finally lands) does the
// banner appear at the top of the final response.
//
// This file gives a live TUI a way to subscribe to "I just entered a
// new room" mid-turn. The orchestrator fires the sink with a
// pre-rendered banner string the moment machine.Turn returns (the
// transition is applied; the on_enter side-effects are collected;
// host calls haven't been dispatched yet). The sink implementation
// is in internal/tui; the orchestrator stays transport-agnostic.

package orchestrator

import (
	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render/elements"
)

// RoomEnterSink receives a pre-rendered banner string when a turn
// transitions into a new room (top-level state change). Banner is
// the styled ANSI output produced by the typed-element pipeline
// against the new state's banner: element — empty when the state
// declares no banner.
//
// Called from the orchestrator's turn path, after machine.Turn
// applies the transition and BEFORE host calls run. Sink implementations
// MUST NOT block — host call dispatch happens immediately after the
// hook returns, so a slow sink stalls the turn for the user.
type RoomEnterSink interface {
	OnRoomEnter(state app.StatePath, banner string)
}

// WithRoomEnterSink installs a RoomEnterSink so the orchestrator can
// notify a live TUI when a turn lands in a new room. The orchestrator
// extracts the room's banner element (if any), pre-renders it at a
// banner-friendly width, and hands the string to the sink before
// running on_enter.
//
// Passing nil clears any previously-installed sink. Headless callers
// (tests, OneShot) typically leave this unwired.
func WithRoomEnterSink(s RoomEnterSink) Option {
	return func(o *Orchestrator) {
		o.roomEnterSink = s
	}
}

// renderRoomBanner pulls the banner element out of state.View (if
// any) and runs it through the elements dispatcher to produce a
// styled ANSI string ready to print. Returns "" when:
//
//   - state is nil or has no view,
//   - the view declares no banner element,
//   - the banner element's `text:` is empty (defensive).
//
// World vars are threaded into env so a banner's pongo2 template
// strings can interpolate runtime state (rarely needed, but
// consistent with how every other element renders).
//
// Width: roomBannerWidth (a wide-enough default that the divider
// strips look like banners; the TUI re-flows narrow viewports via
// its own wrap budget when the message lands).
const roomBannerWidth = 120

func renderRoomBanner(def *app.AppDef, state *app.State, env expr.Env) string {
	if state == nil || state.View.IsEmpty() {
		return ""
	}
	els := bannerElementSources(state.View)
	if len(els) == 0 {
		return ""
	}
	// Build a synthetic single-element view so RenderAll runs the
	// banner renderer for us — keeps the styling logic in one place
	// (internal/render/elements/banner.go) rather than duplicating it
	// here. Glamour is identity: the banner is meant to land in the
	// transcript pane as a pre-styled message, not be markdown-
	// reprocessed.
	subView := app.View{Elements: els}
	out, err := elements.RenderAll(subView, env, roomBannerWidth, elements.IdentityGlamour, nil)
	if err != nil {
		return ""
	}
	return out
}

// bannerElementSources collects the banner elements from view —
// looking BOTH at the flat Elements list (un-extended views) and at
// the Blocks map (the extends/blocks shape used by every bugfix
// room). Returns the matched elements in document order. Returns nil
// when no banner is declared anywhere in the view.
//
// We always look in Blocks["body"] for extends-shape views because
// that's where the bugfix rooms put their banners; if a future
// authoring convention puts banners elsewhere this helper widens.
func bannerElementSources(v app.View) []app.ViewElement {
	var out []app.ViewElement
	for _, el := range v.Elements {
		if el.Kind == "banner" {
			out = append(out, el)
		}
	}
	if v.Blocks != nil {
		// Stable iteration: walk by author-canonical block names.
		// Body is the conventional carrier; we intentionally don't
		// scan choices/status/footer because banners belong with the
		// room's body content.
		for _, el := range v.Blocks["body"] {
			if el.Kind == "banner" {
				out = append(out, el)
			}
		}
	}
	return out
}
