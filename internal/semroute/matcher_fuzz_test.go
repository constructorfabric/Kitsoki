package semroute

import (
	"context"
	"testing"

	"kitsoki/internal/app"
)

// FuzzMatch seeds the matcher with a representative slice of
// traces (synonyms, examples, multi-token utterances) and
// asserts the invariants the orchestrator relies on:
//
//   - Match never panics on arbitrary input.
//   - Verdict.Confidence ∈ [0, 1].
//   - When Confidence == 0, no Intent/Candidates/Slots leak.
//   - When Confidence > 0, either Intent (single hit) or Candidates
//     (tie) is populated, but never both.
//
// The corpus is intentionally varied: short inputs, long inputs,
// mixed-case, punctuation, fullwidth digits, Unicode controls. Run
// with `go test -fuzz=FuzzMatch -fuzztime=5s ./internal/semroute/...`.
func FuzzMatch(f *testing.F) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "fuzz-app", Version: "v0"},
		Intents: map[string]app.Intent{
			"ford":             {Synonyms: []string{"wade", "walk it"}},
			"caulk":            {Synonyms: []string{"seal and float", "float across"}},
			"ferry":            {Synonyms: []string{"pay the ferryman", "take the ferry"}},
			"hunt":             {Examples: []string{"hunt", "go hunting", "look for meat"}},
			"continue":         {Examples: []string{"continue", "press on", "keep going"}},
			"leave_store":      {Examples: []string{"leave", "done shopping", "head out"}},
			"propose_purchase": {Synonyms: []string{"buy oxen food"}},
		},
	}
	m, err := Compile(def)
	if err != nil {
		f.Fatalf("Compile: %v", err)
	}

	// Seeds — representative traces plus adversarial nonsense.
	seeds := []string{
		"wade across the river",
		"let's pay the ferryman",
		"go hunting for some meat",
		"continue",
		"press on please",
		"leave the store now",
		"buy oxen and food",
		"",
		"   ",
		"!!!@@@###",
		"\x00\x01\x02",
		"WADE ACROSS THE RIVER",
		"wade " + string(rune(0x1F600)),
		"6 oxen 200 lbs food",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, input string) {
		v, err := m.Match(ctx, "any.state", []string{
			"ford", "caulk", "ferry", "hunt", "continue", "leave_store", "propose_purchase",
		}, input)
		if err != nil {
			// Match's only error path is ctx.Err(); we used a live
			// context, so any error is a bug.
			t.Fatalf("Match(%q): unexpected error %v", input, err)
		}

		if v.Confidence < 0 || v.Confidence > 1 {
			t.Errorf("Match(%q): Confidence=%v outside [0,1]", input, v.Confidence)
		}

		switch {
		case v.Confidence == 0:
			if v.Intent != "" {
				t.Errorf("Match(%q): zero-Confidence verdict carries Intent=%q",
					input, v.Intent)
			}
			if len(v.Candidates) != 0 {
				t.Errorf("Match(%q): zero-Confidence verdict carries Candidates=%v",
					input, v.Candidates)
			}
		case v.Confidence == ConfidenceTie:
			if v.Intent != "" {
				t.Errorf("Match(%q): tie verdict carries Intent=%q (want empty)",
					input, v.Intent)
			}
			if len(v.Candidates) < 2 {
				t.Errorf("Match(%q): tie verdict has %d candidates (want ≥2)",
					input, len(v.Candidates))
			}
		default:
			if v.Intent == "" {
				t.Errorf("Match(%q): single-hit verdict has empty Intent", input)
			}
			if len(v.Candidates) != 0 {
				t.Errorf("Match(%q): single-hit verdict carries Candidates=%v",
					input, v.Candidates)
			}
		}

		// Slots/MissingSlots invariants (Phase 2: always empty).
		if len(v.MissingSlots) != 0 {
			t.Errorf("Match(%q): MissingSlots=%v (Phase 2 must be empty)",
				input, v.MissingSlots)
		}
	})
}
