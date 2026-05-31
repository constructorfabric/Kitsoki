package orchestrator_test

// state_path_test.go — Finding 2.1: every event has non-empty state_path.
//
// Tests that state_path is non-empty on all events, including:
//   - a successful turn (SubmitDirect accepted)
//   - a rejected turn (INTENT_NOT_ALLOWED)
//
// Uses the JSONL-backed turn path (WithEventSink + WithEventSinkAuthority)
// so state_path appears in the on-disk JSONL.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestStatePathNonEmpty_Accepted verifies that every event written during an
// accepted turn has a non-empty state_path in the on-disk JSONL.
func TestStatePathNonEmpty_Accepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Submit a valid intent for the foyer state.
	out, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	// All events must have non-empty state_path.
	hist := sink.History()
	require.NotEmpty(t, hist, "expected at least one event")
	for _, ev := range hist {
		require.NotEmpty(t, string(ev.StatePath),
			"event kind=%q turn=%d seq=%d must have non-empty state_path", ev.Kind, ev.Turn, ev.Seq)
	}
}

// TestStatePathNonEmpty_Rejected verifies that every event written during an
// immediate-rejection turn (INTENT_NOT_ALLOWED) has a non-empty state_path.
// This is the exact failure mode described in Finding 2.1.
func TestStatePathNonEmpty_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_reject.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// submit an intent that doesn't exist in the foyer state — triggers
	// INTENT_NOT_ALLOWED / rejection path.
	out, err := orch.SubmitDirect(ctx, sid, "nonexistent_intent_xyz", nil)
	require.NoError(t, err, "rejection must be returned as an outcome, not an error")
	require.Equal(t, orchestrator.ModeRejected, out.Mode,
		"nonexistent_intent_xyz must be rejected")

	// All events written during the rejection must have non-empty state_path.
	hist := sink.History()
	require.NotEmpty(t, hist, "rejection must produce at least one event")
	for _, ev := range hist {
		require.NotEmpty(t, string(ev.StatePath),
			"rejection event kind=%q turn=%d seq=%d must have non-empty state_path (finding 2.1)",
			ev.Kind, ev.Turn, ev.Seq)
	}

	// Also check the events on the outcome itself.
	for _, ev := range out.Events {
		require.NotEmpty(t, string(ev.StatePath),
			"outcome event kind=%q must have non-empty state_path", ev.Kind)
	}
}

// TestMidOracleCall_ReIssueOnNextTurn verifies the policy (a) for mid-oracle-call
// traces: a trace that ends with a dangling OracleCalled (no OracleReturned)
// can be loaded and a subsequent turn runs normally. The dangling OracleCalled
// is treated as a no-op by BuildJourney (replay-safe), so the next turn starts
// from the last fully-committed world state.
//
// Finding 2.9: "add an integration test that demonstrates the re-issue". The
// re-issue means the orchestrator starts fresh from the pre-oracle world state,
// so the next SubmitDirect is equivalent to running the same intent from scratch.
// The test asserts that (a) the turn succeeds, (b) the session state is correct
// after the dangling-oracle trace is loaded, and (c) a new SubmitDirect appends
// new events to the trace (demonstrating the oracle would be re-issued if needed).
func TestMidOracleCall_ReIssueOnNextTurn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "mid_oracle.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)

	// Write a trace that has 1 committed turn (go west → cloakroom) then a
	// dangling OracleCalled (as if the oracle was dispatched but the process
	// crashed before OracleReturned was written).
	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)

	// Committed turn 1: go west → cloakroom.
	turn1Events := []store.Event{
		{Turn: 1, Kind: store.TurnStarted, Payload: json.RawMessage(`{"input":"go west"}`)},
		{Turn: 1, Kind: store.TransitionApplied, StatePath: "foyer", Payload: json.RawMessage(`{"from":"foyer","to":"cloakroom","intent":"go"}`)},
		{Turn: 1, Kind: store.TurnEnded, Payload: json.RawMessage(`{"outcome":"transitioned"}`)},
	}
	for _, ev := range turn1Events {
		require.NoError(t, sink.Append(ev))
	}

	// Dangling OracleCalled for turn 2 — process "crashed" before OracleReturned.
	calledPayload, _ := json.Marshal(map[string]any{"verb": "ask", "prompt": "test prompt"})
	require.NoError(t, sink.Append(store.Event{
		Turn:    2,
		Kind:    store.OracleCalled,
		CallID:  "deadbeef12345678",
		Payload: json.RawMessage(calledPayload),
	}))
	require.NoError(t, sink.Close())

	// Now load the trace and run a new turn. The dangling OracleCalled is a
	// fold no-op; the orchestrator should pick up at state="cloakroom" (turn 1's
	// final state) and run turn 3 (turn 2 was claimed by the oracle call).
	sink2, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	defer sink2.Close()

	s, err := store.OpenMemory()
	require.NoError(t, err)
	defer s.Close()

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink2),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// The next SubmitDirect should succeed — proving the orchestrator
	// recovered cleanly from the mid-oracle-call state.
	out, err := orch.SubmitDirect(ctx, sid, "hang_cloak", nil)
	require.NoError(t, err, "SubmitDirect must succeed after mid-oracle-call recovery")
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode,
		"hang_cloak in cloakroom must transition successfully")

	// The trace now has new events appended after the dangling OracleCalled,
	// demonstrating that the re-issue (new turn) completed.
	histAfter := sink2.History()
	require.Greater(t, len(histAfter), 4,
		"trace must have more events after the new turn (dangling oracle + new turn events)")
}

