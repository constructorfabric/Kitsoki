package host_test

// annotation_anchor_test.go — the v2 AnnotationAnchor union (generalizing the v1
// VisualAmbient flat bundle to png/mp4/rrweb/html/slidey via a discriminated
// anchor). These tests prove, with no LLM and no network:
//
//   1. AnchorFromParams decodes EACH target kind (time_range, frame, dom_node,
//      region, semantic_element) from the JSON-RPC param shape (float64 numbers).
//   2. The semantic_element ref is round-tripped VERBATIM (kitsoki never
//      interprets it).
//   3. v1 NORMALIZATION: a bundle with only the legacy flat fields (no `anchor`)
//      synthesizes a dom_node / frame / time_range anchor — AND still emits every
//      legacy key byte-identically (a strict superset of v1, no regressions).
//   4. The template scope exposes `args.visual.anchor.target.kind`.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

func TestAnchorFromParams_EachKind(t *testing.T) {
	t.Parallel()

	t.Run("time_range", func(t *testing.T) {
		t.Parallel()
		a := host.AnchorFromParams(map[string]any{
			"kind":       "time_range",
			"time_range": map[string]any{"start_ms": float64(14000), "end_ms": float64(16000)},
		})
		require.Equal(t, host.AnchorTimeRange, a.Kind)
		require.NotNil(t, a.TimeRange)
		assert.Equal(t, 14000, a.TimeRange.StartMs)
		assert.Equal(t, 16000, a.TimeRange.EndMs)
	})

	t.Run("time_range instant (no end_ms)", func(t *testing.T) {
		t.Parallel()
		a := host.AnchorFromParams(map[string]any{
			"kind":       "time_range",
			"time_range": map[string]any{"start_ms": float64(22000)},
		})
		require.NotNil(t, a.TimeRange)
		assert.Equal(t, 22000, a.TimeRange.StartMs)
		assert.Zero(t, a.TimeRange.EndMs)
	})

	t.Run("frame", func(t *testing.T) {
		t.Parallel()
		a := host.AnchorFromParams(map[string]any{
			"kind":  "frame",
			"frame": map[string]any{"frame_handle": "art:frame-9", "t_ms": float64(3200)},
		})
		require.Equal(t, host.AnchorFrame, a.Kind)
		require.NotNil(t, a.Frame)
		assert.Equal(t, "art:frame-9", a.Frame.FrameHandle)
		assert.Equal(t, 3200, a.Frame.TMs)
	})

	t.Run("dom_node", func(t *testing.T) {
		t.Parallel()
		a := host.AnchorFromParams(map[string]any{
			"kind": "dom_node",
			"dom_node": map[string]any{
				"selector": "[data-testid=run]",
				"role":     "button",
				"text":     "Run",
				"bbox":     []any{float64(10), float64(20), float64(96), float64(40)},
			},
		})
		require.Equal(t, host.AnchorDOMNode, a.Kind)
		require.NotNil(t, a.DOMNode)
		assert.Equal(t, "[data-testid=run]", a.DOMNode.Selector)
		assert.Equal(t, "button", a.DOMNode.Role)
		assert.Equal(t, "Run", a.DOMNode.Text)
		assert.Equal(t, [4]int{10, 20, 96, 40}, a.DOMNode.Bbox)
	})

	t.Run("region", func(t *testing.T) {
		t.Parallel()
		a := host.AnchorFromParams(map[string]any{
			"kind": "region",
			"region": map[string]any{
				"shape": "freeform",
				"path":  []any{[]any{float64(1), float64(2)}, []any{float64(3), float64(4)}},
				"bbox":  []any{float64(1), float64(2), float64(2), float64(2)},
			},
		})
		require.Equal(t, host.AnchorRegion, a.Kind)
		require.NotNil(t, a.Region)
		assert.Equal(t, host.RegionFreeform, a.Region.Shape)
		assert.Equal(t, [][2]int{{1, 2}, {3, 4}}, a.Region.Path)
		assert.Equal(t, [4]int{1, 2, 2, 2}, a.Region.Bbox)
	})

	t.Run("semantic_element round-trips ref verbatim", func(t *testing.T) {
		t.Parallel()
		// A deliberately structured-looking ref kitsoki must NOT interpret.
		const opaque = "scene-3.title#v2/weird:ref"
		a := host.AnchorFromParams(map[string]any{
			"kind": "semantic_element",
			"semantic_element": map[string]any{
				"plugin":        "html-data",
				"ref":           opaque,
				"semantic_kind": "field",
				"label":         "Status",
				"description":   "Issue status field",
				"selector":      "[data-field=status]",
				"text":          "Blocked",
				"value":         "blocked",
				"data":          map[string]any{"path": "issue.status"},
				"bbox":          []any{float64(5), float64(6), float64(7), float64(8)},
			},
		})
		require.Equal(t, host.AnchorSemanticElement, a.Kind)
		require.NotNil(t, a.SemanticElement)
		assert.Equal(t, "html-data", a.SemanticElement.Plugin)
		assert.Equal(t, opaque, a.SemanticElement.Ref, "ref must round-trip verbatim, never interpreted")
		assert.Equal(t, "field", a.SemanticElement.SemanticKind)
		assert.Equal(t, "Status", a.SemanticElement.Label)
		assert.Equal(t, "Issue status field", a.SemanticElement.Description)
		assert.Equal(t, "[data-field=status]", a.SemanticElement.Selector)
		assert.Equal(t, "Blocked", a.SemanticElement.Text)
		assert.Equal(t, "blocked", a.SemanticElement.Value)
		assert.Equal(t, map[string]any{"path": "issue.status"}, a.SemanticElement.Data)
		assert.Equal(t, [4]int{5, 6, 7, 8}, a.SemanticElement.Bbox)

		// And it survives into the recorded/template map under target.ref unchanged.
		amb := host.VisualAmbient{Anchor: a}
		m := amb.AsMapForTest()
		anchor := m["anchor"].(map[string]any)
		target := anchor["target"].(map[string]any)
		assert.Equal(t, "semantic_element", target["kind"])
		assert.Equal(t, opaque, target["ref"])
		assert.Equal(t, "Status", target["label"])
		assert.Equal(t, "Blocked", target["text"])
		assert.Equal(t, map[string]any{"path": "issue.status"}, target["data"])
	})

	t.Run("absent / unkinded anchor decodes to zero", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, host.AnchorKind(""), host.AnchorFromParams(nil).Kind)
		assert.Equal(t, host.AnchorKind(""), host.AnchorFromParams(map[string]any{}).Kind)
	})
}

