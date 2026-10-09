package testrunner_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// TestRunFlows_WorkbenchSmoke_ExpectedEffectsJoin is room-workbench S6's
// real expected_effects join, proven end-to-end (not just unit-tested in
// internal/orchestrator/workbench_gate_signal_test.go): a flow fixture seeds
// bench_expected_effects via initial_world, a real host_cassette stands in
// for the workbench's on_enter host.agent.task dispatch, and this asserts
// the turn.end usable_kitsoki_gate signal's candidate_completed is computed
// by the ENGINE from the cassette's own bound bench_note content — true
// when the note covers every expected effect, false when it honestly
// doesn't — never a hardcoded value either way. See
// tools/session-mining/flow_fixture_compiler.py's real-workbench projection
// target, which seeds this same join input for mined calibration scenarios.
func TestRunFlows_WorkbenchSmoke_ExpectedEffectsJoin(t *testing.T) {
	const appPath = "../../testdata/apps/workbench_smoke/app.yaml"

	cases := []struct {
		name           string
		flow           string
		wantCompleted  bool
		wantSilentBnc  bool
		wantAtLeastOne bool
	}{
		{
			name:           "satisfied",
			flow:           "../../testdata/apps/workbench_smoke/flows/workbench_expected_effects_satisfied.yaml",
			wantCompleted:  true,
			wantAtLeastOne: true,
		},
		{
			name:           "unsatisfied",
			flow:           "../../testdata/apps/workbench_smoke/flows/workbench_expected_effects_unsatisfied.yaml",
			wantCompleted:  false,
			wantAtLeastOne: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sinkHistory store.History
			opts := testrunner.FlowOptions{
				OnRigClose: func(filePath string, st store.Store, sid app.SessionID, sink *store.JSONLSink) error {
					sinkHistory = append(sinkHistory, sink.History()...)
					return nil
				},
			}

			report, err := testrunner.RunFlows(t.Context(), appPath, tc.flow, opts)
			require.NoError(t, err, "RunFlows should not return a fatal error")
			require.Equal(t, 0, report.Failed, "flow should pass its own expect_world assertions")
			require.NotEmpty(t, sinkHistory, "OnRigClose should have been invoked with a non-empty sink")

			foundGateSignal := false
			for _, ev := range sinkHistory {
				if ev.Kind != store.TurnEnded {
					continue
				}
				var payload map[string]any
				require.NoError(t, json.Unmarshal(ev.Payload, &payload))
				raw, ok := payload["usable_kitsoki_gate"]
				if !ok {
					continue
				}
				foundGateSignal = true
				sig, ok := raw.(map[string]any)
				require.True(t, ok, "usable_kitsoki_gate must decode as an object, got %T", raw)
				require.Equal(t, tc.wantCompleted, sig["candidate_completed"],
					"candidate_completed must reflect whether bench_note actually covers bench_expected_effects, not a hardcoded value")
				require.Equal(t, false, sig["silent_bounce"], "dispatch did not fail this turn")
			}
			require.True(t, foundGateSignal, "expected at least one turn.end event to carry a usable_kitsoki_gate signal")
		})
	}
}
