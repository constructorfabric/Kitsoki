// Template-match tests for the Phase-4 `{slot_name}` capture
// (the template grammar). Structural-compile tests live in
// template_compile_test.go; this file pins runtime behaviour:
//
//   - the worked example resolves to Confidence 0.80, intent
//     propose_purchase, slots = {items, total_cost}.
//   - The 0.65 band: when a named slot's parser returns OK=false,
//     the verdict downgrades and lists the slot in MissingSlots.
//   - Most-specific-wins within an intent (more filled slots).
//   - Cross-intent ties surface as 0.50.
//   - string-slot-no-parser: capture text becomes the slot value.
//   - Empty input → zero Verdict.
//   - Leading and trailing capture forms both work.
//   - Order matters: positional templates do NOT match shuffled input.
//   - Idempotency: same input twice → same Verdict.
package semroute

import (
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// ====================== fixtures ======================

// proposePurchaseApp builds the template fixture: propose_purchase with
// the three canonical templates and string/int slots.
func proposePurchaseApp() *app.AppDef {
	return &app.AppDef{
		App: app.AppMeta{ID: "test-app", Version: "v0"},
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

// ====================== worked example ======================

// TestTemplateMatch_WorkedExample pins the canonical
// trace: "buy 6 oxen and 200 lbs food for 240" routes to
// propose_purchase with items=raw text and total_cost=240.
func TestTemplateMatch_WorkedExample(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())
	v := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen and 200 lbs food for 240")

	if v.Intent != "propose_purchase" {
		t.Fatalf("Intent: got %q, want propose_purchase", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Errorf("Confidence: got %v, want %v (all slots filled)",
			v.Confidence, ConfidenceTemplateAllSlots)
	}
	gotCost, ok := v.Slots["total_cost"].(int)
	if !ok || gotCost != 240 {
		t.Errorf("Slots[total_cost]: got %v (%T), want int(240)",
			v.Slots["total_cost"], v.Slots["total_cost"])
	}
	gotItems, _ := v.Slots["items"].(string)
	// We don't pin exact whitespace — the surfaces are joined with
	// single spaces by lex.Tokenize so the comparison is robust.
	wantItems := "6 oxen and 200 lbs food"
	if normalizeSpaces(gotItems) != normalizeSpaces(wantItems) {
		t.Errorf("Slots[items]: got %q, want %q", gotItems, wantItems)
	}
	if !strings.HasPrefix(v.MatchReason, "template:buy ") {
		t.Errorf("MatchReason: got %q, want template:<source>", v.MatchReason)
	}
	if len(v.MissingSlots) != 0 {
		t.Errorf("MissingSlots: got %v, want empty (all slots parsed)", v.MissingSlots)
	}
}

func TestTemplateMatch_DevStoryTicketPickRow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		intent string
	}{
		{name: "standalone", path: "../../stories/dev-story/app.yaml", intent: "pick_ticket"},
		{name: "imported", path: "../../.kitsoki/stories/kitsoki-dev/app.yaml", intent: "core__pick_ticket"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def, err := app.Load(tc.path)
			if err != nil {
				t.Fatalf("load %s: %v", tc.path, err)
			}
			m := mustCompile(t, def)
			v := mustMatch(t, m, "ticket_search", []string{tc.intent}, "pick 1")
			if v.Intent != tc.intent {
				t.Fatalf("Intent: got %q, want %q", v.Intent, tc.intent)
			}
			if v.Confidence != ConfidenceTemplateAllSlots {
				t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
			}
			got, ok := v.Slots["n"].(int)
			if !ok || got != 1 {
				t.Fatalf("Slots[n]: got %v (%T), want int(1)", v.Slots["n"], v.Slots["n"])
			}
		})
	}
}