// TestVisualAmbient_V1Normalization proves a bundle carrying only legacy flat
// fields synthesizes the right anchor AND keeps every v1 key byte-identical.
func TestVisualAmbient_V1Normalization(t *testing.T) {
	t.Parallel()

	t.Run("element ⇒ dom_node, legacy keys intact", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{FrameHandle: "/tmp/f.png", TMs: 14300, MediaHandle: "media:7", Route: "/r"}
		v.Point.X, v.Point.Y = 1180, 540
		v.Element = &struct {
			Selector string `json:"selector"`
			Role     string `json:"role"`
			Text     string `json:"text"`
			Bbox     [4]int `json:"bbox"`
		}{Selector: "[data-testid=run]", Role: "button", Text: "Run", Bbox: [4]int{1140, 520, 96, 40}}

		m := v.RecordedMapForTest()

		// Every v1 key is still present and unchanged (strict superset).
		assert.Equal(t, host.VisualSchemaVersion, m["schema_version"])
		assert.Equal(t, "/tmp/f.png", m["frame_handle"])
		assert.Equal(t, 14300, m["t_ms"])
		assert.Equal(t, "media:7", m["media_handle"])
		assert.Equal(t, "/r", m["route"])
		require.Contains(t, m, "element")
		el := m["element"].(map[string]any)
		assert.Equal(t, "[data-testid=run]", el["selector"])

		// And the synthesized dom_node anchor mirrors the element.
		anchor := m["anchor"].(map[string]any)
		target := anchor["target"].(map[string]any)
		assert.Equal(t, "dom_node", target["kind"])
		assert.Equal(t, "[data-testid=run]", target["selector"])
		assert.Equal(t, "button", target["role"])
		assert.Equal(t, []int{1140, 520, 96, 40}, target["bbox"])
	})

	t.Run("frame only ⇒ frame anchor", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{FrameHandle: "art:frame-2", TMs: 3200}
		anchor := v.RecordedMapForTest()["anchor"].(map[string]any)
		target := anchor["target"].(map[string]any)
		assert.Equal(t, "frame", target["kind"])
		assert.Equal(t, "art:frame-2", target["frame_handle"])
		assert.Equal(t, 3200, target["t_ms"])
	})

	t.Run("media + t_ms only ⇒ time_range anchor", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{MediaHandle: "media:9", TMs: 8000}
		anchor := v.RecordedMapForTest()["anchor"].(map[string]any)
		target := anchor["target"].(map[string]any)
		assert.Equal(t, "time_range", target["kind"])
		assert.Equal(t, 8000, target["start_ms"])
	})

	t.Run("explicit anchor wins over synthesis", func(t *testing.T) {
		t.Parallel()
		// A bundle with a legacy element AND an explicit region anchor: the
		// explicit anchor is what surfaces (a v2 surface drove a region draw).
		v := host.VisualAmbient{FrameHandle: "/tmp/f.png"}
		v.Element = &struct {
			Selector string `json:"selector"`
			Role     string `json:"role"`
			Text     string `json:"text"`
			Bbox     [4]int `json:"bbox"`
		}{Selector: "ignored"}
		v.Anchor = host.AnnotationAnchor{
			Kind:   host.AnchorRegion,
			Region: &host.AnchorRegionTarget{Shape: host.RegionBox, Bbox: [4]int{1, 2, 3, 4}},
		}
		anchor := v.RecordedMapForTest()["anchor"].(map[string]any)
		target := anchor["target"].(map[string]any)
		assert.Equal(t, "region", target["kind"])
		assert.Equal(t, "box", target["shape"])
	})
}

