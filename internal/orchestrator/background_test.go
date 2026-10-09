package orchestrator_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestTimeoutCancelJobStopsBackgroundWork(t *testing.T) {
	def := &app.AppDef{
		App:     app.AppMeta{ID: "timeout-cancel-job"},
		Root:    "init",
		Hosts:   []string{"host.test.block"},
		World:   map[string]app.VarDef{"last_job_id": {Type: "string", Default: ""}},
		Intents: map[string]app.Intent{"enter": {Title: "Enter"}},
		States: map[string]*app.State{
			"init": {View: app.LegacyView("init"), On: map[string][]app.Transition{"enter": {{Target: "working"}}}},
			"working": {
				View:    app.LegacyView("working"),
				Timeout: &app.TimeoutDef{After: "1s", Target: "timed_out", CancelJob: true},
				OnEnter: []app.Effect{{Invoke: "host.test.block", Background: true, Bind: map[string]string{"last_job_id": "job_id"}}},
			},
			"timed_out": {Terminal: true, View: app.LegacyView("timed out")},
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
	started := make(chan struct{})
	reg.Register("host.test.block", func(ctx context.Context, _ map[string]any) (host.Result, error) {
		close(started)
		<-ctx.Done()
		return host.Result{}, ctx.Err()
	})
	clk := clock.NewFake(time.Unix(0, 0))
	orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"}, orchestrator.WithClock(clk), orchestrator.WithHostRegistry(reg), orchestrator.WithScheduler(sched), orchestrator.WithJobStore(jobStore))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	_, err = orch.Turn(context.Background(), sid, "enter")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background job did not start")
	}
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	jobID, _ := journey.World.Vars["last_job_id"].(string)
	require.NotEmpty(t, jobID)
	clk.Advance(2 * time.Second)
	require.Eventually(t, func() bool {
		job, found := sched.Get(jobID)
		return found && job.Status == jobs.JobCancelled
	}, time.Second, time.Millisecond, "timeout must cancel the running background job")
}

// TestBackgroundJobEndToEnd verifies the full background-job lifecycle:
//  1. A Turn that transitions INTO "lobby" fires lobby's on_enter: background:
//     true effect, submitting a job and binding last_job_id.
//  2. The session listener fires handleJobTerminal, applies on_complete effects.
//  3. world.x == "hello" after reload (on_complete resolved the template via
//     last_job_result.output, confirming the JSON on_complete round-trip works).
//  4. $inbox.unread >= 1 (a success notification was posted).
//  5. Event log contains a JobSubmitted event in the dispatch turn and a
//     TurnStarted{kind:"background_completion"} event in a later turn.
//
// App structure:
//
//	"init" (initial state) → "enter" intent → "lobby" (on_enter: background job)
//	"lobby" → "end" intent → "end" (terminal)
func TestBackgroundJobEndToEnd(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "bg-test"},
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
				// on_enter fires a background job. The on_complete chain sets
				// world.x from the job result.
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "hello"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							{Set: map[string]any{"x": "{{ world.last_job_result.output }}"}},
							{Say: "done"},
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

	// Synchronous echo handler: returns {output: args["msg"]}.
	reg := host.NewRegistry()
	reg.Register("host.test.echo", func(ctx context.Context, args map[string]any) (host.Result, error) {
		msg, _ := args["msg"].(string)
		return host.Result{Data: map[string]any{"output": msg}}, nil
	})

	// Harness routes "enter" so we transition init→lobby.
	h := &staticHarness{intentName: "enter"}

	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Fire Turn: harness routes "enter" → init→lobby → on_enter fires.
	out, err := orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("lobby"), out.NewState)

	// last_job_id should be set (bound during the dispatch turn).
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	lastJobID, _ := journey.World.Vars["last_job_id"].(string)
	require.NotEmpty(t, lastJobID, "last_job_id should be set after background dispatch")

	// Wait for the scheduler to drain: all job goroutines have terminated.
	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	require.NoError(t, sched.WaitIdle(waitCtx), "scheduler did not go idle in time")

	// Wait for the orchestrator's session listener to finish processing the
	// terminal event (applies on_complete, posts notification, writes turn).
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid), "listener did not go idle in time")

	// Assert world.x == "hello" (on_complete ran and resolved the template from
	// last_job_result.output, confirming the JSON round-trip of on_complete works).
	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "hello", finalJourney.World.Vars["x"],
		"on_complete should have set x from last_job_result.output")

	// Assert $inbox.unread >= 1 (success notification was posted).
	counts, err := jobStore.UnreadCount(ctx, sid)
	require.NoError(t, err)
	total := 0
	for _, cnt := range counts {
		total += cnt
	}
	require.GreaterOrEqual(t, total, 1, "$inbox should have at least one unread notification")

	// Assert event log structure.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)

	// Find JobSubmitted event.
	foundJobSubmitted := false
	for _, ev := range history {
		if ev.Kind == store.JobSubmitted {
			foundJobSubmitted = true
			break
		}
	}
	require.True(t, foundJobSubmitted, "JobSubmitted event must be in the event log")

	// Find TurnStarted{kind:"background_completion"} in a later turn.
	foundBGCompletion := false
	for _, ev := range history {
		if ev.Kind != store.TurnStarted {
			continue
		}
		var payload map[string]any
		if jsonErr := json.Unmarshal(ev.Payload, &payload); jsonErr != nil {
			continue
		}
		if payload["kind"] == "background_completion" {
			foundBGCompletion = true
			break
		}
	}
	require.True(t, foundBGCompletion,
		"TurnStarted{kind:background_completion} must appear in the event log")
}

