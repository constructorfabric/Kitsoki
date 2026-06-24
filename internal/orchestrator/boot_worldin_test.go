package orchestrator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestRunInitialOnEnter_RootImportWorldIn pins the boot-time projection of a
// ROOT import's `world_in:`.
//
// Import-folding parks an import's `world_in:` setters on the import wrapper's
// compound `on_enter` (app/imports.go §6). A normal entry transition fires
// them, but the ROOT import (`root: core`) is reached by resolving the root
// compound's `initial:` chain at boot — there is no transition into it — so
// RunInitialOnEnter, which previously fired only the resolved LEAF's on_enter,
// silently skipped the ancestor compound and the child kept its own default.
//
// This is the regression the gears-rust external-target instance exposed: its
// profile keys (workdir, durable paths, template_dir, …) are projected into
// the imported dev-story hub purely via the root import's world_in, and none
// of them reached the child. The fix walks the full root→leaf on_enter chain
// at boot, mirroring stateEnterPaths("", initialState).
func TestRunInitialOnEnter_RootImportWorldIn(t *testing.T) {
	dir := t.TempDir()

	// Child app: imported under the alias `core`. Declares the profile key
	// with its OWN default; the parent must override it via world_in.
	childDir := filepath.Join(dir, "child")
	require.NoError(t, os.MkdirAll(childDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(childDir, "app.yaml"), []byte(`
app:
  id: child
  version: 0.1.0
  title: child

world:
  target_dir: { type: string, default: "child-default" }

intents:
  look:
    title: "Look"

root: start

states:
  start:
    view: "target={{ world.target_dir }}"
    on:
      look:
        - target: .
`), 0o644))

	// Parent instance: imports child as the ROOT and projects its own
	// instance-level target_dir into the child via the root import world_in.
	parentDir := filepath.Join(dir, "parent")
	require.NoError(t, os.MkdirAll(parentDir, 0o755))
	parentPath := filepath.Join(parentDir, "app.yaml")
	require.NoError(t, os.WriteFile(parentPath, []byte(`
app:
  id: parent
  version: 0.1.0
  title: parent

imports:
  core:
    source: ../child
    entry: start
    world_in:
      target_dir: "{{ world.target_dir }}"

world:
  target_dir: { type: string, default: "../external" }

root: core
`), 0o644))

	def, err := app.Load(parentPath)
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

	// The root import's world_in must have projected the parent's
	// target_dir into the child's prefixed key at boot. Before the fix this
	// stayed "child-default" because the root compound's on_enter never fired.
	w := orch.CurrentWorld(sid)
	require.Equal(t, "../external", w.Vars["core__target_dir"],
		"root import world_in must project at boot (got child default ⇒ ancestor on_enter skipped)")
}
