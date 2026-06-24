// Tests for [Matcher] and [Compile] (band ordering / bare-string
// matching).
//
// Structure:
//   - "compile"    — happy path + template-syntax rejection + empty defs.
//   - "match"      — single-intent hit, tie, allowed filter, empty input,
//     examples-as-implicit-synonyms, stopwords_extra.
//   - "properties" — order-invariance, stopword insensitivity, repeated calls.
//
// Section banners use the `// =====` style established by
// internal/turncache/memory_test.go so the file stays scannable.
package semroute

import (
	"context"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// ====================== helpers ======================

// mkApp builds a minimal AppDef with the given intent definitions. The
// helper centralises the common shape so each test case is one line of
// "intent → synonyms+examples" and not a wall of YAML-shaped structs.
func mkApp(t *testing.T, intents map[string]app.Intent) *app.AppDef {
	t.Helper()
	return &app.AppDef{
		App:     app.AppMeta{ID: "test-app", Version: "v0"},
		Intents: intents,
	}
}

// mkAppWithStops is mkApp + a per-app StopwordsExtra list.
func mkAppWithStops(t *testing.T, intents map[string]app.Intent, stops []string) *app.AppDef {
	t.Helper()
	rc := app.DefaultRoutingConfig()
	rc.StopwordsExtra = stops
	return &app.AppDef{
		App:     app.AppMeta{ID: "test-app", Version: "v0"},
		Intents: intents,
		Routing: &rc,
	}
}

// mustCompile compiles def with Fatal-on-error semantics. Tests that
// expect Compile to *fail* call Compile directly instead.
func mustCompile(t *testing.T, def *app.AppDef) *Matcher {
	t.Helper()
	m, err := Compile(def)
	if err != nil {
		t.Fatalf("Compile: unexpected error: %v", err)
	}
	if m == nil {
		t.Fatalf("Compile: returned nil Matcher with no error")
	}
	return m
}

// mustMatch runs Match with a fresh context and fail-on-error.
func mustMatch(t *testing.T, m *Matcher, state string, allowed []string, input string) Verdict {
	t.Helper()
	v, err := m.Match(context.Background(), state, allowed, input)
	if err != nil {
		t.Fatalf("Match(state=%q, allowed=%v, input=%q): unexpected error %v",
			state, allowed, input, err)
	}
	return v
}

// allowAll is the canonical "every intent is allowed" sentinel for
// tests that aren't exercising the allow-filter — pass nil. Distinct
// helper to make the intent at the call site obvious.
var allowAll []string

// ====================== compile ======================

func TestCompile_NilAppDef(t *testing.T) {
	t.Parallel()
	m, err := Compile(nil)
	if err != nil {
		t.Fatalf("Compile(nil): want no error, got %v", err)
	}
	if m == nil {
		t.Fatalf("Compile(nil): want non-nil Matcher")
	}
	if !m.IsEmpty() {
		t.Errorf("Compile(nil): want IsEmpty=true, got false")
	}
}

func TestCompile_EmptyIntents(t *testing.T) {
	t.Parallel()
	m := mustCompile(t, mkApp(t, nil))
	if !m.IsEmpty() {
		t.Errorf("Compile(empty intents): want IsEmpty=true")
	}
}

// TestCompile_TemplateStructuralErrors pins the structural failure
// modes for template synonyms. Phase 4 lifted the blanket Phase-2
// rejection of `{`/`}` strings; what remains are the structural rules
// the compiler enforces (the template grammar): every capture names a known
// slot, captures must be separated by literals, and braces must
// balance.
func TestCompile_TemplateStructuralErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		syn          string
		slots        map[string]app.Slot
		wantInReason string
	}{
		{
			name:         "unknown slot",
			syn:          "buy {items}",
			slots:        nil, // no slots declared
			wantInReason: "unknown slot \"items\"",
		},
		{
			name:         "close-brace alone",
			syn:          "buy items}",
			slots:        map[string]app.Slot{"items": {Type: "string"}},
			wantInReason: "unmatched '}'",
		},
		{
			name:         "open-brace alone",
			syn:          "buy {items for cost",
			slots:        map[string]app.Slot{"items": {Type: "string"}, "cost": {Type: "int"}},
			wantInReason: "unmatched '{'",
		},
		{
			name:         "empty capture",
			syn:          "buy {} for cost",
			slots:        map[string]app.Slot{},
			wantInReason: "empty capture",
		},
		{
			name:         "adjacent captures",
			syn:          "buy {items}{cost}",
			slots:        map[string]app.Slot{"items": {Type: "string"}, "cost": {Type: "int"}},
			wantInReason: "literal token between captures",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := mkApp(t, map[string]app.Intent{
				"buy": {Synonyms: []string{tc.syn}, Slots: tc.slots},
			})
			_, err := Compile(def)
			if err == nil {
				t.Fatalf("Compile(synonym=%q): want CompileError, got nil", tc.syn)
			}
			var ce *CompileError
			if !errors.As(err, &ce) {
				t.Fatalf("Compile(synonym=%q): want *CompileError, got %T: %v",
					tc.syn, err, err)
			}
			if ce.Intent != "buy" {
				t.Errorf("CompileError.Intent: got %q, want %q", ce.Intent, "buy")
			}
			if ce.Synonym != tc.syn {
				t.Errorf("CompileError.Synonym: got %q, want %q", ce.Synonym, tc.syn)
			}
			if !strings.Contains(ce.Reason, tc.wantInReason) {
				t.Errorf("CompileError.Reason: %q does not contain %q",
					ce.Reason, tc.wantInReason)
			}
		})
	}
}