// TestVisualAmbient_NoAnchorWhenEmpty proves an empty bundle records no anchor
// noise (the byte-identity-with-nothing case stays clean).
func TestVisualAmbient_NoAnchorWhenEmpty(t *testing.T) {
	t.Parallel()
	v := host.VisualAmbient{Route: "/x"} // no frame, element, media, or anchor
	m := v.RecordedMapForTest()
	_, has := m["anchor"]
	assert.False(t, has, "an anchor-less bundle must record no anchor key")
}

// TestSemanticElementAnchor_OmitsZeroBbox guards the omitempty bbox so a sidecar
// element with no box doesn't fabricate a [0,0,0,0] overlay.
func TestSemanticElementAnchor_OmitsZeroBbox(t *testing.T) {
	t.Parallel()
	v := host.VisualAmbient{Anchor: host.AnnotationAnchor{
		Kind:            host.AnchorSemanticElement,
		SemanticElement: &host.AnchorSemanticElementTarget{Plugin: "slidey", Ref: "x"},
	}}
	target := v.AsMapForTest()["anchor"].(map[string]any)["target"].(map[string]any)
	_, has := target["bbox"]
	assert.False(t, has, "a semantic element with no bbox must omit it (no fabricated overlay)")

	// Sanity: the whole thing still JSON-encodes.
	_, err := json.Marshal(v.AsMapForTest())
	require.NoError(t, err)
}
