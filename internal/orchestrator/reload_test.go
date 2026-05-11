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
