package wire_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/mining"
	"kitsoki/internal/mining/wire"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// These are the no-LLM flow-gate integration tests for the mining accept loop
// (Tasks 2.1 / 2.2). They drive a REAL orchestrator (via wire.Reloader) and the
// REAL testrunner.RunFlows gate (via wire.FlowGate) — no LLM, no cassette: the
// recipe is world-injected and the author draft is a known-good YAML delta, so
// the only thing under test is the engine apply→reload→gate→keep/revert spine.

// baseApp is the live tree's entry manifest. The `back` intent lands the desk
// on itself; the `quit` intent ends. existing_flow.yaml exercises both.
const baseApp = `app:
  id: mining-accept-fixture
  version: 0.1.0
  title: mining-accept-fixture

world:
  visited: { type: string, default: "(nowhere)" }

intents:
  back:
    title: "Back"
    examples: ["back"]
    priority: 60
  quit:
    title: "Quit"
    examples: ["quit"]
    priority: 10

root: desk

states:
  desk:
    description: "Desk"
    view:
      - prose: "At the desk."
    on:
      back:
        - target: .
      quit:
        - target: ended
  ended:
    terminal: true
    description: "Closed"
    view:
      - prose: "Closed."
`

// existingFlow drives only the base intents — it stays green across a benign
// (additive) edit and FAILS when the edit removes the `back` intent it uses.
const existingFlow = `test_kind: flow
app: ../app.yaml
initial_state: desk
turns:
  - intent: { name: back }
    expect_state: desk
  - intent: { name: quit }
    expect_state: ended
`

// scaffold writes the base tree (app.yaml + flows/existing_flow.yaml) into a
// fresh temp dir and returns the tree root.
func scaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "app.yaml"), []byte(baseApp), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "flows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "flows", "existing_flow.yaml"), []byte(existingFlow), 0o644))
	return root
}

// liveOrchestrator builds a real orchestrator over the tree at root and starts a
// session sitting in `desk`, so RerunOnEnter has a journey to re-fire. The store
// is returned so the test can read the mining events off the trace.
func liveOrchestrator(t *testing.T, root string) (*orchestrator.Orchestrator, store.Store, app.SessionID) {
	t.Helper()
	appPath := filepath.Join(root, "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(hostReg))

	ctx := t.Context()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))
	return orch, s, sid
}

func newApplier(orch *orchestrator.Orchestrator, root string, sid app.SessionID) *mining.Applier {
	return &mining.Applier{
		TreeRoot: root,
		Entry:    "app.yaml",
		State:    app.StatePath("desk"),
		SID:      sid,
		FlowGlob: "flows/*.yaml",
		Reloader: wire.Reloader(orch),
		FlowGate: wire.FlowGate(testrunner.FlowOptions{}),
	}
}

// decided reads the single MiningProposalDecided off the session trace.
func decided(t *testing.T, s store.Store, sid app.SessionID) store.MiningProposalDecidedPayload {
	t.Helper()
	hist, err := s.LoadHistory(sid)
	require.NoError(t, err)
	var found *store.MiningProposalDecidedPayload
	for _, ev := range hist {
		if ev.Kind == store.MiningProposalDecided {
			var dp store.MiningProposalDecidedPayload
			require.NoError(t, json.Unmarshal(ev.Payload, &dp))
			found = &dp
		}
	}
	require.NotNil(t, found, "a MiningProposalDecided event must be on the trace")
	return *found
}

