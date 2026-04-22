package testrunner_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"hally/internal/testrunner"
)

const (
	cloakAppPath = "../../testdata/apps/cloak/app.yaml"
	cloakFlowsGlob = "../../testdata/apps/cloak/flows/*.yaml"
)

// TestFlowsCloak runs all three Cloak of Darkness flow fixtures end-to-end.
// This is the primary acceptance test for Stage 7 Mode 2.
func TestFlowsCloak(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, cloakAppPath, cloakFlowsGlob, testrunner.FlowOptions{})
	require.NoError(t, err)

	// Must have exactly 3 flow results.
	require.Len(t, report.Results, 3, "expected 3 flow results (winning, losing, negative)")

	// All must pass.
	for _, r := range report.Results {
		if !r.Passed {
			for _, turn := range r.Turns {
				for _, f := range turn.Failures {
					t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, f)
				}
			}
		}
		require.True(t, r.Passed, "flow %q should pass", filepath.Base(r.File))
	}

	require.Equal(t, 3, report.Passed)
	require.Equal(t, 0, report.Failed)
}

// TestFlowsWinningPath runs only the winning-path fixture and checks key assertions.
func TestFlowsWinningPath(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, cloakAppPath,
		"../../testdata/apps/cloak/flows/winning.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	if !r.Passed {
		for _, turn := range r.Turns {
			for _, f := range turn.Failures {
				t.Logf("turn %d failure: %s", turn.TurnIndex+1, f)
			}
		}
	}
	require.True(t, r.Passed, "winning path should pass")
}

// TestFlowsLosingPath runs the losing-path fixture.
func TestFlowsLosingPath(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, cloakAppPath,
		"../../testdata/apps/cloak/flows/losing.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	if !r.Passed {
		for _, turn := range r.Turns {
			for _, f := range turn.Failures {
				t.Logf("turn %d failure: %s", turn.TurnIndex+1, f)
			}
		}
	}
	require.True(t, r.Passed, "losing path should pass")
}

// TestFlowsNegativePath runs the negative (rejected-intent) fixture.
func TestFlowsNegativePath(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, cloakAppPath,
		"../../testdata/apps/cloak/flows/negative.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	if !r.Passed {
		for _, turn := range r.Turns {
			for _, f := range turn.Failures {
				t.Logf("turn %d failure: %s", turn.TurnIndex+1, f)
			}
		}
	}
	require.True(t, r.Passed, "negative path should pass")
}
