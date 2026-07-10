// Acceptance test for the codeact "promotion ratchet" (concept.md §4,
// docs/goals/codeact/decomposition.yaml s5-promotion-ratchet). Runs the
// codeact_demo_promoted fixture app end-to-end: its `work` room invokes the
// REAL host.starlark.run handler over the frozen scripts/promoted_sum.star
// script (promoted out of codeact_demo's stubbed-agent trajectory by
// internal/host/codeact.ExtractTrajectory), with zero host.agent.codeact
// dispatch anywhere in the app (it is not even declared in `hosts:`, so the
// app would fail to load if the room somehow still tried to invoke it).
package testrunner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func TestFlowsCodeactDemoPromoted(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"testdata/codeact_demo_promoted/app.yaml",
		"testdata/codeact_demo_promoted/flows/*.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err, "codeact_demo_promoted app + flow should load and run cleanly with zero agent dispatch")
	require.Equal(t, 1, report.Passed, "the promoted_happy_path fixture should reproduce result_payload=7 via the frozen script")
	require.Equal(t, 0, report.Failed)
}
