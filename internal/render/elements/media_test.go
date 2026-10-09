package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

func TestRenderAll_MediaAnnotateURL(t *testing.T) {
	view := app.View{Elements: []app.ViewElement{{
		Kind:         "media",
		MediaHandle:  "{{ world.handle }}",
		MediaKind:    "slideshow",
		MediaCaption: "Rendered deck",
		MediaPath:    "{{ world.path }}",
		AnnotateURL:  "{{ world.annotate_url }}",
	}}}
	env := expr.Env{World: map[string]any{
		"handle":       "slidey-edit-render-1",
		"path":         ".artifacts/slidey-edit/deck.html",
		"annotate_url": "http://127.0.0.1:7777/#/s/session-1/chat?visual_annotate=1",
	}}

	out, err := RenderAll(view, env, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	if !strings.Contains(out, "Rendered deck") {
		t.Fatalf("media caption missing from render:\n%s", out)
	}
	if !strings.Contains(out, ".artifacts/slidey-edit/deck.html") {
		t.Fatalf("media path missing from render:\n%s", out)
	}
	if !strings.Contains(out, "annotate → http://127.0.0.1:7777/#/s/session-1/chat?visual_annotate=1") {
		t.Fatalf("annotation URL missing from TUI media render:\n%s", out)
	}
}
