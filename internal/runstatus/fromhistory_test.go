package runstatus_test

// TestFromHistory_PassThrough verifies that FromHistory is a pure pass-through:
// every store.Event in the history maps 1:1 to a TraceEvent in Snapshot.Events,
// no synthesis, no back-fill, no agent-specific code path.
//
// Covers all EventKind values including AgentCalled/AgentReturned/AgentError
// (the wave 3-agent events) to prove they emerge verbatim and the function
// does not synthesise extra events that weren't in the input.
//
// Runtime budget: <5 ms (no I/O, no LLM calls, in-memory only).

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

// buildMinimalAppDef returns the simplest AppDef that FromHistory accepts
// (Compile + FlowchartWithMap must not error).
func buildMinimalAppDef() *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{
			ID:      "test-app",
			Version: "0.0.1",
		},
	}
}

func TestFromHistory_PassThrough(t *testing.T) {
	t.Parallel()

	def := buildMinimalAppDef()

	base := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)

	// Construct a synthetic History that covers:
	//   - a normal turn (TurnStarted, StateEntered, TurnEnded)
	//   - an agent exchange (AgentCalled, AgentReturned)
	//   - an error event (HarnessError — must produce level ERROR)
	//   - an AgentError event
	calledPayload, err := json.Marshal(map[string]any{
		"verb":   "ask",
		"agent":  "my-agent",
		"model":  "claude-sonnet",
		"prompt": "What is the answer?",
	})
	require.NoError(t, err)

	returnedPayload, err := json.Marshal(map[string]any{
		"verb":        "ask",
		"duration_ms": float64(120),
		"response":    "42",
	})
	require.NoError(t, err)

	errorPayload, err := json.Marshal(map[string]any{"error": "something went wrong"})
	require.NoError(t, err)

	hist := store.History{
		{Turn: 1, Ts: base.Add(0), Kind: store.TurnStarted, StatePath: "foyer", Seq: 0},
		{Turn: 1, Ts: base.Add(1 * time.Millisecond), Kind: store.StateEntered, StatePath: "foyer", Seq: 1,
			Payload: json.RawMessage(`{"state":"foyer"}`)},
		{Turn: 1, Ts: base.Add(2 * time.Millisecond), Kind: store.AgentCalled, StatePath: "foyer", Seq: 2,
			CallID: "abc123def456abcd", Payload: calledPayload},
		{Turn: 1, Ts: base.Add(3 * time.Millisecond), Kind: store.AgentReturned, StatePath: "foyer", Seq: 3,
			CallID: "abc123def456abcd", Payload: returnedPayload},
		{Turn: 1, Ts: base.Add(4 * time.Millisecond), Kind: store.HarnessError, StatePath: "foyer", Seq: 4,
			Payload: errorPayload},
		{Turn: 1, Ts: base.Add(5 * time.Millisecond), Kind: store.AgentError, StatePath: "foyer", Seq: 5,
			CallID: "abc123def456abcd", Payload: errorPayload},
		{Turn: 1, Ts: base.Add(6 * time.Millisecond), Kind: store.TurnEnded, StatePath: "foyer", Seq: 6},
	}

	snap, err := runstatus.FromHistory(hist, def, "sess-001")
	require.NoError(t, err)

	// Length must match exactly — no synthesised extra events.
	assert.Equal(t, len(hist), len(snap.Events),
		"Snapshot.Events length must equal History length (no synthesis, no injection)")

	// Verify each event maps 1:1.
	for i, ev := range snap.Events {
		orig := hist[i]
		assert.Equal(t, string(orig.Kind), ev.Msg,
			"events[%d].Msg must equal Kind string", i)
		assert.Equal(t, int(orig.Turn), ev.Turn,
			"events[%d].Turn", i)
		assert.Equal(t, string(orig.StatePath), ev.StatePath,
			"events[%d].StatePath", i)
		assert.Equal(t, int(orig.ParentTurn), ev.ParentTurn,
			"events[%d].ParentTurn", i)
		assert.True(t, orig.Ts.Equal(ev.Time),
			"events[%d].Time: want %v got %v", i, orig.Ts, ev.Time)
	}

	// Level mapping: HarnessError must be ERROR; all others INFO.
	for i, ev := range snap.Events {
		orig := hist[i]
		switch orig.Kind {
		case store.HarnessError, store.ValidationFailed, store.GuardRejected:
			assert.Equal(t, "ERROR", ev.Level, "events[%d] (%s) must be ERROR", i, orig.Kind)
		default:
			assert.Equal(t, "INFO", ev.Level, "events[%d] (%s) must be INFO", i, orig.Kind)
		}
	}

	// call_id must be surfaced in Attrs for agent events.
	agentCalledIdx := 2
	assert.Equal(t, "abc123def456abcd", snap.Events[agentCalledIdx].Attrs["call_id"],
		"AgentCalled.Attrs[call_id] must carry CallID from store.Event")
	agentReturnedIdx := 3
	assert.Equal(t, "abc123def456abcd", snap.Events[agentReturnedIdx].Attrs["call_id"],
		"AgentReturned.Attrs[call_id] must carry CallID from store.Event")

	// Session header fields.
	assert.Equal(t, "sess-001", snap.Session.SessionID)
	assert.Equal(t, "test-app", snap.Session.AppID)
	assert.Equal(t, "foyer", snap.Session.CurrentState)
	assert.Equal(t, 1, snap.Session.Turn)
	assert.True(t, base.Equal(snap.Session.StartedAt),
		"StartedAt must be the timestamp of the first event")

	// Empty history must not panic.
	snapEmpty, errEmpty := runstatus.FromHistory(store.History{}, def, "sess-empty")
	require.NoError(t, errEmpty, "empty history must not error")
	assert.Equal(t, 0, len(snapEmpty.Events))
	assert.Equal(t, "", snapEmpty.Session.CurrentState)
}

