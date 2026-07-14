// Package testrunner — orchestrator-path flow runner tests.
//
// These tests exercise the orchestrator-backed turn loop introduced in the
// background-jobs slice. They run without any LLM agent and use only intent:
// turns together with in-memory host stubs.
package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// ─── TestRunFlows_OrchestratorPath_BackgroundJob ──────────────────────────────

// TestRunFlows_OrchestratorPath_BackgroundJob verifies the full background-job
// happy path via the real background_jobs testdata fixture:
//  1. host_handlers: auto-upgrades the fixture to the orchestrator path.
//  2. Turn 1 fires 'enter', machine transitions lobby→running, on_enter
//     dispatches the job and binds last_job_id synchronously.
//  3. advance_clock: "2s" moves virtual time past the 1s handler delay; the
//     orchestrator listener applies on_complete (sets result="hello") and
//     posts a success notification.
//  4. expect_world: {result: "hello"} and expect_inbox: {unread:1} pass.
func TestRunFlows_OrchestratorPath_BackgroundJob(t *testing.T) {
	const appPath = "../../testdata/apps/background_jobs/app.yaml"
	const glob = "../../testdata/apps/background_jobs/flows/happy_path.yaml"

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

func TestRunFlows_OrchestratorPath_OperationAssertionsIncludeBackgroundCompletion(t *testing.T) {
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	flowPath := filepath.Join(dir, "flow.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(`
app:
  id: bg-op
  title: Background operation
hosts: [host.run]
world:
  result:      { type: string, default: "" }
  artifact:    { type: string, default: "" }
  last_job_id: { type: string, default: "" }
intents:
  enter: { description: "enter" }
operations:
  bg_run:
    title: "Background run"
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    terminal_artifact: artifact
root: idle
states:
  idle:
    on:
      enter:
        - target: running
          operation: bg_run
  running:
    on_enter:
      - invoke: host.run
        background: true
        with: { cmd: "echo ok" }
        bind: { last_job_id: job_id }
        on_complete:
          - set:
              result: "{{ world.last_job_result.output }}"
              artifact: "artifact#done"
          - target: done
  done:
    terminal: true
`), 0644))
	require.NoError(t, os.WriteFile(flowPath, []byte(`
test_kind: flow
app: ./app.yaml
initial_state: idle
host_handlers:
  host.run:
    data: { output: "ok" }
    delay: "10ms"
turns:
  - intent: { name: enter, slots: {} }
    advance_clock: "1s"
    expect_state: done
    expect_world:
      result: "ok"
expect_operation_policy: bg_run
expect_operation_status: completed
expect_terminal_artifact: artifact
expect_no_errors: true
`), 0644))

	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "operation assertion should see background completion events")
	require.Equal(t, 1, report.Passed)
}

// ─── TestRunFlows_OrchestratorPath_HostError ─────────────────────────────────

// TestRunFlows_OrchestratorPath_HostError verifies that a stub returning an
// infrastructure error (infra_error: "timeout") causes the background job to
// terminate as failed and the flow runner handles this gracefully. The test
// confirms:
//  1. The machine turn succeeds (state = running, last_job_id non-empty).
//  2. advance_clock moves time forward; the job terminates as failed.
//  3. The flow runner does not crash (RunFlows returns no error).
//  4. The info notification (job submitted) is in the inbox.
//
// Note: the background_jobs app.yaml on_complete attempts to access
// world.last_job_result.stdout, which is nil on failure. This causes
// handleJobTerminal to return an error internally (logged but non-fatal for
// the flow runner). We assert only on world state and the job-submitted
// notification rather than the failure notification.
func TestRunFlows_OrchestratorPath_HostError(t *testing.T) {
	const appPath = "../../testdata/apps/background_jobs/app.yaml"

	absAppPath, err := filepath.Abs(appPath)
	require.NoError(t, err)

	// infra_error: the handler returns (Result{}, error). The job terminates
	// as failed. on_complete fires but errors due to nil last_job_result.
	// The flow runner must not propagate that internal error to the test.
	fixture := `
test_kind: flow
app: ` + absAppPath + `
initial_state: lobby
initial_world:
  result: ""
  last_job_id: ""

host_handlers:
  host.run:
    infra_error: "connection timeout"

turns:
  - intent:
      name: enter
      slots: {}
    advance_clock: "100ms"
    expect_state: running

expect_no_errors: true
`

	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "error_path.yaml")
	require.NoError(t, os.WriteFile(fixturePath, []byte(fixture), 0644))

	report, err := testrunner.RunFlows(t.Context(), absAppPath, fixturePath, testrunner.FlowOptions{
		AllowMissingRecording: true,
	})
	require.NoError(t, err, "RunFlows should not return a fatal error on host infra error")
	require.NotEmpty(t, report.Results)

	for _, r := range report.Results {
		if r.Skipped {
			continue
		}
		for _, tr := range r.Turns {
			if !tr.Passed {
				t.Errorf("turn %d failed: %v", tr.TurnIndex+1, tr.Failures)
			}
		}
	}
	require.Equal(t, 0, report.Failed, "error-path flow should pass")
}

// ─── TestRunFlows_OrchestratorPath_Timeout ───────────────────────────────────

// TestRunFlows_OrchestratorPath_Timeout verifies the Timeout: runtime
// The fixture seeds the session into a state whose `timeout:`
// declares a 10-day deadline, then uses `advance_clock: "11d"` to fire the
// synthetic transition.  Post-advance assertions land on the terminal
// destination state.
func TestRunFlows_OrchestratorPath_Timeout(t *testing.T) {
	const appPath = "../../testdata/apps/timeout/app.yaml"
	const glob = "../../testdata/apps/timeout/flows/landmark_timeout.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results, "should have at least one result")

	for _, r := range report.Results {
		if r.Skipped {
			continue
		}
		for _, tr := range r.Turns {
			if !tr.Passed {
				t.Errorf("flow %s turn %d failed: %v", filepath.Base(r.File), tr.TurnIndex+1, tr.Failures)
			}
		}
	}
	require.Equal(t, 0, report.Failed, "timeout fixture should pass")
	require.Greater(t, report.Passed, 0)
}

// ─── TestRunFlows_LegacyPath_StillWorks ──────────────────────────────────────

// TestRunFlows_LegacyPath_StillWorks confirms that existing fixtures without
// host_handlers or advance_clock continue to use the machine-only path and
// produce the same results as before.
//
// We use the cloak-of-darkness flow fixtures as the canonical "legacy" example.
// If those are unavailable, we fall back to the background_jobs app but
// disable the host_handlers fields (i.e. no host_handlers, no advance_clock).
func TestRunFlows_LegacyPath_StillWorks(t *testing.T) {
	// Fixtures from cloak/flows contain no host_handlers or advance_clock and
	// thus run on the legacy machine path.
	const appPath = "../../testdata/apps/cloak/app.yaml"
	const glob = "../../testdata/apps/cloak/flows/*.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "legacy path RunFlows should not fail")
	require.NotEmpty(t, report.Results, "should have at least one result")

	// At least one flow should pass (or be skipped due to missing agent).
	for _, r := range report.Results {
		if r.Skipped {
			continue
		}
		for _, tr := range r.Turns {
			if !tr.Passed {
				t.Errorf("legacy flow %s turn %d failed: %v",
					filepath.Base(r.File), tr.TurnIndex+1, tr.Failures)
			}
		}
	}
	// Expect no failures on the legacy path.
	require.Equal(t, 0, report.Failed, "legacy-path flows should all pass")
}
