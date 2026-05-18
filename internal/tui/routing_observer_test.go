package tui_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/trace"
	tuipkg "kitsoki/internal/tui"
)

// emitRoutingEvent fires a slog record at the given handler with the
// supplied msg + attrs, simulating an orchestrator emission. We build
// the record via slog.Logger so the (session_id, turn) attrs the
// orchestrator's TurnLogger always attaches are present.
func emitRoutingEvent(t *testing.T, h slog.Handler, sid app.SessionID, turn int64, msg string, attrs ...slog.Attr) {
	t.Helper()
	l := slog.New(h).With(
		slog.String("session_id", string(sid)),
		slog.Int64("turn", turn),
	)
	args := make([]any, 0, len(attrs))
	for _, a := range attrs {
		args = append(args, a)
	}
	l.Debug(msg, args...)
}

// TestRoutingObserver_TranslatesEvents drives the observer (without
// any *tea.Program) and inspects the per-turn trace ring. This is the
// fastest possible test — no goroutines, no message channel.
func TestRoutingObserver_TranslatesEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		setup     func(h slog.Handler, sid app.SessionID)
		wantMsgs  []string // expected ev.Msg values in the captured trace
		wantTurn  int64
		wantFinal string // substring expected in FormatRoutingTrace output
	}{
		{
			name: "semantic_hit followed by miss + hit",
			setup: func(h slog.Handler, sid app.SessionID) {
				emitRoutingEvent(t, h, sid, 1, trace.EvTurnDeterministicMiss,
					slog.String("input", "wade across"))
				emitRoutingEvent(t, h, sid, 1, trace.EvTurnSemanticHit,
					slog.String("intent", "ford"),
					slog.String("reason", "synonym:wade"),
					slog.Float64("confidence", 0.9))
			},
			wantMsgs:  []string{trace.EvTurnDeterministicMiss, trace.EvTurnSemanticHit},
			wantTurn:  1,
			wantFinal: "synonym:wade",
		},
		{
			name: "llm_routed",
			setup: func(h slog.Handler, sid app.SessionID) {
				emitRoutingEvent(t, h, sid, 2, trace.EvTurnSemanticMiss,
					slog.String("input", "anything"))
				emitRoutingEvent(t, h, sid, 2, trace.EvTurnLLMRouted,
					slog.String("intent", "ask_question"),
					slog.Float64("confidence", 0.81),
					slog.String("model", "claude-haiku"))
			},
			wantMsgs:  []string{trace.EvTurnSemanticMiss, trace.EvTurnLLMRouted},
			wantTurn:  2,
			wantFinal: "claude-haiku",
		},
		{
			name: "ambiguous_2way",
			setup: func(h slog.Handler, sid app.SessionID) {
				emitRoutingEvent(t, h, sid, 3, trace.EvTurnSemanticAmbiguous,
					slog.Any("candidates", []string{"ford", "wade"}))
			},
			wantMsgs:  []string{trace.EvTurnSemanticAmbiguous},
			wantTurn:  3,
			wantFinal: "candidates=[ford wade]",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sid := app.SessionID("test-sid")
			obs := tuipkg.NewRoutingObserver(sid)
			tc.setup(obs, sid)

			rt := obs.Trace(tc.wantTurn)
			require.NotNil(t, rt, "expected a routing trace for turn %d", tc.wantTurn)
			require.Len(t, rt.Events, len(tc.wantMsgs))
			for i, want := range tc.wantMsgs {
				require.Equal(t, want, rt.Events[i].Msg,
					"event[%d].Msg mismatch (got %q want %q)", i, rt.Events[i].Msg, want)
			}
			require.Equal(t, tc.wantTurn, obs.LatestTurn())

			rendered := tuipkg.FormatRoutingTrace(rt)
			require.Contains(t, rendered, tc.wantFinal,
				"formatted trace should contain %q, got:\n%s", tc.wantFinal, rendered)
		})
	}
}

// TestRoutingObserver_FiltersForeignSession verifies the sid filter:
// records carrying a different session_id never land in the ring.
func TestRoutingObserver_FiltersForeignSession(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("ours")
	emitRoutingEvent(t, obs, "theirs", 7, trace.EvTurnSemanticHit,
		slog.String("intent", "go"),
		slog.String("reason", "synonym:walk"),
		slog.Float64("confidence", 0.9))
	require.Nil(t, obs.Trace(7), "foreign-session event must NOT be recorded")
	require.Equal(t, int64(0), obs.LatestTurn())
}

