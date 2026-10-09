// Package host — the AnnotationAnchor union (v2 generalization of VisualAmbient).
//
// v1 (spatial-oracle) grounded an operator's screen context as a flat
// {frame_handle, point, element, t_ms} bundle — implicitly "a DOM node under a
// click point in an rrweb frame". v2 generalizes that to a discriminated
// AnnotationAnchor union so the same WithVisualAmbient → args.visual → input.visual
// pipeline can carry an annotation against ANY rendered artifact kind
// (png / mp4 / rrweb / html / slidey), not just an rrweb DOM node:
//
//   - time_range       — a span (or instant) in a video/recording: {start_ms, end_ms?}.
//   - frame            — a single grabbed still: {frame_handle, t_ms}.
//   - dom_node         — a resolved DOM element: {selector, role, text, bbox}
//     (the v1 element, now one anchor target among several).
//   - region           — a freehand/box/highlight drawn on the pixels:
//     {shape: box|freeform|highlight, path[], bbox}.
//   - semantic_element — a producer-declared element picked from the sibling
//     semantic sidecar: {plugin, ref, bbox?}. kitsoki never
//     interprets `ref`; it round-trips it verbatim.
//
// CRITICAL back-compat contract: the v1 flat fields (frame_handle / point /
// element / t_ms) are STILL accepted and STILL recorded byte-identically. A v1
// bundle with an element NORMALIZES to a dom_node anchor; a v1 bundle with only a
// frame normalizes to a frame anchor; with only a media + t_ms, a time_range. The
// normalized anchor is ADDITIVE — it appears under the new `anchor` key alongside
// every legacy key, so existing spatial-oracle paths and recorded cassettes are
// unaffected (see annotation_anchor_test.go for the byte-identity guards).
//
// The union is a plain tagged struct (kind + the populated target), not a Go
// interface, so it round-trips through the JSON-RPC params map and the recorded
// trace block without bespoke (un)marshalling — the principle-of-least-surprise
// shape every other recorded input block in this package uses.
package host

import "strings"

// AnchorKind is the discriminator naming which target an AnnotationAnchor carries.
type AnchorKind string

const (
	// AnchorTimeRange — a span or instant in a video/recording.
	AnchorTimeRange AnchorKind = "time_range"
	// AnchorFrame — a single grabbed still referenced by handle.
	AnchorFrame AnchorKind = "frame"
	// AnchorDOMNode — a resolved DOM element (the v1 element).
	AnchorDOMNode AnchorKind = "dom_node"
	// AnchorRegion — a box/freeform/highlight drawn on the pixels.
	AnchorRegion AnchorKind = "region"
	// AnchorSemanticElement — a producer-declared element from the semantic sidecar.
	AnchorSemanticElement AnchorKind = "semantic_element"
)

// RegionShape names the geometry an AnchorRegion carries.
type RegionShape string

const (
	// RegionBox — an axis-aligned rectangle (path is the two opposite corners).
	RegionBox RegionShape = "box"
	// RegionFreeform — a freehand polyline/polygon (path is its vertices).
	RegionFreeform RegionShape = "freeform"
	// RegionHighlight — a highlighter stroke (path is its spine).
	RegionHighlight RegionShape = "highlight"
)

// AnnotationAnchor is the discriminated target an operator annotated. Exactly one
// target field is populated, named by Kind. It is the v2 generalization carried
// alongside the v1 flat VisualAmbient fields (and synthesized from them when a
// surface only sent the legacy shape — see VisualAmbient.normalizedAnchor).
type AnnotationAnchor struct {
	// Kind names which target below is populated.
	Kind AnchorKind `json:"kind"`
	// TimeRange is set when Kind == time_range.
	TimeRange *AnchorTimeRangeTarget `json:"time_range,omitempty"`
	// Frame is set when Kind == frame.
	Frame *AnchorFrameTarget `json:"frame,omitempty"`
	// DOMNode is set when Kind == dom_node.
	DOMNode *AnchorDOMNodeTarget `json:"dom_node,omitempty"`
	// Region is set when Kind == region.
	Region *AnchorRegionTarget `json:"region,omitempty"`
	// SemanticElement is set when Kind == semantic_element.
	SemanticElement *AnchorSemanticElementTarget `json:"semantic_element,omitempty"`
}

