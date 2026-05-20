// Package elements implements the per-element renderers and the dispatcher
// that turns a typed app.View into a final string for the TUI transcript
// (Phase D of the view-elements proposal in
// docs/proposals/view-elements-proposal.md §4 / §7).
//
// # Pipeline
//
// For each element in view.Elements:
//
//  1. If the element carries a `when:` guard (expr-lang source), compile-
//     and-evaluate it. A false result drops the element entirely (no blank
//     stub row in a list, no extra blank line between siblings).
//  2. Every leaf string on the element (prose / heading / code / template
//     body, list-item label and hint, kv pair value) is rendered through
//     render.Pongo BEFORE the element-kind renderer sees it. Element
//     renderers operate on concrete text, never on un-substituted templates.
//  3. The element-kind renderer (prose / heading / list / kv / code /
//     template) lays the rendered leaves out at the supplied width.
//
// Element outputs are joined with one blank line between adjacent
// elements. Two consecutive `kv` elements coalesce into a single block
// with no blank line in between, matching the proposal §5.3 hint that
// same-kind kv neighbours read more naturally as one table.
//
// # `template` kind — escape hatch
//
// The `template` kind is the escape hatch into today's Glamour pipe. The
// dispatcher delegates back into the caller-supplied Glamour callback
// (see RenderAll's `glamour` parameter) so the transcript model keeps
// owning the Glamour renderer (it's terminal-style-aware, expensive to
// rebuild, and needs to coexist with preserveLeadingIndent). Tests pass
// an identity callback to inspect the post-Pongo source without invoking
// Glamour.
//
// # Backward compatibility
//
// The orchestrator's pre-Phase-D pipeline still renders views to a string
// before they reach the TUI. Today every state's View carries a single
// {Kind: "template", Source: <legacy markdown>} element, so the
// dispatcher's net effect is identical to today's path — the legacy
// string flows through the template renderer and is handed to Glamour
// verbatim. Phase E/F migrate apps to typed elements; only then does the
// lipgloss-based layout kick in.
//
// # `when:` guard cache
//
// Per-element `when:` guards are compiled lazily and cached in a global
// sync.Map keyed by the raw expression source. The cache is shared
// across all views (a guard string like "available('start_journey')" used
// by two different rooms compiles once). This matches the existing
// guard-compilation cache in internal/expr (see anyProgCache /
// boolProgCache).
package elements