// TestFromHistory_CurrentStatePrefersStateEntered locks the current-state
// derivation: turn.end is stamped with the turn's STARTING state, so after a
// transition (state_entered carries the NEW state, turn.end the OLD one) the
// header must report the entered state, not whatever the last event happened to
// carry. Regression for the live web UI showing the pre-transition state.
func TestFromHistory_CurrentStatePrefersStateEntered(t *testing.T) {
	t.Parallel()

	def := buildMinimalAppDef()
	base := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)

	// A foyer --go--> bar.dark transition turn, mirroring the real event
	// stamping: state_entered carries bar/bar.dark, turn.end carries foyer.
	hist := store.History{
		{Turn: 1, Ts: base.Add(0), Kind: store.TurnStarted, StatePath: "foyer", Seq: 0},
		{Turn: 1, Ts: base.Add(1 * time.Millisecond), Kind: store.StateExited, StatePath: "foyer", Seq: 1,
			Payload: json.RawMessage(`{"state":"foyer"}`)},
		{Turn: 1, Ts: base.Add(2 * time.Millisecond), Kind: store.StateEntered, StatePath: "bar", Seq: 2,
			Payload: json.RawMessage(`{"state":"bar"}`)},
		{Turn: 1, Ts: base.Add(3 * time.Millisecond), Kind: store.StateEntered, StatePath: "bar.dark", Seq: 3,
			Payload: json.RawMessage(`{"state":"bar.dark"}`)},
		{Turn: 1, Ts: base.Add(4 * time.Millisecond), Kind: store.TurnEnded, StatePath: "foyer", Seq: 4},
	}

	snap, err := runstatus.FromHistory(hist, def, "sess-trans")
	require.NoError(t, err)
	assert.Equal(t, "bar.dark", snap.Session.CurrentState,
		"the last state_entered must win over turn.end's starting-state stamp")
}

func TestFromHistory_DerivesOperationRunSummary(t *testing.T) {
	t.Parallel()

	def := buildMinimalAppDef()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	runStarted, err := json.Marshal(map[string]any{
		"operation_id":      "bugfix_full",
		"policy_id":         "bugfix_full",
		"title":             "Fix bug",
		"status":            "running",
		"mode":              "autonomous",
		"execution_mode":    "one-shot",
		"run_in_background": true,
		"from":              "idle",
		"to":                "reproducing",
		"entry_intent":      "start",
	})
	require.NoError(t, err)
	runWaiting, err := json.Marshal(map[string]any{
		"operation_id":             "bugfix_full",
		"policy_id":                "bugfix_full",
		"status":                   "waiting",
		"terminal_state":           "__exit__needs-human",
		"terminal_artifact":        "done_artifact",
		"terminal_artifact_handle": "done#abc123",
		"stop_reason":              "needs-human",
		"stop_detail":              "Regression gate was never RED.",
	})
	require.NoError(t, err)
	runPhase, err := json.Marshal(map[string]any{
		"operation_id": "bugfix_full",
		"policy_id":    "bugfix_full",
		"status":       "running",
		"phase":        "testing_artifact",
		"phase_status": "completed",
	})
	require.NoError(t, err)

	snap, err := runstatus.FromHistory(store.History{
		{Turn: 1, Ts: base, Kind: store.OperationRunStarted, StatePath: "idle", Seq: 1, Payload: runStarted},
		{Turn: 2, Ts: base.Add(time.Second), Kind: store.OperationRunPhaseCompleted, StatePath: "testing", Seq: 2, Payload: runPhase},
		{Turn: 3, Ts: base.Add(2 * time.Second), Kind: store.OperationRunWaiting, StatePath: "testing", Seq: 3, Payload: runWaiting},
	}, def, "sess-op")
	require.NoError(t, err)
	require.NotNil(t, snap.Session.OperationRun)
	assert.Equal(t, "bugfix_full", snap.Session.OperationRun.OperationID)
	assert.Equal(t, "Fix bug", snap.Session.OperationRun.Title)
	assert.Equal(t, "waiting", snap.Session.OperationRun.Status)
	assert.Equal(t, "idle", snap.Session.OperationRun.From)
	assert.Equal(t, "reproducing", snap.Session.OperationRun.To)
	assert.Equal(t, "testing_artifact", snap.Session.OperationRun.Phase)
	assert.Equal(t, "needs-human", snap.Session.OperationRun.StopReason)
	assert.Equal(t, "Regression gate was never RED.", snap.Session.OperationRun.StopDetail)
	assert.Equal(t, "__exit__needs-human", snap.Session.OperationRun.TerminalState)
	assert.Equal(t, "done_artifact", snap.Session.OperationRun.TerminalArtifact)
	assert.Equal(t, "done#abc123", snap.Session.OperationRun.TerminalArtifactHandle)
	assert.True(t, snap.Session.OperationRun.RunInBackground)
}

