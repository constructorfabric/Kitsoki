package kitsokipattern

import (
	"encoding/json"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

func TestAnalyze_DerivesRouteFeedbackAndWindowPattern(t *testing.T) {
	hist := store.History{
		ev(1, 0, store.TurnStarted, "review", map[string]any{
			"input": "this needs another pass", "routed_by": "semantic",
			"match_type": "synonym:accept", "intent": "accept",
		}),
		ev(1, 1, store.TransitionApplied, "review", map[string]any{
			"from": "review", "to": "done", "intent": "accept",
		}),
		ev(2, 0, store.EventKind("turn.route_feedback"), "done", map[string]any{
			"feedback": "bad_route",
			"original": map[string]any{
				"input": "this needs another pass", "state": "review",
				"routed_by": "semantic", "intent": "accept",
			},
			"correction": map[string]any{
				"mode": "switch_route", "intent": "refine",
			},
		}),
		ev(2, 1, store.TurnStarted, "review", map[string]any{
			"input": "this needs another pass", "routed_by": "default",
			"intent": "refine",
		}),
		ev(2, 2, store.TransitionApplied, "review", map[string]any{
			"from": "review", "to": "review", "intent": "refine",
		}),
	}

	report := Analyze(hist, Options{CaseID: "trace-a", MaxWindow: 3, TopK: 10})

	if report.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", report.SchemaVersion, SchemaVersion)
	}
	if len(report.RouteFeedback) != 1 {
		t.Fatalf("route feedback count = %d, want 1", len(report.RouteFeedback))
	}
	if len(report.DirectlyFollows) == 0 {
		t.Fatal("directly-follows graph is empty")
	}
	got := report.RouteFeedback[0]
	if got.Feedback != "bad_route" || got.Original["intent"] != "accept" || got.Correction["intent"] != "refine" {
		t.Fatalf("unexpected route feedback: %+v", got)
	}

	var found bool
	for _, p := range report.Patterns {
		if p.Kind == "route-feedback" &&
			p.Signature == "route(accept) -> feedback(bad_route) -> final(refine)" {
			found = true
			if p.Support != 1 {
				t.Fatalf("route-feedback support = %d, want 1", p.Support)
			}
			if len(p.Evidence) != 1 || len(p.Evidence[0]) == 0 {
				t.Fatalf("route-feedback evidence missing: %+v", p)
			}
		}
	}
	if !found {
		t.Fatalf("route-feedback pattern not found in %+v", report.Patterns)
	}
}

func TestAnalyze_CollapsesRepeatedCyclePath(t *testing.T) {
	hist := store.History{
		ev(1, 0, store.TransitionApplied, "idle", map[string]any{"from": "idle", "to": "design", "intent": "start"}),
		ev(2, 0, store.TransitionApplied, "design", map[string]any{"from": "design", "to": "review", "intent": "draft"}),
		ev(3, 0, store.TransitionApplied, "review", map[string]any{"from": "review", "to": "design", "intent": "refine"}),
		ev(4, 0, store.TransitionApplied, "design", map[string]any{"from": "design", "to": "publish", "intent": "accept"}),
	}

	report := Analyze(hist, Options{CaseID: "trace-loop"})
	if len(report.CyclePaths) != 1 {
		t.Fatalf("cycle paths = %d, want 1", len(report.CyclePaths))
	}
	if got := report.CyclePaths[0].Signature; got == "" || !strings.Contains(got, "SCC(") {
		t.Fatalf("cycle signature %q does not collapse an SCC", got)
	}
}

func ev(turn app.TurnNumber, seq int, kind store.EventKind, state string, payload map[string]any) store.Event {
	b, _ := json.Marshal(payload)
	return store.Event{Turn: turn, Seq: seq, Kind: kind, StatePath: app.StatePath(state), Payload: b}
}
