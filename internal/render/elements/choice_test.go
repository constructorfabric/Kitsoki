package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// ---- single mode -----------------------------------------------------------

func TestChoice_Single_BareList(t *testing.T) {
	c := Choice{
		Mode:   "single",
		Prompt: "Choose a profession",
		Items: []app.ChoiceItem{
			{Label: "Banker", Intent: "pick_profession"},
			{Label: "Carpenter", Intent: "pick_profession"},
			{Label: "Farmer", Intent: "pick_profession"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Choose a profession:") {
		t.Errorf("missing prompt header: %q", out)
	}
	if !strings.Contains(out, "  Banker") {
		t.Errorf("missing Banker row with cursor gutter: %q", out)
	}
	if !strings.Contains(out, choiceFooterSingle) {
		t.Errorf("missing single-mode footer: %q", out)
	}
}

func TestChoice_Single_TwoColumnAlignment(t *testing.T) {
	c := Choice{
		Mode:   "single",
		Prompt: "Choose a profession",
		Items: []app.ChoiceItem{
			{Label: "Banker", Hint: "$1,600 — easy", Intent: "pick"},
			{Label: "Carpenter", Hint: "$800 — medium", Intent: "pick"},
			{Label: "Farmer", Hint: "$400 — hard", Intent: "pick"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The label column should be sized to the longest label
	// ("Carpenter" = 9 chars). Cursor gutter = 2, gutter = 2,
	// so the hint column starts at index 2 + 9 + 2 = 13.
	lines := strings.Split(out, "\n")
	var bankerLine string
	for _, l := range lines {
		if strings.Contains(l, "Banker") {
			bankerLine = l
			break
		}
	}
	if bankerLine == "" {
		t.Fatalf("no Banker line in output:\n%s", out)
	}
	idx := strings.Index(bankerLine, "$1,600")
	if idx != 13 {
		t.Errorf("hint column at index %d, expected 13: %q", idx, bankerLine)
	}
}

func TestChoice_RenderLinesFitWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Choice
	}{
		{
			name: "single",
			c: Choice{
				Mode: "single",
				Items: []app.ChoiceItem{
					{Label: "prd", Hint: "author a PRD, then carry it into design without wrapping the row", Intent: "go_prd"},
					{Label: "very long action label that should not force wrapping", Hint: "long hint", Intent: "go_long"},
				},
			},
		},
		{
			name: "multi",
			c: Choice{
				Mode:   "multi",
				Intent: "choose",
				Slot:   "items",
				Items: []app.ChoiceItem{
					{Label: "coverage", Value: "coverage", Hint: "add deterministic regression coverage before shipping"},
					{Label: "docs", Value: "docs", Hint: "update narrative documentation after landing"},
				},
			},
		},
		{
			name: "form",
			c: Choice{
				Mode:     "form",
				Intent:   "submit",
				Template: "Submit {summary}",
				Fields: []app.ChoiceField{
					{Name: "summary", Type: "string", Placeholder: "a concise summary with too many words"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width = 40
			out, err := tc.c.Render(width, expr.Env{}, nil)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			for i, line := range strings.Split(out, "\n") {
				if got := visibleLen(line); got > width {
					t.Fatalf("line %d exceeds width %d (got %d): %q\n%s", i, width, got, line, out)
				}
			}
		})
	}
}

func TestChoice_Single_ItemWithParamAppendsPlaceholder(t *testing.T) {
	c := Choice{
		Mode: "single",
		Items: []app.ChoiceItem{
			{
				Label:  "Generate names from a theme",
				Intent: "generate_names",
				Param: &app.ChoiceParam{
					Slot:        "theme",
					Type:        "string",
					Placeholder: "e.g. norse mythology",
				},
			},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Generate names from a theme [e.g. norse mythology]") {
		t.Errorf("expected param placeholder appended to label:\n%s", out)
	}
}

func TestChoice_Single_ItemWithParamNoPlaceholderUsesSlot(t *testing.T) {
	c := Choice{
		Mode: "single",
		Items: []app.ChoiceItem{
			{
				Label:  "Pick a theme",
				Intent: "pick_theme",
				Param:  &app.ChoiceParam{Slot: "theme", Type: "string"},
			},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Pick a theme (theme?)") {
		t.Errorf("expected slot-name fallback: %q", out)
	}
}

func TestChoice_Single_WhenFilterDropsRow(t *testing.T) {
	env := expr.Env{World: map[string]any{"show": false}}
	c := Choice{
		Mode: "single",
		Items: []app.ChoiceItem{
			{Label: "always", Intent: "x"},
			{Label: "hidden very long row that would dominate", Intent: "y", When: "world.show"},
			{Label: "also always", Intent: "z"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("filtered row leaked: %s", out)
	}
	// Both survivors present
	if !strings.Contains(out, "always") || !strings.Contains(out, "also always") {
		t.Errorf("survivors missing: %s", out)
	}
}

func TestChoice_Single_PongoInPromptAndLabel(t *testing.T) {
	env := expr.Env{World: map[string]any{"threat_level": "high", "place": "the fort"}}
	c := Choice{
		Mode:   "single",
		Prompt: "Threat level: {{ world.threat_level }}",
		Items: []app.ChoiceItem{
			{Label: "go to {{ world.place }}", Intent: "go"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Threat level: high:") {
		t.Errorf("prompt pongo substitution failed: %s", out)
	}
	if !strings.Contains(out, "go to the fort") {
		t.Errorf("label pongo substitution failed: %s", out)
	}
}

func TestChoice_Single_DifferentWidths(t *testing.T) {
	c := Choice{
		Mode:   "single",
		Prompt: "Pick",
		Items: []app.ChoiceItem{
			{Label: "alpha", Hint: "a", Intent: "x"},
			{Label: "beta", Hint: "b", Intent: "x"},
		},
	}
	for _, w := range []int{40, 60, 80} {
		out, err := c.Render(w, expr.Env{}, nil)
		if err != nil {
			t.Fatalf("width=%d Render: %v", w, err)
		}
		if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
			t.Errorf("width=%d: missing items:\n%s", w, out)
		}
	}
}

// ---- multi mode ------------------------------------------------------------

func TestChoice_Multi_Checkboxes(t *testing.T) {
	c := Choice{
		Mode:   "multi",
		Prompt: "Select symptoms",
		Intent: "report_symptoms",
		Slot:   "symptoms",
		Min:    1, MinSet: true,
		Max: 5, MaxSet: true,
		Items: []app.ChoiceItem{
			{Value: "fever", Label: "Fever", Hint: ">100.4°F"},
			{Value: "cough", Label: "Cough"},
			{Value: "fatigue", Label: "Fatigue"},
			{Value: "rash", Label: "Rash"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Select symptoms (1–5):") {
		t.Errorf("expected `(1–5)` bounds in prompt: %s", out)
	}
	if !strings.Contains(out, "[ ] Fever") {
		t.Errorf("missing checkbox row for Fever: %s", out)
	}
	if !strings.Contains(out, choiceFooterMulti) {
		t.Errorf("missing multi-mode footer: %s", out)
	}
}

func TestChoice_Multi_NoBoundsOmitsSuffix(t *testing.T) {
	c := Choice{
		Mode:   "multi",
		Prompt: "Pick all that apply",
		Intent: "pick",
		Slot:   "picks",
		Items: []app.ChoiceItem{
			{Value: "a", Label: "A"},
			{Value: "b", Label: "B"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "(") || strings.Contains(out, "–") {
		t.Errorf("unbounded multi shouldn't show bounds: %s", out)
	}
	if !strings.Contains(out, "Pick all that apply:") {
		t.Errorf("missing prompt: %s", out)
	}
}

func TestChoice_Multi_MinOnlySet(t *testing.T) {
	c := Choice{
		Mode:   "multi",
		Prompt: "Pick at least one",
		Intent: "x", Slot: "y",
		Min: 1, MinSet: true,
		Items: []app.ChoiceItem{
			{Value: "a", Label: "A"},
			{Value: "b", Label: "B"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// When MinSet=true and MaxSet=false, max defaults to len(visible)=2.
	if !strings.Contains(out, "(1–2)") {
		t.Errorf("expected (1–2) bounds: %s", out)
	}
}

func TestChoice_Multi_LabelDefaultsToValue(t *testing.T) {
	c := Choice{
		Mode:   "multi",
		Intent: "x", Slot: "y",
		Items: []app.ChoiceItem{
			{Value: "fever"}, // no Label
			{Value: "cough"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "[ ] fever") || !strings.Contains(out, "[ ] cough") {
		t.Errorf("expected value-as-label rows: %s", out)
	}
}

func TestChoice_Multi_WhenFilter(t *testing.T) {
	env := expr.Env{World: map[string]any{"day": 1}}
	c := Choice{
		Mode:   "multi",
		Intent: "x", Slot: "y",
		Items: []app.ChoiceItem{
			{Value: "a", Label: "A"},
			{Value: "b", Label: "B", When: "world.day > 3"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "[ ] B") {
		t.Errorf("guarded row leaked: %s", out)
	}
	if !strings.Contains(out, "[ ] A") {
		t.Errorf("survivor missing: %s", out)
	}
}

// ---- form mode -------------------------------------------------------------

func TestChoice_Form_TemplateSubstitution(t *testing.T) {
	c := Choice{
		Mode:     "form",
		Prompt:   "Compose your purchase",
		Intent:   "propose_purchase",
		Template: "Buy {items} for ${total_cost}, leaving ${remaining}.",
		Fields: []app.ChoiceField{
			{Name: "items", Type: "string", Placeholder: "oxen=4, food=1500"},
			{Name: "total_cost", Type: "int", Default: 0},
			{Name: "remaining", Type: "int", Readonly: true, Expr: "1000"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "Compose your purchase:") {
		t.Errorf("missing prompt: %s", out)
	}
	if !strings.Contains(out, "_oxen=4, food=1500_") {
		t.Errorf("expected placeholder rendered as underline value: %s", out)
	}
	if !strings.Contains(out, "_0_") {
		t.Errorf("expected default value 0 rendered: %s", out)
	}
	if !strings.Contains(out, "_1000_") {
		t.Errorf("expected readonly expr 1000 evaluated: %s", out)
	}
	if !strings.Contains(out, choiceFooterForm) {
		t.Errorf("missing form footer: %s", out)
	}
}

func TestChoice_Form_ReadonlyExprUsesEnv(t *testing.T) {
	env := expr.Env{World: map[string]any{"money": 400}}
	c := Choice{
		Mode:     "form",
		Intent:   "x",
		Template: "Remaining ${remaining}.",
		Fields: []app.ChoiceField{
			{Name: "remaining", Type: "int", Readonly: true, Expr: "world.money"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "_400_") {
		t.Errorf("expected world.money=400 evaluated: %s", out)
	}
}

func TestChoice_Form_FieldWhenFilterStubsUnderline(t *testing.T) {
	env := expr.Env{World: map[string]any{"show": false}}
	c := Choice{
		Mode:     "form",
		Intent:   "x",
		Template: "Hello {name}.",
		Fields: []app.ChoiceField{
			{Name: "name", Type: "string", Default: "Brad", When: "world.show"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "Brad") {
		t.Errorf("guarded-away field default leaked: %s", out)
	}
	// Bare underline stub for the guarded-away field.
	if !strings.Contains(out, "Hello _") {
		t.Errorf("expected underline stub for guarded field: %s", out)
	}
}

func TestChoice_Form_NumericDefault(t *testing.T) {
	c := Choice{
		Mode:     "form",
		Intent:   "x",
		Template: "Total ${total}.",
		Fields: []app.ChoiceField{
			{Name: "total", Type: "int", Default: 42},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "_42_") {
		t.Errorf("expected numeric default rendered: %s", out)
	}
}

// ---- empty / edge cases ----------------------------------------------------

func TestChoice_Single_AllItemsGuardedAway(t *testing.T) {
	env := expr.Env{World: map[string]any{"never": false}}
	c := Choice{
		Mode:   "single",
		Prompt: "Nothing to pick",
		Items: []app.ChoiceItem{
			{Label: "x", Intent: "a", When: "world.never"},
			{Label: "y", Intent: "b", When: "world.never"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// Policy: still show prompt + footer (so users see the empty
	// state rather than a vacant block). No item rows.
	if !strings.Contains(out, "Nothing to pick:") {
		t.Errorf("prompt should survive an empty body: %s", out)
	}
	if !strings.Contains(out, choiceFooterSingle) {
		t.Errorf("footer should survive an empty body: %s", out)
	}
	if strings.Contains(out, "x") || strings.Contains(out, "y") {
		t.Errorf("guarded items leaked: %s", out)
	}
}

func TestChoice_NoPromptStillFooter(t *testing.T) {
	c := Choice{
		Mode: "single",
		Items: []app.ChoiceItem{
			{Label: "go", Intent: "x"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// No prompt → output begins with the row (no leading blank line).
	if strings.HasPrefix(out, "\n") {
		t.Errorf("no-prompt output starts with newline: %q", out)
	}
	if !strings.Contains(out, choiceFooterSingle) {
		t.Errorf("missing footer: %q", out)
	}
}

func TestChoice_Form_EmptyBodyEmitsStubAndFooter(t *testing.T) {
	// All fields when:-false AND a template whose visible content
	// collapses to empty. We model the "every field guarded away,
	// nothing to render" terminal case by passing an empty template
	// (the renderer's empty-body branch). The widget must still
	// surface the footer and a "no fields visible" indicator rather
	// than rendering a vacant block with no keybinding hint.
	env := expr.Env{World: map[string]any{"show": false}}
	c := Choice{
		Mode:     "form",
		Prompt:   "Compose your purchase",
		Intent:   "x",
		Template: "",
		Fields: []app.ChoiceField{
			{Name: "items", Type: "string", Default: "x", When: "world.show"},
			{Name: "total", Type: "int", Default: 0, When: "world.show"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out == "" {
		t.Fatalf("expected non-empty output for empty-body form")
	}
	if !strings.Contains(out, choiceFooterForm) {
		t.Errorf("missing form footer in empty-body output: %q", out)
	}
	if !strings.Contains(out, "no fields visible") {
		t.Errorf("expected 'no fields visible' indicator: %q", out)
	}
	// Prompt should still survive an empty body (mirrors the
	// single-mode all-items-guarded-away policy).
	if !strings.Contains(out, "Compose your purchase:") {
		t.Errorf("prompt should survive an empty body: %q", out)
	}
}

func TestChoice_Multi_AllItemsGuardedAwaySuppressesBounds(t *testing.T) {
	// MinSet=true forces multiBounds to compute a "(min–max)" suffix;
	// with zero visible items max would resolve to 0 and the suffix
	// would render the nonsensical "(1–0)". Confirm the renderer
	// suppresses the bounds entirely when nothing is pickable.
	env := expr.Env{World: map[string]any{"never": false}}
	c := Choice{
		Mode:   "multi",
		Prompt: "Pick at least one",
		Intent: "x", Slot: "y",
		Min: 1, MinSet: true,
		Items: []app.ChoiceItem{
			{Value: "a", Label: "A", When: "world.never"},
			{Value: "b", Label: "B", When: "world.never"},
		},
	}
	out, err := c.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "(1–0)") {
		t.Errorf("nonsensical (1–0) bounds suffix leaked: %q", out)
	}
	if strings.Contains(out, "(1)") {
		t.Errorf("nonsensical (1) bounds suffix leaked when nothing visible: %q", out)
	}
	// Prompt should still appear (no dangling bounds), and footer
	// should still surface so the empty state is navigable.
	if !strings.Contains(out, "Pick at least one:") {
		t.Errorf("prompt should survive an empty body: %q", out)
	}
	if !strings.Contains(out, choiceFooterMulti) {
		t.Errorf("footer should survive an empty body: %q", out)
	}
}

func TestChoice_UnrecognizedModeFallsToSingle(t *testing.T) {
	// The loader's schema validator rejects bad modes, so renderer
	// behaviour on a truly bad mode is academic — but we fall back
	// to single so a renderer-only test fixture doesn't crash.
	c := Choice{
		Mode: "weird",
		Items: []app.ChoiceItem{
			{Label: "x", Intent: "a"},
		},
	}
	out, err := c.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, choiceFooterSingle) {
		t.Errorf("expected single-mode footer fallback: %s", out)
	}
}
