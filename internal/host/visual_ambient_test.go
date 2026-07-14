package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// visual_ambient_test.go — the screen-context sibling of ide_ambient_test.go.
// When a web/tui surface attached a point/element bundle (host.WithVisualAmbient
// on the ctx), the operator-facing oracle verbs (ask, converse) append the
// standardized screen-context block to the prompt — describing the element in
// words and referencing the frame by PATH (v1 is text-only; no image bytes) —
// without the story prompt having to reference args.visual. Absent ambient
// renders byte-identical to today. The args.visual.* template vars resolve in a
// pongo2 prompt for authors who want precise placement.

const visualHeader = "## Operator is pointing at the screen"

const visualFramePath = "/tmp/artifacts/frame-0042.png"

// visualCtxOn returns a ctx carrying a fully-resolved visual ambient: a frame
// path, a click point, and the DOM element under it.
func visualCtxOn(parent context.Context) context.Context {
	v := host.VisualAmbient{
		FrameHandle: visualFramePath,
		TMs:         3200,
		MediaHandle: "media:rec-1",
		Route:       "/web/session/abc",
	}
	v.Point.X = 1180
	v.Point.Y = 540
	v.Element = &struct {
		Selector string `json:"selector"`
		Role     string `json:"role"`
		Text     string `json:"text"`
		Bbox     [4]int `json:"bbox"`
	}{
		Selector: "[data-testid=intent-btn-run]",
		Role:     "button",
		Text:     "Run",
		Bbox:     [4]int{1100, 520, 160, 40},
	}
	return host.WithVisualAmbient(parent, v)
}

func TestVisualAmbientPreamble(t *testing.T) {
	t.Parallel()

	t.Run("absent without ambient", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, host.VisualAmbientPreamble(context.Background()),
			"no ambient on ctx must render no preamble")
	})

	t.Run("empty ambient is a no-op", func(t *testing.T) {
		t.Parallel()
		// No frame AND no element — WithVisualAmbient must not inject anything.
		ctx := host.WithVisualAmbient(context.Background(), host.VisualAmbient{Route: "/x"})
		assert.Empty(t, host.VisualAmbientPreamble(ctx),
			"ambient with no frame and no element must render no preamble")
	})

	t.Run("renders element descriptor, point, route and frame path", func(t *testing.T) {
		t.Parallel()
		got := host.VisualAmbientPreamble(visualCtxOn(context.Background()))
		assert.Contains(t, got, visualHeader)
		assert.Contains(t, got, "[data-testid=intent-btn-run]")
		assert.Contains(t, got, "role=button")
		assert.Contains(t, got, `text "Run"`)
		assert.Contains(t, got, "(1180,540)")
		assert.Contains(t, got, "/web/session/abc")
		assert.Contains(t, got, visualFramePath, "frame must ride as a Read-able path")
		assert.NotContains(t, got, "base64", "v1 is text-only: no inline image bytes")
	})

	t.Run("degrades to frame + point when no element", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{FrameHandle: visualFramePath}
		v.Point.X = 10
		v.Point.Y = 20
		ctx := host.WithVisualAmbient(context.Background(), v)
		got := host.VisualAmbientPreamble(ctx)
		assert.Contains(t, got, visualHeader)
		assert.Contains(t, got, "(10,20)")
		assert.Contains(t, got, visualFramePath)
		assert.NotContains(t, got, "Element:", "no element resolved ⇒ no element line")
	})

	t.Run("bare handle renders as-is", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{FrameHandle: "media:frame-handle-xyz"}
		ctx := host.WithVisualAmbient(context.Background(), v)
		got := host.VisualAmbientPreamble(ctx)
		assert.Contains(t, got, "media:frame-handle-xyz",
			"an unresolved handle is rendered as-is for a downstream resolver")
	})

	t.Run("renders semantic field context without a fake point", func(t *testing.T) {
		t.Parallel()
		v := host.VisualAmbient{
			MediaHandle: "issue-card.html",
			Route:       "/s/sess-1",
			Anchor: host.AnnotationAnchor{
				Kind: host.AnchorSemanticElement,
				SemanticElement: &host.AnchorSemanticElementTarget{
					Plugin:       "html-data",
					Ref:          "issue.status",
					SemanticKind: "field",
					Label:        "Status",
					Selector:     "[data-field=status]",
					Text:         "Blocked",
					Data:         map[string]any{"path": "issue.status"},
					Bbox:         [4]int{20, 30, 120, 24},
				},
			},
		}
		ctx := host.WithVisualAmbient(context.Background(), v)
		got := host.VisualAmbientPreamble(ctx)
		assert.Contains(t, got, "Semantic element: Status")
		assert.Contains(t, got, "plugin=html-data")
		assert.Contains(t, got, "kind=field")
		assert.Contains(t, got, "ref=issue.status")
		assert.Contains(t, got, "selector=[data-field=status]")
		assert.Contains(t, got, `text "Blocked"`)
		assert.Contains(t, got, "bbox=[20,30,120,24]")
		assert.Contains(t, got, `"path":"issue.status"`)
		assert.NotContains(t, got, "pointing at (0,0)",
			"a semantic-only anchor must not be described as a fake pixel click")
	})
}