// TestStatePathNonEmpty_RunIntent_Accepted verifies that every event written
// during an accepted turn via the RunIntent path (used by the cassette /
// control-inversion flow runner) has a non-empty state_path.
//
// Regression guard: RunIntent originally skipped stampStatePath, so the bugfix
// runstatus fixture's events all carried empty state_path and the trace UI's
// per-turn phase headers collapsed to the "—" fallback.
func TestStatePathNonEmpty_RunIntent_Accepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_runintent.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.RunIntent(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	hist := sink.History()
	require.NotEmpty(t, hist, "expected at least one event")
	for _, ev := range hist {
		require.NotEmpty(t, string(ev.StatePath),
			"RunIntent event kind=%q turn=%d seq=%d must have non-empty state_path",
			ev.Kind, ev.Turn, ev.Seq)
	}

	// The state-transition pair must still carry the correct per-event states.
	var exited, entered *store.Event
	for i := range hist {
		switch hist[i].Kind {
		case store.StateExited:
			ev := hist[i]
			exited = &ev
		case store.StateEntered:
			ev := hist[i]
			entered = &ev
		}
	}
	require.NotNil(t, exited, "expected a machine.state_exited event")
	require.NotNil(t, entered, "expected a machine.state_entered event")
	require.Equal(t, app.StatePath("foyer"), exited.StatePath,
		"state_exited must carry the FROM state (foyer)")
	require.Equal(t, app.StatePath("cloakroom"), entered.StatePath,
		"state_entered must carry the TO state (cloakroom)")
}

