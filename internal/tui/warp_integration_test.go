package tui

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// osWriteFile / mkdirAll / rmAll are inlined wrappers used only by the
// warp file-form tests; named to avoid stomping on any top-level
// helpers in the same package.
func osWriteFile(path string, body []byte) error { return os.WriteFile(path, body, 0o644) }
func mkdirAll(path string) error                 { return os.MkdirAll(path, 0o755) }

// TestWarp_Interactive_Smoke is the regression test for the user-reported
// "warp isn't working" bug. Drives the slash-command handler directly,
// asserts the Teleport runs to completion, the turnOutcomeMsg comes back
// without an error, and the TUI model has the new state set after the
// outcome is processed.
//
// Why an integration-shaped TUI test (rather than just the orchestrator
// teleport test): the user's path is `/warp foo world.x=1` typed into
// the prompt, which goes through handleSlashCommand → handleWarpCommand
// → an async tea.Cmd → turnOutcomeMsg → handleTurnOutcome. The
// orchestrator test only covers the middle slice. This test covers the
// edges where the bug actually lived.
func TestWarp_Interactive_Smoke(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")
	require.Equal(t, app.StatePath("intro"), root.currentState)

	// Drive the slash command directly — same path the routeKey handler
	// hits when the user types `/warp ...` and presses Enter.
	updated, cmd := root.handleSlashCommand(`/warp leg_c_awaiting_reply world.money=400 world.party_alive=5 world.current_landmark="Chimney Rock" world.miles_traveled=600`)
	require.NotNil(t, cmd, "/warp should return a tea.Cmd that runs the teleport")

	// Execute the command synchronously and route the resulting message
	// back through Update (bubbletea would do this in the event loop).
	msg := cmd()
	require.NotNil(t, msg, "tea.Cmd returned no message")

	outcomeMsg, ok := msg.(turnOutcomeMsg)
	require.True(t, ok, "expected turnOutcomeMsg; got %T", msg)
	require.NoError(t, outcomeMsg.err, "Teleport returned an error: %v", outcomeMsg.err)
	require.NotNil(t, outcomeMsg.outcome)
	require.Equal(t, app.StatePath("leg_c_awaiting_reply"), outcomeMsg.outcome.NewState)
	require.NotEmpty(t, outcomeMsg.outcome.View, "view must render at the teleport target")

	// Update the model with the outcome — verifies handleTurnOutcome
	// applies state and menu changes cleanly.
	next, _ := updated.(RootModel).Update(outcomeMsg)
	nextModel := next.(RootModel)
	require.Equal(t, app.StatePath("leg_c_awaiting_reply"), nextModel.currentState,
		"currentState must update after the turnOutcomeMsg is processed")

	// World overrides should have landed in the new state.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "Chimney Rock", journey.World.Vars["current_landmark"])
	require.Equal(t, int64(400), int64Of(journey.World.Vars["money"]))
	require.Equal(t, int64(5), int64Of(journey.World.Vars["party_alive"]))
}

// TestWarp_UnknownState verifies the state-validation guard fires
// before any orchestrator call.
func TestWarp_UnknownState(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")

	// State that doesn't exist.
	_, cmd := root.handleSlashCommand(`/warp nonexistent_state world.money=1`)
	require.Nil(t, cmd, "unknown state should not dispatch a tea.Cmd; the handler appended a system message instead")
}

// TestWarp_FromFile_Canonical writes a canonical warp-basis YAML
// (state + world) and verifies the file-form loader resolves it,
// teleports to the named state, and merges the world overrides.
func TestWarp_FromFile_Canonical(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Write the basis file into a tmpdir; pass an absolute path so the
	// app-relative fallback doesn't fire.
	tmp := t.TempDir()
	basis := tmp + "/chimney.yaml"
	body := `name: "Chimney Rock encounter"
description: "Primed party at Chimney Rock for the robbery flow."
state: leg_c_awaiting_reply
world:
  money: 400
  party_alive: 5
  current_landmark: "Chimney Rock"
  miles_traveled: 600
`
	require.NoError(t, writeFile(basis, body))

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")
	_, cmd := root.handleSlashCommand("/warp file:" + basis)
	require.NotNil(t, cmd, "warp file should return a tea.Cmd")

	msg := cmd().(turnOutcomeMsg)
	require.NoError(t, msg.err)
	require.Equal(t, app.StatePath("leg_c_awaiting_reply"), msg.outcome.NewState)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "Chimney Rock", journey.World.Vars["current_landmark"])
	require.Equal(t, int64(400), int64Of(journey.World.Vars["money"]))
	require.Equal(t, int64(5), int64Of(journey.World.Vars["party_alive"]))
}