func TestTemplateMatch_PunchListTop10ManifestPhrase(t *testing.T) {
	t.Parallel()
	def, err := app.Load("../../stories/punch-list/app.yaml")
	if err != nil {
		t.Fatalf("load punch-list: %v", err)
	}
	m := mustCompile(t, def)
	v := mustMatch(t, m, "idle", []string{"start"}, "Let's run the top 10 GPT-5.5 punch list manifest now.")
	if v.Intent != "start" {
		t.Fatalf("Intent: got %q, want start", v.Intent)
	}
	got, ok := v.Slots["manifest_path"].(string)
	if !ok || got == "" {
		t.Fatalf("Slots[manifest_path]: got %v (%T), want non-empty string", v.Slots["manifest_path"], v.Slots["manifest_path"])
	}
}

// normalizeSpaces collapses runs of whitespace to one space. The
// matcher's joinSurfaces puts a single space between tokens; this
// helper just shields the assertion from any extra trimming.
func normalizeSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ====================== 0.65 band: named but unparseable ======================

// TestTemplateMatch_MidBandUnparseableSlot pins the 0.65 band: the
// total_cost capture matches by position but its int parser refuses
// the captured tokens. Verdict downgrades to ConfidenceTemplateMissingSlot
// and lists total_cost in MissingSlots.
func TestTemplateMatch_MidBandUnparseableSlot(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())
	v := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen for fjord")

	if v.Intent != "propose_purchase" {
		t.Fatalf("Intent: got %q, want propose_purchase", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateMissingSlot {
		t.Errorf("Confidence: got %v, want %v (mid-band)",
			v.Confidence, ConfidenceTemplateMissingSlot)
	}
	if got, want := normalizeSpaces(v.Slots["items"].(string)), "6 oxen"; got != want {
		t.Errorf("Slots[items]: got %q, want %q", got, want)
	}
	if _, has := v.Slots["total_cost"]; has {
		t.Errorf("Slots[total_cost]: got %v, want absent (unparseable)", v.Slots["total_cost"])
	}
	if !reflect.DeepEqual(v.MissingSlots, []string{"total_cost"}) {
		t.Errorf("MissingSlots: got %v, want [total_cost]", v.MissingSlots)
	}
	if !strings.HasPrefix(v.MatchReason, "template_partial:") {
		t.Errorf("MatchReason: got %q, want template_partial:<source>", v.MatchReason)
	}
}

// ====================== most-specific-wins ======================

// TestTemplateMatch_MostSpecificWins pins the within-intent tie-break:
// when two templates from the same intent both match, the one with
// the most filled slots wins.
func TestTemplateMatch_MostSpecificWins(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())
	// Input matches both:
	//   - "purchase {items}"            — 1 slot filled
	//   - "buy {items} for {total_cost}" doesn't match (no "for"+amount)
	// And also:
	//   - "spend {total_cost} on {items}" doesn't match (no "spend")
	// So we craft an input that matches both a 1-slot and a 2-slot
	// template on the SAME intent. The 2-slot must win.
	v := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen and 200 lbs food for 240")

	// The 2-slot template ("buy ... for ...") wins over any
	// less-specific match on the same intent.
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v (2-slot template wins)",
			v.Confidence, ConfidenceTemplateAllSlots)
	}
	if len(v.Slots) != 2 {
		t.Errorf("Slots: got %d, want 2 (items + total_cost)", len(v.Slots))
	}
	if !strings.HasPrefix(v.MatchReason, "template:buy ") {
		t.Errorf("MatchReason: got %q, want template:buy ...", v.MatchReason)
	}
}

