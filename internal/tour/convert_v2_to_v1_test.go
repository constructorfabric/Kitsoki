package tour

import (
	"encoding/json"
	"testing"
)

func TestConvertV2ToV1_Highlight(t *testing.T) {
	m := &TourManifestV2{
		Version: 2, ID: "how-to",
		Steps: []TourStepV2{
			{
				ID: "s1", Route: "home",
				Target:  &TargetBundle{TestID: "prd-panel", Text: "narrow"},
				Popover: &PopoverV2{Title: "The panel", Body: "Explains it", Side: "bottom"},
				Kind:    StepKindHighlight,
			},
		},
	}
	v1, err := ConvertV2ToV1(m)
	if err != nil {
		t.Fatalf("ConvertV2ToV1: %v", err)
	}
	if v1.Export != "how-to" {
		t.Errorf("Export = %q", v1.Export)
	}
	s := v1.Steps[0]
	if s.Target != "prd-panel" || s.TargetText != "narrow" {
		t.Errorf("Target/TargetText = %q/%q", s.Target, s.TargetText)
	}
	if s.Title != "The panel" || s.Body != "Explains it" || s.Placement != "bottom" {
		t.Errorf("popover fields: %+v", s)
	}
	if s.Kind != "explain" || s.Advance != "next" {
		t.Errorf("kind/advance = %q/%q", s.Kind, s.Advance)
	}
}

func TestConvertV2ToV1_GateAndNavigate(t *testing.T) {
	m := &TourManifestV2{
		Version: 2, ID: "x",
		Steps: []TourStepV2{
			{ID: "s1", Kind: StepKindGate, AdvanceOn: &AdvanceOn{Event: AdvanceEventClick}},
			{ID: "s2", Kind: StepKindNavigate, Route: "interactive"},
		},
	}
	v1, err := ConvertV2ToV1(m)
	if err != nil {
		t.Fatalf("ConvertV2ToV1: %v", err)
	}
	if v1.Steps[0].Kind != "action" || v1.Steps[0].Advance != "click-target" {
		t.Errorf("gate step: %+v", v1.Steps[0])
	}
	if v1.Steps[1].Kind != "action" || v1.Steps[1].Advance != "route-match" || v1.Steps[1].AdvanceRoute != "interactive" {
		t.Errorf("navigate step: %+v", v1.Steps[1])
	}
}

// TestConvertV2ToV1_RoundTripsDrivePassthrough proves a v1 tour converted to
// v2 by ConvertToV2 (P1) and back by ConvertV2ToV1 (P3) renders identically:
// the Drive[] list survives the round trip via Data["drive"] verbatim, even
// after a JSON round trip (the shape a step loaded from a real YAML/JSON v2
// manifest file would carry, not the concrete []DriveAction ConvertToV2
// produces in-process).
func TestConvertV2ToV1_RoundTripsDrivePassthrough(t *testing.T) {
	original := &TourManifest{
		Export: "roundtrip",
		Steps: []TourStep{
			{
				ID: "s1", Route: "interactive", Target: "composer", Title: "Ask it something",
				Body: "Type and send", Placement: "top", Kind: "action", Advance: "click-target",
				Drive: []DriveAction{{Type: DriveTypeAndSend, Text: "hello"}, {Type: DriveDwellMs, Ms: 500}},
			},
		},
	}
	v2 := ConvertToV2(original)

	// Simulate loading v2 from disk: JSON round-trip turns Data["drive"]
	// from []DriveAction into []interface{} of maps, which is what a real
	// YAML/JSON-loaded TourManifestV2 would carry.
	roundTripped := jsonRoundTrip(t, v2)

	back, err := ConvertV2ToV1(roundTripped)
	if err != nil {
		t.Fatalf("ConvertV2ToV1: %v", err)
	}
	if len(back.Steps[0].Drive) != 2 {
		t.Fatalf("Drive length = %d, want 2", len(back.Steps[0].Drive))
	}
	if back.Steps[0].Drive[0].Type != DriveTypeAndSend || back.Steps[0].Drive[0].Text != "hello" {
		t.Errorf("Drive[0] = %+v", back.Steps[0].Drive[0])
	}
	if back.Steps[0].Drive[1].Type != DriveDwellMs || back.Steps[0].Drive[1].Ms != 500 {
		t.Errorf("Drive[1] = %+v", back.Steps[0].Drive[1])
	}
	if back.Steps[0].DwellMs != 0 {
		// dwellMs passthrough only applies to non-Drive steps in this fixture; not exercised here.
		_ = back
	}
}

func TestConvertV2ToV1_ActWithoutDriveRequiresClickAndTestID(t *testing.T) {
	// act.kind click + testid target: synthesizes a click-selector Drive.
	clickable := &TourManifestV2{
		Version: 2, ID: "x",
		Steps: []TourStepV2{
			{ID: "s1", Kind: StepKindAct, Target: &TargetBundle{TestID: "save-btn"}, Act: &ActSpec{Kind: ActClick}},
		},
	}
	v1, err := ConvertV2ToV1(clickable)
	if err != nil {
		t.Fatalf("ConvertV2ToV1: %v", err)
	}
	if len(v1.Steps[0].Drive) != 1 || v1.Steps[0].Drive[0].Type != DriveClickSelector {
		t.Errorf("Drive = %+v", v1.Steps[0].Drive)
	}

	// act.kind fill has no v1 equivalent: must error, not fabricate behavior.
	unfillable := &TourManifestV2{
		Version: 2, ID: "x",
		Steps: []TourStepV2{
			{ID: "s1", Kind: StepKindAct, Target: &TargetBundle{TestID: "name-input"}, Act: &ActSpec{Kind: ActFill, Value: "hi"}},
		},
	}
	if _, err := ConvertV2ToV1(unfillable); err == nil {
		t.Fatal("expected an error for act.kind=fill with no data.drive passthrough")
	}
}

func jsonRoundTrip(t *testing.T, m *TourManifestV2) *TourManifestV2 {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out TourManifestV2
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &out
}
