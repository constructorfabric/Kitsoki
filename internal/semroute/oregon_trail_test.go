// End-to-end calibration test for the Phase-2 matcher against the
// canonical proposal calibration corpus, stories/oregon-trail.
//
// The proposal commits to "the matcher routes at least 3-5 intents
// end-to-end given the Examples declared today (no new synonyms
// needed)." This test loads the actual story manifest and asserts
// which utterances from stories/oregon-trail/recording.yaml the
// Phase-2 matcher resolves *without* the LLM.
//
// The test is a *smoke* test, not a regression bar: as authors add
// synonyms over time more utterances will resolve, which is fine —
// the test only asserts the floor.
package semroute

import (
	"context"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
)

// oregonTrailFixture is one row from recording.yaml the test claims
// semroute alone can resolve. The state field is informational —
// recording.yaml's state values are paths into the intent menu;
// semroute itself doesn't look up state, so we just pass each
// utterance against the full allowed-intent list of the relevant
// intent.
type oregonTrailFixture struct {
	// Input is the recorded user utterance.
	Input string
	// WantIntent is the intent the matcher must resolve to.
	WantIntent string
	// Allowed is the set of intent names visible at the recorded
	// state. The test pins these inline rather than calling into
	// the machine so the assertion is hermetic — the matcher is a
	// pure function of (def, allowed, input).
	Allowed []string
}

// oregonTrailFixtures are the 10 calibration utterances. Five-plus
// is the calibration floor; we list ten so a regression in
// one row doesn't silently sink the floor. The Allowed lists are
// hand-curated from stories/oregon-trail/app.yaml's room definitions
// to model what the orchestrator would compute at the recorded state.
var oregonTrailFixtures = []oregonTrailFixture{
	// general_store.idle — leave_store has examples
	// ["leave", "leave the store", "done shopping", "head out", "depart"].
	{
		Input:      "leave",
		WantIntent: "leave_store",
		Allowed:    []string{"leave_store", "propose_purchase", "accept_purchase", "cancel_purchase", "refine_purchase"},
	},
	{
		Input:      "leave the store",
		WantIntent: "leave_store",
		Allowed:    []string{"leave_store", "propose_purchase", "accept_purchase", "cancel_purchase", "refine_purchase"},
	},
	{
		Input:      "done shopping",
		WantIntent: "leave_store",
		Allowed:    []string{"leave_store", "propose_purchase", "accept_purchase", "cancel_purchase", "refine_purchase"},
	},
	// leg_*_executing.traveling — continue has examples
	// ["continue", "go", "press on", "keep going", "onward"].
	{
		Input:      "continue",
		WantIntent: "continue",
		Allowed:    []string{"continue", "set_pace", "set_rations", "hunt", "rest"},
	},
	{
		Input:      "press on",
		WantIntent: "continue",
		Allowed:    []string{"continue", "set_pace", "set_rations", "hunt", "rest"},
	},
	{
		Input:      "keep going",
		WantIntent: "continue",
		Allowed:    []string{"continue", "set_pace", "set_rations", "hunt", "rest"},
	},
	// hunt has examples ["hunt", "go hunting", "shoot game", "look for meat"].
	{
		Input:      "look for meat",
		WantIntent: "hunt",
		Allowed:    []string{"hunt", "shoot", "rest", "continue"},
	},
	{
		Input:      "go hunting",
		WantIntent: "hunt",
		Allowed:    []string{"hunt", "shoot", "rest", "continue"},
	},
	// rest has examples ["rest", "camp", "rest 3 days", "make camp for a week"].
	{
		Input:      "make camp",
		WantIntent: "rest",
		Allowed:    []string{"rest", "hunt", "continue"},
	},
	// cancel_purchase has examples ["cancel", "never mind", "drop it"].
	{
		Input:      "never mind",
		WantIntent: "cancel_purchase",
		Allowed:    []string{"leave_store", "cancel_purchase", "accept_purchase", "refine_purchase"},
	},
}