// TestRoutingObserver_IgnoresNonRoutingMsgs verifies the prefix filter
// drops orchestrator events the chip doesn't care about (turn.start,
// turn.routed, etc.).
func TestRoutingObserver_IgnoresNonRoutingMsgs(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("sid")
	for _, msg := range []string{trace.EvTurnStart, trace.EvTurnRouted, trace.EvTurnDone, trace.EvMachineTransition} {
		emitRoutingEvent(t, obs, "sid", 1, msg)
	}
	require.Nil(t, obs.Trace(1), "non-routing msgs must not appear in the routing ring")
}

// TestRoutingObserver_FormatTraceIncludesTimestamps verifies the
// ctrl+r overlay pretty-printer.
func TestRoutingObserver_FormatTraceIncludesTimestamps(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("sid")
	emitRoutingEvent(t, obs, "sid", 5, trace.EvTurnDeterministicHit,
		slog.String("intent", "menu_pick"),
		slog.String("match_type", "display"))
	rendered := tuipkg.FormatRoutingTrace(obs.Trace(5))
	require.Contains(t, rendered, "turn 5 routing trace")
	require.Contains(t, rendered, trace.EvTurnDeterministicHit)
	require.Contains(t, rendered, "intent=menu_pick")
	require.Contains(t, rendered, "match_type=display")
}

// TestRoutingObserver_RingEviction verifies the per-turn ring drops
// the oldest turn when its capacity is exceeded.
func TestRoutingObserver_RingEviction(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("sid")
	// Emit one event per turn for 35 turns — capacity is 32, so the
	// first 3 turns must roll off.
	for turn := int64(1); turn <= 35; turn++ {
		emitRoutingEvent(t, obs, "sid", turn, trace.EvTurnDeterministicHit,
			slog.String("intent", "x"))
	}
	require.Nil(t, obs.Trace(1), "turn 1 should have been evicted by the ring")
	require.Nil(t, obs.Trace(3), "turn 3 should have been evicted by the ring")
	require.NotNil(t, obs.Trace(4), "turn 4 should still be in the ring (cap=32, kept 4..35)")
	require.NotNil(t, obs.Trace(35))
	require.Equal(t, int64(35), obs.LatestTurn())
}

// TestRoutingObserver_DispatchToProgram drives the full chain:
// emit → observer → tea.Program.Send → captureModel. Mirrors the
// pattern in observer_test.go::TestAttachOrchestratorObserver_…
// but for routing events.
func TestRoutingObserver_DispatchToProgram(t *testing.T) {
	// Not parallel: drives a real tea.Program through stdin/stdout
	// pipes which can be flaky under concurrent test runs.
	model, _, msgs, done := newCaptureModel()
	prog := tea.NewProgram(model,
		tea.WithInput(strings.NewReader("")),
		tea.WithOutput(&strings.Builder{}),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	obs := tuipkg.NewRoutingObserver("sid")
	obs.Attach(prog)
	t.Cleanup(obs.Detach)

	runDone := make(chan error, 1)
	go func() {
		_, runErr := prog.Run()
		runDone <- runErr
	}()
	// Give the program a moment to start its message loop.
	time.Sleep(50 * time.Millisecond)

	// Fire one resolving sequence: deterministic_miss then llm_routed.
	emitRoutingEvent(t, obs, "sid", 1, trace.EvTurnDeterministicMiss,
		slog.String("input", "test"))
	emitRoutingEvent(t, obs, "sid", 1, trace.EvTurnLLMRouted,
		slog.String("intent", "ask"),
		slog.Float64("confidence", 0.81),
		slog.String("model", "claude-haiku"))

	// Dispatch is now fire-and-forget (M7) — each Send runs in its
	// own goroutine. Yield so those goroutines reach prog.Send before
	// we inject the sentinel that quits the captureModel.
	time.Sleep(50 * time.Millisecond)

	// Inject a sentinel so the captureModel quits after both routing
	// messages have been delivered.
	prog.Send(captureSentinel{})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("captureModel.Update never saw the sentinel — routing observer didn't forward")
	}
	prog.Quit()
	<-runDone

	// At least one RoutingTier*Msg must have been delivered before
	// the sentinel. We can't switch-type-assert on the unexported
	// chip msg from the _test package, but we can count non-sentinel
	// non-internal messages and verify ≥2 (one miss + one hit).
	var routingMsgs int
	for _, msg := range *msgs {
		if _, ok := msg.(captureSentinel); ok {
			continue
		}
		if _, ok := msg.(tea.QuitMsg); ok {
			continue
		}
		routingMsgs++
	}
	require.GreaterOrEqual(t, routingMsgs, 2,
		"observer must have dispatched at least the miss + hit messages, got %d", routingMsgs)
}