func TestAgentConverse_InjectsVisualAmbient(t *testing.T) {
	t.Parallel()

	t.Run("with ambient", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(visualCtxOn(context.Background()), captureStdinRunner(&stdin))
		res, err := host.AgentConverseHandler(ctx, map[string]any{"question": "what is this?"})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Contains(t, stdin, "what is this?", "the original question must survive")
		assert.Contains(t, stdin, visualHeader, "screen-context block must be appended")
		assert.Contains(t, stdin, "[data-testid=intent-btn-run]")
		assert.Contains(t, stdin, visualFramePath)
	})

	t.Run("without ambient is byte-identical", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(context.Background(), captureStdinRunner(&stdin))
		res, err := host.AgentConverseHandler(ctx, map[string]any{"question": "what is this?"})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Equal(t, "what is this?", stdin,
			"no screen context must leave the question unchanged")
	})
}

func TestAgentAsk_InjectsVisualAmbient(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	require.NoError(t, os.WriteFile(p, []byte("INSTRUCTIONS"), 0o644))

	t.Run("with ambient", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(visualCtxOn(context.Background()), captureStdinRunner(&stdin))
		res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": p})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Contains(t, stdin, "INSTRUCTIONS", "the original prompt must survive")
		assert.Contains(t, stdin, visualHeader)
		assert.Contains(t, stdin, visualFramePath)
	})

	t.Run("without ambient is byte-identical", func(t *testing.T) {
		var stdin string
		ctx := host.WithClaudeRunner(context.Background(), captureStdinRunner(&stdin))
		res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": p})
		require.NoError(t, err)
		require.Empty(t, res.Error)
		assert.Equal(t, "INSTRUCTIONS", stdin,
			"no screen context must leave the prompt unchanged")
	})
}

// TestAgentAsk_VisualTemplateVars proves the opt-in `{{ args.visual.* }}`
// template scope resolves in a pongo2 prompt (mergeVisualAmbient), mirroring
// args.ide.*. The prompt references the selector, the click point, and the
// frame path; all three must render from the ambient on the ctx.
func TestAgentAsk_VisualTemplateVars(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.md")
	tmpl := "Looking at {{ args.visual.element.selector }} " +
		"({{ args.visual.element.text }}) at {{ args.visual.point.x }},{{ args.visual.point.y }} " +
		"in frame {{ args.visual.frame }}."
	require.NoError(t, os.WriteFile(p, []byte(tmpl), 0o644))

	var stdin string
	ctx := host.WithClaudeRunner(visualCtxOn(context.Background()), captureStdinRunner(&stdin))
	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": p})
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.Contains(t, stdin, "[data-testid=intent-btn-run]")
	assert.Contains(t, stdin, "(Run)")
	assert.Contains(t, stdin, "1180,540")
	assert.Contains(t, stdin, visualFramePath)
}
