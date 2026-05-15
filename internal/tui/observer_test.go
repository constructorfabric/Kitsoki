package tui_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

// captureHarness is a minimal harness that always returns a fixed
// intent.  Used by TestAttachOrchestratorObserver_DeliversBackgroundOutcome
// so the foreground Turn deterministically transitions into the state
// whose on_enter fires the background job.
type captureHarness struct {
	intentName string
}

func (h *captureHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: map[string]any{"intent": h.intentName},
	}, nil
}

func (h *captureHarness) Close() error { return nil }

// captureModel is a minimal tea.Model used only to assert that messages
// fanned out by AttachOrchestratorObserver's bridge reach the program's
// message channel.  Messages are recorded under a mutex so the
// orchestrator goroutine and the test goroutine can both observe them.
type captureModel struct {
	mu   *sync.Mutex
	msgs *[]tea.Msg
	done chan struct{}
}

func newCaptureModel() (captureModel, *sync.Mutex, *[]tea.Msg, chan struct{}) {
	mu := &sync.Mutex{}
	msgs := &[]tea.Msg{}
	done := make(chan struct{})
	return captureModel{mu: mu, msgs: msgs, done: done}, mu, msgs, done
}

func (m captureModel) Init() tea.Cmd { return nil }

func (m captureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.QuitMsg:
		return m, tea.Quit
	default:
		_ = v
	}
	m.mu.Lock()
	*m.msgs = append(*m.msgs, msg)
	hasOutcome := false
	for _, recorded := range *m.msgs {
		if _, ok := recorded.(captureSentinel); ok {
			hasOutcome = true
			break
		}
	}
	m.mu.Unlock()
	if hasOutcome {
		select {
		case <-m.done:
			// already closed
		default:
			close(m.done)
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m captureModel) View() string { return "" }

// captureSentinel is a synthetic marker the test injects to detect that
// the bridge fired AND was delivered to Update.  We can't directly match
// turnOutcomeMsg because it is unexported — but we can rely on the
// bridge delivering EXACTLY one message per OnBackgroundTurn, so by
// counting non-tick messages we get a deterministic check.
type captureSentinel struct{}

// TestAttachOrchestratorObserver_DeliversBackgroundOutcome wires a real
// orchestrator + scheduler + jobStore through AttachOrchestratorObserver
// and asserts that completing a background job results in a message
// being delivered to the tea.Program's Update goroutine.
//
// We can't introspect turnOutcomeMsg from this _test package (it's
// unexported), so the test counts deliveries: before the background
// job, no messages should have arrived; after WaitListenerIdle, at
// least one observer-delivered message must have arrived.
//
// This is the end-to-end proof that the orchestrator → TUI push works.
// Combined with TestSessionObserver_BackgroundJobTerminal (which
// verifies the outcome contents), the round-trip is covered.
func TestAttachOrchestratorObserver_DeliversBackgroundOutcome(t *testing.T) {
	// ── build a real orchestrator wired with a background job ────
	def := &app.AppDef{
		App:   app.AppMeta{ID: "tui-obs"},
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
						With:       map[string]any{"msg": "ack"},
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

	mach, err := machine.New(def)
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

	h := &captureHarness{intentName: "enter"}
	orch := orchestrator.New(def, mach, s, h,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// ── set up a headless tea.Program that records sent messages ──
	model, _, msgs, done := newCaptureModel()
	var stdout bytes.Buffer
	prog := tea.NewProgram(model,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(&stdout),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	detach := tuipkg.AttachOrchestratorObserver(orch, prog, sid)
	t.Cleanup(detach)

	runDone := make(chan error, 1)
	go func() {
		_, runErr := prog.Run()
		runDone <- runErr
	}()
	// Give the program a moment to start its message loop so Send
	// doesn't block on the goroutine launch.
	time.Sleep(50 * time.Millisecond)

	// ── drive the foreground turn → background job → completion ──
	out, err := orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
	defer waitCancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	// Inject the sentinel AFTER the bridge has fired.  The capture
	// model will quit upon seeing the sentinel; if the bridge fired
	// first, at least one preceding message will already be in msgs.
	prog.Send(captureSentinel{})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("captureModel.Update never saw the sentinel — program message loop not running")
	}
	prog.Quit()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("tea.Program did not exit after Quit")
	}

	// Filter to "real" delivered messages.  The bridge sends exactly
	// one turnOutcomeMsg per OnBackgroundTurn; everything else (tea
	// internal QuitMsg, our sentinel) we can identify by elimination.
	// We can't import turnOutcomeMsg from the _test package, so the
	// assertion checks that AT LEAST ONE non-sentinel, non-Quit
	// message arrived BEFORE the sentinel.
	gotBridgeMsg := false
	for _, m := range *msgs {
		if _, isSentinel := m.(captureSentinel); isSentinel {
			break
		}
		switch m.(type) {
		case tea.QuitMsg:
			continue
		default:
			gotBridgeMsg = true
		}
	}
	require.True(t, gotBridgeMsg,
		"AttachOrchestratorObserver should have delivered a turnOutcomeMsg to the tea.Program before the sentinel; got msgs=%v",
		*msgs,
	)
}
