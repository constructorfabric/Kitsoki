// Package host — ambient screen-context seam for agent prompts.
//
// Sibling to ide_ambient.go. Where IDEAmbient carries "what's selected in the
// editor", VisualAmbient carries "what's on screen and where the operator is
// pointing": a captured video/screenshot frame, a click point, and (when a web
// surface resolved it) the DOM element under that point — selector, role,
// visible text, and bounding box. A web/tui surface builds the bundle and the
// runstatus offpath RPC lifts it onto the turn ctx with WithVisualAmbient; the
// operator-facing agent handlers (agent_ask.go, agent_ask_with_mcp.go,
// agent_converse.go) expose it two ways, exactly as the IDE selection is:
//
//   - As the reserved `args.visual` template key (mergeVisualAmbient), so a
//     prompt can reference `{{ args.visual.element.selector }}` /
//     `{{ args.visual.element.text }}` / `{{ args.visual.point }}` /
//     `{{ args.visual.frame }}` for precise placement when it wants to.
//   - As a standardized block appended to the rendered prompt automatically
//     (VisualAmbientPreamble / appendVisualAmbient), so the screen context feeds
//     the request by default without any story-author opt-in — the "always
//     injected when a surface attached a point/element" behavior.
//
// When no surface attached anything (CLI one-shots, flow tests, headless runs,
// or a chat with no point) no ambient value is set: the template scope and the
// rendered prompt are byte-identical to a run with no screen context.
//
// v1 is TEXT-ONLY (epic shared decisions 3–4): the preamble describes the
// element in words and appends the frame's artifact PATH for the agent to Read
// on demand. It never inlines image bytes — the moat's frame-by-path discipline
// (the engine never base64-bloats a prompt with a still). When only a handle is
// present (no resolver has mapped it to a path yet) it is rendered as-is; a
// downstream resolver maps it. See docs/architecture/visual-ambient.md and the
// shipped host.video.frame / host.artifacts_dir substrate (video_frame.go,
// artifacts_dir_transport.go) the handle resolves through.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// VisualAmbient is the screen context captured at turn-submit and exposed to
// prompt templates as `args.visual`. It is built by the web/tui surfaces and
// carried via WithVisualAmbient. Everything is optional: a bare point with no
// element still grounds "the operator is pointing here in the frame".
type VisualAmbient struct {
	// FrameHandle is the captured frame the operator was looking at, as an
	// artifact handle or path. v1 references it by path for an optional Read;
	// a bare handle is rendered as-is for a downstream resolver to map.
	FrameHandle string `json:"frame_handle"`
	// Point is the click/pointer position within the frame, in frame pixels.
	Point struct {
		X int `json:"x"`
		Y int `json:"y"`
	} `json:"point"`
	// Element is the DOM element resolved under Point by the capturing surface
	// (slice 2). Nil when no element was resolved (the future arbitrary-media
	// path), in which case the preamble degrades to frame + point.
	Element *struct {
		// Selector is a stable selector for the element (e.g.
		// `[data-testid=intent-btn-run]`).
		Selector string `json:"selector"`
		// Role is the element's ARIA/semantic role (e.g. "button").
		Role string `json:"role"`
		// Text is the element's visible text.
		Text string `json:"text"`
		// Bbox is the element's bounding box [x, y, w, h] in frame pixels.
		Bbox [4]int `json:"bbox"`
	} `json:"element"`
	// TMs is the timestamp within the source media the frame was grabbed at,
	// in milliseconds. Zero when the frame is a standalone screenshot.
	TMs int `json:"t_ms"`
	// MediaHandle is the source media the frame came from (the video/recording),
	// as an artifact handle or path. Empty for a standalone screenshot.
	MediaHandle string `json:"media_handle"`
	// Route is the UI route/URL the operator was on when they pointed, for
	// human context in the preamble.
	Route string `json:"route"`
	// Anchor is the v2 discriminated annotation target (see annotation_anchor.go).
	// A v2 surface attaches it explicitly; a v1 surface leaves it zero and the
	// normalization synthesizes one from the flat fields (normalizedAnchor). It is
	// ADDITIVE — the legacy fields above are still recorded byte-identically.
	Anchor AnnotationAnchor `json:"anchor,omitempty"`
}

