package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestDriveOperation_AutonomousAcceptsToCompletion(t *testing.T) {
	const yamlSrc = `
app:
  id: operation-driver-complete
  version: 0.1.0
world:
  refined: { type: bool, default: false }
intents:
  start: {}
  accept: {}
  refine: {}
  quit: {}
root: idle
operations:
  demo_run:
    title: Demo run
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    terminal_artifact: done_artifact
    phase_summary:
      from: [reproduction_artifact, done_artifact]
states:
  idle:
    on:
      start:
        - target: reproducing
          operation: demo_run
  reproducing:
    on:
      accept:
        - target: proposing
          effects:
            - set:
                reproduction_artifact: { summary_title: Reproduced }
      refine:
        - target: reproducing
          effects:
            - set: { refined: true }
      quit:
        - target: needs-human
  proposing:
    on:
      accept:
        - target: done
          effects:
            - set:
                done_artifact: { summary_title: Done }
      quit:
        - target: needs-human
  needs-human:
    terminal: true
  done:
    terminal: true
`
	orch, sid := newOperationDriverTestOrchestrator(t, yamlSrc)
	ctx := context.Background()

	started, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("reproducing"), started.NewState)

	drive, err := orch.DriveOperation(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 2, drive.Turns)
	require.Equal(t, "operation-completed", drive.StopReason)
	require.Equal(t, "accept", drive.LastIntent)
	require.NotNil(t, drive.Final)
	require.Equal(t, orchestrator.ModeCompleted, drive.Final.Mode)
	require.Equal(t, app.StatePath("done"), drive.Final.NewState)
	requireOperationDriverProvenance(t, drive.Final.Events)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), journey.State)
	require.NotEqual(t, true, journey.World.Vars["refined"], "driver must not pick refine while accept is available")
	handle := requireOperationDriverHandle(t, journey)
	require.Equal(t, "completed", handle["status"])
	require.Equal(t, "done", handle["terminal_state"])
}

func TestDriveOperation_SupervisedStopsAtCheckpoint(t *testing.T) {
	const yamlSrc = `
app:
  id: operation-driver-supervised
  version: 0.1.0
intents:
  start: {}
  accept: {}
  approve: {}
  revise: {}
root: idle
operations:
  demo_run:
    title: Demo run
    mode: supervised
    execution_mode: one-shot
    run_in_background: true
states:
  idle:
    on:
      start:
        - target: drafting
          operation: demo_run
  drafting:
    on:
      accept:
        - target: review
  review:
    on:
      approve:
        - target: done
      revise:
        - target: drafting
  done:
    terminal: true
`
	orch, sid := newOperationDriverTestOrchestrator(t, yamlSrc)
	ctx := context.Background()

	started, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("drafting"), started.NewState)

	drive, err := orch.DriveOperation(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, drive.Turns)
	require.Equal(t, "no-driver-intent", drive.StopReason)
	require.Equal(t, "accept", drive.LastIntent)
	require.NotNil(t, drive.Final)
	require.Equal(t, app.StatePath("review"), drive.Final.NewState)
	requireOperationDriverProvenance(t, drive.Final.Events)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("review"), journey.State)
	handle := requireOperationDriverHandle(t, journey)
	require.Equal(t, "running", handle["status"])
	require.Equal(t, "supervised", handle["mode"])
}

func TestDriveOperation_StopsOnWaitingHandle(t *testing.T) {
	const yamlSrc = `
app:
  id: operation-driver-waiting
  version: 0.1.0
intents:
  start: {}
  accept: {}
root: idle
operations:
  demo_run:
    title: Demo run
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    stop_on: [needs-human]
states:
  idle:
    on:
      start:
        - target: reproducing
          operation: demo_run
  reproducing:
    on:
      accept:
        - target: needs-human
          effects:
            - set:
                status: needs-human
                needs_human_reason: Regression gate stayed red.
  needs-human:
    terminal: true
`
	orch, sid := newOperationDriverTestOrchestrator(t, yamlSrc)
	ctx := context.Background()

	_, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)

	drive, err := orch.DriveOperation(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 1, drive.Turns)
	require.Equal(t, "operation-waiting", drive.StopReason)
	require.Equal(t, "accept", drive.LastIntent)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("needs-human"), journey.State)
	handle := requireOperationDriverHandle(t, journey)
	require.Equal(t, "waiting", handle["status"])
	require.Equal(t, "needs-human", handle["stop_reason"])
	require.Equal(t, "Regression gate stayed red.", handle["stop_detail"])
}

func TestDriveBackgroundOperation_RequiresRunInBackground(t *testing.T) {
	const yamlSrc = `
app:
  id: operation-driver-foreground
  version: 0.1.0
intents:
  start: {}
  accept: {}
root: idle
operations:
  demo_run:
    title: Demo run
    mode: autonomous
    execution_mode: one-shot
states:
  idle:
    on:
      start:
        - target: work
          operation: demo_run
  work:
    on:
      accept:
        - target: done
  done:
    terminal: true
`
	orch, sid := newOperationDriverTestOrchestrator(t, yamlSrc)
	ctx := context.Background()

	started, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("work"), started.NewState)

	drive, err := orch.DriveBackgroundOperation(ctx, sid)
	require.NoError(t, err)
	require.Nil(t, drive)

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("work"), journey.State)
	handle := requireOperationDriverHandle(t, journey)
	require.Equal(t, "running", handle["status"])
	require.NotEqual(t, true, handle["run_in_background"])
}

func newOperationDriverTestOrchestrator(t *testing.T, yamlSrc string) (*orchestrator.Orchestrator, app.SessionID) {
	t.Helper()
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	h, _ := harness.NewReplay("")
	orch := orchestrator.New(def, m, s, h)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, sid
}

func requireOperationDriverHandle(t *testing.T, journey *store.JourneyState) map[string]any {
	t.Helper()
	handle, ok := journey.World.Vars[app.OperationRunWorldKey].(map[string]any)
	require.True(t, ok)
	return handle
}

func requireOperationDriverProvenance(t *testing.T, events []store.Event) {
	t.Helper()
	for _, ev := range events {
		if ev.Kind != store.TurnStarted {
			continue
		}
		var payload map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &payload))
		if payload["routed_by"] == "operation_driver" {
			require.Contains(t, payload["match_type"], "preferred:")
			return
		}
	}
	require.Fail(t, "missing operation driver TurnStarted provenance")
}