// TestMiningAcceptLive — Task 2.1. An additive delta (a new `ping` intent that
// loops the desk on itself) is accepted: the gate stays green, the edit is
// LIVE, the new intent is routable after the reload, and the verdict is
// {accept, flows_green:true, reverted:false}.
func TestMiningAcceptLive(t *testing.T) {
	root := scaffold(t)
	orch, s, sid := liveOrchestrator(t, root)

	// The author's drafted delta: add a `ping` intent + its self-transition,
	// plus a flow that exercises it (so the new structure is itself gated).
	newApp := `app:
  id: mining-accept-fixture
  version: 0.1.0
  title: mining-accept-fixture

world:
  visited: { type: string, default: "(nowhere)" }

intents:
  back:
    title: "Back"
    examples: ["back"]
    priority: 60
  ping:
    title: "Ping"
    examples: ["ping"]
    priority: 50
  quit:
    title: "Quit"
    examples: ["quit"]
    priority: 10

root: desk

states:
  desk:
    description: "Desk"
    view:
      - prose: "At the desk."
    on:
      back:
        - target: .
      ping:
        - target: .
          effects:
            - set: { visited: "pinged" }
      quit:
        - target: ended
  ended:
    terminal: true
    description: "Closed"
    view:
      - prose: "Closed."
`
	pingFlow := `test_kind: flow
app: ../app.yaml
initial_state: desk
turns:
  - intent: { name: ping }
    expect_state: desk
    expect_world: { visited: "pinged" }
`
	prop := &mining.Proposal{
		Recipe: mining.Recipe{ID: "r-ping", Kind: mining.KindIntent, Target: store.MiningTargetRootInstance},
		Rung:   2,
		Files: map[string][]byte{
			"app.yaml":             []byte(newApp),
			"flows/ping_flow.yaml": []byte(pingFlow),
		},
	}

	res, err := newApplier(orch, root, sid).Accept(t.Context(), prop, &mining.SessionSink{SID: sid, Sink: orch})
	require.NoError(t, err)
	assert.True(t, res.FlowsGreen, "the no-LLM flow suite must stay green for an additive delta")
	assert.False(t, res.Reverted)

	// The edit is LIVE on disk and in the reloaded orchestrator.
	live, _ := os.ReadFile(filepath.Join(root, "app.yaml"))
	assert.Equal(t, newApp, string(live))
	allowed := orch.AllowedIntents(app.StatePath("desk"), orch.CurrentWorld(sid))
	var names []string
	for _, ai := range allowed {
		names = append(names, ai.Name)
	}
	assert.Contains(t, names, "ping", "the new intent must be routable after reload")

	dp := decided(t, s, sid)
	assert.Equal(t, store.MiningVerdictAccept, dp.Verdict)
	assert.True(t, dp.FlowsGreen)
	assert.False(t, dp.Reverted)
}

// TestMiningAcceptBreaksFixture — Task 2.2. A delta that removes the `back`
// intent an existing fixture uses fails the gate: the pre-edit tree is restored
// byte-for-byte, the app is re-Reloaded to the original, and the verdict is
// {flows_green:false, reverted:true} with the proposal held.
func TestMiningAcceptBreaksFixture(t *testing.T) {
	root := scaffold(t)
	orch, s, sid := liveOrchestrator(t, root)

	originalApp, err := os.ReadFile(filepath.Join(root, "app.yaml"))
	require.NoError(t, err)

	// A regressing delta: drop the `back` intent + its transition. The desk's
	// existing_flow.yaml fires `back` on turn 1 → ValidationFailed → gate red.
	brokenApp := `app:
  id: mining-accept-fixture
  version: 0.1.0
  title: mining-accept-fixture

world:
  visited: { type: string, default: "(nowhere)" }

intents:
  quit:
    title: "Quit"
    examples: ["quit"]
    priority: 10

root: desk

states:
  desk:
    description: "Desk"
    view:
      - prose: "At the desk."
    on:
      quit:
        - target: ended
  ended:
    terminal: true
    description: "Closed"
    view:
      - prose: "Closed."
`
	prop := &mining.Proposal{
		Recipe: mining.Recipe{ID: "r-break", Kind: mining.KindIntent, Target: store.MiningTargetRootInstance},
		Rung:   2,
		Files:  map[string][]byte{"app.yaml": []byte(brokenApp)},
	}

	res, err := newApplier(orch, root, sid).Accept(t.Context(), prop, &mining.SessionSink{SID: sid, Sink: orch})
	require.NoError(t, err, "a clean revert is not an error")
	assert.False(t, res.FlowsGreen)
	assert.True(t, res.Reverted)
	assert.Greater(t, res.FailedFlows, 0)

	// Byte-for-byte restore of the live tree.
	restored, _ := os.ReadFile(filepath.Join(root, "app.yaml"))
	assert.Equal(t, originalApp, restored, "the pre-edit app.yaml must be restored byte-for-byte")

	// The reloaded orchestrator is back on the original graph: `back` routes.
	allowed := orch.AllowedIntents(app.StatePath("desk"), orch.CurrentWorld(sid))
	var names []string
	for _, ai := range allowed {
		names = append(names, ai.Name)
	}
	assert.Contains(t, names, "back", "the original `back` intent must be live again after revert")

	dp := decided(t, s, sid)
	assert.Equal(t, store.MiningVerdictAccept, dp.Verdict)
	assert.False(t, dp.FlowsGreen)
	assert.True(t, dp.Reverted)
}

// noopHarness is a do-nothing harness for orchestrator construction (the flow
// gate dispatches intents directly via RunIntent, never the harness).
type noopHarness struct{}

func (noopHarness) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (noopHarness) Close() error { return nil }