// VisualSchemaVersion is the schema version stamped on the recorded
// `input.visual` block (the trace bundle). It is independent of the
// template-facing `args.visual` shape: the recorded block is the auditable
// decision INPUT, so it carries an explicit version that lets the shape evolve
// without breaking older traces (docs/tracing/trace-format.md, input.visual).
//
// v2 adds the discriminated `anchor` block (annotation_anchor.go) alongside every
// v1 flat field. The bump is back-compatible: a v2 reader still finds the legacy
// frame_handle/point/element/t_ms keys, and a v1 bundle is normalized into a
// dom_node/frame anchor so the recorded shape is a strict superset of v1.
const VisualSchemaVersion = 2

// recordedMap renders the ambient context as the structured `input.visual`
// block recorded on the agent call event — the auditable record of what the
// operator pointed at. Unlike asMap (the pongo2 template scope, keyed `frame`),
// this uses the trace wire names: `frame_handle` for the still and a positional
// `bbox` [x,y,w,h] for re-overlay (epic Q2 lean: record the bbox so a viewer can
// redraw the crosshair). The frame rides BY HANDLE only — never inlined bytes —
// reusing the artifact substrate the handle resolves through.
func (a VisualAmbient) recordedMap() map[string]any {
	m := map[string]any{
		"schema_version": VisualSchemaVersion,
		"frame_handle":   a.FrameHandle,
		"point":          map[string]any{"x": a.Point.X, "y": a.Point.Y},
		"t_ms":           a.TMs,
		"media_handle":   a.MediaHandle,
		"route":          a.Route,
	}
	if a.Element != nil {
		m["element"] = map[string]any{
			"selector": a.Element.Selector,
			"role":     a.Element.Role,
			"text":     a.Element.Text,
			"bbox": []int{
				a.Element.Bbox[0], a.Element.Bbox[1],
				a.Element.Bbox[2], a.Element.Bbox[3],
			},
		}
	}
	// v2: the discriminated anchor, additive alongside the legacy keys. A v1
	// bundle synthesizes one (dom_node/frame) so the block always carries it.
	if anchor := a.normalizedAnchor().asMap(); anchor != nil {
		m["anchor"] = anchor
	}
	return m
}

// asMap renders the ambient context as the map a pongo2 template sees under
// `args.visual`. Kept in one place so the template-facing key names stay stable.
func (a VisualAmbient) asMap() map[string]any {
	m := map[string]any{
		"frame":        a.FrameHandle,
		"point":        map[string]any{"x": a.Point.X, "y": a.Point.Y},
		"t_ms":         a.TMs,
		"media_handle": a.MediaHandle,
		"route":        a.Route,
	}
	if a.Element != nil {
		m["element"] = map[string]any{
			"selector": a.Element.Selector,
			"role":     a.Element.Role,
			"text":     a.Element.Text,
			"bbox":     a.Element.Bbox,
		}
	}
	// v2: expose the discriminated anchor so a prompt can reference
	// `{{ args.visual.anchor.target.kind }}` while the legacy keys above stay live.
	if anchor := a.normalizedAnchor().asMap(); anchor != nil {
		m["anchor"] = anchor
	}
	return m
}

// visualAmbientKey is the unexported context key for the injected ambient context.
type visualAmbientKey struct{}

// WithVisualAmbient injects the turn's ambient screen context into ctx so the
// agent handlers expose it as `args.visual`. An empty VisualAmbient (no frame
// AND no element) is a no-op — the scope stays byte-identical to a turn with no
// screen context, so the surfaces that don't attach a bundle never need a
// separate ctx branch.
func WithVisualAmbient(ctx context.Context, a VisualAmbient) context.Context {
	if a.FrameHandle == "" && a.Element == nil && a.Anchor.Kind == "" {
		return ctx
	}
	return context.WithValue(ctx, visualAmbientKey{}, a)
}

// VisualAmbientFromCtx returns the ambient screen context previously injected
// with WithVisualAmbient, and ok=false when none was injected.
func VisualAmbientFromCtx(ctx context.Context) (VisualAmbient, bool) {
	a, ok := ctx.Value(visualAmbientKey{}).(VisualAmbient)
	return a, ok
}

// FrameResolver checks that a frame artifact handle resolves to a recorded
// artifact. It is the read side of the artifact substrate the frame_handle was
// produced into (host.video.frame → host.artifacts_dir records the still; the
// JournalArtifactResolver resolves it). The agent handler consults it before
// recording an `input.visual` block so the trace can never carry a dangling
// frame reference (docs/tracing/trace-format.md: "reject a visual block whose
// frame_handle doesn't resolve to a recorded artifact").
type FrameResolver interface {
	// ResolveFrame reports whether handle names a recorded artifact.
	ResolveFrame(handle string) bool
}

