package tour

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestStepPointer(t *testing.T) {
	feature := &TourManifest{SpecPointerBase: "/tour/steps"}
	if got := feature.stepPointer(5); got != "/tour/steps/5" {
		t.Errorf("feature pointer = %q, want /tour/steps/5", got)
	}
	standalone := &TourManifest{SpecPointerBase: "/steps"}
	if got := standalone.stepPointer(0); got != "/steps/0" {
		t.Errorf("standalone pointer = %q, want /steps/0", got)
	}
	// Empty base defaults to /steps so a pointer is never malformed.
	if got := (&TourManifest{}).stepPointer(2); got != "/steps/2" {
		t.Errorf("default pointer = %q, want /steps/2", got)
	}
}

func TestWaitStates(t *testing.T) {
	drive := []DriveAction{
		{Type: DriveTypeAndSend, Text: "hi"},
		{Type: DriveWaitState, State: "prd.clarifying"},
		{Type: DriveRevealTurn},
		{Type: DriveWaitState, State: "prd.brief"},
		{Type: DriveWaitState, State: "prd.clarifying"}, // a second round — order preserved
	}
	got := waitStates(drive)
	want := []string{"prd.clarifying", "prd.brief", "prd.clarifying"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("waitStates = %v, want %v", got, want)
	}
	if waitStates(nil) != nil {
		t.Error("waitStates(nil) should be nil")
	}
}

func TestStepShot_DeterministicSpecRef(t *testing.T) {
	m := &TourManifest{SpecPath: "features/x.yaml", SpecPointerBase: "/tour/steps"}
	step := TourStep{
		ID: "ds-prd-clarify", Route: "interactive", Title: "Clarify", Body: "narration",
		WaitForTarget: "chat-transcript",
		Drive:         []DriveAction{{Type: DriveWaitState, State: "prd.clarifying"}},
	}
	shot := m.stepShot(5, 4, "05-ds-prd-clarify.png", step)

	if shot.Capture != 5 || shot.StepIndex != 4 || shot.PNG != "05-ds-prd-clarify.png" {
		t.Errorf("shot ordinals/png wrong: %+v", shot)
	}
	if shot.SpecRef != (StepSpecRef{Kind: "tour", SpecPath: "features/x.yaml", Pointer: "/tour/steps/4", StepID: "ds-prd-clarify"}) {
		t.Errorf("spec_ref = %+v", shot.SpecRef)
	}
	if !shot.TitleAsserted || shot.Title != "Clarify" || shot.Body != "narration" || shot.Route != "interactive" {
		t.Errorf("narration/assertion fields wrong: %+v", shot)
	}
	if !reflect.DeepEqual(shot.StatesAsserted, []string{"prd.clarifying"}) {
		t.Errorf("states_asserted = %v", shot.StatesAsserted)
	}
}

func TestWriteStepShots_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "demo.mp4")
	shots := []StepShot{{
		Capture: 1, StepIndex: 0, PNG: "01-a.png", TitleAsserted: true,
		SpecRef: StepSpecRef{Kind: "tour", SpecPath: "f.yaml", Pointer: "/tour/steps/0", StepID: "a"},
	}}
	path, err := writeStepShots(video, shots)
	if err != nil {
		t.Fatalf("writeStepShots: %v", err)
	}
	if path != video+".steps.json" {
		t.Errorf("sidecar path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got []StepShot
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, shots) {
		t.Errorf("roundtrip mismatch: %+v vs %+v", got, shots)
	}

	// A nil slice writes an empty JSON array, not null — a stable consumer shape.
	p2, err := writeStepShots(filepath.Join(dir, "empty.mp4"), nil)
	if err != nil {
		t.Fatalf("writeStepShots(nil): %v", err)
	}
	b, _ := os.ReadFile(p2)
	if string(b) != "[]\n" {
		t.Errorf("empty sidecar = %q, want []", string(b))
	}
}
