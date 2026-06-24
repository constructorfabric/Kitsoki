package orchestrator_test

// agent_usage_background_test.go — verifies that token usage captured by the
// claude-CLI transport reaches AgentReturned.Meta even when the agent call
// runs as a BACKGROUND job. The foreground path is covered by the host
// package's agent_usage_test.go; this exercises the separate scheduler ctx,
// where the usage box is installed inside dispatchBackground's job handler
// closure rather than in the foreground host_dispatch loop.

import (
	"context"
	"encoding/json"
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

// lockedSink is a mutex-guarded EventSink — the background job appends from a
// scheduler goroutine while the test goroutine reads, so the bare append-slice
// sinks used elsewhere would race under -race.
type lockedSink struct {
	mu     sync.Mutex
	events []store.Event
}

func (s *lockedSink) Append(ev store.Event) error {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	return nil
}

func (s *lockedSink) History() store.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(store.History, len(s.events))
	copy(out, s.events)
	return out
}

func (s *lockedSink) snapshot() []store.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Event, len(s.events))
	copy(out, s.events)
	return out
}

// streamJSONUsageRunner is a ClaudeRunner whose stdout is a stream-json
// transcript ending in a result event carrying usage + total_cost_usd.
func streamJSONUsageRunner() host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		out := `{"type":"system","subtype":"init","session_id":"sess-bg-1"}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"text","text":"thinking"}]}}` + "\n" +
			`{"type":"result","subtype":"success","result":"done","session_id":"sess-bg-1",` +
			`"total_cost_usd":0.0123,"usage":{"input_tokens":1200,"output_tokens":345,` +
			`"cache_read_input_tokens":900,"cache_creation_input_tokens":50}}` + "\n"
		return host.ClaudeRun{Stdout: out}, nil
	}
}

// TestBackgroundAgentAsk_UsageMeta runs host.agent.ask as a background job and
// asserts the AgentReturned event carries the token usage in Meta.
func TestBackgroundAgentAsk_UsageMeta(t *testing.T) {
	const storyYAML = `
app:
  id: bg-usage-test
  version: 0.1.0

world:
  last_job_id:
    type: string
    default: ""

root: asking

states:
  asking:
    terminal: true
    on_enter:
      - invoke: host.agent.ask
        background: true
        with:
          prompt: "summarise please"
`

	def, err := app.LoadBytes([]byte(storyYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jobStore, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	sched := jobs.NewScheduler(jobStore)

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	sink := &lockedSink{}

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(jobStore),
		orchestrator.WithEventSink(sink),
	)

	// Install the stubbed claude runner on the ctx that drives the turn. It
	// propagates through dispatchBackground → scheduler.Submit → the job
	// goroutine's handler, so the background agent call hits the stub rather
	// than forking a real claude subprocess.
	ctx := host.WithClaudeRunner(context.Background(), streamJSONUsageRunner())

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// RunInitialOnEnter fires the root's on_enter chain, which submits the
	// background agent.ask job.
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	// Wait for the scheduler to drain so the job handler has written its
	// AgentReturned event.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx), "scheduler did not go idle")

	// Find the AgentReturned event and assert its Meta carries the usage.
	var returned *store.Event
	for _, ev := range sink.snapshot() {
		if ev.Kind == store.AgentReturned {
			ev := ev
			returned = &ev
		}
	}
	require.NotNil(t, returned, "AgentReturned event must appear in the background trace")

	var payload host.AgentReturnedPayload
	require.NoError(t, json.Unmarshal(returned.Payload, &payload))
	require.NotNil(t, payload.Meta, "AgentReturned.Meta is nil — background usage was not captured")

	usage, ok := payload.Meta["usage"].(map[string]any)
	require.True(t, ok, "Meta.usage missing: %#v", payload.Meta)
	require.Equal(t, float64(1200), usage["input_tokens"])
	require.Equal(t, float64(345), usage["output_tokens"])
	require.Equal(t, 0.0123, payload.Meta["cost_usd"])
}
