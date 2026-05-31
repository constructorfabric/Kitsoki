package semroute

import (
	"context"
	"testing"

	"kitsoki/internal/app"
)

// benchApp builds a small but realistic AppDef so the benchmarks
// reflect the Oregon-Trail-ish steady state, not a one-intent toy.
// Eight intents × 2 synonyms + 2 examples each → 32 entries, which
// matches the order of magnitude of real apps.
func benchApp() *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{ID: "bench-app", Version: "v0"},
		Intents: map[string]app.Intent{
			"ford":             {Synonyms: []string{"wade", "walk it"}, Examples: []string{"ford", "ford the river"}},
			"caulk":            {Synonyms: []string{"seal and float"}, Examples: []string{"caulk", "caulk the wagon"}},
			"ferry":            {Synonyms: []string{"pay the ferryman"}, Examples: []string{"ferry", "take the ferry"}},
			"continue":         {Synonyms: []string{"press on"}, Examples: []string{"continue", "keep going"}},
			"hunt":             {Synonyms: []string{"go hunting"}, Examples: []string{"hunt", "look for meat"}},
			"rest":             {Synonyms: []string{"camp"}, Examples: []string{"rest", "make camp"}},
			"leave_store":      {Synonyms: []string{"done shopping"}, Examples: []string{"leave", "leave the store"}},
			"propose_purchase": {Synonyms: []string{"buy oxen food"}, Examples: []string{"buy 6 oxen and 200 lbs food"}},
		},
	}
}

// BenchmarkMatch_Hit measures the hot path: input contains one
// synonym's stem set, exactly one intent allowed. The design target
// is ~3 µs; the bench validates the order of magnitude.
func BenchmarkMatch_Hit(b *testing.B) {
	def := benchApp()
	m, err := Compile(def)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	ctx := context.Background()
	allowed := []string{"ford", "caulk", "ferry", "continue", "hunt", "rest", "leave_store", "propose_purchase"}
	input := "let's wade across the river"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := m.Match(ctx, "river_crossing.scouting", allowed, input)
		if v.Intent != "ford" {
			b.Fatalf("Match: want ford, got %q", v.Intent)
		}
	}
}

// BenchmarkMatch_Miss measures the cold path: input shares no
// stems with any indexed synonym. The AC pre-filter should exit
// without entering the subset-check loop.
func BenchmarkMatch_Miss(b *testing.B) {
	def := benchApp()
	m, err := Compile(def)
	if err != nil {
		b.Fatalf("Compile: %v", err)
	}
	ctx := context.Background()
	allowed := []string{"ford", "caulk", "ferry", "continue", "hunt", "rest", "leave_store", "propose_purchase"}
	input := "tell me about the weather in seattle yesterday afternoon"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, _ := m.Match(ctx, "river_crossing.scouting", allowed, input)
		if v.Confidence != 0 {
			b.Fatalf("Match: want miss, got %+v", v)
		}
	}
}

// BenchmarkCompile measures one-shot index construction so we have
// a number for app-load cost when an author bumps synonym count.
func BenchmarkCompile(b *testing.B) {
	def := benchApp()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(def); err != nil {
			b.Fatalf("Compile: %v", err)
		}
	}
}
