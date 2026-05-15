package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

func TestHeading_RendersStyledText(t *testing.T) {
	out, err := Heading{Source: "Available areas"}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// The styled body must contain the literal text. Whether ANSI
	// escapes appear depends on the terminal profile that lipgloss
	// detected when the tests run — in `go test` stdout is not a TTY,
	// so lipgloss may emit plain text. Assert on the visible content
	// to keep the test robust.
	if !strings.Contains(out, "Available areas") {
		t.Errorf("heading must contain literal text, got %q", out)
	}
}

func TestHeading_PongoInterpolation(t *testing.T) {
	env := expr.Env{World: map[string]any{"who": "Frodo"}}
	out, err := Heading{Source: "About {{ world.who }}"}.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "About Frodo") {
		t.Errorf("heading interpolation failed, got %q", out)
	}
}

func TestHeading_EmptyReturnsEmpty(t *testing.T) {
	out, err := Heading{Source: "   "}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("empty heading should produce empty output, got %q", out)
	}
}
