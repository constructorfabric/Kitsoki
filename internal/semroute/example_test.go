// Runnable godoc example for the [Matcher] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/semroute/...`.
package semroute_test

import (
	"context"
	"fmt"
	"sort"

	"kitsoki/internal/app"
	"kitsoki/internal/semroute"
)

// ExampleMatcher_Match is the canonical bare-synonym worked example: a
// single-intent app with one synonym, matching the "wade across the
// river" input (the same trace shown in the package doc).
func ExampleMatcher_Match() {
	def := &app.AppDef{
		App: app.AppMeta{ID: "demo", Version: "v0"},
		Intents: map[string]app.Intent{
			"ford":  {Synonyms: []string{"wade", "walk it"}},
			"caulk": {Synonyms: []string{"seal and float"}},
		},
	}
	m, err := semroute.Compile(def)
	if err != nil {
		panic(err)
	}

	verdict, err := m.Match(
		context.Background(),
		"river_crossing.scouting",
		[]string{"ford", "caulk"},
		"let's wade across the river",
	)
	if err != nil {
		panic(err)
	}

	fmt.Println("intent:    ", verdict.Intent)
	fmt.Printf("confidence: %.2f\n", verdict.Confidence)
	fmt.Println("reason:    ", verdict.MatchReason)
	// Output:
	// intent:     ford
	// confidence: 0.90
	// reason:     synonym:wade
}

// ExampleMatcher_Match_template is the template worked example: a
// Phase-4 template fills both {items} and {total_cost} from one
// utterance, producing a Confidence-0.80 Verdict.
func ExampleMatcher_Match_template() {
	def := &app.AppDef{
		App: app.AppMeta{ID: "demo", Version: "v0"},
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
	m, err := semroute.Compile(def)
	if err != nil {
		panic(err)
	}

	verdict, err := m.Match(
		context.Background(),
		"general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen and 200 lbs food for 240",
	)
	if err != nil {
		panic(err)
	}

	// Iterate Slots in a fixed order for deterministic Example output.
	keys := make([]string, 0, len(verdict.Slots))
	for k := range verdict.Slots {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Println("intent:    ", verdict.Intent)
	fmt.Printf("confidence: %.2f\n", verdict.Confidence)
	for _, k := range keys {
		fmt.Printf("slot[%s]: %v\n", k, verdict.Slots[k])
	}
	// Output:
	// intent:     propose_purchase
	// confidence: 0.80
	// slot[items]: 6 oxen and 200 lbs food
	// slot[total_cost]: 240
}