// TestTemplateMatch_MostSpecificWins_TieBreakByOrder pins the
// declaration-order tie-break when two templates on the same intent
// both produce the same fill count.
func TestTemplateMatch_MostSpecificWins_TieBreakByOrder(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"echo": {
			Synonyms: []string{
				"say {first}",  // declared first
				"say {second}", // declared second
			},
			Slots: map[string]app.Slot{
				"first":  {Type: "string"},
				"second": {Type: "string"},
			},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any", []string{"echo"}, "say hello world")
	// Both templates fill exactly 1 slot. The first-declared wins.
	if got, want := v.MatchReason, "template:say {first}"; got != want {
		t.Errorf("MatchReason: got %q, want %q (declaration order wins)",
			got, want)
	}
	if _, has := v.Slots["first"]; !has {
		t.Errorf("Slots[first]: missing; declaration-order template should fill 'first'")
	}
}

// ====================== inter-intent tie ======================

// TestTemplateMatch_InterIntentTie pins the tie band: two intents matching
// the same input with the same fill count → ConfidenceTie (0.50)
// with both candidates in the verdict.
func TestTemplateMatch_InterIntentTie(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"go_a": {
			Synonyms: []string{"head {direction}"},
			Slots:    map[string]app.Slot{"direction": {Type: "string"}},
		},
		"go_b": {
			Synonyms: []string{"head {bearing}"},
			Slots:    map[string]app.Slot{"bearing": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any", []string{"go_a", "go_b"}, "head north")
	if v.Confidence != ConfidenceTie {
		t.Fatalf("Confidence: got %v, want %v (inter-intent tie)",
			v.Confidence, ConfidenceTie)
	}
	if v.Intent != "" {
		t.Errorf("Intent: got %q, want empty (tie verdict)", v.Intent)
	}
	if len(v.Candidates) != 2 {
		t.Fatalf("Candidates: got %d, want 2", len(v.Candidates))
	}
	want := []string{"go_a", "go_b"}
	for i, w := range want {
		if v.Candidates[i].Intent != w {
			t.Errorf("Candidates[%d].Intent: got %q, want %q",
				i, v.Candidates[i].Intent, w)
		}
	}
}

// ====================== string slot without a parser ======================

// TestTemplateMatch_StringSlotNoParser pins the string-slot-no-parser rule: a string-typed slot
// with no further parser specialisation has its captured raw text
// taken as the slot value, joined with single spaces and preserving
// original surface case.
func TestTemplateMatch_StringSlotNoParser(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"name_wagon": {
			Synonyms: []string{"name the wagon {wagon_name}"},
			Slots:    map[string]app.Slot{"wagon_name": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"name_wagon"},
		"name the wagon the rolling thunder")
	if v.Intent != "name_wagon" {
		t.Fatalf("Intent: got %q, want name_wagon", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Errorf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	got, _ := v.Slots["wagon_name"].(string)
	want := "the rolling thunder"
	if normalizeSpaces(got) != normalizeSpaces(want) {
		t.Errorf("Slots[wagon_name]: got %q, want %q", got, want)
	}
}

// ====================== empty input ======================

// TestTemplateMatch_EmptyInput pins the empty-input contract: zero
// Verdict, regardless of which templates the matcher knows about.
func TestTemplateMatch_EmptyInput(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())
	for _, in := range []string{"", "   ", "\t\n"} {
		v := mustMatch(t, m, "general_store",
			[]string{"propose_purchase"}, in)
		if v.Confidence != 0 || v.Intent != "" {
			t.Errorf("Match(%q): got %+v, want zero Verdict", in, v)
		}
	}
}

// ====================== leading / trailing capture forms ======================

// TestTemplateMatch_LeadingCapture pins that "{value} dollars" matches
// "240 dollars" by capturing 240 first.
func TestTemplateMatch_LeadingCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"price": {
			Synonyms: []string{"{value} dollars"},
			Slots:    map[string]app.Slot{"value": {Type: "int"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any", []string{"price"}, "240 dollars")
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	if got := v.Slots["value"]; got != 240 {
		t.Errorf("Slots[value]: got %v (%T), want 240 (int)", got, got)
	}
}

// TestTemplateMatch_TrailingCapture pins "spend money on {items}" →
// captures everything after "on".
func TestTemplateMatch_TrailingCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"spend money on {items}"},
			Slots:    map[string]app.Slot{"items": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"propose_purchase"},
		"spend money on 6 oxen and food")
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	got, _ := v.Slots["items"].(string)
	want := "6 oxen and food"
	if normalizeSpaces(got) != normalizeSpaces(want) {
		t.Errorf("Slots[items]: got %q, want %q", got, want)
	}
}

// ====================== ordering (positional contract) ======================

// TestTemplateMatch_PositionalNotBagStyle pins that templates are
// positional: shuffling the input word order does NOT match a
// template with the corresponding literal-then-capture shape.
//
// This is the explicit complement to TestProperty_OrderInvariance —
// the bare-string matcher IS order-invariant, but templates are NOT.
// Authors who want bag-style match keep using bare strings.
func TestTemplateMatch_PositionalNotBagStyle(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"buy {items} for {total_cost}"},
			Slots: map[string]app.Slot{
				"items":      {Type: "string"},
				"total_cost": {Type: "int"},
			},
		},
	})
	m := mustCompile(t, def)

	// In-order input: matches.
	v := mustMatch(t, m, "any",
		[]string{"propose_purchase"},
		"buy 6 oxen for 240")
	if v.Confidence == 0 {
		t.Fatalf("in-order baseline: expected match, got miss")
	}

	// Shuffled (positions broken): "for" before "buy" must NOT match.
	shuffled := mustMatch(t, m, "any",
		[]string{"propose_purchase"},
		"for 240 buy 6 oxen")
	if shuffled.Confidence != 0 {
		t.Errorf("shuffled input: templates must be positional; got %+v", shuffled)
	}
}

