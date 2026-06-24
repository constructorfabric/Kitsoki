package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// stubIDELink is a minimal host.IDELink for exercising the world.ide.connected
// gate. It is never dialed — Connected() returns a fixed value and CallTool is
// never invoked by these tests (the story has no host calls).
type stubIDELink struct{ connected bool }

func (s *stubIDELink) CallTool(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, nil
}
func (s *stubIDELink) Connected() bool   { return s.connected }
func (s *stubIDELink) IDEName() string   { return "stub-editor" }
func (s *stubIDELink) Workspace() string { return "" }
func (s *stubIDELink) Port() int         { return 0 }

// TestOrchestratorWorldIDEConnectedGate exercises the documented world gate:
// a story whose transition is guarded by `when: world.ide.connected` (NESTED
// navigation, World["ide"]["connected"]) must take the connected branch when a
// live IDELink is attached and the not-connected branch when none is — even
// though the story declares NO host calls of its own (so the seam that seeds
// the gate is loadJourney, not host dispatch). This is the coverage that was
// missing while the key was written as an unreachable flat dotted key.
func TestOrchestratorWorldIDEConnectedGate(t *testing.T) {
	const appYAML = `
app:
  id: ide-gate-test
  version: 0.1.0

world: {}

intents:
  go:
    title: "Go"

root: start

states:
  start:
    view: "start"
    on:
      go:
        - when: "world.ide.connected"
          target: with_ide
        - default: true
          target: no_ide

  with_ide:
    terminal: true
    view: "editor connected"

  no_ide:
    terminal: true
    view: "no editor"
`

	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	newOrch := func(t *testing.T) (*orchestrator.Orchestrator, store.Store) {
		t.Helper()
		m, err := machine.New(def)
		require.NoError(t, err)
		s, err := store.OpenMemory()
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return orchestrator.New(def, m, s, &staticHarness{intentName: "go"}), s
	}

	ctx := context.Background()

	t.Run("connected link takes the with_ide branch", func(t *testing.T) {
		orch, _ := newOrch(t)
		orch.SetIDELink(&stubIDELink{connected: true})
		sid, err := orch.NewSession(ctx)
		require.NoError(t, err)
		out, err := orch.Turn(ctx, sid, "go")
		require.NoError(t, err)
		require.Equal(t, app.StatePath("with_ide"), out.NewState,
			"world.ide.connected==true must route to with_ide")
	})

	t.Run("nil link takes the no_ide branch", func(t *testing.T) {
		orch, _ := newOrch(t)
		// No SetIDELink → o.ideLink is nil → world.ide.connected is false.
		sid, err := orch.NewSession(ctx)
		require.NoError(t, err)
		out, err := orch.Turn(ctx, sid, "go")
		require.NoError(t, err)
		require.Equal(t, app.StatePath("no_ide"), out.NewState,
			"a nil link must route to no_ide")
	})

	t.Run("disconnected link takes the no_ide branch", func(t *testing.T) {
		orch, _ := newOrch(t)
		orch.SetIDELink(&stubIDELink{connected: false})
		sid, err := orch.NewSession(ctx)
		require.NoError(t, err)
		out, err := orch.Turn(ctx, sid, "go")
		require.NoError(t, err)
		require.Equal(t, app.StatePath("no_ide"), out.NewState,
			"a link reporting Connected()==false must route to no_ide")
	})
}