// frameResolverKey is the unexported context key for the injected FrameResolver.
type frameResolverKey struct{}

// WithFrameResolver injects fr so the agent handlers can validate a recorded
// frame_handle resolves before stamping it on the trace. Nil is a safe no-op:
// when no resolver is wired (CLI one-shots, flow fixtures without an artifact
// substrate, headless replay) the dangling-reference check is skipped and the
// bundle records as-is — the same posture every other artifact-substrate seam
// takes (the journal writer, the prompt renderer) when not wired.
func WithFrameResolver(ctx context.Context, fr FrameResolver) context.Context {
	if fr == nil {
		return ctx
	}
	return context.WithValue(ctx, frameResolverKey{}, fr)
}

// frameResolverFromCtx returns the FrameResolver previously injected with
// WithFrameResolver, or nil if none was injected.
func frameResolverFromCtx(ctx context.Context) FrameResolver {
	fr, _ := ctx.Value(frameResolverKey{}).(FrameResolver)
	return fr
}

// FrameResolverFunc adapts a plain predicate to the FrameResolver interface, so
// a caller (the orchestrator) can wire one from its journal reader without the
// host package importing the journal scan. The mirror of http.HandlerFunc.
type FrameResolverFunc func(handle string) bool

// ResolveFrame calls f(handle).
func (f FrameResolverFunc) ResolveFrame(handle string) bool { return f(handle) }

// recordedVisualInput builds the structured `input.visual` block to stamp on
// the agent call event when a surface attached a screen-context bundle to the
// turn, and (ok=false, "") when none was attached so the call records exactly as
// before. When a FrameResolver is wired AND the bundle carries a frame_handle,
// it rejects (err != nil) a handle that does not resolve to a recorded
// artifact, so the trace can never carry a dangling frame reference. The frame
// is referenced BY HANDLE only — the recorded block never inlines bytes.
func recordedVisualInput(ctx context.Context) (block map[string]any, ok bool, err error) {
	amb, present := VisualAmbientFromCtx(ctx)
	if !present {
		return nil, false, nil
	}
	if fh := amb.anchorFrameHandle(); fh != "" {
		if fr := frameResolverFromCtx(ctx); fr != nil && !fr.ResolveFrame(fh) {
			return nil, false, fmt.Errorf(
				"visual frame_handle %q does not resolve to a recorded artifact (dangling frame reference)", fh)
		}
	}
	return amb.recordedMap(), true, nil
}

// mergeVisualAmbient returns templateArgs with the ambient screen context added
// under the reserved `visual` key when one is present in ctx and the author has
// not already bound `visual` themselves (an explicit author binding wins). It
// never mutates the caller's map: when there is nothing to merge it returns the
// input unchanged, and otherwise it shallow-copies before adding the key. This
// is the single seam every agent prompt path calls so `{{ args.visual.* }}`
// resolves consistently — the mirror of mergeIDEAmbient.
func mergeVisualAmbient(ctx context.Context, templateArgs map[string]any) map[string]any {
	amb, ok := VisualAmbientFromCtx(ctx)
	if !ok {
		return templateArgs
	}
	if _, taken := templateArgs["visual"]; taken {
		return templateArgs
	}
	merged := make(map[string]any, len(templateArgs)+1)
	for k, v := range templateArgs {
		merged[k] = v
	}
	merged["visual"] = amb.asMap()
	return merged
}

// visualAmbientPreambleHeader marks the auto-injected screen-context block so it
// is unmistakable in a rendered prompt (and greppable in a recorded trace).
const visualAmbientPreambleHeader = "## Operator is pointing at the screen"

