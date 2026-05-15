package orchestrator_test

// Tests for runtime dispatch of Effect.Target inside on_complete: chains.
// Load-time validation is covered by internal/app/loader_target_test.go.
//
// Coverage here:
//
//   - happy path: background invoke → on_complete with Target → session
//     ends up in the target state, observer fires with NewState=target.
//   - on_error redirect from a host call inside on_complete wins over Target.

import (
	"context"
	"errors"
	"sync"
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

// captureObserver collects every OnBackgroundTurn callback so tests can
// assert on outcome.NewState without polling the event log.
type captureObserver struct {
	mu       sync.Mutex
	outcomes []*orchestrator.TurnOutcome
	sids     []app.SessionID
}

func (c *captureObserver) OnBackgroundTurn(sid app.SessionID, outcome *orchestrator.TurnOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sids = append(c.sids, sid)
	c.outcomes = append(c.outcomes, outcome)
}

func (c *captureObserver) last() *orchestrator.TurnOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.outcomes) == 0 {
		return nil
	}
	return c.outcomes[len(c.outcomes)-1]
}

// TestOnCompleteTarget_HappyPath: background invoke completes, on_complete:
// chain has [set, target] — final state is target, observer fires with the
// target state, world is updated.
func TestOnCompleteTarget_HappyPath(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "target-happy"},
		Root:  "init",
		Hosts: []string{"host.test.echo"},
		World: map[string]app.VarDef{
			"x":           {Type: "string", Default: ""},
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "executing"}},
				},
			},
			"executing": {
				View: app.LegacyView("executing"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "ping"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							{Set: map[string]any{"x": "applied"}},
							{Target: "done"},
						},
					},
				},
			},
			"done": {
				View: app.LegacyView("done x={{ world.x }}"),
				OnEnter: []app.Effect{
					{Set: map[string]any{"x": "applied-and-entered"}},
				},
				Terminal: true,
			},
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
	reg.Register("host.test.echo", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})

	h := &staticHarness{intentName: "enter"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	obs := &captureObserver{}
	orch.RegisterObserver(obs)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)
	require.Equal(t, app.StatePath("executing"), out.NewState)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// Post-completion: session should have landed on "done".
	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), finalJourney.State,
		"on_complete: target should have advanced session out of executing")
	require.Equal(t, "applied-and-entered", finalJourney.World.Vars["x"],
		"on_complete set: should apply, then target on_enter should overwrite")

	// Observer must have fired with NewState=done.
	require.NotNil(t, obs.last(), "observer must be notified of background turn")
	require.Equal(t, app.StatePath("done"), obs.last().NewState,
		"observer NewState should reflect the on_complete target")
}

// TestOnCompleteTarget_OnErrorWinsOverTarget: when a host call inside the
// on_complete chain hits its on_error redirect, the session should land on
// the error state and the Target effect later in the chain is suppressed.
//
// This guards the design call that on_error (which is itself a terminal
// state-change) takes precedence over the optional Target dispatch.
func TestOnCompleteTarget_OnErrorWinsOverTarget(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "target-on-error"},
		Root:  "init",
		Hosts: []string{"host.test.echo", "host.test.fail"},
		World: map[string]app.VarDef{
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "executing"}},
				},
			},
			"executing": {
				View: app.LegacyView("executing"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							// host.test.fail returns an error so the on_error
							// redirect fires and the session lands on "errored".
							{Invoke: "host.test.fail", OnError: "errored"},
							// This Target should be SUPPRESSED because the
							// on_error already moved the session.
							{Target: "done"},
						},
					},
				},
			},
			"errored": {View: app.LegacyView("errored")},
			"done":    {View: app.LegacyView("done")},
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
	reg.Register("host.test.echo", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})
	// Synchronous failing handler — kicks the on_error redirect.
	reg.Register("host.test.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, errors.New("simulated host failure")
	})

	h := &staticHarness{intentName: "enter"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("errored"), finalJourney.State,
		"on_error should win over on_complete: target:")
}

// TestOnCompleteTarget_PriorEffectFailureSkipsTarget: if a prior effect in
// the on_complete chain causes RunEffects to fail (e.g. a set: with an
// uncompileable template), the entire synthetic turn returns with a
// RunEffects error and the Target transition is suppressed.  The session
// stays in the origin state — the fail-fast invariant.
//
// We force the failure with a deliberately broken set: template that
// references a non-existent dotted field — expr returns an error.
func TestOnCompleteTarget_PriorEffectFailureSkipsTarget(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "target-fail-fast"},
		Root:  "init",
		Hosts: []string{"host.test.echo"},
		World: map[string]app.VarDef{
			"x":           {Type: "string", Default: ""},
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "executing"}},
				},
			},
			"executing": {
				View: app.LegacyView("executing"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							// Broken template: function call that doesn't exist
							// — expr render fails, RunEffects errors out.
							{Set: map[string]any{"x": "{{ this_function_does_not_exist() }}"}},
							{Target: "done"},
						},
					},
				},
			},
			"done": {View: app.LegacyView("done")},
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
	reg.Register("host.test.echo", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})

	h := &staticHarness{intentName: "enter"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// The on_complete RunEffects errored out, so handleJobTerminal returned
	// early.  No synthetic turn was committed, so the session must still
	// be at the origin state ("executing") — Target did NOT fire.
	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("executing"), finalJourney.State,
		"a failed prior on_complete effect must suppress the Target dispatch (fail-fast)")
}