import (
	"fmt"
	"strings"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// GlamourFunc is the callback used by the `template` element kind to
// delegate styling back to the caller's Glamour pipeline. The transcript
// model owns its glamour.TermRenderer and the preserveLeadingIndent
// helper; the elements package never imports glamour directly.
//
// Tests pass an identity function (the post-Pongo source unchanged) so
// they can assert against template-leaf substitution without depending
// on a TTY-aware Glamour renderer.
type GlamourFunc func(rendered string) string

// IdentityGlamour is a GlamourFunc that returns its input verbatim. The
// default fallback when the caller passes nil — tests use it directly.
func IdentityGlamour(s string) string { return s }

// ViewRenderer is the per-app loader-aware pongo2 renderer the dispatcher
// threads through to every element renderer. Element renderers call
// renderLeaf (the helper below) which delegates to this interface when
// non-nil — so {% include "partials/x.pongo" %} inside a kv value or a
// code body resolves through the app's per-app loader rooted at
// <appDir>/views/, not against the loader-less package-level
// render.Pongo (which would fail with a "no such template" error).
//
// *render.AppRenderer implements this interface via its Render method;
// tests can supply a mock or a nil to fall back to the package-level
// render.Pongo behaviour (no loader, fine for pure inline templates).
//
// RenderExtended is the typed-extends companion: when RenderAll is
// given a View with `extends:` set, it pre-renders each block at the
// dispatcher's width and then asks the ViewRenderer to splice them
// into the base template. Tests can pass an extendsCapable mock or
// stay on the legacy path by passing nil (RenderAll will refuse the
// extends shape with a clear error).
type ViewRenderer interface {
	Render(src string, env expr.Env) (string, error)
	RenderExtended(extends string, blocks map[string]string, env expr.Env) (string, error)
}

// renderLeaf is the single seam every element renderer routes its leaf-
// string substitution through. When the dispatcher supplied a renderer,
// the leaf renders against the per-app loader (so {% include %} /
// {% extends %} resolve). Otherwise the package-level loader-less
// render.Pongo fast path is used — preserving the existing test surface
// for code paths that don't care about per-app templates.
func renderLeaf(r ViewRenderer, src string, env expr.Env) (string, error) {
	if r == nil {
		return render.Pongo(src, env)
	}
	return r.Render(src, env)
}

// Renderer is the per-kind layout strategy interface. Each element kind
// implements it; the dispatcher invokes Render with the available
// horizontal width, the expr.Env (leaf strings may carry pongo2
// templates), and the per-app ViewRenderer (so leaf substitution
// resolves {% include %} against <appDir>/views/).
type Renderer interface {
	Render(width int, env expr.Env, rr ViewRenderer) (string, error)
}

// RenderAll renders a typed View at the supplied viewport width. Returns
// the joined element output. The Glamour callback is invoked for any
// `template` element; pass IdentityGlamour (or nil — it is normalised) in
// non-TUI contexts. The ViewRenderer is the per-app loader-aware pongo2
// renderer (e.g. *render.AppRenderer) used to expand leaf strings; pass
// nil to fall back to the loader-less package-level render.Pongo (the
// existing test surface for views that don't use {% include %} /
// {% extends %} inside leaves).
//
// Behaviour summary:
//
//   - When view.IsEmpty() → returns "" with no error.
//   - When view.Extends != "" → pre-renders each block at the supplied
//     `width` (recursive RenderAll), splices the rendered blocks into
//     the base template via rr.RenderExtended, then applies `glamour`
//     to the composite. Returns "" with no error when rr is nil
//     (legacy callers that haven't wired the typed-extends path).
//   - Otherwise iterates view.Elements: guard-filters, pongo-expands
//     leaf strings, dispatches to the kind-specific renderer, and joins
//     with the standard spacing.
//
// The extends-aware branch is what lets the TUI re-render typed-extends
// views at the real viewport width (instead of the orchestrator-side
// blockRenderWidth=80 default that the machine pre-renders with). Before
// this branch existed, the TUI fell back to the 80-wide pre-render, so
// a single wide label in any block clamped the hint column to ~25 chars
// even on a 150-col terminal. See stories/dev-story/rooms/main.yaml:62
// for the wide-label canary.
func RenderAll(view app.View, env expr.Env, width int, glamour GlamourFunc, rr ViewRenderer) (string, error) {
	if glamour == nil {
		glamour = IdentityGlamour
	}
	if view.IsEmpty() {
		return "", nil
	}
	if view.Extends != "" {
		// Legacy entry points (RenderAll with rr=nil) can't resolve
		// the base template — fall back to the pre-typed-extends
		// behaviour (empty string, caller's fallback path takes over).
		if rr == nil {
			return "", nil
		}
		blocks := make(map[string]string, len(view.Blocks))
		for name, els := range view.Blocks {
			sub := app.View{Elements: els}
			body, err := RenderAll(sub, env, width, glamour, rr)
			if err != nil {
				return "", fmt.Errorf("render block %q: %w", name, err)
			}
			blocks[name] = body
		}
		composite, err := rr.RenderExtended(view.Extends, blocks, env)
		if err != nil {
			return "", fmt.Errorf("render extends %q: %w", view.Extends, err)
		}
		// Glamour styles the composite (markdown chrome from base.pongo
		// + the plain-text element bodies). For the IdentityGlamour
		// case (machine.renderViewBody, trace dumps, OneShot) this is a
		// no-op, preserving the pre-typed-extends output verbatim.
		return glamour(composite), nil
	}

	parts := make([]string, 0, len(view.Elements))
	kinds := make([]string, 0, len(view.Elements))
	for i, el := range view.Elements {
		// Step 1 — `when:` guard. A failing guard suppresses the element
		// entirely; the element does not contribute spacing.
		keep, err := evalWhen(el.When, env)
		if err != nil {
			return "", fmt.Errorf("view[%d] (%s) when: %w", i, el.Kind, err)
		}
		if !keep {
			continue
		}

		// Step 2/3 — pongo-expand leaves and dispatch.
		out, err := renderOne(el, env, width, glamour, rr)
		if err != nil {
			return "", fmt.Errorf("view[%d] (%s): %w", i, el.Kind, err)
		}
		if out == "" {
			continue
		}
		parts = append(parts, out)
		kinds = append(kinds, el.Kind)
	}

	return joinElements(parts, kinds), nil
}

// renderOne dispatches a single element through its kind's renderer.
// Leaf-string substitution (renderLeaf on prose/heading/code/template
// bodies, on list item labels & hints, on kv values) happens inside each
// concrete renderer so the dispatch site stays kind-agnostic.
func renderOne(el app.ViewElement, env expr.Env, width int, glamour GlamourFunc, rr ViewRenderer) (string, error) {
	switch el.Kind {
	case "prose":
		return Prose{Source: el.Source}.Render(width, env, rr)
	case "heading":
		return Heading{Source: el.Source}.Render(width, env, rr)
	case "list":
		return List{Items: el.Items, Marker: el.Marker}.Render(width, env, rr)
	case "kv":
		return KV{Pairs: el.Pairs}.Render(width, env, rr)
	case "code":
		return Code{Source: el.Source}.Render(width, env, rr)
	case "template":
		return Template{Source: el.Source, Glamour: glamour}.Render(width, env, rr)
	case "banner":
		return Banner{Source: el.Source, Subtitle: el.Subtitle, Color: el.Color}.Render(width, env, rr)
	default:
		return "", fmt.Errorf("unknown element kind %q", el.Kind)
	}
}

// joinElements joins rendered element strings with the inter-element
// spacing policy from proposal §5.3:
//
//   - One blank line between adjacent elements by default.
//   - Two adjacent `kv` elements coalesce — no blank line between them.
//     Authors often split a single logical "status block" across two
//     kv: declarations (e.g. world stats above, slot stats below) and
//     expect them to read as one column.
func joinElements(parts, kinds []string) string {
	if len(parts) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, p := range parts {
		if i > 0 {
			if kinds[i] == "kv" && kinds[i-1] == "kv" {
				sb.WriteString("\n")
			} else {
				sb.WriteString("\n\n")
			}
		}
		sb.WriteString(p)
	}
	return sb.String()
}

// ---- when: guard cache ------------------------------------------------------

// whenCache caches compiled `when:` programs keyed by the raw expression
// source. The cache is process-global so the same guard string used by
// two rooms compiles once. Mirrors the pattern in internal/expr's
// anyProgCache / boolProgCache — keying on source means duplicate
// expressions (very common — "available('foo')" repeats across rooms)
// share a compiled program without explicit registration.
var whenCache sync.Map // map[string]*expr.Program

// evalWhen evaluates an optional element-level `when:` guard. An empty
// guard means "always render" and returns true. A non-empty guard is
// compiled (or fetched from cache) and evaluated as a bool. Non-bool
// results fall through expr.EvalBool's error handling.
func evalWhen(src string, env expr.Env) (bool, error) {
	if src == "" {
		return true, nil
	}
	if p, ok := whenCache.Load(src); ok {
		return expr.EvalBool(p.(*expr.Program), env)
	}
	p, err := expr.CompileBool(src)
	if err != nil {
		return false, err
	}
	whenCache.Store(src, p)
	return expr.EvalBool(p, env)
}