// TestCompile_EmptyAndStopwordOnlySynonyms verifies that a synonym
// reducing to an empty stem set (empty string, pure whitespace,
// all stopwords) is silently skipped — the loader owns the
// "non-empty synonym" validation; semroute just doesn't index them.
func TestCompile_EmptyAndStopwordOnlySynonyms(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {
			Synonyms: []string{"", "   ", "the and to", "wade"},
		},
	})
	m := mustCompile(t, def)
	if m.IsEmpty() {
		t.Fatalf("Compile: want non-empty (wade is a real synonym), got IsEmpty=true")
	}
	v := mustMatch(t, m, "river_crossing", allowAll, "wade")
	if v.Intent != "ford" || v.Confidence != ConfidenceWholeSynonym {
		t.Errorf("Match(wade): want ford@0.90, got %+v", v)
	}
}

// ====================== match ======================

// TestMatch_SingleSynonymExactWholeUtterance pins the bare-synonym
// worked example: synonym "wade" matches input "wade" exactly.
func TestMatch_SingleSynonymExactWholeUtterance(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade"}},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "river_crossing", []string{"ford"}, "wade")

	if v.Intent != "ford" {
		t.Errorf("Intent: got %q, want %q (input=%q)", v.Intent, "ford", "wade")
	}
	if v.Confidence != ConfidenceWholeSynonym {
		t.Errorf("Confidence: got %v, want %v (input=%q)",
			v.Confidence, ConfidenceWholeSynonym, "wade")
	}
	if v.MatchReason != "synonym:wade" {
		t.Errorf("MatchReason: got %q, want %q (input=%q)",
			v.MatchReason, "synonym:wade", "wade")
	}
	if len(v.Slots) != 0 {
		t.Errorf("Slots: got %v, want empty (Phase 2 carries no slots)", v.Slots)
	}
	if len(v.Candidates) != 0 {
		t.Errorf("Candidates: got %v, want empty (single hit)", v.Candidates)
	}
}

