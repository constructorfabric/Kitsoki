// Package host — prompt rendering seam for the oracle verb handlers.
//
// Prompt extension: when the orchestrator injects a prompt renderer (built
// from a story's overlay → story search path), the oracle handlers resolve a
// prompt reference overlay-first and render it through a pongo2.TemplateSet so
// {% extends %} / {% include %} and the @story / @shared namespaces resolve —
// the same machinery views already use. When no renderer is in ctx (CLI
// one-shots, tests, the legacy path) resolution and rendering fall back to the
// pre-extension behavior (KITSOKI_APP_DIR join + render.Pongo), byte-identical
// for a story with no overlay and no blocks. See docs/stories/prompts.md.
package host

import (
	"context"
	"os"
	"strings"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// promptRendererKey is the unexported context key for the injected prompt
// renderer.
type promptRendererKey struct{}

// WithPromptRenderer injects a prompt renderer into ctx so the oracle handlers
// resolve and render prompt files across the story's overlay → story search
// path. Passing nil is safe — handlers then use the legacy path. The renderer
// is built by the orchestrator per run (it knows the story base dir, the
// optional overlay, and shared dirs).
func WithPromptRenderer(ctx context.Context, r *render.AppRenderer) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, promptRendererKey{}, r)
}

// PromptRendererFromCtx returns the prompt renderer previously injected with
// WithPromptRenderer, or nil when none was injected.
func PromptRendererFromCtx(ctx context.Context) *render.AppRenderer {
	if v, ok := ctx.Value(promptRendererKey{}).(*render.AppRenderer); ok {
		return v
	}
	return nil
}

// resolvePromptPathCtx resolves a prompt reference to an absolute path,
// overlay-first when a prompt renderer is in ctx and the reference resolves on
// its search path. Otherwise it falls back to resolvePromptPath (KITSOKI_APP_DIR
// join), so absolute paths, off-search-path references, and the no-renderer
// case behave exactly as before.
func resolvePromptPathCtx(ctx context.Context, p string) string {
	if pr := PromptRendererFromCtx(ctx); pr != nil {
		if abs, ok := pr.ResolvePromptName(p); ok {
			return abs
		}
	}
	return resolvePromptPath(p)
}

// renderPromptBytes renders prompt source against the Args-only template scope.
// When a prompt renderer is in ctx the source is rendered through its
// TemplateSet so {% extends %} / {% include %} and @story / @shared resolve;
// otherwise it uses the package-level render.Pongo (no template root). Both
// apply the same `??` rewrite and fast path, so the no-renderer result is
// byte-identical to the pre-extension path.
func renderPromptBytes(ctx context.Context, src string, templateArgs map[string]any) (string, error) {
	env := expr.Env{Args: templateArgs}
	if pr := PromptRendererFromCtx(ctx); pr != nil {
		return pr.Render(src, env)
	}
	return render.Pongo(src, env)
}

// promptTraceProvenance returns the active overlay dir and the spec_ block
// provenance (which of the base's spec_ blocks the overlay overrode vs. left
// defaulted) for a prompt reference, for recording on the oracle.call event.
// All-empty when there's no prompt renderer/overlay or ref isn't an overlaid
// story prompt — so callers can attach the results unconditionally.
func promptTraceProvenance(ctx context.Context, ref string) (overlay string, defaulted, overridden []string) {
	pr := PromptRendererFromCtx(ctx)
	if pr == nil {
		return "", nil, nil
	}
	overlay = pr.OverlayDir()
	defaulted, overridden = pr.SpecProvenance(strings.TrimSpace(ref))
	return overlay, defaulted, overridden
}

// readPromptFile is a thin wrapper kept next to the render seam so call sites
// read through one place; it exists mainly to document that prompt files are
// read whole (they are small) and rendered as a unit.
func readPromptFile(path string) ([]byte, error) { return os.ReadFile(path) }