// TestRunIntent_EmitsTurnInputAndIntentAccepted is the regression guard for
// trace fidelity on the flow path: a flow/RunIntent-driven turn must emit
// turn.input (UserInputReceived) AND machine.intent_accepted, like a live
// session does, so flow-driven traces match live ones. Before the fix RunIntent
// emitted neither — the timeline showed transitions happening unprompted.
func TestRunIntent_EmitsTurnInputAndIntentAccepted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_intent_events.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.RunIntent(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	hist := sink.History()

	var input, accepted *store.Event
	for i := range hist {
		switch hist[i].Kind {
		case store.UserInputReceived:
			ev := hist[i]
			input = &ev
		case store.IntentAccepted:
			ev := hist[i]
			accepted = &ev
		}
	}

	require.NotNil(t, input, "RunIntent must emit a turn.input (UserInputReceived) event")
	require.NotNil(t, accepted, "RunIntent must emit a machine.intent_accepted event")

	// turn.input carries the unified {input, intent} payload.
	var inputPayload map[string]any
	require.NoError(t, json.Unmarshal(input.Payload, &inputPayload))
	require.Equal(t, "go", inputPayload["intent"],
		"turn.input payload must name the intent that drove the turn")

	// machine.intent_accepted records WHAT advanced the turn.
	var acceptedPayload map[string]any
	require.NoError(t, json.Unmarshal(accepted.Payload, &acceptedPayload))
	require.Equal(t, "go", acceptedPayload["intent"],
		"machine.intent_accepted payload must name the accepted intent")

	// Both must carry a turn and a non-empty state_path like every other event.
	require.Equal(t, app.TurnNumber(1), input.Turn)
	require.Equal(t, app.TurnNumber(1), accepted.Turn)
	require.NotEmpty(t, string(input.StatePath))
	require.NotEmpty(t, string(accepted.StatePath))
}

// TestStatePathNonEmpty_RunIntent_Rejected verifies every event written during
// a rejected RunIntent turn (INTENT_NOT_ALLOWED) has a non-empty state_path.
func TestStatePathNonEmpty_RunIntent_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_runintent_reject.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.RunIntent(ctx, sid, "nonexistent_intent_xyz", nil)
	require.NoError(t, err, "rejection must be returned as an outcome, not an error")
	require.Equal(t, orchestrator.ModeRejected, out.Mode)

	hist := sink.History()
	require.NotEmpty(t, hist, "rejection must produce at least one event")
	for _, ev := range hist {
		require.NotEmpty(t, string(ev.StatePath),
			"RunIntent rejection event kind=%q turn=%d seq=%d must have non-empty state_path",
			ev.Kind, ev.Turn, ev.Seq)
	}
	for _, ev := range out.Events {
		require.NotEmpty(t, string(ev.StatePath),
			"RunIntent rejection outcome event kind=%q must have non-empty state_path", ev.Kind)
	}
}

// TestStatePathPerEvent_FoyerToCloakroom asserts the G5 fix: machine.state_exited
// carries the FROM state ("foyer") and machine.state_entered carries the TO state
// ("cloakroom") — not the uniform FROM-state that stampStatePath would assign without
// the per-event pre-stamp.
func TestStatePathPerEvent_FoyerToCloakroom(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_g5.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// foyer → go west → cloakroom
	out, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	hist := sink.History()
	require.NotEmpty(t, hist)

	var exited, entered *store.Event
	for i := range hist {
		switch hist[i].Kind {
		case store.StateExited:
			ev := hist[i]
			exited = &ev
		case store.StateEntered:
			ev := hist[i]
			entered = &ev
		}
	}

	require.NotNil(t, exited, "expected a machine.state_exited event")
	require.NotNil(t, entered, "expected a machine.state_entered event")

	require.Equal(t, app.StatePath("foyer"), exited.StatePath,
		"machine.state_exited must carry the FROM state (foyer), not the TO state")
	require.Equal(t, app.StatePath("cloakroom"), entered.StatePath,
		"machine.state_entered must carry the TO state (cloakroom), not the FROM state (foyer)")
}

// TestTsNonZero_AfterTurn verifies that every event written during a turn
// has a non-zero, post-2020 timestamp (finding 2.3 regression guard).
func TestTsNonZero_AfterTurn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace_ts.jsonl")

	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)

	hist := sink.History()
	require.NotEmpty(t, hist)
	cutoff := mustParseTime("2020-01-01T00:00:00Z")
	for _, ev := range hist {
		require.False(t, ev.Ts.IsZero(),
			"event kind=%q turn=%d seq=%d must have non-zero ts (finding 2.3)", ev.Kind, ev.Turn, ev.Seq)
		require.True(t, ev.Ts.After(cutoff),
			"event kind=%q ts=%v must be after 2020-01-01 (finding 2.3)", ev.Kind, ev.Ts)
	}
}