// TestMatch_SynonymInsideLongerUtterance pins the bare-synonym
// trace: "wade across the river" carries synonym stem {wade} ⊂
// {wade, acros, river}.
func TestMatch_SynonymInsideLongerUtterance(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade"}},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "river_crossing", []string{"ford"}, "let's wade across the river")
	if v.Intent != "ford" {
		t.Errorf("Intent: got %q, want %q (input=%q)",
			v.Intent, "ford", "let's wade across the river")
	}
	if v.Confidence != ConfidenceWholeSynonym {
		t.Errorf("Confidence: got %v, want %v", v.Confidence, ConfidenceWholeSynonym)
	}
}

// TestMatch_BareSynonymRejectsLargePaste is the dogfood regression: a
// pasted bug report that merely CONTAINS a one-word synonym ("cancel",
// an example/synonym of quit) somewhere in a wall of prose must NOT
// bare-match that intent. In the field this short-circuited a clarifying
// room straight to the quit intent → @exit → "game over", because the
// user had pasted the choice footer "… Esc cancel …". The whole-utterance
// guard (bareMaxUncoveredDefault) rejects it; the input then falls
// through to the LLM router. Without the guard this returns quit@0.90.
func TestMatch_BareSynonymRejectsLargePaste(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"quit": {Synonyms: []string{"cancel", "quit", "abort", "bail"}},
		"look": {Synonyms: []string{"look"}},
	})
	m := mustCompile(t, def)

	paste := "when we resume a session the kitsoki header is shown again " +
		"and it is highlighted as model output move enter pick tab esc cancel " +
		strings.Repeat("filler prose describing the rendering defect in detail ", 8)

	v := mustMatch(t, m, "clarifying", []string{"quit", "look"}, paste)
	if v.Confidence != 0 {
		t.Errorf("large paste containing 'cancel' should NOT bare-match; got intent=%q confidence=%v",
			v.Intent, v.Confidence)
	}
}

// TestMatch_BareSynonymShortStillMatches guards against over-correction:
// the whole-utterance bound must not break the common case where the
// synonym is (nearly) the entire utterance.
func TestMatch_BareSynonymShortStillMatches(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"quit": {Synonyms: []string{"cancel", "quit"}},
	})
	m := mustCompile(t, def)
	for _, in := range []string{"cancel", "please cancel that", "ok cancel"} {
		v := mustMatch(t, m, "clarifying", []string{"quit"}, in)
		if v.Intent != "quit" || v.Confidence != ConfidenceWholeSynonym {
			t.Errorf("Match(%q): want quit@%v, got intent=%q conf=%v",
				in, ConfidenceWholeSynonym, v.Intent, v.Confidence)
		}
	}
}

// TestMatch_TwoIntentsShareSynonym pins the shared-synonym tie: when "leave" matches
// both leave_store and cancel_purchase, the verdict carries
// Confidence=0.50 + the candidate list.
func TestMatch_TwoIntentsShareSynonym(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"leave_store":     {Synonyms: []string{"leave"}},
		"cancel_purchase": {Synonyms: []string{"leave"}},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "general_store", []string{"leave_store", "cancel_purchase"}, "leave")
	if v.Confidence != ConfidenceTie {
		t.Errorf("Confidence: got %v, want %v (tie)", v.Confidence, ConfidenceTie)
	}
	if v.Intent != "" {
		t.Errorf("Intent: got %q, want empty (tie verdict carries Candidates)", v.Intent)
	}
	if len(v.Candidates) != 2 {
		t.Fatalf("Candidates: got %d, want 2", len(v.Candidates))
	}
	// Sort-and-compare so the assertion is order-insensitive but we
	// also assert the matcher orders deterministically.
	got := []string{v.Candidates[0].Intent, v.Candidates[1].Intent}
	if !sort.StringsAreSorted(got) {
		t.Errorf("Candidates not sorted: %v", got)
	}
	want := []string{"cancel_purchase", "leave_store"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Candidates[%d].Intent: got %q, want %q", i, got[i], w)
		}
	}
}

