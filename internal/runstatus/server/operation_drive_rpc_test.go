package server

import (
	"context"
	"fmt"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

type operationDriveDriver struct {
	captureDriver
	called           bool
	backgroundCalled bool
	turn             *orchestrator.TurnOutcome
	final            *orchestrator.TurnOutcome
	backgroundFinal  *orchestrator.TurnOutcome
	view             *orchestrator.TurnOutcome
	err              error
}

func (d *operationDriveDriver) Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error) {
	if d.turn != nil {
		return d.turn, nil
	}
	return d.captureDriver.Turn(ctx, input)
}

func (d *operationDriveDriver) DriveOperation(context.Context) (*orchestrator.OperationDriveOutcome, error) {
	d.called = true
	if d.err != nil {
		return nil, d.err
	}
	return &orchestrator.OperationDriveOutcome{Turns: 1, Final: d.final, StopReason: "operation-completed"}, nil
}

func (d *operationDriveDriver) DriveBackgroundOperation(context.Context) (*orchestrator.OperationDriveOutcome, error) {
	d.backgroundCalled = true
	if d.err != nil {
		return nil, d.err
	}
	return &orchestrator.OperationDriveOutcome{Turns: 2, Final: d.backgroundFinal, StopReason: "operation-completed", LastIntent: "accept"}, nil
}

func (d *operationDriveDriver) View(ctx context.Context) (*orchestrator.TurnOutcome, error) {
	if d.view != nil {
		return d.view, nil
	}
	return d.captureDriver.View(ctx)
}

func TestDriveOperationRPC_CallsThrough(t *testing.T) {
	drv := &operationDriveDriver{
		final: &orchestrator.TurnOutcome{
			Mode:     orchestrator.ModeTransitioned,
			NewState: app.StatePath("__exit__done"),
		},
	}
	s := New("", &app.AppDef{}, WithDriver(drv))
	out, rerr := s.dispatch(context.Background(), "runstatus.session.drive_operation", map[string]any{"session_id": "x"})
	if rerr != nil {
		t.Fatalf("drive_operation error: %+v", rerr)
	}
	if !drv.called {
		t.Fatalf("driver DriveOperation was not called")
	}
	tr := out.(turnResult)
	if tr.Mode != "transitioned" || tr.State != "__exit__done" {
		t.Fatalf("drive_operation returned wrong turn result: %+v", tr)
	}
	if tr.OperationDrive == nil || tr.OperationDrive.Turns != 1 || tr.OperationDrive.StopReason != "operation-completed" {
		t.Fatalf("drive_operation did not include operation summary: %+v", tr.OperationDrive)
	}
}

func TestTurnRPC_AutoDrivesBackgroundOperation(t *testing.T) {
	drv := &operationDriveDriver{
		turn: &orchestrator.TurnOutcome{
			Mode:     orchestrator.ModeTransitioned,
			NewState: app.StatePath("work"),
		},
		backgroundFinal: &orchestrator.TurnOutcome{
			Mode:     orchestrator.ModeCompleted,
			NewState: app.StatePath("__exit__done"),
		},
	}
	s := New("", &app.AppDef{}, WithDriver(drv))
	out, rerr := s.dispatch(context.Background(), "runstatus.session.turn", map[string]any{"session_id": "x", "input": "start"})
	if rerr != nil {
		t.Fatalf("turn error: %+v", rerr)
	}
	if !drv.backgroundCalled {
		t.Fatalf("driver DriveBackgroundOperation was not called")
	}
	if drv.called {
		t.Fatalf("turn RPC must not use explicit DriveOperation")
	}
	tr := out.(turnResult)
	if tr.Mode != "completed" || tr.State != "__exit__done" {
		t.Fatalf("turn returned wrong auto-driven result: %+v", tr)
	}
	if tr.OperationDrive == nil || tr.OperationDrive.Turns != 2 || tr.OperationDrive.LastIntent != "accept" {
		t.Fatalf("turn did not include background operation summary: %+v", tr.OperationDrive)
	}
}

func TestDriveOperationRPC_FallsBackToViewWhenNoTurnDriven(t *testing.T) {
	drv := &operationDriveDriver{
		view: &orchestrator.TurnOutcome{
			Mode:     orchestrator.ModeOffPath,
			NewState: app.StatePath("checkpoint"),
		},
	}
	s := New("", &app.AppDef{}, WithDriver(drv))
	out, rerr := s.dispatch(context.Background(), "runstatus.session.drive_operation", map[string]any{"session_id": "x"})
	if rerr != nil {
		t.Fatalf("drive_operation error: %+v", rerr)
	}
	if !drv.called {
		t.Fatalf("driver DriveOperation was not called")
	}
	tr := out.(turnResult)
	if tr.Mode != "offpath" || tr.State != "checkpoint" {
		t.Fatalf("fallback view returned wrong turn result: %+v", tr)
	}
	if tr.OperationDrive == nil || tr.OperationDrive.Turns != 1 || tr.OperationDrive.StopReason != "operation-completed" {
		t.Fatalf("fallback view did not include operation summary: %+v", tr.OperationDrive)
	}
}

func TestDriveOperationRPC_Errors(t *testing.T) {
	t.Run("read only", func(t *testing.T) {
		s := New("", &app.AppDef{})
		_, rerr := s.dispatch(context.Background(), "runstatus.session.drive_operation", map[string]any{"session_id": "x"})
		if rerr == nil || rerr.Code != codeReadOnly {
			t.Fatalf("expected read-only error, got %+v", rerr)
		}
	})

	t.Run("driver unavailable", func(t *testing.T) {
		s := New("", &app.AppDef{}, WithDriver(&captureDriver{}))
		_, rerr := s.dispatch(context.Background(), "runstatus.session.drive_operation", map[string]any{"session_id": "x"})
		if rerr == nil || rerr.Code != codeServerError {
			t.Fatalf("expected server error, got %+v", rerr)
		}
	})

	t.Run("driver failure", func(t *testing.T) {
		s := New("", &app.AppDef{}, WithDriver(&operationDriveDriver{err: fmt.Errorf("drive failed")}))
		_, rerr := s.dispatch(context.Background(), "runstatus.session.drive_operation", map[string]any{"session_id": "x"})
		if rerr == nil || rerr.Code != codeServerError {
			t.Fatalf("expected server error, got %+v", rerr)
		}
	})
}
