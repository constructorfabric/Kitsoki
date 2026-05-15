package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

func TestProse_ReflowsAtWidth(t *testing.T) {
	// A hand-wrapped paragraph (typical YAML folded scalar collapse
	// product) gets re-wrapped at the requested width. Specifically,
	// the renderer must not respect the author's original hand-wrap.
	body := "The quick brown fox jumps over the lazy dog. " +
		"Sphinx of black quartz, judge my vow."

	wide, err := Prose{Source: body}.Render(200, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render(200): %v", err)
	}
	narrow, err := Prose{Source: body}.Render(20, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render(20): %v", err)
	}

	// At width 200 the body fits on one line.
	if strings.Contains(wide, "\n") {
		t.Errorf("width=200 should fit on one line, got:\n%s", wide)
	}
	// At width 20 we get multiple lines and every line is <= 20 chars.
	lines := strings.Split(narrow, "\n")
	if len(lines) < 2 {
		t.Errorf("width=20 should wrap to multiple lines, got:\n%s", narrow)
	}
	for i, line := range lines {
		if n := len([]rune(line)); n > 20 {
			t.Errorf("width=20 line %d (%q) is %d chars wide", i, line, n)
		}
	}
}

func TestProse_PongoInterpolation(t *testing.T) {
	env := expr.Env{
		World: map[string]any{"hero": "Frodo", "land": "the Shire"},
	}
	out, err := Prose{Source: "{{ world.hero }} sets out from {{ world.land }}."}.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if want := "Frodo sets out from the Shire."; out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestProse_CollapsesNewlinesToSpaces(t *testing.T) {
	// YAML literal scalar (`|`) preserves newlines; prose collapses
	// them so the paragraph reflows correctly.
	body := "line one\nline two\nline three"
	out, err := Prose{Source: body}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := out, "line one line two line three"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProse_EmptyReturnsEmpty(t *testing.T) {
	out, err := Prose{Source: "   \n  "}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}