// TestMatch_NoIntentMatches asserts the zero-Verdict miss path.
func TestMatch_NoIntentMatches(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade"}},
	})
	m := mustCompile(t, def)
	v := mustMatch(t, m, "river_crossing", []string{"ford"}, "fly across with a balloon")
	if v.Confidence != 0 {
		t.Errorf("Confidence: got %v, want 0 (no match)", v.Confidence)
	}
	if v.Intent != "" {
		t.Errorf("Intent: got %q, want empty (miss)", v.Intent)
	}
	if len(v.Candidates) != 0 {
		t.Errorf("Candidates: got %d, want 0 (miss)", len(v.Candidates))
	}
}

// TestMatch_AllowedFilter asserts a synonym on a non-allowed intent
// does NOT trigger — even if it would otherwise be a clean hit.
func TestMatch_AllowedFilter(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford":  {Synonyms: []string{"wade"}},
		"caulk": {Synonyms: []string{"seal"}},
	})
	m := mustCompile(t, def)
	// Only caulk is allowed; wade should NOT route to ford.
	v := mustMatch(t, m, "river_crossing", []string{"caulk"}, "wade across")
	if v.Confidence != 0 {
		t.Errorf("Confidence: got %v, want 0 (ford not allowed)", v.Confidence)
	}
}

// TestMatch_EmptyInput asserts whitespace-only input returns zero Verdict.
func TestMatch_EmptyInput(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade"}},
	})
	m := mustCompile(t, def)
	for _, input := range []string{"", "   ", "\t\n"} {
		v := mustMatch(t, m, "river_crossing", []string{"ford"}, input)
		if v.Confidence != 0 {
			t.Errorf("Match(%q): want zero Verdict, got %+v", input, v)
		}
	}
}

// TestMatch_ExamplesAreImplicitSynonyms asserts that intents with no
// declared synonyms can still match via their Examples list, with the
// MatchReason carrying the "example:" tag.
func TestMatch_ExamplesAreImplicitSynonyms(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"continue": {
			Examples: []string{"press on", "keep going"},
		},
	})
	m := mustCompile(t, def)

	cases := []struct {
		input      string
		wantReason string
	}{
		{"press on", "example:press on"},
		{"keep going", "example:keep going"},
		{"let's press on now", "example:press on"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			v := mustMatch(t, m, "leg_a_executing", []string{"continue"}, tc.input)
			if v.Intent != "continue" {
				t.Errorf("Intent(%q): got %q, want %q", tc.input, v.Intent, "continue")
			}
			if v.MatchReason != tc.wantReason {
				t.Errorf("MatchReason(%q): got %q, want %q",
					tc.input, v.MatchReason, tc.wantReason)
			}
			if v.Confidence != ConfidenceWholeSynonym {
				t.Errorf("Confidence(%q): got %v, want %v",
					tc.input, v.Confidence, ConfidenceWholeSynonym)
			}
		})
	}
}

// TestMatch_PerAppStopwordsExtra asserts that a per-app stopword
// filters out an otherwise-content word from BOTH the synonym index
// and the input bag — so an intent declared as "wagon: leave" still
// matches input "leave the wagon" even though "wagon" was a content
// word before the StopwordsExtra was applied.
func TestMatch_PerAppStopwordsExtra(t *testing.T) {
	t.Parallel()
	intents := map[string]app.Intent{
		"abandon_wagon": {Synonyms: []string{"abandon"}},
	}
	// Without the stopword, the synonym set is {abandon}; input
	// "abandon the wagon" has bag {abandon, wagon}, which is a
	// proper superset → hit. The interesting case is when the
	// synonym INCLUDES the stop-able word: "wagon abandon" should
	// reduce to {abandon} once "wagon" is dropped.
	intents["abandon_wagon"] = app.Intent{Synonyms: []string{"wagon abandon"}}
	def := mkAppWithStops(t, intents, []string{"wagon"})
	m := mustCompile(t, def)

	v := mustMatch(t, m, "trail", []string{"abandon_wagon"}, "abandon")
	if v.Intent != "abandon_wagon" {
		t.Errorf("Intent(abandon): got %q, want abandon_wagon "+
			"(stopword should reduce synonym 'wagon abandon' → {abandon})",
			v.Intent)
	}
}