// TestWarp_FromFile_FlowFixtureAliases verifies the loader accepts the
// flow-fixture-style `initial_state` + `initial_world` keys so an
// existing flow fixture can double as a warp basis.
func TestWarp_FromFile_FlowFixtureAliases(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	tmp := t.TempDir()
	basis := tmp + "/legacy.yaml"
	body := `initial_state: leg_b_awaiting_reply
initial_world:
  money: 200
  current_landmark: "Fort Kearney"
  party_alive: 4
`
	require.NoError(t, writeFile(basis, body))

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")
	_, cmd := root.handleSlashCommand("/warp " + basis) // no `file:` prefix; .yaml suffix detected
	require.NotNil(t, cmd)

	msg := cmd().(turnOutcomeMsg)
	require.NoError(t, msg.err)
	require.Equal(t, app.StatePath("leg_b_awaiting_reply"), msg.outcome.NewState)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "Fort Kearney", journey.World.Vars["current_landmark"])
}

// TestWarp_FromFile_MissingState verifies the loader rejects a basis
// file with no `state:` (or `initial_state:`) field.
func TestWarp_FromFile_MissingState(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	tmp := t.TempDir()
	basis := tmp + "/noState.yaml"
	body := `description: "no state declared"
world:
  money: 1
`
	require.NoError(t, writeFile(basis, body))

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")
	_, cmd := root.handleSlashCommand("/warp file:" + basis)
	require.Nil(t, cmd, "missing state should surface as a system message; no tea.Cmd")
}

// TestWarp_FromFile_AppRelativeFallback verifies the path resolution
// falls back to the app's directory when the literal path doesn't
// exist relative to cwd.
func TestWarp_FromFile_AppRelativeFallback(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Write a basis next to the oregon-trail app.yaml — this is what
	// authors check in. From any cwd, `/warp file:scenarios/foo.yaml`
	// resolves via the app-relative fallback. Use a uniquely-named
	// file so a parallel test run cleaning it up doesn't stomp on
	// the real shipped scenarios in this directory.
	appPath := "../../stories/oregon-trail/app.yaml"
	scenariosDir := "../../stories/oregon-trail/scenarios"
	_ = mkdirAll(scenariosDir)
	basisRel := "scenarios/__test_fallback.yaml"
	basisAbs := "../../stories/oregon-trail/" + basisRel
	t.Cleanup(func() { _ = os.Remove(basisAbs) })
	body := `state: leg_a_awaiting_reply
world:
  current_landmark: "Kansas River Crossing"
`
	require.NoError(t, writeFile(basisAbs, body))

	root := NewRootModel(orch, sid, appPath, "")
	_, cmd := root.handleSlashCommand("/warp file:" + basisRel)
	require.NotNil(t, cmd, "app-relative fallback should resolve scenarios/fallback.yaml")
	msg := cmd().(turnOutcomeMsg)
	require.NoError(t, msg.err)
	require.Equal(t, app.StatePath("leg_a_awaiting_reply"), msg.outcome.NewState)
}

// writeFile is a tiny helper used only in this test file. Wraps
// os.WriteFile so the test bodies stay flat.
func writeFile(path, body string) error {
	return osWriteFile(path, []byte(body))
}

// TestWarp_MissingArgs verifies the usage hint fires when no args.
func TestWarp_MissingArgs(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarnessForWarp{})
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	root := NewRootModel(orch, sid, "../../stories/oregon-trail/app.yaml", "")

	_, cmd := root.handleSlashCommand(`/warp`)
	require.Nil(t, cmd, "missing-args case should not dispatch a tea.Cmd")
}

// noopHarnessForWarp is a stub harness — /warp never invokes the harness.
type noopHarnessForWarp struct{}

func (noopHarnessForWarp) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (noopHarnessForWarp) Close() error { return nil }

func int64Of(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
