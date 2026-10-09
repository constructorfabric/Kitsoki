package server

import (
	"context"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// routingFeedbackDriver is a captureDriver that records the
// RecordRoutingFeedback arguments, so a white-box test can prove the
// runstatus.session.routing_feedback RPC calls through to the SAME driver
// method the TUI's `/route up|down` command drives (WS-C C4 web surface).
type routingFeedbackDriver struct {
	captureDriver
	called      bool
	lastState   string
	lastIntent  string
	lastPhrase  string
	lastTier    string
	lastVerdict orchestrator.RoutingFeedbackVerdict
	err         error
}

func (d *routingFeedbackDriver) RecordRoutingFeedback(_ context.Context, statePath, intent, phrase, tier string, verdict orchestrator.RoutingFeedbackVerdict) error {
	d.called = true
	d.lastState, d.lastIntent, d.lastPhrase, d.lastTier, d.lastVerdict = statePath, intent, phrase, tier, verdict
	return d.err
}

func newRoutingFeedbackServer(d Driver) *Server {
	return New("", &app.AppDef{}, WithDriver(d))
}

// routing_feedback calls through to the Driver with the state/intent/phrase/
// tier/verdict recovered client-side from the turn's trace events, and
// returns ok:true (no TurnOutcome — this never advances the turn).
func TestRoutingFeedbackRPC_CallsThrough(t *testing.T) {
	drv := &routingFeedbackDriver{}
	s := newRoutingFeedbackServer(drv)
	out, rerr := s.dispatch(context.Background(), "runstatus.session.routing_feedback", map[string]any{
		"session_id": "x",
		"state":      "hub",
		"intent":     "go_bugfix",
		"phrase":     "fix the crash on login",
		"tier":       "semantic",
		"verdict":    "down",
	})
	if rerr != nil {
		t.Fatalf("routing_feedback error: %+v", rerr)
	}
	if !drv.called {
		t.Fatalf("driver RecordRoutingFeedback was not called")
	}
	if drv.lastState != "hub" || drv.lastIntent != "go_bugfix" || drv.lastPhrase != "fix the crash on login" ||
		drv.lastTier != "semantic" || drv.lastVerdict != orchestrator.RoutingFeedbackDown {
		t.Fatalf("driver driven with wrong args: %+v", drv)
	}
	m, ok := out.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("expected ok:true, got %+v", out)
	}
}

// An unrecognised (or missing) verdict is a malformed request, not a driver call.
func TestRoutingFeedbackRPC_BadVerdict(t *testing.T) {
	drv := &routingFeedbackDriver{}
	s := newRoutingFeedbackServer(drv)
	if _, rerr := s.dispatch(context.Background(), "runstatus.session.routing_feedback", map[string]any{
		"session_id": "x",
		"state":      "hub",
		"intent":     "go_bugfix",
		"verdict":    "sideways",
	}); rerr == nil {
		t.Fatalf("bad verdict should error")
	}
	if drv.called {
		t.Fatalf("driver should not be called on a malformed request")
	}
}

// A read-only surface (no Driver) reports codeReadOnly rather than nil-derefing.
func TestRoutingFeedbackRPC_ReadOnly(t *testing.T) {
	s := New("", &app.AppDef{}) // no WithDriver
	_, rerr := s.dispatch(context.Background(), "runstatus.session.routing_feedback", map[string]any{
		"session_id": "x",
		"state":      "hub",
		"intent":     "go_bugfix",
		"verdict":    "up",
	})
	if rerr == nil || rerr.Code != codeReadOnly {
		t.Fatalf("expected codeReadOnly on a read-only surface, got %+v", rerr)
	}
}
