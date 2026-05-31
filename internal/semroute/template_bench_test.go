package semroute

import (
	"context"
	"testing"

	"kitsoki/internal/app"
)

// benchTemplateApp builds the worked-example fixture for steady-state template
// benchmarks. Three templates × two slots — mirrors what an authored
// intent looks like in practice.
func benchTemplateApp() *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{ID: "bench-template", Version: "v0"},
		Intents: map[string]app.Intent{
			"propose_purchase": {
				Synonyms: []string{
					"buy {items} for {total_cost}",
					"purchase {items}",
					"spend {total_cost} on {items}",
				},
				Slots: map[string]app.Slot{
					"items":      {Type: "string"},
					"total_cost": {Type: "int"},
				},
			},
		},
	}
}

// BenchmarkTemplate_Match_Hit measures the template hot path — a template
// match that fills every slot. Compare with BenchmarkMatch_Hit
// (Phase 2 bare-string).
func BenchmarkTemplate_Match_Hit(b *testing.B) {
	def := benchTemplateApp()
	m, err := Compile(def)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	ctx := context.Background()
	allowed := []string{"propose_purchase"}
	input := "buy 6 oxen and 200 lbs food for 240"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := m.Match(ctx, "general_store", allowed, input)
		if v.Intent != "propose_purchase" {
			b.Fatalf("Match: want propose_purchase, got %q", v.Intent)
		}
	}
}

// BenchmarkTemplate_Match_Miss measures the cold path — no template's
// literal anchors align. The matcher should exit quickly after the
// first literal seek fails.
func BenchmarkTemplate_Match_Miss(b *testing.B) {
	def := benchTemplateApp()
	m, err := Compile(def)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	ctx := context.Background()
	allowed := []string{"propose_purchase"}
	input := "tell me about the weather in seattle yesterday afternoon"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := m.Match(ctx, "general_store", allowed, input)
		if v.Confidence != 0 {
			b.Fatalf("Match: want miss, got %+v", v)
		}
	}
}
