package studio

import (
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

func TestExplain_RouteSurpriseFromExplicitEvidence(t *testing.T) {
	history := store.History{{Turn: 1, Seq: 0, Kind: store.UserInputReceived, StatePath: app.StatePath("landing"), Payload: []byte(`{"intent":"workbench"}`)}}
	got := explainHistory(history, ExplainArgs{ExpectedRoute: "bugfix"}, "", time.Unix(0, 0))
	if len(got.Findings) != 1 || got.Findings[0].Class != "route_surprise" {
		t.Fatalf("findings = %#v", got.Findings)
	}
	if got.Findings[0].NextCall.Tool == "" || got.Findings[0].Evidence[0].Detail != "expected=bugfix, observed=workbench" {
		t.Fatalf("route finding = %#v", got.Findings[0])
	}
}

func TestExplain_RecordedErrorThenTransitionDoesNotClaimCause(t *testing.T) {
	history := store.History{
		{Turn: 4, Seq: 1, Kind: store.HarnessError, StatePath: app.StatePath("reproducing"), Payload: []byte(`{"error":"fixture host failed"}`)},
		{Turn: 4, Seq: 2, Kind: store.TransitionApplied, StatePath: app.StatePath("fallback"), Payload: []byte(`{"to":"fallback"}`)},
	}
	got := explainHistory(history, ExplainArgs{}, "/tmp/replay.jsonl", time.Unix(0, 0))
	if len(got.Findings) != 1 || got.Findings[0].Class != "recorded_error_then_transition" {
		t.Fatalf("findings = %#v", got.Findings)
	}
	if got.Findings[0].Evidence[0].Detail != "fixture host failed" || got.Findings[0].NextCall.Tool != "trace.read" {
		t.Fatalf("error finding = %#v", got.Findings[0])
	}
}

func TestExplain_StalledTurnIsBoundedAndTimeEvidence(t *testing.T) {
	start := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	history := store.History{{Turn: 7, Seq: 0, Kind: store.TurnStarted, StatePath: app.StatePath("implementing"), Ts: start, Payload: []byte(`{"input":"continue"}`)}}
	got := explainHistory(history, ExplainArgs{StallAfterMS: 1000, ObservedAtUnixMic: start.Add(2 * time.Second).UnixMicro()}, "", time.Unix(0, 0))
	if len(got.Findings) != 1 || got.Findings[0].Class != "stalled_or_unfinished_turn" {
		t.Fatalf("findings = %#v", got.Findings)
	}
	if got.Findings[0].NextCall.Tool != "session.status" {
		t.Fatalf("stall finding = %#v", got.Findings[0])
	}
}

func TestExplain_DoesNotCallAnIncompleteFreshTurnStalled(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	history := store.History{{Turn: 7, Seq: 0, Kind: store.TurnStarted, Ts: now}}
	got := explainHistory(history, ExplainArgs{StallAfterMS: 1000, ObservedAtUnixMic: now.Add(500 * time.Millisecond).UnixMicro()}, "", now)
	if len(got.Findings) != 0 {
		t.Fatalf("fresh incomplete turn must not be called stalled: %#v", got.Findings)
	}
}