// AnchorTimeRangeTarget is a span (EndMs > 0) or instant (EndMs == 0) in ms.
type AnchorTimeRangeTarget struct {
	StartMs int `json:"start_ms"`
	EndMs   int `json:"end_ms,omitempty"`
}

// AnchorFrameTarget is a single grabbed still referenced by handle at TMs.
type AnchorFrameTarget struct {
	FrameHandle string `json:"frame_handle"`
	TMs         int    `json:"t_ms,omitempty"`
}

// AnchorDOMNodeTarget is a resolved DOM element (the v1 element, generalized).
type AnchorDOMNodeTarget struct {
	Selector string `json:"selector"`
	Role     string `json:"role,omitempty"`
	Text     string `json:"text,omitempty"`
	Bbox     [4]int `json:"bbox"`
}

// AnchorRegionTarget is a box/freeform/highlight drawn on the pixels. Path is the
// ordered list of frame-pixel vertices; Bbox is the tight enclosing box for
// re-overlay and quick hit-testing.
type AnchorRegionTarget struct {
	Shape RegionShape `json:"shape"`
	Path  [][2]int    `json:"path"`
	Bbox  [4]int      `json:"bbox"`
}

// AnchorSemanticElementTarget references a producer-declared element from the
// sibling semantic sidecar. Plugin names the producer; Ref is opaque to kitsoki
// (round-tripped verbatim, never interpreted); Bbox (optional) is the element's
// box for re-overlay when the sidecar supplied one.
type AnchorSemanticElementTarget struct {
	Plugin       string         `json:"plugin"`
	Ref          string         `json:"ref"`
	SemanticKind string         `json:"semantic_kind,omitempty"`
	Label        string         `json:"label,omitempty"`
	Description  string         `json:"description,omitempty"`
	Selector     string         `json:"selector,omitempty"`
	Text         string         `json:"text,omitempty"`
	Value        string         `json:"value,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	Bbox         [4]int         `json:"bbox,omitempty"`
}

// asMap renders the anchor as the template-facing map a pongo2 prompt sees under
// `args.visual.anchor`, and the recorded `input.visual.anchor` block (one shape
// for both — the anchor wire names are stable across the two surfaces). Returns
// nil for an empty/unkinded anchor so a v1-only bundle records no anchor noise.
//
// The discriminated target lives under a `target` sub-object keyed by `kind`, so
// a prompt references `{{ args.visual.anchor.target.kind }}` and then the
// kind-specific fields (e.g. `{{ args.visual.anchor.target.selector }}` for a
// dom_node, `{{ args.visual.anchor.target.ref }}` for a semantic_element). The
// target's own fields are inlined beside `kind` (not nested again under the kind
// name) so a template needn't double-dot through the discriminator.
func (a AnnotationAnchor) asMap() map[string]any {
	if a.Kind == "" {
		return nil
	}
	target := map[string]any{"kind": string(a.Kind)}
	switch a.Kind {
	case AnchorTimeRange:
		if a.TimeRange != nil {
			target["start_ms"] = a.TimeRange.StartMs
			if a.TimeRange.EndMs != 0 {
				target["end_ms"] = a.TimeRange.EndMs
			}
		}
	case AnchorFrame:
		if a.Frame != nil {
			target["frame_handle"] = a.Frame.FrameHandle
			target["t_ms"] = a.Frame.TMs
		}
	case AnchorDOMNode:
		if a.DOMNode != nil {
			target["selector"] = a.DOMNode.Selector
			target["role"] = a.DOMNode.Role
			target["text"] = a.DOMNode.Text
			target["bbox"] = bboxSlice(a.DOMNode.Bbox)
		}
	case AnchorRegion:
		if a.Region != nil {
			target["shape"] = string(a.Region.Shape)
			target["path"] = pathSlice(a.Region.Path)
			target["bbox"] = bboxSlice(a.Region.Bbox)
		}
	case AnchorSemanticElement:
		if a.SemanticElement != nil {
			target["plugin"] = a.SemanticElement.Plugin
			target["ref"] = a.SemanticElement.Ref
			if a.SemanticElement.SemanticKind != "" {
				target["semantic_kind"] = a.SemanticElement.SemanticKind
			}
			if a.SemanticElement.Label != "" {
				target["label"] = a.SemanticElement.Label
			}
			if a.SemanticElement.Description != "" {
				target["description"] = a.SemanticElement.Description
			}
			if a.SemanticElement.Selector != "" {
				target["selector"] = a.SemanticElement.Selector
			}
			if a.SemanticElement.Text != "" {
				target["text"] = a.SemanticElement.Text
			}
			if a.SemanticElement.Value != "" {
				target["value"] = a.SemanticElement.Value
			}
			if len(a.SemanticElement.Data) > 0 {
				target["data"] = a.SemanticElement.Data
			}
			if a.SemanticElement.Bbox != ([4]int{}) {
				target["bbox"] = bboxSlice(a.SemanticElement.Bbox)
			}
		}
	}
	return map[string]any{"target": target}
}

// bboxSlice renders a [4]int bbox as a positional []int (the wire shape every
// recorded bbox uses, so a viewer can redraw the overlay).
func bboxSlice(b [4]int) []int { return []int{b[0], b[1], b[2], b[3]} }

// pathSlice renders a [][2]int vertex list as positional [][]int for the wire.
func pathSlice(p [][2]int) [][]int {
	out := make([][]int, len(p))
	for i, pt := range p {
		out[i] = []int{pt[0], pt[1]}
	}
	return out
}

// normalizedAnchor returns the anchor to expose for this bundle: the explicit
// Anchor a v2 surface attached, or — when none was attached — one SYNTHESIZED
// from the v1 flat fields so a legacy bundle still carries an anchor. The
// normalization precedence mirrors how specific the v1 signal is:
//
//	element present            → dom_node
//	else frame_handle present  → frame
//	else media + t_ms present  → time_range
//	else                       → empty (no anchor)
//
// This is the back-compat seam: a v1 cassette decodes with no Anchor field, gets
// a dom_node/frame anchor synthesized here, and every legacy key is still emitted
// unchanged — so the recorded block is a strict superset of v1.
func (a VisualAmbient) normalizedAnchor() AnnotationAnchor {
	if a.Anchor.Kind != "" {
		return a.Anchor
	}
	if a.Element != nil {
		return AnnotationAnchor{
			Kind: AnchorDOMNode,
			DOMNode: &AnchorDOMNodeTarget{
				Selector: a.Element.Selector,
				Role:     a.Element.Role,
				Text:     a.Element.Text,
				Bbox:     a.Element.Bbox,
			},
		}
	}
	if strings.TrimSpace(a.FrameHandle) != "" {
		return AnnotationAnchor{
			Kind:  AnchorFrame,
			Frame: &AnchorFrameTarget{FrameHandle: a.FrameHandle, TMs: a.TMs},
		}
	}
	if strings.TrimSpace(a.MediaHandle) != "" && a.TMs != 0 {
		return AnnotationAnchor{
			Kind:      AnchorTimeRange,
			TimeRange: &AnchorTimeRangeTarget{StartMs: a.TMs},
		}
	}
	return AnnotationAnchor{}
}

// anchorFrameHandle returns the frame handle this bundle references for the
// dangling-frame check: the explicit anchor's frame handle when it is a frame
// anchor, else the v1 flat FrameHandle. This keeps the FrameResolver seam working
// for both the legacy frame field and a v2 frame anchor.
func (a VisualAmbient) anchorFrameHandle() string {
	if a.Anchor.Kind == AnchorFrame && a.Anchor.Frame != nil {
		if fh := strings.TrimSpace(a.Anchor.Frame.FrameHandle); fh != "" {
			return fh
		}
	}
	return strings.TrimSpace(a.FrameHandle)
}

// AnchorFromParams decodes the optional `anchor` object on a session.offpath RPC
// (or /point return) into an AnnotationAnchor. An absent/unkinded object decodes
// to the zero AnnotationAnchor, so a v1 surface that sends only the flat fields
// gets Kind == "" and the synthesized normalization takes over. The shape mirrors
// AnnotationAnchor's JSON tags; numbers arrive as JSON float64.
//
// Exported so the runstatus server's visualAmbientFromParams (a different package)
// reuses the one decoder — the principle-of-least-surprise single source of truth
// for the wire shape, mirroring how it already maps the flat fields by hand.
func AnchorFromParams(m map[string]any) AnnotationAnchor {
	if m == nil {
		return AnnotationAnchor{}
	}
	kind, _ := m["kind"].(string)
	if kind == "" {
		return AnnotationAnchor{}
	}
	a := AnnotationAnchor{Kind: AnchorKind(kind)}
	switch a.Kind {
	case AnchorTimeRange:
		if t, ok := m["time_range"].(map[string]any); ok {
			tr := &AnchorTimeRangeTarget{}
			tr.StartMs = anchorNum(t, "start_ms")
			tr.EndMs = anchorNum(t, "end_ms")
			a.TimeRange = tr
		}
	case AnchorFrame:
		if f, ok := m["frame"].(map[string]any); ok {
			fr := &AnchorFrameTarget{}
			fr.FrameHandle, _ = f["frame_handle"].(string)
			fr.TMs = anchorNum(f, "t_ms")
			a.Frame = fr
		}
	case AnchorDOMNode:
		if d, ok := m["dom_node"].(map[string]any); ok {
			dn := &AnchorDOMNodeTarget{}
			dn.Selector, _ = d["selector"].(string)
			dn.Role, _ = d["role"].(string)
			dn.Text, _ = d["text"].(string)
			dn.Bbox = anchorBbox(d["bbox"])
			a.DOMNode = dn
		}
	case AnchorRegion:
		if r, ok := m["region"].(map[string]any); ok {
			rg := &AnchorRegionTarget{}
			shape, _ := r["shape"].(string)
			rg.Shape = RegionShape(shape)
			rg.Path = anchorPath(r["path"])
			rg.Bbox = anchorBbox(r["bbox"])
			a.Region = rg
		}
	case AnchorSemanticElement:
		if se, ok := m["semantic_element"].(map[string]any); ok {
			s := &AnchorSemanticElementTarget{}
			s.Plugin, _ = se["plugin"].(string)
			s.Ref, _ = se["ref"].(string)
			s.SemanticKind, _ = se["semantic_kind"].(string)
			s.Label, _ = se["label"].(string)
			s.Description, _ = se["description"].(string)
			s.Selector, _ = se["selector"].(string)
			s.Text, _ = se["text"].(string)
			s.Value, _ = se["value"].(string)
			if data, ok := se["data"].(map[string]any); ok {
				s.Data = data
			}
			s.Bbox = anchorBbox(se["bbox"])
			a.SemanticElement = s
		}
	}
	return a
}

// anchorNum reads a numeric field (JSON float64) from m as an int.
func anchorNum(m map[string]any, key string) int {
	switch n := m[key].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

// anchorBbox coerces a JSON array (or absent value) into a [4]int bbox.
func anchorBbox(v any) [4]int {
	var b [4]int
	arr, ok := v.([]any)
	if !ok {
		return b
	}
	for i := 0; i < len(arr) && i < 4; i++ {
		b[i] = anchorScalar(arr[i])
	}
	return b
}

// anchorPath coerces a JSON array of [x,y] pairs into a [][2]int vertex list.
func anchorPath(v any) [][2]int {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([][2]int, 0, len(arr))
	for _, pt := range arr {
		pair, ok := pt.([]any)
		if !ok || len(pair) < 2 {
			continue
		}
		out = append(out, [2]int{anchorScalar(pair[0]), anchorScalar(pair[1])})
	}
	return out
}

// anchorScalar coerces a single JSON number to an int (zero when not numeric).
func anchorScalar(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}
