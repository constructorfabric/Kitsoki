// Template-compile tests for the Phase-4 `{slot_name}` capture
// syntax (the template grammar). Structural failure modes are covered in
// matcher_test.go's TestCompile_TemplateStructuralErrors; this file
// pins the happy-path shape of the compiled segments.
package semroute

import (
	"errors"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// templateForIntent returns the slice of compiled templates the index
// holds for intentID. Used by every test in this file as a peek into
// the (otherwise unexported) compiledIndex. Returns nil when the
// intent declared no templates.
func templateForIntent(t *testing.T, m *Matcher, intentID string) []*compiledTemplate {
	t.Helper()
	if m == nil || m.idx == nil {
		return nil
	}
	return m.idx.templates[intentID]
}

// ====================== happy-path shape ======================

// TestCompileTemplate_LiteralCaptureLiteral asserts a canonical
// "buy {items} for {total_cost}" template parses into a four-segment
// sequence (literal "buy" / capture items / literal "for" / capture
// total_cost) with stem-stripped literals and the slot names
// preserved verbatim.
func TestCompileTemplate_LiteralCaptureLiteral(t *testing.T) {
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
	tpls := templateForIntent(t, m, "propose_purchase")
	if len(tpls) != 1 {
		t.Fatalf("templates: got %d, want 1", len(tpls))
	}
	tpl := tpls[0]
	if tpl.intent != "propose_purchase" {
		t.Errorf("intent: got %q, want propose_purchase", tpl.intent)
	}
	if tpl.source != "buy {items} for {total_cost}" {
		t.Errorf("source: got %q, want %q", tpl.source, "buy {items} for {total_cost}")
	}
	if tpl.slotCount != 2 {
		t.Errorf("slotCount: got %d, want 2", tpl.slotCount)
	}
	if len(tpl.segments) != 4 {
		t.Fatalf("segments: got %d, want 4 (literal+capture+literal+capture)", len(tpl.segments))
	}
	// seg 0: literal "buy"
	if tpl.segments[0].isCapture() ||
		len(tpl.segments[0].literalStems) != 1 ||
		tpl.segments[0].literalStems[0] != "buy" {
		t.Errorf("segments[0]: want literal[buy], got %+v", tpl.segments[0])
	}
	// seg 1: capture {items}
	if !tpl.segments[1].isCapture() || tpl.segments[1].captureSlot != "items" {
		t.Errorf("segments[1]: want capture(items), got %+v", tpl.segments[1])
	}
	// seg 2: literal "for"
	if tpl.segments[2].isCapture() ||
		len(tpl.segments[2].literalStems) != 1 ||
		tpl.segments[2].literalStems[0] != "for" {
		t.Errorf("segments[2]: want literal[for], got %+v", tpl.segments[2])
	}
	// seg 3: capture {total_cost}
	if !tpl.segments[3].isCapture() || tpl.segments[3].captureSlot != "total_cost" {
		t.Errorf("segments[3]: want capture(total_cost), got %+v", tpl.segments[3])
	}
}

// TestCompileTemplate_LeadingCapture pins that "{x} more text"
// compiles to capture-then-literal. Leading captures are explicitly
// allowed by the template grammar.
//
// We use a non-stopword literal ("now") so the post-compile shape is
// unambiguous; template literals preserve stopwords as positional
// anchors (see compileLiteralStems), which is a separate property
// pinned by the worked example test.
func TestCompileTemplate_LeadingCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"reply": {
			Synonyms: []string{"{answer} now"},
			Slots:    map[string]app.Slot{"answer": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	tpls := templateForIntent(t, m, "reply")
	if len(tpls) != 1 {
		t.Fatalf("templates: got %d, want 1", len(tpls))
	}
	tpl := tpls[0]
	if len(tpl.segments) != 2 {
		t.Fatalf("segments: got %d, want 2 (leading capture + trailing literal)", len(tpl.segments))
	}
	if !tpl.segments[0].isCapture() || tpl.segments[0].captureSlot != "answer" {
		t.Errorf("segments[0]: want capture(answer), got %+v", tpl.segments[0])
	}
	if tpl.segments[1].isCapture() {
		t.Errorf("segments[1]: want literal, got capture(%s)", tpl.segments[1].captureSlot)
	}
}

// TestCompileTemplate_LeadingCaptureWithLiteral pins the non-stopword
// case: "{x} cost" compiles to two segments.
func TestCompileTemplate_LeadingCaptureWithLiteral(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"price": {
			Synonyms: []string{"{value} dollars"},
			Slots:    map[string]app.Slot{"value": {Type: "int"}},
		},
	})
	m := mustCompile(t, def)
	tpls := templateForIntent(t, m, "price")
	if len(tpls) != 1 || len(tpls[0].segments) != 2 {
		t.Fatalf("segments: want 2 (capture+literal), got tpls=%v", tpls)
	}
	if !tpls[0].segments[0].isCapture() {
		t.Errorf("segments[0]: want capture, got literal %v",
			tpls[0].segments[0].literalStems)
	}
	if tpls[0].segments[1].isCapture() {
		t.Errorf("segments[1]: want literal, got capture(%s)",
			tpls[0].segments[1].captureSlot)
	}
}