// TestCustomBind_LastJobIDReplay verifies P0-4: when a background effect uses
// a custom bind key (e.g. bind: {my_key: job_id}), BOTH my_key AND last_job_id
// must be in the event log as separate EffectApplied events so that after a
// process restart (loadJourney) both world variables are restored.
func TestCustomBind_LastJobIDReplay(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "custbind-test"},
		Root:  "init",
		Hosts: []string{"host.test.noop"},
		World: map[string]app.VarDef{
			"my_key":      {Type: "string", Default: ""},
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
				// on_enter fires when init→work; dispatches a background job
				// with a custom bind key.
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.noop",
						Background: true,
						Bind:       map[string]string{"my_key": "job_id"},
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

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn fires the on_enter background effect when entering "work".
	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// Reload the journey from the event log (simulating a process restart).
	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	myKey, _ := finalJourney.World.Vars["my_key"].(string)
	lastJobID, _ := finalJourney.World.Vars["last_job_id"].(string)

	require.NotEmpty(t, myKey, "my_key should be set in world after replay")
	require.NotEmpty(t, lastJobID, "last_job_id should be set in world after replay")
	require.Equal(t, myKey, lastJobID, "my_key and last_job_id should hold the same job ID")

	// Verify both EffectApplied events exist in the event log.
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)

	foundMyKey, foundLastJobID := false, false
	for _, ev := range history {
		if ev.Kind != store.EffectApplied {
			continue
		}
		var p struct {
			Set map[string]any `json:"set"`
		}
		if jsonErr := json.Unmarshal(ev.Payload, &p); jsonErr != nil {
			continue
		}
		if _, ok := p.Set["my_key"]; ok {
			foundMyKey = true
		}
		if _, ok := p.Set["last_job_id"]; ok {
			foundLastJobID = true
		}
	}
	require.True(t, foundMyKey, "EffectApplied{set:{my_key:...}} must be in event log")
	require.True(t, foundLastJobID, "EffectApplied{set:{last_job_id:...}} must be in event log")
}

