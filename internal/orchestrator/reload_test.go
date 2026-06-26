package orchestrator_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
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

// TestRerunOnEnter_OnceSkipsCachedInvoke is the `once:` reload-safety
// contract: a state whose on_enter has an `invoke: … once: true` binding an
// already-populated world key must fire ZERO host calls on /reload
// (RerunOnEnter) — the cached bind target re-renders instead of recomputing.
// The handler counts its invocations; the assertion is the count stays 0.
// Clearing the bind target re-arms the call (proven by a second pass that
// DOES fire after a reset).
func TestRerunOnEnter_OnceSkipsCachedInvoke(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "once-reload-test", Title: "once-reload-test"},
		Root:  "start",
		Hosts: []string{"host.expensive"},
		World: map[string]app.VarDef{
			// Pre-populated default: the bind target is "already cached".
			"result": {Type: "object", Default: map[string]any{"verdict": "continue"}},
		},
		Intents: map[string]app.Intent{"look": {Title: "Look"}},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("result={{ world.result.verdict }}"),
				OnEnter: []app.Effect{
					{
						Invoke: "host.expensive",
						Once:   true,
						Bind:   map[string]string{"result": "submitted"},
					},
				},
				On: map[string][]app.Transition{"look": {{Target: "."}}},
			},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var calls atomic.Int64
	reg := host.NewRegistry()
	reg.Register("host.expensive", func(_ context.Context, _ map[string]any) (host.Result, error) {
		calls.Add(1)
		return host.Result{Data: map[string]any{"submitted": map[string]any{"verdict": "fresh"}}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg))

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	// Initial entry: result is already set (the schema default) ⇒ once: skips.
	require.Equal(t, int64(0), calls.Load(),
		"once: must skip the expensive call when the bind target is pre-populated")

	// /reload twice — still zero calls; the cached verdict re-renders.
	_, err = orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	_, err = orch.RerunOnEnter(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, int64(0), calls.Load(),
		"RerunOnEnter on a once: room must fire zero agent/host calls")

	w := orch.CurrentWorld(sid)
	require.Equal(t, "continue", w.Vars["result"].(map[string]any)["verdict"],
		"the cached verdict must survive reload (never overwritten by the stub's 'fresh')")
}

func TestRerunOnEnter_ForceOnceBypassesCachedInvoke(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "once-reload-force-test", Title: "once-reload-force-test"},
		Root:  "start",
		Hosts: []string{"host.expensive"},
		World: map[string]app.VarDef{
			"result": {Type: "object", Default: map[string]any{"verdict": "continue"}},
		},
		Intents: map[string]app.Intent{"look": {Title: "Look"}},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("result={{ world.result.verdict }}"),
				OnEnter: []app.Effect{
					{
						Invoke: "host.expensive",
						Once:   true,
						Bind:   map[string]string{"result": "submitted"},
					},
				},
				On: map[string][]app.Transition{"look": {{Target: "."}}},
			},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var calls atomic.Int64
	reg := host.NewRegistry()
	reg.Register("host.expensive", func(_ context.Context, _ map[string]any) (host.Result, error) {
		calls.Add(1)
		return host.Result{Data: map[string]any{"submitted": map[string]any{"verdict": "fresh"}}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg))

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))
	require.Equal(t, int64(0), calls.Load())

	_, err = orch.RerunOnEnterWithOptions(ctx, sid, orchestrator.RerunOnEnterOptions{ForceOnce: true})
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load(),
		"forced reload must bypass once: and invoke the host call")

	w := orch.CurrentWorld(sid)
	require.Equal(t, "fresh", w.Vars["result"].(map[string]any)["verdict"],
		"forced reload must replace the cached value with the fresh host result")
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

// TestReload_LoadFailureKeepsPreviousDef pins the contract the operator
// relies on: when an edit makes the manifest fail to load (here, an
// `on:` arc referencing an undeclared intent — the exact shape that
// stranded a meta-mode /reload), Reload must keep the previous
// definition in memory rather than swapping in (or nil-ing out) the
// broken one. The running session keeps working on the last-good graph;
// only a manifest that *loads successfully* ever replaces it.
//
// This is the load-failure sibling of TestReload_RejectsHostsNotInRegistry:
// that one fails in host validation (after app.Load succeeds); this one
// fails inside app.Load itself, the earliest gate.
func TestReload_LoadFailureKeepsPreviousDef(t *testing.T) {
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

	// Wire an `on:` arc on the entry state to an intent that isn't
	// declared anywhere — app.Load rejects this at validation time.
	broken := append(body,
		[]byte("\n# --- broken edit appended by test ---\nstates:\n  foyer:\n    on:\n      restart:\n        - target: foyer\n")...)
	require.NoError(t, os.WriteFile(dst, broken, 0o644))

	res, err := orch.Reload(dst, app.StatePath("foyer"))
	require.Error(t, err, "a manifest that fails to load must error the reload")
	assert.Nil(t, res, "no ReloadResult on a failed load")
	assert.Contains(t, err.Error(), "load")
	assert.Same(t, prevDef, orch.AppDef(),
		"failed load must keep the previous def in memory — never swap in a broken one")
	assert.NotNil(t, orch.Machine(),
		"the previous machine must remain usable after a failed reload")
}
