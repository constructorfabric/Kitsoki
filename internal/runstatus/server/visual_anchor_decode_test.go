package server

// visual_anchor_decode_test.go — the server-side decode of the v2 annotation
// `anchor` on a session.offpath / point-return param map. visualAmbientFromParams
// is unexported, so this is a white-box (package server) test. It proves, with no
// LLM / network:
//
//   1. A v2 param map carrying an explicit `anchor` decodes into the host
//      VisualAmbient's Anchor (here: a semantic_element, the new producer-agnostic
//      pick) with `ref` round-tripped verbatim.
//   2. A v1 param map (flat frame_handle/point/element, NO `anchor`) decodes with
//      Anchor.Kind == "" — the host layer's normalization synthesizes the anchor,
//      so the server stays a thin transport (back-compat: the legacy shape still
//      decodes byte-identically into the flat fields).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

func TestVisualAmbientFromParams_DecodesAnchor(t *testing.T) {
	t.Parallel()

	v := visualAmbientFromParams(map[string]any{
		"frame_handle": "art:frame-1",
		"point":        map[string]any{"x": float64(10), "y": float64(20)},
		"anchor": map[string]any{
			"kind": "semantic_element",
			"semantic_element": map[string]any{
				"plugin":        "html-data",
				"ref":           "issue.status",
				"semantic_kind": "field",
				"label":         "Status",
				"selector":      "[data-field=status]",
				"text":          "Blocked",
				"data":          map[string]any{"path": "issue.status"},
				"bbox":          []any{float64(1), float64(2), float64(3), float64(4)},
			},
		},
	})

	// The legacy flat fields still decode (transport unchanged).
	assert.Equal(t, "art:frame-1", v.FrameHandle)
	assert.Equal(t, 10, v.Point.X)

	// The v2 anchor decodes, ref verbatim.
	require.Equal(t, host.AnchorSemanticElement, v.Anchor.Kind)
	require.NotNil(t, v.Anchor.SemanticElement)
	assert.Equal(t, "html-data", v.Anchor.SemanticElement.Plugin)
	assert.Equal(t, "issue.status", v.Anchor.SemanticElement.Ref)
	assert.Equal(t, "field", v.Anchor.SemanticElement.SemanticKind)
	assert.Equal(t, "Status", v.Anchor.SemanticElement.Label)
	assert.Equal(t, "[data-field=status]", v.Anchor.SemanticElement.Selector)
	assert.Equal(t, "Blocked", v.Anchor.SemanticElement.Text)
	assert.Equal(t, map[string]any{"path": "issue.status"}, v.Anchor.SemanticElement.Data)
	assert.Equal(t, [4]int{1, 2, 3, 4}, v.Anchor.SemanticElement.Bbox)
}

func TestVisualAmbientFromParams_V1NoAnchor(t *testing.T) {
	t.Parallel()

	// A pure-v1 bundle (the recorded-cassette shape) — no `anchor` key at all.
	v := visualAmbientFromParams(map[string]any{
		"frame_handle": "art:frame-1",
		"t_ms":         float64(3200),
		"point":        map[string]any{"x": float64(1180), "y": float64(540)},
		"element": map[string]any{
			"selector": "[data-testid=run]",
			"role":     "button",
			"text":     "Run",
			"bbox":     []any{float64(1140), float64(520), float64(96), float64(40)},
		},
	})

	// Flat fields decode exactly as before this slice.
	assert.Equal(t, "art:frame-1", v.FrameHandle)
	assert.Equal(t, 3200, v.TMs)
	require.NotNil(t, v.Element)
	assert.Equal(t, "[data-testid=run]", v.Element.Selector)

	// No explicit anchor on the wire — the host normalization synthesizes it.
	assert.Equal(t, host.AnchorKind(""), v.Anchor.Kind,
		"a v1 bundle carries no explicit anchor; normalization happens host-side")
}
