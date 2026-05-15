package orchestrator_test

// Regression tests for the P1-D fix from the dev-story-bugfix-unify
// Opus review: three orchestrator callsites used RunEffects (which
// discards the post-emit state), so an emit_intent fired inside the
// target's on_enter would EXECUTE (host calls run, effects apply)
// but the orchestrator pinned the post-effect state to the literal
// target rather than the emit-resolved leaf.  After the fix, all
// three sites use RunEffectsAndState and route the returned state
// through their surrounding state-update logic.

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestEnterRedirectState_EmitIntent_FollowsThrough — P1-D site 1.
// An on_error: arc routes the session to an error state whose
// on_enter chain immediately emits a follow-on intent.  After the
// fix the session lands at the emit's target, NOT at the error
// state's literal target.
func TestEnterRedirectState_EmitIntent_FollowsThrough(t *testing.T) {
	const yamlSrc = `
app:
  id: redirect-emit-test
  version: 0.1.0
hosts:
  - host.always_fail
intents:
  start: {}
  recovered: {}
root: idle
states:
  idle:
    on:
      start:
        - target: working
  working:
    on_enter:
      - invoke: host.always_fail
        with: {}
        on_error: recovery
  recovery:
    on_enter:
      - emit_intent: recovered
    on:
      recovered:
        - target: recovered_landing
  recovered_landing:
    view: "we landed at recovered_landing"
  unreachable_fallback:
    view: "should not see this"
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.always_fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: "deliberate failure for on_error redirect"}, nil
	})

	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{},
		orchestrator.WithHostRegistry(reg),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("recovered_landing"), out.NewState,
		"after on_error redirect, emit_intent in the error state's on_enter must steer the final landing state")

	// Sanity: journey replay also lands at the emit-resolved leaf.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("recovered_landing"), journey.State,
		"replayed journey must reflect the emit-resolved leaf")
}

// TestFireTimeout_EmitIntent_FollowsThrough — P1-D site 2.  When a
// timeout fires and its target state's on_enter emits a follow-on
// intent, the synthetic timeout turn's NewState must reflect the
// emit's target, not the literal timeout target.
func TestFireTimeout_EmitIntent_FollowsThrough(t *testing.T) {
	const yamlSrc = `
app:
  id: timeout-emit-test
  version: 0.1.0
intents:
  start: {}
  recovered: {}
root: idle
states:
  idle:
    on:
      start:
        - target: waiting
  waiting:
    timeout:
      after: "10ms"
      target: timed_out
    on:
      recovered:
        - target: recovered_landing
  timed_out:
    on_enter:
      - emit_intent: recovered
    on:
      recovered:
        - target: recovered_landing
  recovered_landing:
    view: "we landed at recovered_landing"
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("waiting"), out.NewState)

	// Wait for the timeout to fire and the synthetic turn to land.
	// 250ms is generous — the after: is 10ms.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		journey, jerr := orch.LoadJourney(sid)
		require.NoError(t, jerr)
		if journey.State == app.StatePath("recovered_landing") {
			return
		}
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
	}

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("recovered_landing"), journey.State,
		"timeout target's emit_intent must steer the final landing state; got %q", journey.State)
}

// TestOnComplete_EmitIntent_FollowsThrough — P1-D site 3.  A
// background job's on_complete chain emits a follow-on intent; the
// synthetic completion turn must land at the emit's target rather
// than the job's OriginState.
func TestOnComplete_EmitIntent_FollowsThrough(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "oncomplete-emit-test"},
		Root:  "idle",
		Hosts: []string{"host.bg.work"},
		World: map[string]app.VarDef{
			"last_job_id":     {Type: "string", Default: ""},
			"last_job_result": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"start":     {Title: "Start"},
			"recovered": {Title: "Recovered"},
		},
		States: map[string]*app.State{
			"idle": {
				View: app.LegacyView("idle"),
				On: map[string][]app.Transition{
					"start": {{Target: "working"}},
				},
			},
			"working": {
				View: app.LegacyView("working"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.bg.work",
						With:       map[string]any{"x": "y"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							{EmitIntent: "recovered"},
						},
					},
				},
				On: map[string][]app.Transition{
					"recovered": {{Target: "recovered_landing"}},
				},
			},
			"recovered_landing": {Terminal: true, View: app.LegacyView("recovered_landing")},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jobStore, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	sched := jobs.NewScheduler(jobStore)

	reg := host.NewRegistry()
	reg.Register("host.bg.work", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"output": "done"}}, nil
	})

	h := &staticHarness{intentName: "start"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithJobStore(jobStore),
		orchestrator.WithScheduler(sched),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "start")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("working"), out.NewState,
		"the foreground turn should land at working; the background job advances asynchronously")

	// Wait for the background job to complete and the synthetic
	// completion turn to apply its on_complete chain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		journey, jerr := orch.LoadJourney(sid)
		require.NoError(t, jerr)
		if journey.State == app.StatePath("recovered_landing") {
			return
		}
		runtime.Gosched()
		time.Sleep(20 * time.Millisecond)
	}

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("recovered_landing"), journey.State,
		"on_complete chain's emit_intent must steer the final landing leaf; got %q", journey.State)
}
