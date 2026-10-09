package testrunner_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// TestRunFlows_WorkbenchSmoke_AgentCallRecordsDispatchingStatePath is
// room-workbench Task 1.4(a): confirm that a desugared workbench: room's
// synthesized on_enter host.agent.task dispatch is legible in the trace as
// workbench-origin using ONLY the existing agent.call.* provenance — no new
// event kind. store.Event.StatePath is populated by the orchestrator at
// write time (internal/store/event.go) for every event including
// AgentCalled (internal/testrunner/cassette.go's writeCassetteAgentEvents,
// mirroring internal/host/agent_dispatch.go's live-dispatch equivalent);
// this test proves that for workbench_smoke's `bench` room, the synthesized
// dispatch's AgentCalled event carries state_path == "bench" — a reviewer
// scanning an unfamiliar trace can tell "this agent.call came from the
// workbench floor" from the existing field alone, per
// docs/architecture/room-workbench.md's Decision recording section ("carried
// as an existing field ... rather than a new event").
//
// This must read the run's authoritative JSONL sink via OnRigClose rather
// than TurnResult.Events: the latter is populated from
// orchestrator.Outcome.Events, which collects only the host-dispatch-loop's
// own harness.called/dispatched/returned bookkeeping events, NOT the
// AgentCalled/AgentReturned pair the cassette dispatcher writes straight to
// the deferred event sink (see FlowOptions.OnRigClose's own doc comment:
// "the JSONL sink is faithful ... whereas [TurnResult.Events] is lossy").
func TestRunFlows_WorkbenchSmoke_AgentCallRecordsDispatchingStatePath(t *testing.T) {
	const appPath = "../../testdata/apps/workbench_smoke/app.yaml"
	const glob = "../../testdata/apps/workbench_smoke/flows/workbench_walk_cassette.yaml"

	var sinkHistory store.History
	opts := testrunner.FlowOptions{
		OnRigClose: func(filePath string, st store.Store, sid app.SessionID, sink *store.JSONLSink) error {
			sinkHistory = append(sinkHistory, sink.History()...)
			return nil
		},
	}

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, opts)
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.Equal(t, 0, report.Failed, "all flows should pass")
	require.NotEmpty(t, report.Results, "should have at least one result")
	require.NotEmpty(t, sinkHistory, "OnRigClose should have been invoked with a non-empty sink")

	var agentCalledEvents []store.Event
	for _, ev := range sinkHistory {
		if ev.Kind == store.AgentCalled {
			agentCalledEvents = append(agentCalledEvents, ev)
		}
	}

	require.NotEmpty(t, agentCalledEvents, "workbench_walk_cassette.yaml's turn 1 dispatches the synthesized on_enter host.agent.task call through a real host_cassette (not host_handlers, which replaces the handler wholesale and never writes AgentCalled) and should produce at least one agent.call.start event in the JSONL sink")

	for _, ev := range agentCalledEvents {
		require.Equal(t, app.StatePath("bench"), ev.StatePath,
			"the synthesized workbench dispatch fires from bench's on_enter; its agent.call.start event must carry the dispatching state path so a trace reader can attribute it to the workbench floor without reading YAML")
	}
}

// TestRunFlows_WorkbenchSmoke_UsableKitsokiGateSignal is room-workbench Task
// 1.4(b): end-to-end proof that the S6 usable-kitsoki-gate producer contract
// (docs/tracing/usable-kitsoki-gate.md) is actually wired into a real
// workbench: room's turn — not just unit-tested in isolation
// (internal/orchestrator/workbench_gate_signal_test.go covers the pure
// function; this proves orchestrator.transitionedTurnEndWithGateSignal is
// really called on workbench_smoke's turn.end event). Reads the same JSONL
// sink as the sibling AgentCalled test above, for the same reason (turn.end
// is a store-level event, not something TurnResult.Events exposes faithfully).
func TestRunFlows_WorkbenchSmoke_UsableKitsokiGateSignal(t *testing.T) {
	const appPath = "../../testdata/apps/workbench_smoke/app.yaml"
	const glob = "../../testdata/apps/workbench_smoke/flows/workbench_walk_cassette.yaml"

	var sinkHistory store.History
	opts := testrunner.FlowOptions{
		OnRigClose: func(filePath string, st store.Store, sid app.SessionID, sink *store.JSONLSink) error {
			sinkHistory = append(sinkHistory, sink.History()...)
			return nil
		},
	}

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, opts)
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.Equal(t, 0, report.Failed, "all flows should pass")
	require.NotEmpty(t, sinkHistory, "OnRigClose should have been invoked with a non-empty sink")

	var turnEndedEvents []store.Event
	for _, ev := range sinkHistory {
		if ev.Kind == store.TurnEnded {
			turnEndedEvents = append(turnEndedEvents, ev)
		}
	}
	require.NotEmpty(t, turnEndedEvents, "expected at least one turn.end event")

	var foundGateSignal bool
	for _, ev := range turnEndedEvents {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &payload), "turn.end payload must decode as JSON")

		raw, ok := payload["usable_kitsoki_gate"]
		if !ok {
			continue // not every turn is workbench-origin (e.g. a turn with no dispatch)
		}
		foundGateSignal = true

		sig, ok := raw.(map[string]any)
		require.True(t, ok, "usable_kitsoki_gate must decode as an object, got %T", raw)

		require.Contains(t, sig, "candidate_completed")
		require.Contains(t, sig, "silent_bounce")
		require.Contains(t, sig, "misroute_adjacent")
		require.Contains(t, sig, "evidence_refs")

		require.Equal(t, true, sig["candidate_completed"],
			"workbench_walk_cassette.yaml's turn 1 dispatch returns a successful cassette episode with no error — candidate_completed should reflect that")
		require.Equal(t, false, sig["silent_bounce"],
			"the dispatch succeeded (no AgentError this turn) so silent_bounce must be false")
	}

	require.True(t, foundGateSignal, "expected at least one turn.end event to carry a usable_kitsoki_gate signal for bench's workbench-origin dispatch")
}
