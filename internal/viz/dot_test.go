package viz_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

func TestDOTExporterCloak(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	var buf bytes.Buffer
	err = viz.Export(def, &buf)
	require.NoError(t, err)

	dot := buf.String()
	t.Logf("DOT output:\n%s", dot)

	// Must be a valid DOT digraph.
	require.Contains(t, dot, "digraph", "should be a directed graph")

	// Must contain the key states as node labels.
	require.Contains(t, dot, "foyer", "should contain foyer state")
	require.Contains(t, dot, "cloakroom", "should contain cloakroom state")
	require.Contains(t, dot, "bar", "should contain bar state")
	require.Contains(t, dot, "ended", "should contain ended terminal state")

	// Must contain intent-labelled edges.
	require.Contains(t, dot, "go", "should contain go intent edge")
	require.Contains(t, dot, "hang_cloak", "should contain hang_cloak edge")
	require.Contains(t, dot, "read_message", "should contain read_message edge")
}

func TestDOTExporterBytes(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	b, err := viz.DOTBytes(def)
	require.NoError(t, err)
	require.NotEmpty(t, b)

	dot := string(b)
	require.True(t, strings.HasPrefix(strings.TrimSpace(dot), "digraph") ||
		strings.Contains(dot, "digraph"),
		"output should be a valid DOT digraph")
}

func TestDOTMinimalApp(t *testing.T) {
	// Minimal app with two states and one transition.
	def := &app.AppDef{
		App:  app.AppMeta{ID: "test", Title: "Test App"},
		Root: "start",
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("Start state"),
				On: map[string][]app.Transition{
					"next": {{Target: "finish"}},
				},
			},
			"finish": {
				Terminal: true,
				View:     app.LegacyView("Done"),
			},
		},
	}

	var buf bytes.Buffer
	err := viz.Export(def, &buf)
	require.NoError(t, err)

	dot := buf.String()
	require.Contains(t, dot, "start")
	require.Contains(t, dot, "finish")
	require.Contains(t, dot, "next")
}