// ====================== idempotency ======================

// TestTemplateMatch_Idempotency pins that the matcher is a pure
// function of (def, allowed, input). Calling Match twice with the
// same arguments yields equal Verdicts.
func TestTemplateMatch_Idempotency(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())
	a := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen and 200 lbs food for 240")
	b := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy 6 oxen and 200 lbs food for 240")
	if !reflect.DeepEqual(a, b) {
		t.Errorf("non-idempotent: a=%+v b=%+v", a, b)
	}
}

// TestTemplateMatch_ShuffledInOrderIsNotIdempotent — the contrapositive
// helper used by docs: shuffling the *input* should NOT preserve the
// Verdict on a template. (This is a smoke test that the positional
// behaviour is *strict* enough; if a future refactor weakens
// positionality the table-driven assertion below will catch the
// regression.)
func TestTemplateMatch_OrderSensitivity(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, proposePurchaseApp())

	base := "buy 6 oxen for 240"
	baseV := mustMatch(t, m, "any", []string{"propose_purchase"}, base)
	if baseV.Confidence == 0 {
		t.Fatalf("baseline (%q): expected match", base)
	}

	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 8; trial++ {
		toks := strings.Fields(base)
		r.Shuffle(len(toks), func(a, b int) { toks[a], toks[b] = toks[b], toks[a] })
		shuf := strings.Join(toks, " ")
		if shuf == base {
			continue
		}
		v := mustMatch(t, m, "any", []string{"propose_purchase"}, shuf)
		// Either the shuffled input misses entirely OR it captures
		// different slot values — what it MUST NOT do is yield the
		// same Verdict as the in-order input.
		if reflect.DeepEqual(v, baseV) {
			t.Errorf("shuffled %q matched identically to baseline %q (templates lost positionality)",
				shuf, base)
		}
	}
}

// TestTemplateMatch_AllowedFilter pins that a non-allowed intent's
// templates are not considered.
func TestTemplateMatch_AllowedFilter(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"buy {items}"},
			Slots:    map[string]app.Slot{"items": {Type: "string"}},
		},
		"sell_supplies": {
			Synonyms: []string{"sell {items}"},
			Slots:    map[string]app.Slot{"items": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "store",
		[]string{"sell_supplies"}, // propose_purchase not allowed
		"buy 6 oxen")
	if v.Confidence != 0 {
		t.Errorf("non-allowed intent must not match: got %+v", v)
	}
}