func TestSessionHeaderFromTrace_DerivesOperationRunSummary(t *testing.T) {
	t.Parallel()

	def := buildMinimalAppDef()
	header := runstatus.SessionHeaderFromTrace(def, []runstatus.TraceEvent{
		{
			Time:      time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC),
			Msg:       string(store.EffectApplied),
			SessionID: "sess-trace",
			Turn:      1,
			StatePath: "idle",
			Attrs: map[string]any{
				"set": map[string]any{
					app.OperationRunWorldKey: map[string]any{
						"operation_id":   "bugfix_full",
						"policy_id":      "bugfix_full",
						"title":          "Fix bug",
						"status":         "running",
						"from":           "idle",
						"to":             "reproducing",
						"entry_intent":   "start",
						"execution_mode": "one-shot",
					},
				},
			},
		},
		{
			Time:      time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
			Msg:       string(store.OperationRunCompleted),
			SessionID: "sess-trace",
			Turn:      2,
			StatePath: "__exit__direct-ship",
			Attrs: map[string]any{
				"operation_id":             "bugfix_full",
				"status":                   "completed",
				"terminal_state":           "__exit__direct-ship",
				"terminal_artifact":        "done_artifact",
				"terminal_artifact_handle": "done#abc123",
			},
		},
	}, runstatus.HeaderOverrides{})

	require.NotNil(t, header.OperationRun)
	assert.Equal(t, "bugfix_full", header.OperationRun.OperationID)
	assert.Equal(t, "Fix bug", header.OperationRun.Title)
	assert.Equal(t, "completed", header.OperationRun.Status)
	assert.Equal(t, "idle", header.OperationRun.From)
	assert.Equal(t, "reproducing", header.OperationRun.To)
	assert.Equal(t, "__exit__direct-ship", header.OperationRun.TerminalState)
	assert.Equal(t, "done_artifact", header.OperationRun.TerminalArtifact)
	assert.Equal(t, "done#abc123", header.OperationRun.TerminalArtifactHandle)
}

// TestFromHistory_AgentEventsPassThroughVerbatim confirms that when a History
// already contains AgentCalled/AgentReturned events, FromHistory does not
// inject any additional agent events. This is the core wave 4a contract: the
// JSONL is authoritative; no synthesis path exists.
func TestFromHistory_AgentEventsPassThroughVerbatim(t *testing.T) {
	t.Parallel()

	def := buildMinimalAppDef()
	base := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	calledPayload, err := json.Marshal(map[string]any{"verb": "decide", "prompt": "left or right?"})
	require.NoError(t, err)
	returnedPayload, err := json.Marshal(map[string]any{"verb": "decide", "response": "left"})
	require.NoError(t, err)

	hist := store.History{
		{Turn: 1, Ts: base, Kind: store.TurnStarted, StatePath: "start", Seq: 0},
		{Turn: 1, Ts: base.Add(1 * time.Millisecond), Kind: store.AgentCalled, StatePath: "start", Seq: 1,
			CallID: "callid-001", Payload: calledPayload},
		{Turn: 1, Ts: base.Add(2 * time.Millisecond), Kind: store.AgentReturned, StatePath: "start", Seq: 2,
			CallID: "callid-001", Payload: returnedPayload},
		{Turn: 1, Ts: base.Add(3 * time.Millisecond), Kind: store.TurnEnded, StatePath: "start", Seq: 3},
	}

	snap, err := runstatus.FromHistory(hist, def, "sess-agent")
	require.NoError(t, err)

	// Exactly 4 events — no synthesis of extra agent events.
	assert.Equal(t, 4, len(snap.Events),
		"must have exactly 4 events (no extra synthesised agent pairs)")

	// Kinds in order.
	wantMsgs := []string{
		string(store.TurnStarted),
		string(store.AgentCalled),
		string(store.AgentReturned),
		string(store.TurnEnded),
	}
	for i, want := range wantMsgs {
		assert.Equal(t, want, snap.Events[i].Msg, "events[%d].Msg", i)
	}
}
