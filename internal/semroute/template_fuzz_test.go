package semroute

import (
	"context"
	"testing"

	"kitsoki/internal/app"
)

// FuzzTemplate seeds the matcher with worked-example templates and jittered
// inputs. Invariants: no panics, Confidence ∈ [0, 1], slot/missing-
// slot accounting stays consistent.
//
// Run with `go test -fuzz=FuzzTemplate -fuzztime=5s ./internal/semroute/...`.
func FuzzTemplate(f *testing.F) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "fuzz-template", Version: "v0"},
		Intents: map[string]app.Intent{
			"propose_purchase": {
				Synonyms: []string{
					"buy {items} for {total_cost}",
					"purchase {items}",
					"spend {total_cost} on {items}",
					"I'd like to spend {total_cost}",
				},
				Slots: map[string]app.Slot{
					"items":      {Type: "string"},
					"total_cost": {Type: "int"},
				},
			},
			"name_wagon": {
				Synonyms: []string{"name the wagon {wagon_name}"},
				Slots:    map[string]app.Slot{"wagon_name": {Type: "string"}},
			},
		},
	}
	m, err := Compile(def)
	if err != nil {
		f.Fatalf("Compile: %v", err)
	}

	// Seeds — worked-example templates + jittered variants.
	seeds := []string{
		"buy 6 oxen and 200 lbs food for 240",
		"buy 6 oxen for 240",
		"purchase 6 oxen",
		"spend 100 on bullets",
		"name the wagon the rolling thunder",
		"I'd like to spend 200",
		"buy {items} for {total_cost}", // adversarial: looks like a template
		"BUY 6 OXEN FOR 240",
		"buy  6  oxen   for   240",
		"buy",
		"for 240 buy 6 oxen", // out of order
		"",
		"  ",
		"!!@@##",
		"buy 6 oxen and 200 lbs food for fjord", // unparseable cost
		"buy 6 oxen for two hundred forty",      // spelled cardinal
	}
	for _, s := range seeds {
		f.Add(s)
	}

	ctx := context.Background()
	allowed := []string{"propose_purchase", "name_wagon"}
	f.Fuzz(func(t *testing.T, input string) {
		v, err := m.Match(ctx, "any.state", allowed, input)
		if err != nil {
			t.Fatalf("Match(%q): unexpected error %v", input, err)
		}
		if v.Confidence < 0 || v.Confidence > 1 {
			t.Errorf("Match(%q): Confidence=%v outside [0,1]", input, v.Confidence)
		}
		switch {
		case v.Confidence == 0:
			if v.Intent != "" {
				t.Errorf("Match(%q): zero-Confidence verdict carries Intent=%q", input, v.Intent)
			}
			if len(v.Slots) != 0 {
				t.Errorf("Match(%q): zero-Confidence verdict carries Slots=%v", input, v.Slots)
			}
		case v.Confidence == ConfidenceTie:
			if len(v.Candidates) < 2 {
				t.Errorf("Match(%q): tie verdict has %d candidates (want ≥2)", input, len(v.Candidates))
			}
		case v.Confidence == ConfidenceTemplateAllSlots:
			if v.Intent == "" {
				t.Errorf("Match(%q): 0.80 verdict missing Intent", input)
			}
			if len(v.MissingSlots) != 0 {
				t.Errorf("Match(%q): 0.80 verdict has MissingSlots=%v", input, v.MissingSlots)
			}
		case v.Confidence == ConfidenceTemplateMissingSlot:
			if v.Intent == "" {
				t.Errorf("Match(%q): 0.65 verdict missing Intent", input)
			}
			if len(v.MissingSlots) == 0 {
				t.Errorf("Match(%q): 0.65 verdict has empty MissingSlots", input)
			}
		case v.Confidence == ConfidenceWholeSynonym:
			// bare-string hit — fine even with a template-shaped app.
		default:
			// Any other confidence is a logic bug.
			t.Errorf("Match(%q): unexpected confidence %v", input, v.Confidence)
		}
	})
}
