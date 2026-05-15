package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// TestTemplate_DelegatesToGlamour asserts the template element passes
// the post-Pongo rendered string to the supplied Glamour callback. The
// callback is the integration seam — the elements package itself never
// invokes Glamour.
func TestTemplate_DelegatesToGlamour(t *testing.T) {
	called := false
	got := ""
	gl := func(s string) string {
		called = true
		got = s
		return "[STYLED] " + s
	}
	out, err := Template{Source: "hello", Glamour: gl}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !called {
		t.Errorf("Glamour callback was not invoked")
	}
	if got != "hello" {
		t.Errorf("Glamour received %q, want %q", got, "hello")
	}
	if out != "[STYLED] hello" {
		t.Errorf("output not styled: got %q", out)
	}
}

func TestTemplate_PongoBeforeGlamour(t *testing.T) {
	env := expr.Env{World: map[string]any{"who": "Bilbo"}}
	gl := func(s string) string { return s }
	out, err := Template{Source: "hi {{ world.who }}!", Glamour: gl}.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "hi Bilbo!" {
		t.Errorf("got %q, want %q", out, "hi Bilbo!")
	}
}

func TestTemplate_NilGlamourUsesIdentity(t *testing.T) {
	out, err := Template{Source: "raw text"}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "raw text" {
		t.Errorf("nil Glamour should be identity; got %q", out)
	}
}

// TestTemplate_LegacyViewParity walks the full dispatch path for a
// legacy-form View (single template element) and verifies the
// dispatcher's output matches calling the Glamour callback on the
// substituted source. This is the back-compat guarantee — typed-view
// dispatch must not change behaviour for the existing string form.
func TestTemplate_LegacyViewParity(t *testing.T) {
	gl := func(s string) string { return "(g) " + s }
	source := "Workspace: {{ world.ws }}"
	env := expr.Env{World: map[string]any{"ws": "demo"}}

	view := app.LegacyView(source)
	out, err := RenderAll(view, env, 80, gl, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	want := "(g) Workspace: demo"
	if !strings.Contains(out, want) {
		t.Errorf("got %q, want substring %q", out, want)
	}
}

func TestTemplate_EmptyReturnsEmpty(t *testing.T) {
	out, err := Template{Source: ""}.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("got %q, want empty", out)
	}
}
