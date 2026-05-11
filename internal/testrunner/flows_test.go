package testrunner_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
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

	// Must have exactly 4 flow results.
	require.Len(t, report.Results, 4, "expected 4 flow results (winning, losing, negative, world_override)")

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

	require.Equal(t, 4, report.Passed)
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

// TestFlowsWorldOverride runs the world_override fixture (§7.19): forcing
// wearing_cloak=false before the south transition routes the player into
// bar.lit on the very first turn instead of bar.dark.
func TestFlowsWorldOverride(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, cloakAppPath,
		"../../testdata/apps/cloak/flows/world_override.yaml",
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
	require.True(t, r.Passed, "world_override fixture should pass")
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

// TestFlowsProposalSmoke runs the proposal smoke app flow fixtures (roadmap step 4).
func TestFlowsProposalSmoke(t *testing.T) {
	const smokeAppPath = "../../testdata/apps/proposal_smoke/app.yaml"
	const smokeGlob = "../../testdata/apps/proposal_smoke/flows/*.yaml"

	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, smokeAppPath, smokeGlob, testrunner.FlowOptions{})
	require.NoError(t, err)

	for _, r := range report.Results {
		if !r.Passed {
			for _, turn := range r.Turns {
				for _, f := range turn.Failures {
					t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, f)
				}
			}
		}
		require.True(t, r.Passed, "smoke flow %q should pass", filepath.Base(r.File))
	}
	require.Equal(t, 0, report.Failed, "all smoke flows must pass")
}

// TestFlowsDevStory runs all dev-story flow fixtures (the main app build).
// Covers: navigation, terminal proposal lifecycle, background jobs, oracle, clarification.
func TestFlowsDevStory(t *testing.T) {
	const devStoryAppPath = "../../testdata/apps/dev-story/app.yaml"
	const devStoryGlob = "../../testdata/apps/dev-story/flows/*.yaml"

	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, devStoryAppPath, devStoryGlob, testrunner.FlowOptions{
		Verbose: true,
	})
	require.NoError(t, err)

	for _, r := range report.Results {
		if !r.Passed {
			for _, turn := range r.Turns {
				for _, f := range turn.Failures {
					t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, f)
				}
			}
		}
		require.True(t, r.Passed, "dev-story flow %q should pass", filepath.Base(r.File))
	}
	require.Equal(t, 0, report.Failed, "all dev-story flows must pass")
}

// TestFlowsDevStoryFlow1 runs the workspace navigation flow (history stack exercise).
func TestFlowsDevStoryFlow1(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow1_workspace_nav.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow1 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow1 workspace navigation should pass")
}

// TestFlowsDevStoryFlow2 runs the terminal proposal flows (happy path, cancel, error).
func TestFlowsDevStoryFlow2(t *testing.T) {
	flows := []string{
		"../../testdata/apps/dev-story/flows/flow2a_terminal_propose_accept.yaml",
		"../../testdata/apps/dev-story/flows/flow2b_terminal_cancel.yaml",
		"../../testdata/apps/dev-story/flows/flow2c_terminal_error.yaml",
	}
	for _, f := range flows {
		f := f
		t.Run(filepath.Base(f), func(t *testing.T) {
			ctx := context.Background()
			report, err := testrunner.RunFlows(ctx,
				"../../testdata/apps/dev-story/app.yaml",
				f, testrunner.FlowOptions{})
			require.NoError(t, err)
			require.Len(t, report.Results, 1)
			r := report.Results[0]
			for _, turn := range r.Turns {
				for _, ff := range turn.Failures {
					t.Logf("turn %d failure: %s", turn.TurnIndex+1, ff)
				}
			}
			require.True(t, r.Passed, "terminal flow %q should pass", filepath.Base(f))
		})
	}
}

// TestFlowsDevStoryFlow3 runs the background job flow.
func TestFlowsDevStoryFlow3(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow3_background_job.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow3 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow3 background job should pass")
}

// TestFlowsDevStoryFlow4 runs the Oracle Room flow.
func TestFlowsDevStoryFlow4(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow4_oracle.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow4 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow4 oracle room should pass")
}

// TestFlowsDevStoryFlow8 runs the Oracle background-chat-turn flow.
// This proves that Effect.Background + chat-aware host.oracle.talk +
// on_complete bridge all compose correctly: the job is submitted, the clock is
// advanced past the stub delay, on_complete sets oracle_answer, and the inbox
// shows a chat-friendly "Reply ready" success notification.
func TestFlowsDevStoryFlow8(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow8_oracle_background.yaml",
		testrunner.FlowOptions{Verbose: true})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow8 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow8 oracle background should pass")
}

// TestFlowsDevStoryFlow5 runs the clarification flow.
func TestFlowsDevStoryFlow5(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow5_clarification.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow5 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow5 clarification should pass")
}