// VisualAmbientPreamble renders the standardized screen-context block that is
// appended to an operator-facing agent prompt (ask / ask_with_mcp / converse)
// whenever a surface attached a point/element — the "always injected when a
// surface is pointing" seam. It returns "" when no ambient is present, so a turn
// with no screen context produces a byte-identical prompt. Unlike the
// `args.visual.*` template scope (which each prompt must reference explicitly),
// this block lands without any story-author opt-in.
//
// v1 is text-only: the block describes the element in words (selector, role,
// visible text, position) and appends the frame's PATH for the agent to Read on
// demand — never inline image bytes (epic shared decisions 3–4). When the
// element is absent the block degrades to frame + point. The block is appended,
// not prepended: a prompt's role and instructions come first, then the screen
// context as trailing material.
func VisualAmbientPreamble(ctx context.Context) string {
	amb, ok := VisualAmbientFromCtx(ctx)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n")
	sb.WriteString(visualAmbientPreambleHeader)
	sb.WriteString("\n\n")

	anchor := amb.normalizedAnchor()
	if amb.Element != nil {
		// Descriptive element line: "Element: <selector> (role=button, text
		// "Run") at (1180,540)." Each clause is omitted when its source is empty
		// so a sparse element still reads cleanly.
		sb.WriteString("Element: ")
		if sel := strings.TrimSpace(amb.Element.Selector); sel != "" {
			sb.WriteString(sel)
		} else {
			sb.WriteString("(unnamed)")
		}
		var attrs []string
		if r := strings.TrimSpace(amb.Element.Role); r != "" {
			attrs = append(attrs, "role="+r)
		}
		if txt := strings.TrimSpace(amb.Element.Text); txt != "" {
			attrs = append(attrs, fmt.Sprintf("text %q", txt))
		}
		if len(attrs) > 0 {
			sb.WriteString(" (" + strings.Join(attrs, ", ") + ")")
		}
		sb.WriteString(fmt.Sprintf(" at (%d,%d).", amb.Point.X, amb.Point.Y))
	} else if anchor.Kind == AnchorSemanticElement && anchor.SemanticElement != nil {
		writeSemanticElementPreamble(&sb, anchor.SemanticElement)
	} else {
		// No element resolved: ground by frame + point only.
		sb.WriteString(fmt.Sprintf("The operator is pointing at (%d,%d) in the frame.", amb.Point.X, amb.Point.Y))
	}
	if route := strings.TrimSpace(amb.Route); route != "" {
		sb.WriteString(fmt.Sprintf("\nRoute: %s.", route))
	}
	if frame := strings.TrimSpace(amb.FrameHandle); frame != "" {
		// The frame rides as a PATH for optional Read, never inline bytes.
		sb.WriteString(fmt.Sprintf("\nFrame: %s (Read it with your tools if you need to see the screen).", frame))
	}
	return sb.String()
}

func writeSemanticElementPreamble(sb *strings.Builder, se *AnchorSemanticElementTarget) {
	label := strings.TrimSpace(se.Label)
	if label == "" {
		label = strings.TrimSpace(se.Ref)
	}
	if label == "" {
		label = "(unnamed)"
	}
	sb.WriteString("Semantic element: ")
	sb.WriteString(label)
	var attrs []string
	if plugin := strings.TrimSpace(se.Plugin); plugin != "" {
		attrs = append(attrs, "plugin="+plugin)
	}
	if kind := strings.TrimSpace(se.SemanticKind); kind != "" {
		attrs = append(attrs, "kind="+kind)
	}
	if ref := strings.TrimSpace(se.Ref); ref != "" {
		attrs = append(attrs, "ref="+ref)
	}
	if selector := strings.TrimSpace(se.Selector); selector != "" {
		attrs = append(attrs, "selector="+selector)
	}
	if text := strings.TrimSpace(se.Text); text != "" {
		attrs = append(attrs, fmt.Sprintf("text %q", text))
	}
	if value := strings.TrimSpace(se.Value); value != "" {
		attrs = append(attrs, fmt.Sprintf("value %q", value))
	}
	if desc := strings.TrimSpace(se.Description); desc != "" {
		attrs = append(attrs, fmt.Sprintf("description %q", desc))
	}
	if se.Bbox != ([4]int{}) {
		attrs = append(attrs, fmt.Sprintf("bbox=[%d,%d,%d,%d]", se.Bbox[0], se.Bbox[1], se.Bbox[2], se.Bbox[3]))
	}
	if len(attrs) > 0 {
		sb.WriteString(" (" + strings.Join(attrs, ", ") + ")")
	}
	sb.WriteString(".")
	if len(se.Data) > 0 {
		if b, err := json.Marshal(se.Data); err == nil {
			sb.WriteString("\nData: ")
			sb.Write(b)
			sb.WriteString(".")
		}
	}
}

// appendVisualAmbient returns prompt with the screen-context preamble appended
// when a surface attached a point/element to the turn, else prompt unchanged. It
// is the one-liner the operator-facing verb handlers call right before dispatch
// so the block lands on both the plugin (Dispatch) and subprocess paths — the
// mirror of appendIDEAmbient.
func appendVisualAmbient(ctx context.Context, prompt string) string {
	return prompt + VisualAmbientPreamble(ctx)
}