// TestOregonTrail_SemRouteCalibration loads the real story manifest
// and asserts the calibration floor: at least 5 of the calibration rows
// resolve without an LLM, and any row that DOES resolve resolves to
// the intent the recording expected.
//
// The test fails-fast on a mis-route: if Phase 2 ever routes
// "leave" to cancel_purchase (which has no "leave" example) the
// signal is louder than a silent floor regression.
func TestOregonTrail_SemRouteCalibration(t *testing.T) {
	t.Parallel()
	def := loadOregonTrail(t)
	m, err := Compile(def)
	if err != nil {
		t.Fatalf("Compile(oregon-trail): %v", err)
	}
	if m.IsEmpty() {
		t.Fatalf("Compile(oregon-trail): IsEmpty=true; story has examples that should index")
	}

	ctx := context.Background()
	const proposalFloor = 5
	resolved := 0
	for _, fx := range oregonTrailFixtures {
		v, err := m.Match(ctx, "", fx.Allowed, fx.Input)
		if err != nil {
			t.Errorf("Match(%q): unexpected error %v", fx.Input, err)
			continue
		}
		switch {
		case v.Confidence == ConfidenceWholeSynonym && v.Intent == fx.WantIntent:
			resolved++
			t.Logf("%-30s → %s (%s, confidence=%.2f)",
				fx.Input, v.Intent, v.MatchReason, v.Confidence)
		case v.Confidence == ConfidenceWholeSynonym && v.Intent != fx.WantIntent:
			// Loud failure — wrong intent at the high band.
			t.Errorf("Match(%q): routed to %q at confidence %.2f, want %q",
				fx.Input, v.Intent, v.Confidence, fx.WantIntent)
		case v.Confidence == ConfidenceTie:
			// Tie is acceptable if the WantIntent appears in
			// Candidates — the orchestrator will surface a
			// disambiguation card. We log but don't count
			// this toward the floor because the user still has
			// to pick.
			contains := false
			for _, c := range v.Candidates {
				if c.Intent == fx.WantIntent {
					contains = true
					break
				}
			}
			if !contains {
				t.Errorf("Match(%q): tie verdict %v omits want=%q",
					fx.Input, v.Candidates, fx.WantIntent)
			} else {
				t.Logf("%-30s → tie %v (want %q present)",
					fx.Input, candidateNames(v.Candidates), fx.WantIntent)
			}
		default:
			// Miss — fine, the LLM would handle it.
			t.Logf("%-30s → miss (would fall through to LLM)", fx.Input)
		}
	}

	if resolved < proposalFloor {
		t.Errorf("oregon-trail calibration: resolved %d/%d, want ≥ %d (calibration floor)",
			resolved, len(oregonTrailFixtures), proposalFloor)
	}
}

// candidateNames extracts intent ids from a Candidate slice for logging.
func candidateNames(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Intent)
	}
	return out
}

// TestOregonTrail_TemplateWorkedExample is the Phase-4 end-to-end
// pin for the worked example. It loads the real Oregon-Trail
// app manifest (which now ships the three propose_purchase templates
// — see stories/oregon-trail/intents.yaml) and asserts the canonical
// "buy 6 oxen and 200 lbs food for 240" utterance resolves to
// propose_purchase{items, total_cost} at Confidence 0.80 *without*
// any LLM call.
func TestOregonTrail_TemplateWorkedExample(t *testing.T) {
	t.Parallel()
	def := loadOregonTrail(t)
	m, err := Compile(def)
	if err != nil {
		t.Fatalf("Compile(oregon-trail): %v", err)
	}

	// At general_store.idle only propose_purchase and leave_store are
	// allowed (see stories/oregon-trail/rooms/general_store.yaml). The
	// accept/cancel/refine variants only become available after a
	// draft is in flight (the .reviewing substate).
	allowed := []string{"propose_purchase", "leave_store"}
	input := "buy 6 oxen and 200 lbs food for 240"
	v, err := m.Match(context.Background(), "general_store.idle", allowed, input)
	if err != nil {
		t.Fatalf("Match(%q): %v", input, err)
	}
	if v.Intent != "propose_purchase" {
		t.Fatalf("Intent: got %q, want propose_purchase", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Errorf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	gotCost, ok := v.Slots["total_cost"].(int)
	if !ok || gotCost != 240 {
		t.Errorf("Slots[total_cost]: got %v (%T), want int(240)",
			v.Slots["total_cost"], v.Slots["total_cost"])
	}
	gotItems, _ := v.Slots["items"].(string)
	if gotItems == "" {
		t.Errorf("Slots[items]: got empty, want non-empty (raw capture)")
	}
	t.Logf("worked example: %q → %s%+v (conf=%.2f, reason=%s)",
		input, v.Intent, v.Slots, v.Confidence, v.MatchReason)
}

// loadOregonTrail loads the oregon-trail story relative to the
// kitsoki repo root. The path walks up from the test's own
// directory because `go test` runs with cwd = the package dir.
func loadOregonTrail(t *testing.T) *app.AppDef {
	t.Helper()
	// internal/semroute → ../../stories/oregon-trail/app.yaml
	path := filepath.Join("..", "..", "stories", "oregon-trail", "app.yaml")
	def, err := app.Load(path)
	if err != nil {
		t.Fatalf("app.Load(%s): %v", path, err)
	}
	return def
}
