package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// rewindDriver is a captureDriver that records the RewindRoute arguments and can
// be configured to return an error, so a white-box test can prove the
// runstatus.session.rewind_route RPC calls through to the Driver and that an
// engine error (e.g. the intent-class not-implemented gap) surfaces cleanly.
type rewindDriver struct {
	captureDriver
	lastDecisionID string
	lastClass      orchestrator.ContextRouteClass
	lastReason     string
	called         bool
	err            error
}

func (d *rewindDriver) RewindRoute(_ context.Context, decisionID string, newClass orchestrator.ContextRouteClass, reason string, _ string) (*orchestrator.TurnOutcome, error) {
	d.called = true
	d.lastDecisionID, d.lastClass, d.lastReason = decisionID, newClass, reason
	if d.err != nil {
		return nil, d.err
	}
	// Echo a re-dispatched turn so newTurnResult has a non-nil outcome to flatten.
	return &orchestrator.TurnOutcome{
		Mode:     orchestrator.ModeOffPath,
		NewState: app.StatePath("idle"),
		ContextRoute: &orchestrator.ContextRouteReceipt{
			Class:      string(newClass),
			DecisionID: decisionID,
		},
	}, nil
}

func newRewindServer(d Driver) *Server {
	return New("", &app.AppDef{}, WithDriver(d))
}

// rewind_route calls through to the Driver with the decision id / class / reason
// and returns the re-dispatched turn (carrying the rewound decision id).
func TestRewindRouteRPC_CallsThrough(t *testing.T) {
	drv := &rewindDriver{}
	s := newRewindServer(drv)
	out, rerr := s.dispatch(context.Background(), "runstatus.session.rewind_route", map[string]any{
		"session_id":  "x",
		"decision_id": "sess-1:3",
		"new_class":   "help",
		"reason":      "operator redirect",
	})
	if rerr != nil {
		t.Fatalf("rewind_route error: %+v", rerr)
	}
	if !drv.called {
		t.Fatalf("driver RewindRoute was not called")
	}
	if drv.lastDecisionID != "sess-1:3" || drv.lastClass != orchestrator.ClassHelp || drv.lastReason != "operator redirect" {
		t.Fatalf("driver driven with wrong args: id=%q class=%q reason=%q",
			drv.lastDecisionID, drv.lastClass, drv.lastReason)
	}
	tr := out.(turnResult)
	if tr.ContextRoute == nil || tr.ContextRoute.DecisionID != "sess-1:3" {
		t.Fatalf("re-dispatched turn missing rewound decision id: %+v", tr.ContextRoute)
	}
}

// A missing decision_id is a malformed request, not a driver call.
func TestRewindRouteRPC_MissingDecisionID(t *testing.T) {
	drv := &rewindDriver{}
	s := newRewindServer(drv)
	if _, rerr := s.dispatch(context.Background(), "runstatus.session.rewind_route",
		map[string]any{"session_id": "x"}); rerr == nil {
		t.Fatalf("missing decision_id should error")
	}
	if drv.called {
		t.Fatalf("driver should not be called on a malformed request")
	}
}

// An intent-class rewind hits the engine's not-implemented gap; it must surface
// as a sanitized user-facing RPC error (no orchestrator wrapper prefix), not a
// raw 500 with internals.
func TestRewindRouteRPC_IntentNotImplemented(t *testing.T) {
	drv := &rewindDriver{
		err: fmt.Errorf("orchestrator: RewindRoute: class=intent rewind requires IntentAccepted recovery; not yet implemented"),
	}
	s := newRewindServer(drv)
	_, rerr := s.dispatch(context.Background(), "runstatus.session.rewind_route", map[string]any{
		"session_id":  "x",
		"decision_id": "sess-1:3",
		"new_class":   "intent",
	})
	if rerr == nil {
		t.Fatalf("intent-class rewind should surface an error")
	}
	if rerr.Code != codeServerError {
		t.Fatalf("expected codeServerError, got %d", rerr.Code)
	}
	// userfacing.Error strips the "orchestrator: RewindRoute: " wrapper prefix.
	if strings.Contains(rerr.Message, "orchestrator:") || strings.Contains(rerr.Message, "RewindRoute:") {
		t.Fatalf("user-facing message leaked internals: %q", rerr.Message)
	}
	if !strings.Contains(rerr.Message, "not yet implemented") {
		t.Fatalf("user-facing message lost the actionable leaf: %q", rerr.Message)
	}
	// The raw chain is preserved in Data for logs/dev tools.
	if !strings.Contains(rerr.Data, "orchestrator: RewindRoute:") {
		t.Fatalf("raw chain not preserved in Data: %q", rerr.Data)
	}
}

// A read-only surface (no Driver) reports codeReadOnly rather than nil-derefing.
func TestRewindRouteRPC_ReadOnly(t *testing.T) {
	s := New("", &app.AppDef{}) // no WithDriver
	_, rerr := s.dispatch(context.Background(), "runstatus.session.rewind_route", map[string]any{
		"session_id":  "x",
		"decision_id": "sess-1:3",
	})
	if rerr == nil || rerr.Code != codeReadOnly {
		t.Fatalf("expected codeReadOnly on a read-only surface, got %+v", rerr)
	}
}
