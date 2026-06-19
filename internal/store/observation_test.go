package store_test

// observation_test.go — drift guard for the ObservationKind taxonomy.
//
// Two goals:
//  1. Every declared EventKind constant maps to a non-empty, valid Kind.
//  2. The set of tested constants stays in sync with event.go: if a new
//     EventKind is added without a corresponding case in observation.go, the
//     default branch silently returns KindLifecycle — which is intentional for
//     forward compat, but any *explicit* constant should appear in the table.

import (
	"testing"

	"kitsoki/internal/store"
)

func TestObservationKind(t *testing.T) {
	t.Parallel()

	validKinds := map[store.Kind]bool{
		store.KindDecision:      true,
		store.KindRouting:       true,
		store.KindOracleCall:    true,
		store.KindHostCall:      true,
		store.KindNarration:     true,
		store.KindWorldMutation: true,
		store.KindLifecycle:     true,
	}

	tests := []struct {
		eventKind store.EventKind
		wantKind  store.Kind
	}{
		// decision
		{store.GateDecided, store.KindDecision},
		{store.WriteModeGranted, store.KindDecision},
		{store.OffPathQuestion, store.KindDecision},
		{store.OffPathAnswer, store.KindDecision},

		// routing
		{store.TurnStarted, store.KindRouting},
		{store.IntentAccepted, store.KindRouting},

		// oracle-call
		{store.OracleCalled, store.KindOracleCall},
		{store.OracleReturned, store.KindOracleCall},
		{store.OracleError, store.KindOracleCall},
		{store.LLMToolCall, store.KindOracleCall},

		// host-call
		{store.HostInvoked, store.KindHostCall},
		{store.HostDispatched, store.KindHostCall},
		{store.HostReturned, store.KindHostCall},
		{store.HarnessError, store.KindHostCall},

		// narration
		{store.MachineSay, store.KindNarration},
		{store.TurnEnded, store.KindNarration},

		// world-mutation
		{store.EffectApplied, store.KindWorldMutation},

		// lifecycle — structural / bookkeeping
		{store.ValidationFailed, store.KindLifecycle},
		{store.TransitionApplied, store.KindLifecycle},
		{store.StateExited, store.KindLifecycle},
		{store.StateEntered, store.KindLifecycle},
		{store.GuardRejected, store.KindLifecycle},
		{store.JobSubmitted, store.KindLifecycle},
		{store.JobCompleted, store.KindLifecycle},
		{store.TimeoutFired, store.KindLifecycle},
		{store.MachineError, store.KindLifecycle},
		{store.OffPathEntered, store.KindLifecycle},
		{store.OffPathExited, store.KindLifecycle},
		{store.IDEContextCaptured, store.KindLifecycle},
		{store.StorySnapshot, store.KindLifecycle},
		{store.StoryChanged, store.KindLifecycle},
		{store.UserInputReceived, store.KindLifecycle},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.eventKind), func(t *testing.T) {
			t.Parallel()
			got := store.ObservationKind(tc.eventKind)
			if got == "" {
				t.Errorf("ObservationKind(%q) returned empty string", tc.eventKind)
			}
			if !validKinds[got] {
				t.Errorf("ObservationKind(%q) = %q, not a valid Kind", tc.eventKind, got)
			}
			if got != tc.wantKind {
				t.Errorf("ObservationKind(%q) = %q, want %q", tc.eventKind, got, tc.wantKind)
			}
		})
	}
}

// TestObservationKindUnknown verifies that an unrecognised EventKind string
// falls back to KindLifecycle and never returns empty.
func TestObservationKindUnknown(t *testing.T) {
	t.Parallel()
	got := store.ObservationKind(store.EventKind("some.future.event"))
	if got != store.KindLifecycle {
		t.Errorf("unknown EventKind: got %q, want %q", got, store.KindLifecycle)
	}
}
