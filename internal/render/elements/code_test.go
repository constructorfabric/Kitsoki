package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

func TestCode_PreservesIndentation(t *testing.T) {
	body := `propose "list files in /tmp"
  propose "git status"
    propose "go test ./..."`
	out, err := Code{Source: body}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != body {
		t.Errorf("got:\n%q\nwant:\n%q", out, body)
	}
}

func TestCode_NoReflowAtNarrowWidth(t *testing.T) {
	// A long line should not wrap — code preserves layout exactly.
	body := "this is a single very long line that should not wrap inside a code block at any width"
	out, err := Code{Source: body}.Render(20, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "\n") {
		t.Errorf("code should not wrap, got:\n%s", out)
	}
	if out != body {
		t.Errorf("code body changed; got %q want %q", out, body)
	}
}

func TestCode_PongoInterpolation(t *testing.T) {
	env := expr.Env{World: map[string]any{"cmd": "git status"}}
	out, err := Code{Source: "  $ {{ world.cmd }}"}.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got, want := out, "  $ git status"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCode_TrimsTrailingNewlines(t *testing.T) {
	// YAML block literal (`|`) appends a final newline; the renderer
	// trims that so the dispatcher's inter-element blank line doesn't
	// stack with it.
	out, err := Code{Source: "line\n"}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "line" {
		t.Errorf("got %q, want %q", out, "line")
	}
}

func TestCode_EmptyReturnsEmpty(t *testing.T) {
	out, err := Code{Source: "\n\n"}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}
