package tour

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTourStepV2Validate(t *testing.T) {
	cases := []struct {
		name    string
		step    TourStepV2
		wantErr bool
	}{
		{"highlight ok", TourStepV2{ID: "s1", Kind: StepKindHighlight}, false},
		{"gate requires advanceOn", TourStepV2{ID: "s1", Kind: StepKindGate}, true},
		{"gate ok", TourStepV2{ID: "s1", Kind: StepKindGate, AdvanceOn: &AdvanceOn{Event: AdvanceEventClick}}, false},
		{"act requires act.kind", TourStepV2{ID: "s1", Kind: StepKindAct}, true},
		{"act ok", TourStepV2{ID: "s1", Kind: StepKindAct, Act: &ActSpec{Kind: ActClick}}, false},
		{"act unknown policy", TourStepV2{ID: "s1", Kind: StepKindAct, Act: &ActSpec{Kind: ActClick}, Policy: "bogus"}, true},
		{"navigate requires route", TourStepV2{ID: "s1", Kind: StepKindNavigate}, true},
		{"navigate ok", TourStepV2{ID: "s1", Kind: StepKindNavigate, Route: "/settings"}, false},
		{"no kind", TourStepV2{ID: "s1"}, true},
		{"unknown kind", TourStepV2{ID: "s1", Kind: "bogus"}, true},
		{"no id", TourStepV2{Kind: StepKindHighlight}, true},
		{"bad interaction", TourStepV2{ID: "s1", Kind: StepKindHighlight, Interaction: "bogus"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.step.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestTourStepV2EffectivePolicy(t *testing.T) {
	act := TourStepV2{Kind: StepKindAct, Act: &ActSpec{Kind: ActClick}}
	if got := act.EffectivePolicy(); got != PolicyConfirm {
		t.Fatalf("act step with unset Policy: got %q, want %q", got, PolicyConfirm)
	}
	act.Policy = PolicyWatch
	if got := act.EffectivePolicy(); got != PolicyWatch {
		t.Fatalf("act step with explicit Policy: got %q, want %q", got, PolicyWatch)
	}
	highlight := TourStepV2{Kind: StepKindHighlight}
	if got := highlight.EffectivePolicy(); got != "" {
		t.Fatalf("highlight step: got %q, want empty", got)
	}
}

func TestTourManifestV2Validate(t *testing.T) {
	m := &TourManifestV2{
		Version: 2,
		ID:      "how-to-export-report",
		Steps: []TourStepV2{
			{ID: "step-1", Kind: StepKindHighlight},
			{ID: "step-2", Kind: StepKindGate, AdvanceOn: &AdvanceOn{Event: AdvanceEventClick}},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("valid manifest failed: %v", err)
	}

	wrongVersion := &TourManifestV2{Version: 1, ID: "x", Steps: m.Steps}
	if err := wrongVersion.Validate(); err == nil {
		t.Fatal("expected error for wrong version")
	}

	noID := &TourManifestV2{Version: 2, Steps: m.Steps}
	if err := noID.Validate(); err == nil {
		t.Fatal("expected error for missing id")
	}

	noSteps := &TourManifestV2{Version: 2, ID: "x"}
	if err := noSteps.Validate(); err == nil {
		t.Fatal("expected error for no steps")
	}

	dup := &TourManifestV2{
		Version: 2, ID: "x",
		Steps: []TourStepV2{
			{ID: "s1", Kind: StepKindHighlight},
			{ID: "s1", Kind: StepKindHighlight},
		},
	}
	if err := dup.Validate(); err == nil {
		t.Fatal("expected error for duplicate step id")
	}
}

// TestTourManifestV2RoundTrip proves the brief's example JSON parses into
// TourManifestV2 and re-marshals with the same key set — the schema and the
// Go struct tags haven't drifted.
func TestTourManifestV2RoundTrip(t *testing.T) {
	const src = `{
		"version": 2,
		"id": "how-to-export-report",
		"origin": "https://app.example.com",
		"steps": [{
			"id": "step-3",
			"route": "/settings",
			"target": {
				"role": "button", "name": "Save",
				"testid": "save-btn", "text": "Save",
				"css": "[data-qa*='save']",
				"ancestor": "#billing-pane",
				"frame": [],
				"waitFor": { "timeoutMs": 5000 }
			},
			"popover": { "title": "Save your report", "body": "Click here", "side": "bottom", "align": "start" },
			"kind": "gate",
			"advanceOn": { "event": "click" },
			"policy": "confirm",
			"interaction": "block",
			"viewport": "scroll-pane-id",
			"data": {}
		}]
	}`

	var m TourManifestV2
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if m.Origin != "https://app.example.com" {
		t.Fatalf("origin = %q", m.Origin)
	}
	step := m.Steps[0]
	if step.Target.Role != "button" || step.Target.Name != "Save" || step.Target.TestID != "save-btn" {
		t.Fatalf("target anchors: %+v", step.Target)
	}
	if step.Target.WaitFor == nil || step.Target.WaitFor.TimeoutMs != 5000 {
		t.Fatalf("waitFor: %+v", step.Target.WaitFor)
	}
	if step.Popover.Side != "bottom" || step.Popover.Align != "start" {
		t.Fatalf("popover: %+v", step.Popover)
	}
	if step.AdvanceOn.Event != AdvanceEventClick {
		t.Fatalf("advanceOn: %+v", step.AdvanceOn)
	}

	roundTripped, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again TourManifestV2
	if err := json.Unmarshal(roundTripped, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if again.ID != m.ID || len(again.Steps) != len(m.Steps) {
		t.Fatalf("round trip mismatch: %+v vs %+v", again, m)
	}
}

// TestTourManifestV2SchemaFieldParity walks schemas/tour-v2.schema.json's
// declared property names for the manifest and step objects and asserts
// every schema-declared field has a corresponding json tag on the Go struct
// (and vice versa), so the schema and the Go types can't silently drift.
func TestTourManifestV2SchemaFieldParity(t *testing.T) {
	root := repoRootForTest(t)
	data, err := os.ReadFile(filepath.Join(root, "schemas", "tour-v2.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema struct {
		Properties  map[string]json.RawMessage `json:"properties"`
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}

	assertSameKeys(t, "manifest", schema.Properties, jsonTagSet(TourManifestV2{}))
	assertSameKeys(t, "step", schema.Definitions["step"].Properties, jsonTagSet(TourStepV2{}))
	assertSameKeys(t, "targetBundle", schema.Definitions["targetBundle"].Properties, jsonTagSet(TargetBundle{}))
	assertSameKeys(t, "popover", schema.Definitions["popover"].Properties, jsonTagSet(PopoverV2{}))
	assertSameKeys(t, "healEvent", schema.Definitions["healEvent"].Properties, jsonTagSet(HealEvent{}))
}

func assertSameKeys(t *testing.T, label string, schemaProps map[string]json.RawMessage, goFields map[string]bool) {
	t.Helper()
	for k := range schemaProps {
		if !goFields[k] {
			t.Errorf("%s: schema declares %q, no matching Go json tag", label, k)
		}
	}
	for k := range goFields {
		if _, ok := schemaProps[k]; !ok {
			t.Errorf("%s: Go json tag %q has no matching schema property", label, k)
		}
	}
}

// jsonTagSet reflects over v's type and returns the set of its top-level
// json tag names (the part before any ",omitempty"), skipping "-" tags.
// Unlike marshaling a zero value, this doesn't miss omitempty fields.
func jsonTagSet(v any) map[string]bool {
	set := map[string]bool{}
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		set[name] = true
	}
	return set
}
