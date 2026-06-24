package orchestrator_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_CancelDuringHostCallPersistsNothing is the orchestrator-side
// guard for the web "Stop" button: when the execution context is cancelled while
// an agent/host call is in flight (the operator hit Stop, which propagates a
// context cancel down to the agent subprocess), the turn must abort WITHOUT
// baking the cancellation into the journal. A cancelled turn leaves the session
// exactly at its pre-turn state — never persisting "context canceled" into
// world.last_error or routing through an on_error arc, which would otherwise be
// replayed on every later reopen.
func TestOrchestrator_CancelDuringHostCallPersistsNothing(t *testing.T) {
	def, err := app.Load("testdata/hostbind/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	entered := make(chan struct{}) // closed when the host call is entered
	var calls int32
	reg := host.NewRegistry()
	reg.Register("host.probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// First call: simulate a long-running agent that is killed by the
			// operator's Stop — block until the context is cancelled, then surface
			// the cancellation as an error the way a SIGKILLed subprocess would.
			close(entered)
			<-ctx.Done()
			return host.Result{}, ctx.Err()
		}
		// Later calls succeed, so we can prove the session was not poisoned.
		return host.Result{Data: map[string]any{"message": "hello world"}}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	type res struct {
		out *orchestrator.TurnOutcome
		err error
	}
	done := make(chan res, 1)
	go func() {
		out, err := orch.SubmitDirect(ctx, sid, "ask", map[string]any{})
		done <- res{out, err}
	}()

	<-entered // host call is in flight
	cancel()  // operator hits Stop

	var r res
	select {
	case r = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitDirect did not return after cancellation within 2s")
	}
	assert.ErrorIs(t, r.err, context.Canceled, "a cancelled turn must surface the cancellation")

	// Nothing was persisted: the journey is still at the pre-turn state with no
	// poisoned world keys and turn count unchanged.
	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	assert.Equal(t, app.StatePath("idle"), journey.State, "cancelled turn must not transition the session")
	assert.Equal(t, app.TurnNumber(0), journey.Turn, "cancelled turn must not advance the turn counter")
	// last_error exists as a default empty world key; the point is the
	// cancellation must NOT have written the failure into it.
	assert.Empty(t, journey.World.Vars["last_error"], "cancellation must not bake last_error")
	assert.NotContains(t, journey.World.Vars, "host_error", "cancellation must not bake host_error")
	assert.Empty(t, journey.World.Vars["greeting"], "cancelled turn must not bind a partial result")

	// The session is healthy: a fresh turn transitions and binds cleanly.
	out, err := orch.SubmitDirect(context.Background(), sid, "ask", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	assert.Contains(t, out.View, "hello world")
}
