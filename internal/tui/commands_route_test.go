package tui

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// TestRouteCommandNoRoutedTurnYet: before any turn has resolved this
// session, /route has nothing to attach feedback to.
func TestRouteCommandNoRoutedTurnYet(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-route-1"), "../../testdata/apps/cloak/app.yaml", "")

	body, _, cmd := RouteCommand{}.Run(m, []string{"up"})
	if cmd != nil {
		t.Fatal("route command should be synchronous")
	}
	if !strings.Contains(body, "no routed turn yet") {
		t.Fatalf("expected 'no routed turn yet' message, got %q", body)
	}
}

// TestRouteCommandUsage: an unrecognised/missing verb prints usage rather
// than silently doing nothing or guessing.
func TestRouteCommandUsage(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-route-2"), "../../testdata/apps/cloak/app.yaml", "")

	for _, args := range [][]string{nil, {"sideways"}} {
		body, _, cmd := RouteCommand{}.Run(m, args)
		if cmd != nil {
			t.Fatal("route command should be synchronous")
		}
		if !strings.Contains(body, "usage: /route up|down") {
			t.Fatalf("args=%v: expected usage message, got %q", args, body)
		}
	}
}

// TestRouteCommandRecordsAgainstSnapshot proves /route reads the durable
// lastRouted* snapshot (not the live, possibly-reset m.routing pipeline) and
// reports both up and down verdicts.
func TestRouteCommandRecordsAgainstSnapshot(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-route-3"), "../../testdata/apps/cloak/app.yaml", "")
	m.lastInput = "look around"
	m.lastRoutedIntent = "look"
	m.lastRoutedTier = "deterministic"
	m.lastRoutedState = app.StatePath("foyer")
	m.lastRoutedDecisionID = "session-route-3:1"

	body, _, cmd := RouteCommand{}.Run(m, []string{"up"})
	if cmd != nil {
		t.Fatal("route command should be synchronous")
	}
	if !strings.Contains(body, "👍") || !strings.Contains(body, "look") {
		t.Fatalf("expected an up-vote confirmation naming the intent, got %q", body)
	}

	body, _, cmd = RouteCommand{}.Run(m, []string{"down"})
	if cmd != nil {
		t.Fatal("route command should be synchronous")
	}
	if !strings.Contains(body, "👎") {
		t.Fatalf("expected a down-vote confirmation, got %q", body)
	}
}

// TestRouteCommandRerouteWiresAnAsyncRewind proves /route retry dispatches a
// reroute command for the stored decision id rather than just printing help.
func TestRouteCommandRerouteWiresAnAsyncRewind(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-route-5"), "../../testdata/apps/cloak/app.yaml", "")
	m.lastInput = "look around"
	m.lastRoutedDecisionID = "session-route-5:1"

	body, _, cmd := RouteCommand{}.Run(m, []string{"retry", "meta"})
	if cmd == nil {
		t.Fatal("expected reroute command to return an async tea.Cmd")
	}
	if !strings.Contains(body, "rerouting") {
		t.Fatalf("expected reroute confirmation, got %q", body)
	}
}

// TestSnapshotRoutedTurnNoop confirms an unresolved pipeline leaves the
// lastRouted* fields untouched (a rejected/cancelled turn must not clobber
// the previous turn's snapshot with empty values).
func TestSnapshotRoutedTurnNoop(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("session-route-4"), "../../testdata/apps/cloak/app.yaml", "")
	m.lastRoutedIntent = "look"
	m.lastRoutedTier = "deterministic"
	m.lastRoutedState = app.StatePath("foyer")

	m.routing = newRoutingPipeline() // fresh, unresolved (winner == -1)
	m.snapshotRoutedTurn("")

	if m.lastRoutedIntent != "look" {
		t.Fatalf("unresolved pipeline must not clobber snapshot, got intent=%q", m.lastRoutedIntent)
	}
}