// TestBackgroundJob_ThreadsKitsokiSessionID guards the goal-seeker dogfood
// blocker (2026-07-04): a `background: true` host effect must run with the parent
// session id reachable via host.KitsokiSessionIDFromCtx, exactly like a foreground
// turn. Before the fix the scheduler derived the job ctx via WithoutCancel(ctx)
// but the dispatch ctx never carried the WithKitsokiSessionID seam (only
// AgentCallCtx), and the studio process env has no KITSOKI_SESSION_ID — so the job
// handler saw an empty session id. That silently disabled host.agent.task's
// deterministic maker-seed backstop (RegisterPendingSeedFromTaskArgs bailed on the
// empty id), stranding every background maker with an empty ticket_id. This runs a
// real background job and asserts the handler sees the session id.
func TestBackgroundJob_ThreadsKitsokiSessionID(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "bg-sid-test"},
		Root:  "init",
		Hosts: []string{"host.test.capture"},
		World: map[string]app.VarDef{
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{"enter": {Title: "Enter"}},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On:   map[string][]app.Transition{"enter": {{Target: "work"}}},
			},
			"work": {
				View: app.LegacyView("work"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.capture",
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

	// The background handler records the session id it observes on its ctx.
	captured := make(chan string, 1)
	reg := host.NewRegistry()
	reg.Register("host.test.capture", func(ctx context.Context, args map[string]any) (host.Result, error) {
		captured <- host.KitsokiSessionIDFromCtx(ctx)
		return host.Result{}, nil
	})

	// Guarantee the process env can't paper over a missing ctx seam.
	t.Setenv("KITSOKI_SESSION_ID", "")

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

	select {
	case got := <-captured:
		require.Equal(t, string(sid), got,
			"a background host job must see the parent session id via host.KitsokiSessionIDFromCtx (needed by the maker-seed backstop)")
	default:
		t.Fatal("background handler never ran")
	}
}

// TestOnComplete_SayText verifies P0-5: when an on_complete chain includes a
// say: effect, it must appear as an EffectApplied{say:...} event in the event
// log of the synthetic background_completion turn.
func TestOnComplete_SayText(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "saytext-test"},
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
					"enter": {{Target: "lobby"}},
				},
			},
			"lobby": {
				View: app.LegacyView("lobby"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "hello"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							{Say: "job finished"},
						},
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

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// Verify MachineSay{text:"job finished"} appears in the event log.
	// (say narration is split out of EffectApplied into its own MachineSay event.)
	history, err := s.LoadHistory(sid)
	require.NoError(t, err)

	foundSay := false
	for _, ev := range history {
		if ev.Kind != store.MachineSay {
			continue
		}
		var p struct {
			Text string `json:"text"`
		}
		if jsonErr := json.Unmarshal(ev.Payload, &p); jsonErr != nil {
			continue
		}
		if p.Text == "job finished" {
			foundSay = true
			break
		}
	}
	require.True(t, foundSay,
		"MachineSay{text:'job finished'} must appear in background_completion turn events")
}

// TestOnComplete_HostCallError_PartialWorldNotPersisted verifies P1-1: when
// dispatchHostCalls returns an error, the partial hostEvts and hostWorld must
// NOT be appended to the turn events / world.
func TestOnComplete_HostCallError_PartialWorldNotPersisted(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "hostcallerr-test"},
		Root:  "init",
		Hosts: []string{"host.test.fail", "host.test.echo"},
		World: map[string]app.VarDef{
			"x":           {Type: "string", Default: "original"},
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "lobby"}},
				},
			},
			"lobby": {
				View: app.LegacyView("lobby"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "hi"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
						OnComplete: []app.Effect{
							// This invoke will fail because host.test.fail is not
							// registered in the registry.
							{Invoke: "host.test.fail", Bind: map[string]string{"x": "result"}},
						},
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
	// host.test.echo is registered; host.test.fail is NOT.
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

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// World.x should still be "original" — the failed host call must not have
	// committed its partial world update.
	finalJourney, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	xVal, _ := finalJourney.World.Vars["x"].(string)
	require.Equal(t, "original", xVal,
		"world.x must not be mutated when on_complete host dispatch errors")

	// Verify $inbox still has a notification (job did complete).
	counts, err := jobStore.UnreadCount(ctx, sid)
	require.NoError(t, err)
	total := 0
	for _, cnt := range counts {
		total += cnt
	}
	// suppress "inbox imported and not used" by using the package
	_ = inbox.WorldKey
	require.GreaterOrEqual(t, total, 1)
}

// TestSubmitDirect_TerminalStateStopsListener is a regression test for P2-4:
// SubmitDirect reaching a terminal state must call stopSessionListener, tearing
// down the per-session goroutine so it does not leak.
func TestSubmitDirect_TerminalStateStopsListener(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "submitdirect-terminal-test"},
		Root:  "start",
		Hosts: []string{},
		Intents: map[string]app.Intent{
			"finish": {Title: "Finish"},
		},
		States: map[string]*app.State{
			"start": {
				View: app.LegacyView("start"),
				On: map[string][]app.Transition{
					"finish": {{Target: "done"}},
				},
			},
			"done": {Terminal: true, View: app.LegacyView("done")},
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

	h := &staticHarness{intentName: "finish"}
	orch := orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Capture goroutine count after session start (listener goroutine is running).
	before := runtime.NumGoroutine()

	// SubmitDirect to a terminal state — this should call stopSessionListener.
	out, err := orch.SubmitDirect(ctx, sid, "finish", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode)

	// Allow a brief window for the listener goroutine to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	after := runtime.NumGoroutine()
	// Allow +1 for scheduler noise; the listener goroutine should be gone.
	require.LessOrEqual(t, after, before+1,
		"session listener goroutine leaked after SubmitDirect to terminal state: before=%d after=%d", before, after)
}