// TestTemplateMatch_BareStringWinsOverTemplate — when a bare-string
// synonym already matched at Confidence 0.90, the template path is
// skipped entirely. Pins the band ordering.
func TestTemplateMatch_BareStringWinsOverTemplate(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"hunt": {
			// Bare synonym matches stem-bag, template would also match
			// a different captured run. The bare-string 0.90 must win.
			Synonyms: []string{"hunt", "look for {prey}"},
			Slots:    map[string]app.Slot{"prey": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any", []string{"hunt"}, "hunt")
	if v.Confidence != ConfidenceWholeSynonym {
		t.Errorf("Confidence: got %v, want %v (bare-string wins)",
			v.Confidence, ConfidenceWholeSynonym)
	}
}

// ====================== stopwords between literal stems (C1 fix) ======================
//
// These cases pin the fix to a bug where findLiteralAnchor returned
// only the *start* index of the matched literal run but the caller
// advanced `cursor` by `len(literalStems)`. When the matcher skipped
// stopwords between literal stems (rule 2), the skipped positions
// were not counted in literalStems, so the cursor landed inside the
// consumed literal and the next capture inherited the leftover token.
//
// Reproducer from the reviewer: template "across river {x}", input
// "across the river dog" used to capture {x="river dog"} instead of
// {x="dog"}.
//
// The fix returns the END index (one past the last consumed input
// token) from findLiteralAnchor, so the cursor lands strictly past
// every token the matcher consumed — including any inline stopwords.

// TestTemplateMatch_StopwordBetweenLiterals_Single is the canonical
// reviewer reproducer: one stopword "the" sits between two literal
// stems "across" and "river"; the trailing capture must NOT inherit
// either of them.
func TestTemplateMatch_StopwordBetweenLiterals_Single(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"cross": {
			Synonyms: []string{"across river {x}"},
			Slots:    map[string]app.Slot{"x": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"cross"},
		"across the river dog")
	if v.Intent != "cross" {
		t.Fatalf("Intent: got %q, want cross", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	got, _ := v.Slots["x"].(string)
	if normalizeSpaces(got) != "dog" {
		t.Errorf("Slots[x]: got %q, want %q (stopword \"the\" must be absorbed by the literal, not leaked into the capture)",
			got, "dog")
	}
}

// TestTemplateMatch_StopwordBetweenLiterals_Multi exercises a
// multi-stem literal segment with several stopwords stacked between
// its stems. Template "buy from store {items} for {total_cost}" —
// the first literal segment is the three-stem run "buy from store"
// and the test input wedges two stopwords inside it. The C1 fix
// guarantees that all five consumed input positions (3 stems + 2
// skipped stopwords) advance the cursor, so the {items} capture
// starts at the right position and slotparse.ParseInt picks up
// only the 240 for total_cost.
func TestTemplateMatch_StopwordBetweenLiterals_Multi(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"buy from store {items} for {total_cost}"},
			Slots: map[string]app.Slot{
				"items":      {Type: "string"},
				"total_cost": {Type: "int"},
			},
		},
	})
	m := mustCompile(t, def)
	// Input weaves "at" and "the" between "buy", "from" and "store"
	// — both stopwords. The literal run absorbs all five tokens; the
	// {items} capture starts at "6". "and" / "of" inside the items
	// capture stay in the capture surface (joinSurfaces preserves
	// them). "$240" is the money form ParseInt picks up via the
	// existing $-prefix path.
	v := mustMatch(t, m, "general_store",
		[]string{"propose_purchase"},
		"buy at the from store 6 oxen and 200 lbs of food for $240")
	if v.Intent != "propose_purchase" {
		t.Fatalf("Intent: got %q, want propose_purchase", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	gotItems, _ := v.Slots["items"].(string)
	wantItems := "6 oxen and 200 lbs of food"
	if normalizeSpaces(gotItems) != normalizeSpaces(wantItems) {
		t.Errorf("Slots[items]: got %q, want %q (literal run must absorb 'at the' between its stems, not leak them into items)",
			gotItems, wantItems)
	}
	gotCost, ok := v.Slots["total_cost"].(int)
	if !ok || gotCost != 240 {
		t.Errorf("Slots[total_cost]: got %v (%T), want int(240)",
			v.Slots["total_cost"], v.Slots["total_cost"])
	}
}

// TestTemplateMatch_StopwordBetweenLiterals_LeadingCapture pins that
// the literal's start anchor remains correct when there is a capture
// before the literal AND stopwords inside the literal. The leading
// capture should swallow tokens up to (but not into) the literal,
// regardless of how many stopwords sit inside the literal run.
func TestTemplateMatch_StopwordBetweenLiterals_LeadingCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"send": {
			Synonyms: []string{"{x} across the river"},
			Slots:    map[string]app.Slot{"x": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"send"},
		"send dog across the river")
	if v.Intent != "send" {
		t.Fatalf("Intent: got %q, want send", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	got, _ := v.Slots["x"].(string)
	want := "send dog"
	if normalizeSpaces(got) != normalizeSpaces(want) {
		t.Errorf("Slots[x]: got %q, want %q", got, want)
	}
}

// TestTemplateMatch_StopwordBetweenLiterals_NoCapture pins that a
// captures-free template ("across river" — no {slot}) still matches
// an input with stopwords between its literal stems. Templates
// without captures aren't actually accepted by compileTemplate
// (they're treated as bare-string synonyms), so we exercise the
// equivalent path via Synonyms ["across river"] and confirm the
// 0.90 whole-synonym band on input "across the river".
func TestTemplateMatch_StopwordBetweenLiterals_NoCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"cross": {
			// No {slot} → compileBareSynonym handles this. The
			// 0.90 band is the correct whole-utterance synonym hit;
			// the test guards against any regression in the
			// stopword-tolerant subset matching that the bare-string
			// path inherits.
			Synonyms: []string{"across river"},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"cross"},
		"across the river")
	if v.Intent != "cross" {
		t.Fatalf("Intent: got %q, want cross", v.Intent)
	}
	if v.Confidence != ConfidenceWholeSynonym {
		t.Errorf("Confidence: got %v, want %v (bare-string synonym, stopword absorbed)",
			v.Confidence, ConfidenceWholeSynonym)
	}
}

// TestTemplateMatch_StopwordBetweenLiterals_AdversarialMultiStopword
// pins the chosen behaviour for the "stopword soup inside a capture"
// case: template "go {direction} fast" against input
// "go and then quickly to the south fast".
//
// The matcher anchors on "go" (literal), then seeks "fast" (literal).
// Every token between the two anchors (including non-stopword fillers
// like "quickly" and "then") becomes part of the {direction} capture
// because rule (2) — "skip non-matching stopwords" — only fires when
// the matcher is scanning FOR a literal stem at the current `want[k]`.
// Inside a capture range there is no `want[k]` to match against; the
// span is verbatim. So {direction} = "and then quickly to the south".
//
// This is the documented "greedy to the right" interpretation: each
// literal lands at the EARLIEST position consistent with the prior
// anchor, and the preceding capture takes the shortest run that
// satisfies the next literal. Here "fast" only appears once, so the
// capture run is whatever sits between "go" and "fast" verbatim,
// stopwords included.
func TestTemplateMatch_StopwordBetweenLiterals_AdversarialMultiStopword(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"go_dir": {
			Synonyms: []string{"go {direction} fast"},
			Slots:    map[string]app.Slot{"direction": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "any",
		[]string{"go_dir"},
		"go and then quickly to the south fast")
	if v.Intent != "go_dir" {
		t.Fatalf("Intent: got %q, want go_dir", v.Intent)
	}
	if v.Confidence != ConfidenceTemplateAllSlots {
		t.Fatalf("Confidence: got %v, want %v", v.Confidence, ConfidenceTemplateAllSlots)
	}
	got, _ := v.Slots["direction"].(string)
	// Stopwords inside a capture are preserved verbatim via
	// joinSurfaces (string-slot-no-parser rule). Pinning the exact phrase guards against
	// future "strip stopwords inside captures" regressions.
	want := "and then quickly to the south"
	if normalizeSpaces(got) != normalizeSpaces(want) {
		t.Errorf("Slots[direction]: got %q, want %q (capture verbatim, stopwords preserved)",
			got, want)
	}
}

// ====================== property test: stopword-insertion invariance ======================

// TestProperty_StopwordInsertionInvariance asserts that inserting a
// stopword at any position in a baseline-matching sentence (except
// inside the captured run) does not change the verdict for the slot
// `{x}` in a leading-literal template. The baseline phrase
// "across river dog" tokenises to ("across","river","dog"); we pick
// 50 random insertion points in the prefix "across river" range and
// confirm {x:"dog"} survives every one.
func TestProperty_StopwordInsertionInvariance(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"cross": {
			Synonyms: []string{"across river {x}"},
			Slots:    map[string]app.Slot{"x": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)

	// Stopwords are taken from a representative subset of the
	// builtin English list (see internal/lex). We deliberately pick
	// ones that don't collide with the literals or "dog".
	stopwords := []string{"the", "a", "an", "is", "of", "to"}
	// Tokens in the literal prefix where inserting a stopword should
	// be safe. We insert AT position 1 only — between literal stems
	// "across" and "river" — which is the position the C1 fix
	// addresses (rule 2 stopword skip during the literal-anchor
	// walk).
	//
	// Baseline: ["across", "river", "dog"]
	// Position 0: before "across" — there's no leading capture, so
	//   the template can't match if anything precedes the first
	//   literal (matchTemplate's `anchor != cursor` guard).
	// Position 2: between "river" and the capture — this is INSIDE
	//   the captured run (per the "(except inside the
	//   captured run)" exclusion); stopwords here are preserved
	//   verbatim by joinSurfaces (string-slot-no-parser rule) and become part of the slot
	//   text. Test TestTemplateMatch_StringSlotNoParser pins this.
	// Position 3: after "dog" — trailing-stopword exception path,
	//   orthogonal to C1.
	//
	// So position 1 is the sole property-test target: rule (2)
	// stopword absorption between two literal stems of the SAME
	// literal segment.
	insertIdxRange := []int{1}

	baselineTokens := []string{"across", "river", "dog"}

	r := rand.New(rand.NewSource(0xC1FE)) // C1FE = "C1 fix"
	for trial := 0; trial < 50; trial++ {
		toks := append([]string(nil), baselineTokens...)
		// Pick how many stopwords to splice in (1-3) and where.
		nInserts := 1 + r.Intn(3)
		for k := 0; k < nInserts; k++ {
			pos := insertIdxRange[r.Intn(len(insertIdxRange))]
			sw := stopwords[r.Intn(len(stopwords))]
			toks = append(toks[:pos], append([]string{sw}, toks[pos:]...)...)
		}
		input := strings.Join(toks, " ")
		v := mustMatch(t, m, "any", []string{"cross"}, input)
		if v.Intent != "cross" {
			t.Errorf("trial %d input %q: Intent got %q, want cross",
				trial, input, v.Intent)
			continue
		}
		if v.Confidence != ConfidenceTemplateAllSlots {
			t.Errorf("trial %d input %q: Confidence got %v, want %v",
				trial, input, v.Confidence, ConfidenceTemplateAllSlots)
			continue
		}
		got, _ := v.Slots["x"].(string)
		if normalizeSpaces(got) != "dog" {
			t.Errorf("trial %d input %q: Slots[x] got %q, want %q (stopword inserted before capture must not leak in)",
				trial, input, got, "dog")
		}
	}
}

// TestTemplateMatch_DeterministicMissingSlotsOrder — when multiple
// captures are unparseable, MissingSlots is sorted so test assertions
// remain stable across map-iteration orders.
func TestTemplateMatch_DeterministicMissingSlotsOrder(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"two_ints": {
			Synonyms: []string{"set {a} and {b}"},
			Slots: map[string]app.Slot{
				"a": {Type: "int"},
				"b": {Type: "int"},
			},
		},
	})
	m := mustCompile(t, def)
	// Both captures will fail to parse as int (nothing digit-like).
	v := mustMatch(t, m, "any", []string{"two_ints"}, "set foo and bar")
	want := []string{"a", "b"}
	if !sort.StringsAreSorted(v.MissingSlots) {
		t.Errorf("MissingSlots not sorted: %v", v.MissingSlots)
	}
	if !reflect.DeepEqual(v.MissingSlots, want) {
		t.Errorf("MissingSlots: got %v, want %v", v.MissingSlots, want)
	}
}
