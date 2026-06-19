package orchestrator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// buildSynthLikeApp builds a tiny in-memory AppDef whose view echoes a world
// key set from a config-derived value — standing in for a config-synthesized
// root (no app.yaml on disk). The `tag` becomes the instance-level world
// default, modelling how a rung-1 overrides edit reshapes the synthesized def.
func buildSynthLikeApp(tag string) *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "synth-reload-test", Title: "synth-reload-test"},
		Root: "start",
		World: map[string]app.VarDef{
			"tag":     {Type: "string", Default: tag},
			"counter": {Type: "int", Default: 0},
		},
		Intents: map[string]app.Intent{"look": {Title: "Look"}},
		States: map[string]*app.State{
			"start": {
				OnEnter: []app.Effect{
					{Set: map[string]any{"counter": "{{ world.counter + 1 }}"}},
				},
				// The view template itself varies by tag, so a re-synthesized def
				// (built from the mutated config) drives an observably different
				// render even though the LIVE world's tag value is unchanged —
				// isolating "the swapped def is what renders" from world state.
				View: app.LegacyView("render=" + tag + " counter={{ world.counter }}"),
				On:   map[string][]app.Transition{"look": {{Target: "."}}},
			},
		},
	}
}

// TestReload_InjectedReloader_ReSynthesizesAndPreservesWorld is the
// implicit-project-root reload contract: a config-synthesized root (no app.yaml
// path) reloads via the injected reloader closure — Reload ignores appPath and
// re-fetches the def from the closure, which re-reads a temp .kitsoki.yaml on
// disk — and RerunOnEnter preserves the live world (the counter built up across
// turns survives the def swap). Mirrors the file-backed reload test with the
// injected reloader as the only difference.
func TestReload_InjectedReloader_ReSynthesizesAndPreservesWorld(t *testing.T) {
	// A temp ".kitsoki.yaml" whose single line is the tag the reloader projects
	// into the synthesized def — mutating it models a rung-1 overrides edit.
	cfgPath := filepath.Join(t.TempDir(), ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("v1"), 0o644))

	reloadCount := 0
	reloader := func() (*app.AppDef, error) {
		b, err := os.ReadFile(cfgPath)
		if err != nil {
			return nil, err
		}
		reloadCount++
		return buildSynthLikeApp(strings.TrimSpace(string(b))), nil
	}

	def := buildSynthLikeApp("v1")
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithReloader(reloader),
	)

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	// Build up live world: counter == 1 after the initial on_enter.
	w := orch.CurrentWorld(sid)
	require.Equal(t, int64(1), toInt64(w.Vars["counter"]))
	require.Equal(t, "v1", w.Vars["tag"])

	// Mutate the config on disk — a rung-1 overrides edit.
	require.NoError(t, os.WriteFile(cfgPath, []byte("v2"), 0o644))

	// Reload with a BOGUS appPath: the injected reloader must be used instead,
	// so a nonexistent path is irrelevant (proves appPath is ignored).
	res, err := orch.Reload("/nonexistent/app.yaml", app.StatePath("start"))
	require.NoError(t, err, "reload must use the injected closure, not app.Load(appPath)")
	require.Equal(t, 1, reloadCount, "reloader closure should have been invoked exactly once")
	assert.True(t, res.PrevStateExists, "start still exists in the re-synthesized def")
	assert.Same(t, res.Def, orch.AppDef(), "Reload swaps the re-synthesized def in")

	// RerunOnEnter re-fires on_enter against the LIVE world (counter survives the
	// def swap and advances to 2), and the new tag from the mutated config shows
	// up in the freshly rendered view.
	out, err := orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	w = orch.CurrentWorld(sid)
	assert.Equal(t, int64(2), toInt64(w.Vars["counter"]),
		"the live counter must be preserved across the synthesized reload, then advance")
	assert.Contains(t, out.View, "render=v2",
		"the re-synthesized def (from the mutated config) must drive the new render")
}
