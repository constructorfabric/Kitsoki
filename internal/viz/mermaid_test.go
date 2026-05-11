package viz_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

func TestMermaidExporterCloak(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	out, err := viz.MermaidBytes(def)
	require.NoError(t, err)
	s := string(out)
	t.Logf("Mermaid output:\n%s", s)

	require.True(t, strings.HasPrefix(strings.TrimSpace(s), "stateDiagram-v2"),
		"output should start with stateDiagram-v2")

	// Initial-state arrow.
	require.Contains(t, s, "[*] --> foyer")
	// Compound state opens a block.
	require.Contains(t, s, `state "bar" as bar {`)
	// Nested children declared with full-path labels and underscore IDs.
	require.Contains(t, s, `state "bar.dark" as bar_dark`)
	require.Contains(t, s, `state "bar.lit" as bar_lit`)
	// Terminal state has --> [*].
	require.Contains(t, s, "ended --> [*]")
	// Slash-relative target resolves correctly: bar.dark --> ../../foyer
	// must land on `foyer`, not `bar_dark_foyer`.
	require.Contains(t, s, "bar_dark --> foyer :")
	require.NotContains(t, s, "bar_dark_foyer")
	// Read-message edge into the terminal.
	require.Contains(t, s, "bar_lit --> ended : read_message")
}

func TestMermaidMinimalApp(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "test", Title: "Test"},
		Root: "start",
		States: map[string]*app.State{
			"start":  {On: map[string][]app.Transition{"next": {{Target: "finish"}}}},
			"finish": {Terminal: true},
		},
	}
	out, err := viz.MermaidBytes(def)
	require.NoError(t, err)
	s := string(out)
	require.Contains(t, s, "[*] --> start")
	require.Contains(t, s, "start --> finish : next")
	require.Contains(t, s, "finish --> [*]")
}
