package testrunner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestShipItExample runs the ship-it story under stories/ship-it/ end-to-end
// through the orchestrator. All flows under stories/ship-it/flows/*.yaml are
// executed via the flow harness — no LLM is involved and no socket is opened,
// so the example is CI-verified and cost-free.
//
// This doubles as the ship-it README's "Test it" command:
//
//	go test ./internal/testrunner/ -run TestShipItExample
func TestShipItExample(t *testing.T) {
	const (
		appPath  = "../../stories/ship-it/app.yaml"
		flowGlob = "../../stories/ship-it/flows/*.yaml"
	)

	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, appPath, flowGlob, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, report.Results, "expected at least one flow result")

	for _, r := range report.Results {
		if !r.Passed {
			logFlowFailures(t, r)
		}
		require.True(t, r.Passed, "example flow %q should pass", r.File)
	}
	require.Equal(t, 0, report.Failed, "all ship-it example flows must pass")
}
