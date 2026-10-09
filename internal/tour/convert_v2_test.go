package tour

import "testing"

func TestConvertToV2(t *testing.T) {
	v1 := &TourManifest{
		Export: "how-to-export-report",
		Steps: []TourStep{
			{
				ID: "s1", Route: "home", Target: "prd-panel", Title: "The PRD panel",
				Body: "This is where...", Placement: "bottom", Kind: "explain", Advance: "next",
			},
			{
				ID: "s2", Route: "home", Target: "core__prd__start", Title: "Start", Body: "Click to start",
				Placement: "top", Kind: "action", Advance: "click-target",
			},
			{
				ID: "s3", Route: "interactive", Title: "Onward", Body: "Moving to interactive",
				Placement: "center", Kind: "action", Advance: "route-match", AdvanceRoute: "interactive",
			},
			{
				ID: "s4", Route: "interactive", Target: "composer", Title: "Ask it something",
				Body: "Type and send", Placement: "top", Kind: "action", Advance: "click-target",
				Drive: []DriveAction{{Type: DriveTypeAndSend, Text: "hello"}},
			},
		},
	}

	v2 := ConvertToV2(v1)
	if err := v2.Validate(); err != nil {
		t.Fatalf("converted manifest failed validation: %v", err)
	}
	if v2.Version != 2 {
		t.Fatalf("version = %d, want 2", v2.Version)
	}
	if v2.ID != "how-to-export-report" {
		t.Fatalf("id = %q", v2.ID)
	}
	if len(v2.Steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(v2.Steps))
	}

	explain := v2.Steps[0]
	if explain.Kind != StepKindHighlight {
		t.Errorf("s1 kind = %q, want highlight", explain.Kind)
	}
	if explain.Target == nil || explain.Target.TestID != "prd-panel" {
		t.Errorf("s1 target = %+v", explain.Target)
	}
	if explain.Popover == nil || explain.Popover.Title != "The PRD panel" || explain.Popover.Side != "bottom" {
		t.Errorf("s1 popover = %+v", explain.Popover)
	}

	clickAction := v2.Steps[1]
	if clickAction.Kind != StepKindGate {
		t.Errorf("s2 kind = %q, want gate", clickAction.Kind)
	}
	if clickAction.AdvanceOn == nil || clickAction.AdvanceOn.Event != AdvanceEventClick {
		t.Errorf("s2 advanceOn = %+v", clickAction.AdvanceOn)
	}

	routeAction := v2.Steps[2]
	if routeAction.Kind != StepKindNavigate {
		t.Errorf("s3 kind = %q, want navigate", routeAction.Kind)
	}
	if routeAction.Route != "interactive" {
		t.Errorf("s3 route = %q, want interactive", routeAction.Route)
	}

	driveAction := v2.Steps[3]
	if driveAction.Kind != StepKindAct {
		t.Errorf("s4 kind = %q, want act", driveAction.Kind)
	}
	if driveAction.Data == nil {
		t.Fatal("s4 expected Data with drive passthrough")
	}
	drive, ok := driveAction.Data["drive"].([]DriveAction)
	if !ok || len(drive) != 1 || drive[0].Type != DriveTypeAndSend {
		t.Errorf("s4 Data[drive] = %#v", driveAction.Data["drive"])
	}
}

func TestConvertToV2_DwellAndWaitForTargetPassthrough(t *testing.T) {
	v1 := &TourManifest{
		Export: "x",
		Steps: []TourStep{
			{ID: "s1", Kind: "explain", Advance: "next", DwellMs: 1500, WaitForTarget: "some-testid"},
		},
	}
	v2 := ConvertToV2(v1)
	step := v2.Steps[0]
	if step.Data["dwellMs"] != 1500 {
		t.Errorf("Data[dwellMs] = %v, want 1500", step.Data["dwellMs"])
	}
	if step.Data["waitForTarget"] != "some-testid" {
		t.Errorf("Data[waitForTarget] = %v, want some-testid", step.Data["waitForTarget"])
	}
}
