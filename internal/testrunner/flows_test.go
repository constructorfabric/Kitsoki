package testrunner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

const (
	cloakAppPath   = "../../testdata/apps/cloak/app.yaml"
	cloakFlowsGlob = "../../testdata/apps/cloak/flows/*.yaml"
)

// TestFlowsHostBindingStarlark exercises S3a (kits-implementation-plan.md
// D2.1): a `host_bindings` entry naming a starlark script instead of a
// concrete handler. The child's `greeter` host_interface gets rebound onto
// scripts/greet.star; iface.greeter.hello must resolve through the
// synthesized StarlarkBindingHandler and actually run the script, proving
// both that the effect's `with:` args reach ctx.inputs and that the
// dispatched op is injected into ctx.inputs.op. No LLM, no HTTP.
func TestFlowsHostBindingStarlark(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/host_binding_starlark/parent/app.yaml",
		"../../testdata/apps/host_binding_starlark/parent/flows/*.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)

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
	require.Equal(t, 1, report.Passed)
	require.Equal(t, 0, report.Failed)
}

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

func TestFlowsCommaSeparatedFixtureList(t *testing.T) {
	ctx := context.Background()
	patterns := strings.Join([]string{
		"../../testdata/apps/cloak/flows/winning.yaml",
		"../../testdata/apps/cloak/flows/losing.yaml",
	}, ",")
	report, err := testrunner.RunFlows(ctx, cloakAppPath, patterns, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 2)
	require.Equal(t, 2, report.Passed)
	require.Equal(t, 0, report.Failed)
	require.Equal(t, "winning.yaml", filepath.Base(report.Results[0].File))
	require.Equal(t, "losing.yaml", filepath.Base(report.Results[1].File))
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

// TestFlowsWorldOverride runs the world_override fixture: forcing
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
// Covers: navigation, terminal proposal lifecycle, background jobs, agent, clarification.
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

// TestFlowsDevStoryFlow4 runs the Agent Room flow.
func TestFlowsDevStoryFlow4(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow4_agent.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow4 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow4 agent room should pass")
}

// TestFlowsDevStoryFlow8 runs the Agent background-chat-turn flow.
// This proves that Effect.Background + chat-aware host.agent.talk +
// on_complete bridge all compose correctly: the job is submitted, the clock is
// advanced past the stub delay, on_complete sets agent_answer, and the inbox
// shows a chat-friendly "Reply ready" success notification.
func TestFlowsDevStoryFlow8(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"../../testdata/apps/dev-story/app.yaml",
		"../../testdata/apps/dev-story/flows/flow8_agent_background.yaml",
		testrunner.FlowOptions{Verbose: true})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow8 turn %d failure: %s", turn.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "flow8 agent background should pass")
}

// ─── expect_jobs assertion tests ─────────────────────────────────────────────
//
// The expect_jobs assertion locks down the bug class where a background
// host.run job silently lands status=failed (e.g. because a `cmd:` field is
// passed as a list rather than a string) yet on_complete still applies its
// effects and the game continues. expect_inbox catches the notification
// count but not per-job terminal status; expect_jobs closes that gap.

// jobsTestApp is a minimal stand-alone app with one room that dispatches a
// background host.run job. Reused by the positive and negative expect_jobs
// tests below.
const jobsTestApp = `
app:
  id: expect-jobs-test
  version: 0.1.0
  title: "expect_jobs test app"

hosts:
  - host.run

world:
  result: { type: string, default: "" }
  last_job_id: { type: string, default: "" }

intents:
  enter:
    title: "Start"
    description: "Kick off the background job."
    examples: ["start", "run", "go"]

root: lobby

states:
  lobby:
    description: "Lobby."
    view: "Lobby."
    on:
      enter:
        - target: running

  running:
    description: "Running."
    view: "Running: {{ world.result }}"
    on_enter:
      - invoke: host.run
        with:
          cmd: "echo hello"
        background: true
        bind:
          last_job_id: job_id
        on_complete:
          - set:
              result: "{{ world.last_job_result.stdout }}"
`

// TestRunFlows_ExpectJobs_Positive verifies that a turn with
// expect_jobs: [{namespace: host.run, status: done}] passes when the stub
// resolves cleanly. The pre-turn snapshot is empty; after advance_clock the
// host.run job lands status=done and the assertion matches.
func TestRunFlows_ExpectJobs_Positive(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(jobsTestApp), 0644))

	fixture := `
test_kind: flow
app: ./app.yaml
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

host_handlers:
  host.run:
    data:
      stdout: "hello"
      exit: 0
    delay: "10ms"

turns:
  - intent: { name: enter, slots: {} }
    advance_clock: "100ms"
    expect_state: running
    expect_jobs:
      - namespace: host.run
        status:    done

expect_no_errors: true
`
	fixturePath := filepath.Join(dir, "positive.yaml")
	require.NoError(t, os.WriteFile(fixturePath, []byte(fixture), 0644))

	report, err := testrunner.RunFlows(context.Background(), appPath, fixturePath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	for _, tr := range r.Turns {
		for _, f := range tr.Failures {
			t.Logf("turn %d failure: %s", tr.TurnIndex+1, f)
		}
	}
	require.True(t, r.Passed, "expect_jobs positive flow should pass")
	require.Equal(t, 0, report.Failed)
}

// TestRunFlows_ExpectJobs_Negative verifies that a mismatched expect_jobs
// entry — declaring status: failed while the stub actually returns done —
// fails the fixture with a clear diff. This is the assertion that would
// have caught commit 2d96c3a's regression.
func TestRunFlows_ExpectJobs_Negative(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(jobsTestApp), 0644))

	fixture := `
test_kind: flow
app: ./app.yaml
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

host_handlers:
  host.run:
    data:
      stdout: "hello"
      exit: 0
    delay: "10ms"

turns:
  - intent: { name: enter, slots: {} }
    advance_clock: "100ms"
    expect_state: running
    expect_jobs:
      - namespace: host.run
        status:    failed
`
	fixturePath := filepath.Join(dir, "negative.yaml")
	require.NoError(t, os.WriteFile(fixturePath, []byte(fixture), 0644))

	report, err := testrunner.RunFlows(context.Background(), appPath, fixturePath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	require.False(t, r.Passed, "expect_jobs negative flow should fail")
	require.Equal(t, 1, report.Failed)

	// The failure message must mention the namespace, the actual status
	// (done) and the wanted status (failed) so a developer can see the
	// mismatch at a glance.
	var sawDiff bool
	for _, tr := range r.Turns {
		for _, f := range tr.Failures {
			if strings.Contains(f, "expect_jobs") &&
				strings.Contains(f, "host.run") &&
				strings.Contains(f, `got status="done"`) &&
				strings.Contains(f, `want "failed"`) {
				sawDiff = true
			}
		}
	}
	require.True(t, sawDiff, "expect_jobs failure should include namespace + got/want status diff")
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
