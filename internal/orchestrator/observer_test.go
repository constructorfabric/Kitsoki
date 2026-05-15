package orchestrator_test

import (
	"context"
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

// recordingObserver captures the OnBackgroundTurn callbacks made by the
// orchestrator so a test can assert what the TUI bridge would have
// received.  Safe to use across goroutines: the orchestrator invokes
// observers on the per-session listener goroutine.
type recordingObserver struct {
	mu       sync.Mutex
	sids     []app.SessionID
	outcomes []*orchestrator.TurnOutcome
}

func (r *recordingObserver) OnBackgroundTurn(sid app.SessionID, outcome *orchestrator.TurnOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sids = append(r.sids, sid)
	r.outcomes = append(r.outcomes, outcome)
}

func (r *recordingObserver) Snapshot() (sids []app.SessionID, outcomes []*orchestrator.TurnOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sids = append(sids, r.sids...)
	outcomes = append(outcomes, r.outcomes...)
	return
}

// TestSessionObserver_BackgroundJobTerminal verifies that after
// handleJobTerminal commits the synthetic on_complete turn, every
// registered SessionObserver receives an OnBackgroundTurn call with a
// non-nil outcome whose NewState, View and AllowedIntents reflect the
// post-completion world.
//
// This is the unit-test contract behind the TUI fix for the "main
// transcript frozen until next keystroke" bug: the TUI's observer impl
// (internal/tui/observer.go) just forwards this outcome into the Bubble
// Tea message loop, so if the orchestrator side fires correctly the
// TUI re-render path is guaranteed.
func TestSessionObserver_BackgroundJobTerminal(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "obs-test"},
		Root:  "init",
		Hosts: []string{"host.test.echo"},
		World: map[string]app.VarDef{
			"x":           {Type: "string", Default: ""},
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"done":  {Title: "Done"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "lobby"}},
				},
			},
			"lobby": {
				View: app.LegacyView("lobby x={{ world.x }}"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "world"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							{Set: map[string]any{"x": "{{ world.last_job_result.output }}"}},
						},
					},
				},
				On: map[string][]app.Transition{
					"done": {{Target: "end"}},
				},
			},
			"end": {Terminal: true, View: app.LegacyView("ended")},
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
		msg, _ := args["msg"].(string)
		return host.Result{Data: map[string]any{"output": msg}}, nil
	})

	h := &staticHarness{intentName: "enter"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	obs := &recordingObserver{}
	orch.RegisterObserver(obs)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Drive the foreground turn that fires the background effect.
	out, err := orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("lobby"), out.NewState)

	// Wait for the background goroutine to commit the on_complete turn.
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	sids, outcomes := obs.Snapshot()
	require.Len(t, sids, 1, "observer should have been notified exactly once")
	require.Equal(t, sid, sids[0])
	require.NotNil(t, outcomes[0], "outcome must be non-nil")

	got := outcomes[0]
	require.Equal(t, orchestrator.ModeTransitioned, got.Mode,
		"non-terminal post-completion state must surface as ModeTransitioned")
	require.Equal(t, app.StatePath("lobby"), got.NewState,
		"on_complete cannot transition (loader-enforced); state stays at originating state")
	require.Contains(t, got.View, "x=world",
		"View must reflect the world AFTER on_complete applied (x=world resolved from job result)")
	require.Contains(t, got.AllowedIntents, "done",
		"AllowedIntents must reflect the post-completion state's allowed list")
	require.NotZero(t, got.TurnNumber,
		"TurnNumber must be stamped on the outcome")
}

// TestSessionObserver_UnregisterStopsCallbacks verifies the
// Unregister path: after UnregisterObserver returns, subsequent
// background-job terminals must NOT invoke the observer.  Regression
// guard for the case where a TUI is torn down (defer detach()) but the
// orchestrator outlives it.
func TestSessionObserver_UnregisterStopsCallbacks(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "obs-unreg"},
		Root:  "init",
		Hosts: []string{"host.test.noop"},
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
					"enter": {{Target: "work"}},
				},
			},
			"work": {
				View: app.LegacyView("work"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.noop",
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
					},
				},
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
	reg.Register("host.test.noop", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})

	h := &staticHarness{intentName: "enter"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	obs := &recordingObserver{}
	orch.RegisterObserver(obs)
	orch.UnregisterObserver(obs)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	sids, _ := obs.Snapshot()
	require.Empty(t, sids,
		"observer must NOT receive callbacks after UnregisterObserver")
}