// TestCompileTemplate_TrailingCapture pins "spend money on {items}"
// compiles to literal+capture.
func TestCompileTemplate_TrailingCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"spend money on {items}"},
			Slots:    map[string]app.Slot{"items": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	tpls := templateForIntent(t, m, "propose_purchase")
	if len(tpls) != 1 {
		t.Fatalf("templates: got %d, want 1", len(tpls))
	}
	tpl := tpls[0]
	// "spend money on" → ["spend", "money"] after "on" drops as a stopword.
	if len(tpl.segments) != 2 {
		t.Fatalf("segments: got %d, want 2 (literal+capture); segments=%+v",
			len(tpl.segments), tpl.segments)
	}
	if tpl.segments[0].isCapture() {
		t.Errorf("segments[0]: want literal, got capture")
	}
	if !tpl.segments[1].isCapture() || tpl.segments[1].captureSlot != "items" {
		t.Errorf("segments[1]: want capture(items), got %+v", tpl.segments[1])
	}
}

// TestCompileTemplate_SingleCapture pins "{x}" alone compiles to a
// one-segment template (the whole-input capture).
func TestCompileTemplate_SingleCapture(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"echo": {
			Synonyms: []string{"{text}"},
			Slots:    map[string]app.Slot{"text": {Type: "string"}},
		},
	})
	m := mustCompile(t, def)
	tpls := templateForIntent(t, m, "echo")
	if len(tpls) != 1 || len(tpls[0].segments) != 1 {
		t.Fatalf("segments: want 1 (single capture), got tpls=%v", tpls)
	}
	if !tpls[0].segments[0].isCapture() || tpls[0].segments[0].captureSlot != "text" {
		t.Errorf("segments[0]: want capture(text), got %+v", tpls[0].segments[0])
	}
	if tpls[0].slotCount != 1 {
		t.Errorf("slotCount: got %d, want 1", tpls[0].slotCount)
	}
}

// TestCompileTemplate_UnknownSlot pins the template-grammar invariant: a capture
// must reference a declared slot. The error message must name both
// the intent and the offending slot.
func TestCompileTemplate_UnknownSlot(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"buy {nonsense} for {total_cost}"},
			Slots:    map[string]app.Slot{"total_cost": {Type: "int"}},
		},
	})
	_, err := Compile(def)
	if err == nil {
		t.Fatalf("Compile: want error, got nil")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("Compile: want *CompileError, got %T", err)
	}
	if ce.Intent != "propose_purchase" {
		t.Errorf("Intent: got %q, want propose_purchase", ce.Intent)
	}
	if !strings.Contains(ce.Synonym, "nonsense") {
		t.Errorf("Synonym: got %q, want it to contain the offending template", ce.Synonym)
	}
	if !strings.Contains(ce.Reason, "nonsense") {
		t.Errorf("Reason: got %q, want it to name the unknown slot", ce.Reason)
	}
}

// TestCompileTemplate_AdjacentCaptures pins the no-ambiguity rule.
func TestCompileTemplate_AdjacentCaptures(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{"buy {items}{total_cost}"},
			Slots: map[string]app.Slot{
				"items":      {Type: "string"},
				"total_cost": {Type: "int"},
			},
		},
	})
	_, err := Compile(def)
	if err == nil {
		t.Fatalf("Compile: want error, got nil")
	}
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("Compile: want *CompileError, got %T", err)
	}
	if !strings.Contains(ce.Reason, "literal token between captures") {
		t.Errorf("Reason: %q must mention the literal-between-captures rule", ce.Reason)
	}
}

// TestCompileTemplate_MultiplePerIntent pins that an intent can carry
// multiple templates and they preserve declaration order — load-
// bearing for the most-specific-wins tie-break.
func TestCompileTemplate_MultiplePerIntent(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
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
	})
	m := mustCompile(t, def)
	tpls := templateForIntent(t, m, "propose_purchase")
	if len(tpls) != 3 {
		t.Fatalf("templates: got %d, want 3", len(tpls))
	}
	wantOrder := []string{
		"buy {items} for {total_cost}",
		"purchase {items}",
		"spend {total_cost} on {items}",
	}
	for i, w := range wantOrder {
		if tpls[i].source != w {
			t.Errorf("templates[%d].source: got %q, want %q", i, tpls[i].source, w)
		}
	}
}

// TestCompileTemplate_MixedBareAndTemplateOnSameIntent — an intent
// can carry both bare-string and template synonyms; both paths must
// compile.
func TestCompileTemplate_MixedBareAndTemplateOnSameIntent(t *testing.T) {
	t.Parallel()
	def := mkApp(t, map[string]app.Intent{
		"propose_purchase": {
			Synonyms: []string{
				"shop",                         // bare
				"buy {items} for {total_cost}", // template
				"purchase",                     // bare
			},
			Slots: map[string]app.Slot{
				"items":      {Type: "string"},
				"total_cost": {Type: "int"},
			},
		},
	})
	m := mustCompile(t, def)
	if m.IsEmpty() {
		t.Fatalf("matcher is empty; want bare + template entries")
	}
	tpls := templateForIntent(t, m, "propose_purchase")
	if len(tpls) != 1 {
		t.Errorf("templates: got %d, want 1 (the {items}/{total_cost} entry)", len(tpls))
	}
	// Bare entries: shop, purchase → 2.
	bare := 0
	for _, e := range m.idx.entries {
		if e.Intent == "propose_purchase" && e.Kind == kindSynonym {
			bare++
		}
	}
	if bare != 2 {
		t.Errorf("bare-string entries on propose_purchase: got %d, want 2", bare)
	}
}
