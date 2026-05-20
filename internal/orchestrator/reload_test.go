package orchestrator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestReload_PrevStateStillExists verifies that a benign edit (no state
// removed) keeps the user's current state path resolvable.
func TestReload_PrevStateStillExists(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	body, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, body, 0o644))

	def, err := app.Load(dst)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
	)

	// Append an unrelated comment — no schema change.
	updated := append([]byte("# touched\n"), body...)
	require.NoError(t, os.WriteFile(dst, updated, 0o644))

	res, err := orch.Reload(dst, app.StatePath("foyer"))
	require.NoError(t, err)
	assert.True(t, res.PrevStateExists, "foyer should still exist after a no-op edit")
	assert.Same(t, res.Def, orch.AppDef(), "Reload must swap def into orchestrator")
}

// TestReload_PushesDefToHarness verifies that a harness implementing
// defSetter (here ClaudeCLIHarness) sees its appDef swapped on Reload,
// so the LLM-router's system prompt reflects the new app on the very
// next turn. We don't actually run a turn; we assert the harness's
// internal appDef pointer changed.
func TestReload_PushesDefToHarness(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	body, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, body, 0o644))

	def, err := app.Load(dst)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Use a real ClaudeCLI harness so we exercise SetAppDef.
	h, err := harness.NewClaudeCLI(def, harness.ClaudeCLIConfig{})
	require.NoError(t, err)
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(hostReg),
	)

	// Touch the file so Reload re-loads (any benign edit will do).
	require.NoError(t, os.WriteFile(dst, append([]byte("# touched\n"), body...), 0o644))

	res, err := orch.Reload(dst, app.StatePath("foyer"))
	require.NoError(t, err)
	// Harness's def should now match the new orchestrator def, not the
	// original.
	assert.Same(t, res.Def, h.AppDef(),
		"Reload must push the new app def into the harness")
}

// TestRerunOnEnter_ReFiresOnEnterEffects covers the `/reload` payoff
// case: a state's on_enter has a set: effect, and a second
// RerunOnEnter applies it again so a counter advances. This locks in
// that "redo whatever actions" is more than a no-op render — it
// actually walks the on_enter chain twice.
func TestRerunOnEnter_ReFiresOnEnterEffects(t *testing.T) {
	src := []byte(`
app:
  id: rerun-test
  version: 0.1.0
  title: rerun-test

world:
  counter: { type: int, default: 0 }

intents:
  look:
    title: "Look"

root: start

states:
  start:
    on_enter:
      - set:
          counter: "{{ world.counter + 1 }}"
    view: "counter={{ world.counter }}"
    on:
      look:
        - target: .
`)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, src, 0o644))

	def, err := app.Load(dst)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
	)

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	// Sanity: initial on_enter ran once → counter == 1.
	w := orch.CurrentWorld(sid)
	require.Equal(t, int64(1), toInt64(w.Vars["counter"]),
		"initial on_enter should bump counter to 1")

	// First /reload — on_enter fires again, counter advances to 2.
	out, err := orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	assert.NotEmpty(t, out.View, "RerunOnEnter must return a rendered view")
	assert.Equal(t, app.StatePath("start"), out.NewState)
	w = orch.CurrentWorld(sid)
	assert.Equal(t, int64(2), toInt64(w.Vars["counter"]),
		"RerunOnEnter should re-apply set: and bump counter")

	// Second /reload — verifies the action is genuinely repeatable
	// (not just a one-shot side effect of the new turn number).
	_, err = orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	w = orch.CurrentWorld(sid)
	assert.Equal(t, int64(3), toInt64(w.Vars["counter"]),
		"repeated RerunOnEnter should keep advancing the counter")
}

// TestRerunOnEnter_NoOnEnter_StillRenders covers the "edited a view
// template only" path: a state without on_enter should still produce a
// rendered view so callers don't need a separate render-only branch.
func TestRerunOnEnter_NoOnEnter_StillRenders(t *testing.T) {
	src := []byte(`
app:
  id: rerun-render-test
  version: 0.1.0
  title: rerun-render-test

intents:
  look:
    title: "Look"

root: start

states:
  start:
    view: "hello"
    on:
      look:
        - target: .
`)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, src, 0o644))

	def, err := app.Load(dst)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
	)

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	assert.Contains(t, out.View, "hello")
	assert.Equal(t, app.StatePath("start"), out.NewState)
}

// TestReload_RejectsHostsNotInRegistry verifies the host allow-list
// re-validation: if the new YAML declares a host the registry can't
// satisfy, Reload errors out and the orchestrator is untouched.
func TestReload_RejectsHostsNotInRegistry(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	body, err := os.ReadFile(src)
	require.NoError(t, err)
	dst := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(dst, body, 0o644))

	def, err := app.Load(dst)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	prevDef := def
	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
	)

	// Inject a hosts: stanza that the registry can't satisfy.
	bad := append([]byte("hosts:\n  - host.does.not.exist\n"), body...)
	require.NoError(t, os.WriteFile(dst, bad, 0o644))

	_, err = orch.Reload(dst, app.StatePath("foyer"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validate hosts")
	assert.Same(t, prevDef, orch.AppDef(),
		"failed reload must leave orchestrator's def untouched")
}