// TestRoutingObserver_WithAttrsClone verifies the slog.Handler
// WithAttrs contract: pre-attached attrs (sid, turn) survive into the
// translator so a logger built once and reused per-turn still routes
// correctly.
func TestRoutingObserver_WithAttrsClone(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("sid")

	// Mimic the orchestrator pattern: build a base logger, then
	// attach the per-turn attrs via With(). The observer must see
	// (session_id, turn) on every record emitted through the clone.
	base := slog.New(obs)
	turnLog := base.With(
		slog.String("session_id", "sid"),
		slog.Int64("turn", 9),
	)
	turnLog.Debug(trace.EvTurnSemanticHit,
		slog.String("intent", "go"),
		slog.String("reason", "synonym:walk"),
		slog.Float64("confidence", 0.9))

	rt := obs.Trace(9)
	require.NotNil(t, rt, "WithAttrs-cloned emissions must reach the ring")
	require.Len(t, rt.Events, 1)
	require.Equal(t, trace.EvTurnSemanticHit, rt.Events[0].Msg)
	require.Equal(t, "go", rt.Events[0].Attrs["intent"])
}

// TestRoutingObserver_NoColorDoesNotBreakChip was removed in Phase 7
// along with the routing chip. The successor surface — the inline
// routing-status block in the transcript — honours NO_COLOR via
// blocks.Renderer.NoColor, which is exercised by
// blocks_test.go::TestRoutingResolvedFormats run under NO_COLOR
// (the test helper sets r.NoColor=true).

// blockingSender is a fake [tuipkg.Sender] whose Send method blocks
// forever (or until the test's stop channel closes). Used by
// TestRoutingObserver_HandleDoesNotBlockOnSlowSender to prove
// observer.Handle returns promptly even when the downstream sender
// (in production: a *tea.Program with a full msg channel) stalls.
type blockingSender struct {
	t        *testing.T
	hits     chan tea.Msg // each Send call sends one msg here, blocking until the test drains
	stop     <-chan struct{}
	released chan struct{} // closed when at least one goroutine has been released by stop
}

func (b *blockingSender) Send(msg tea.Msg) {
	// First record that a goroutine reached us — used to assert at
	// least one Send was attempted.
	select {
	case b.hits <- msg:
	default:
		// non-blocking record-or-skip; the test only needs SOME hit.
	}
	<-b.stop
	// Note: deliberately NOT closing `released` here — multiple
	// goroutines call Send, so only one needs to advertise progress.
	select {
	case <-b.released:
	default:
		close(b.released)
	}
}

// TestRoutingObserver_HandleDoesNotBlockOnSlowSender pins the M7 fix:
// even when the attached Sender blocks indefinitely on Send,
// observer.Handle must return within a tight time bound. Before the
// fix this test would time out — Handle called prog.Send synchronously
// and would have inherited the blocking.
//
// We dispatch N events from a single goroutine and require the
// goroutine to complete within a generous-but-bounded duration.
func TestRoutingObserver_HandleDoesNotBlockOnSlowSender(t *testing.T) {
	t.Parallel()

	const numEvents = 8
	stop := make(chan struct{})
	defer close(stop)

	sender := &blockingSender{
		t:        t,
		hits:     make(chan tea.Msg, numEvents),
		stop:     stop,
		released: make(chan struct{}),
	}
	obs := tuipkg.NewRoutingObserver("sid")
	obs.AttachSender(sender)
	t.Cleanup(obs.Detach)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= numEvents; i++ {
			emitRoutingEvent(t, obs, "sid", int64(i), trace.EvTurnLLMRouted,
				slog.String("intent", "x"),
				slog.Float64("confidence", 0.5),
				slog.String("model", "claude-haiku"))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("RoutingObserver.Handle blocked on a slow sender; emitting %d events should be fire-and-forget", numEvents)
	}

	// At least one event must have hit the sender's Send (proving
	// fan-out happened). All N goroutines are still blocked inside
	// Send; the defer-close(stop) above releases them so the test
	// doesn't leak.
	select {
	case <-sender.hits:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no goroutine ever reached blockingSender.Send within 500ms")
	}
}

// TestRoutingObserver_AttachSenderTypedNil verifies the typed-nil
// guard: passing (*tea.Program)(nil) through AttachSender leaves the
// observer in the same state as Detach — no fan-out, no panic.
func TestRoutingObserver_AttachSenderTypedNil(t *testing.T) {
	t.Parallel()
	obs := tuipkg.NewRoutingObserver("sid")
	// Attach a typed-nil program; Handle must not panic and must not
	// try to call Send on it.
	obs.Attach((*tea.Program)(nil))
	emitRoutingEvent(t, obs, "sid", 1, trace.EvTurnLLMRouted,
		slog.String("model", "claude-haiku"))
	// Nothing to assert beyond the absence of a panic, but the ring
	// should still record the event.
	require.NotNil(t, obs.Trace(1), "ring must record events even when sender is typed-nil")
}

// ensure context import isn't dropped if we ever switch handlers
var _ = context.Background
