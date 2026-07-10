package testrunner_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestRunFlows_WorkbenchSmoke drives testdata/apps/workbench_smoke — a
// synthetic, no-LLM regression for the `workbench:` state-block desugaring
// (docs/architecture/room-workbench.md): free-text capture into the
// synthesized default_intent, the synthesized on_enter host.agent.task
// dispatch stubbed by a flow cassette, the read-only floor holding across a
// repeated dispatch, the synthesized agent_off_ramp's residual rejection
// path, and a hand-authored route-out whose set: effect commits to world
// before its emit_intent's target room on_enter reads it.
func TestRunFlows_WorkbenchSmoke(t *testing.T) {
	const appPath = "../../testdata/apps/workbench_smoke/app.yaml"
	const glob = "../../testdata/apps/workbench_smoke/flows/*.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results, "should have at least one result")

	for _, r := range report.Results {
		if r.Skipped {
			t.Logf("SKIP %s", filepath.Base(r.File))
			continue
		}
		for _, tr := range r.Turns {
			if !tr.Passed {
				t.Errorf("flow %s turn %d failed: %v", filepath.Base(r.File), tr.TurnIndex+1, tr.Failures)
			}
		}
	}
	require.Equal(t, 0, report.Failed, "all flows should pass")
	require.Greater(t, report.Passed, 0, "at least one flow should pass")
}