// ====================== properties ======================

// TestProperty_OrderInvariance — shuffling content tokens of an input
// never changes the Verdict (because the matcher operates on stem
// SETS, not sequences).
func TestProperty_OrderInvariance(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {Synonyms: []string{"buy food oxen"}},
	})
	m := mustCompile(t, def)

	base := []string{"buy", "food", "oxen", "now"}
	r := rand.New(rand.NewSource(42))

	const trials = 20
	var first Verdict
	for i := 0; i < trials; i++ {
		shuffled := append([]string(nil), base...)
		r.Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		input := strings.Join(shuffled, " ")
		v := mustMatch(t, m, "store", []string{"propose_purchase"}, input)
		if i == 0 {
			first = v
		}
		if v.Intent != first.Intent || v.Confidence != first.Confidence {
			t.Errorf("trial %d: shuffled input %q yielded %+v, first yielded %+v",
				i, input, v, first)
		}
	}
}

// TestProperty_StopwordInsensitivity — inserting "please" and "let's"
// (both stopwords) anywhere in an input never changes the Verdict.
func TestProperty_StopwordInsensitivity(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade across"}},
	})
	m := mustCompile(t, def)

	base := mustMatch(t, m, "river", []string{"ford"}, "wade across")
	if base.Intent != "ford" {
		t.Fatalf("baseline did not hit: %+v", base)
	}

	insertions := []string{
		"please wade across",
		"wade please across",
		"wade across please",
		"let's wade across",
		"wade let's across",
		"please let's wade across",
	}
	for _, input := range insertions {
		v := mustMatch(t, m, "river", []string{"ford"}, input)
		if v.Intent != base.Intent || v.Confidence != base.Confidence {
			t.Errorf("input=%q yielded %+v, baseline yielded %+v", input, v, base)
		}
	}
}

// TestMatch_NilMatcherIsSafe asserts a nil receiver behaves like
// IsEmpty=true — orchestrator wiring relies on this for "routing
// disabled" defaults.
func TestMatch_NilMatcherIsSafe(t *testing.T) {
	t.Parallel()
	var m *Matcher
	v, err := m.Match(context.Background(), "any", nil, "anything")
	if err != nil {
		t.Errorf("nil matcher Match: unexpected error %v", err)
	}
	if v.Confidence != 0 {
		t.Errorf("nil matcher Match: want zero Verdict, got %+v", v)
	}
	if !m.IsEmpty() {
		t.Errorf("nil matcher IsEmpty: got false, want true")
	}
}

// TestMatch_ContextCancellation surfaces cancel as a Match error
// (the only error path semroute owns today).
func TestMatch_ContextCancellation(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"ford": {Synonyms: []string{"wade"}},
	})
	m := mustCompile(t, def)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Match(ctx, "river", []string{"ford"}, "wade")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Match with cancelled ctx: got err=%v, want context.Canceled", err)
	}
}

// TestMatch_StateLocalIntents asserts intents declared inside states
// (not just at def.Intents) are indexed too. This matters because
// state-machine apps often declare intents inline next to the state
// that uses them.
func TestMatch_StateLocalIntents(t *testing.T) {
	t.Parallel()
	def := &app.AppDef{
		App: app.AppMeta{ID: "test-app", Version: "v0"},
		States: map[string]*app.State{
			"river_crossing": {
				Intents: map[string]app.Intent{
					"ford": {Synonyms: []string{"wade"}},
				},
			},
		},
	}
	m := mustCompile(t, def)
	v := mustMatch(t, m, "river_crossing", []string{"ford"}, "wade")
	if v.Intent != "ford" {
		t.Errorf("state-local intent: got Intent=%q, want %q", v.Intent, "ford")
	}
}
