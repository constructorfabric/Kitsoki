package server

import (
	"context"
	"fmt"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// harnessDriver is a captureDriver that also implements HarnessController, so a
// white-box test can drive the harness RPCs without a live orchestrator.
type harnessDriver struct {
	captureDriver
	profiles    []orchestrator.ProfileInfo
	sel         orchestrator.ProfileSelection
	setErr      error
	lastProfile string
	lastModel   string
	lastEffort  string
}

func (d *harnessDriver) HarnessProfiles() []orchestrator.ProfileInfo { return d.profiles }
func (d *harnessDriver) HarnessSelection() orchestrator.ProfileSelection {
	return d.sel
}
func (d *harnessDriver) SetHarnessSelection(profile, model, effort string) error {
	if d.setErr != nil {
		return d.setErr
	}
	d.lastProfile, d.lastModel, d.lastEffort = profile, model, effort
	d.sel = orchestrator.ProfileSelection{Profile: profile, Model: model, Effort: effort}
	return nil
}

func newHarnessServer(d Driver) *Server {
	return New("", &app.AppDef{}, WithDriver(d))
}

// session.harness returns the declared profiles + live selection (no env).
func TestHarnessRPC_Read(t *testing.T) {
	drv := &harnessDriver{
		profiles: []orchestrator.ProfileInfo{
			{Name: "claude-native", Backend: "claude", Active: true},
			{Name: "synthetic-codex", Backend: "codex"},
		},
		sel: orchestrator.ProfileSelection{Profile: "claude-native"},
	}
	s := newHarnessServer(drv)
	out, rerr := s.dispatch(context.Background(), "runstatus.session.harness", map[string]any{"session_id": "x"})
	if rerr != nil {
		t.Fatalf("harness RPC error: %+v", rerr)
	}
	m := out.(map[string]any)
	profiles := m["profiles"].([]orchestrator.ProfileInfo)
	if len(profiles) != 2 || profiles[0].Name != "claude-native" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}
	if sel := m["selection"].(orchestrator.ProfileSelection); sel.Profile != "claude-native" {
		t.Fatalf("unexpected selection: %+v", sel)
	}
}

// session.set_selection drives SetHarnessSelection and echoes the new state.
func TestHarnessRPC_SetSelection(t *testing.T) {
	drv := &harnessDriver{
		profiles: []orchestrator.ProfileInfo{{Name: "synthetic-codex", Backend: "codex"}},
	}
	s := newHarnessServer(drv)
	out, rerr := s.dispatch(context.Background(), "runstatus.session.set_selection",
		map[string]any{"session_id": "x", "profile": "synthetic-codex", "model": "hf:Qwen", "effort": "high"})
	if rerr != nil {
		t.Fatalf("set_selection error: %+v", rerr)
	}
	if drv.lastProfile != "synthetic-codex" || drv.lastModel != "hf:Qwen" || drv.lastEffort != "high" {
		t.Fatalf("driver not driven: profile=%q model=%q effort=%q", drv.lastProfile, drv.lastModel, drv.lastEffort)
	}
	if sel := out.(map[string]any)["selection"].(orchestrator.ProfileSelection); sel.Profile != "synthetic-codex" {
		t.Fatalf("echoed selection wrong: %+v", sel)
	}
}

// A missing profile param and a substrate rejection both surface as RPC errors.
func TestHarnessRPC_SetSelectionErrors(t *testing.T) {
	s := newHarnessServer(&harnessDriver{})
	if _, rerr := s.dispatch(context.Background(), "runstatus.session.set_selection",
		map[string]any{"session_id": "x"}); rerr == nil {
		t.Fatalf("missing profile should error")
	}

	drv := &harnessDriver{setErr: fmt.Errorf("unknown harness profile \"nope\"")}
	s = newHarnessServer(drv)
	if _, rerr := s.dispatch(context.Background(), "runstatus.session.set_selection",
		map[string]any{"session_id": "x", "profile": "nope"}); rerr == nil {
		t.Fatalf("substrate rejection should surface as an RPC error")
	}
}

// A read-only / no-HarnessController driver yields empty profiles (picker hidden)
// rather than erroring.
func TestHarnessRPC_NoController(t *testing.T) {
	s := newHarnessServer(&captureDriver{})
	out, rerr := s.dispatch(context.Background(), "runstatus.session.harness", map[string]any{"session_id": "x"})
	if rerr != nil {
		t.Fatalf("harness RPC should not error for a plain driver: %+v", rerr)
	}
	if profiles := out.(map[string]any)["profiles"].([]orchestrator.ProfileInfo); len(profiles) != 0 {
		t.Fatalf("expected empty profiles, got %+v", profiles)
	}
}
