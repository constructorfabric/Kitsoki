package render

import (
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

// extendsAppDir returns the absolute path to the extends test fixture
// app tree (testdata/extends/views/{layout,base}.pongo).
func extendsAppDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "extends"))
	if err != nil {
		t.Fatalf("abs testdata/extends: %v", err)
	}
	return abs
}

func extendsEnv() expr.Env {
	return expr.Env{
		World: map[string]any{
			"title": "Cloak Foyer",
			"day":   3,
		},
	}
}

// TestRenderExtended_NoBlockOverride exercises the case where the
// extends form supplies zero blocks. Every block in the base layout
// should fall through to its default body.
func TestRenderExtended_NoBlockOverride(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderExtended("layout.pongo", nil, extendsEnv())
	if err != nil {
		t.Fatalf("RenderExtended: %v", err)
	}
	for _, want := range []string{
		"layout default heading",
		"layout default status",
		"layout default body",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderExtended_MultipleBlocks covers the canonical state-view
// shape: an `extends:` + multiple block overrides. All overrides
// should win over the base defaults, and untouched blocks fall back.
func TestRenderExtended_MultipleBlocks(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderExtended("layout.pongo", map[string]string{
		"heading": "Welcome to the Foyer",
		"body":    "A small room with a cloak hook.",
	}, extendsEnv())
	if err != nil {
		t.Fatalf("RenderExtended: %v", err)
	}
	for _, want := range []string{
		"Welcome to the Foyer",
		"A small room with a cloak hook.",
		// status was not overridden — falls through to layout default.
		"layout default status",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"layout default heading", // overridden
		"layout default body",    // overridden
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected base default leaked: %q in:\n%s", unwanted, out)
		}
	}
}

// TestRenderExtended_Nested asserts that an extends chain (base.pongo
// extends layout.pongo) resolves correctly when the caller's view
// extends the intermediate base. Block overrides at the leaf level
// must win over both intermediates' defaults; un-overridden blocks
// inherit through the chain.
func TestRenderExtended_Nested(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderExtended("base.pongo", map[string]string{
		"body": "Leaf body override.",
	}, extendsEnv())
	if err != nil {
		t.Fatalf("RenderExtended: %v", err)
	}
	// heading + status come from base.pongo's pongo2 expressions
	// (which render world.* via the env passed in).
	for _, want := range []string{
		"base heading: Cloak Foyer",
		"base status: day 3",
		"Leaf body override.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The layout's defaults for heading/status/body must NOT leak —
	// each has been overridden somewhere up the chain.
	for _, unwanted := range []string{
		"layout default heading",
		"layout default status",
		"layout default body",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected layout default leaked: %q in:\n%s", unwanted, out)
		}
	}
}

// TestRenderExtended_NoExtensionName accepts a template reference
// without the .pongo suffix and resolves it the same way (the helper
// appends the default extension).
func TestRenderExtended_NoExtensionName(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderExtended("layout", map[string]string{
		"heading": "no-ext heading",
	}, extendsEnv())
	if err != nil {
		t.Fatalf("RenderExtended: %v", err)
	}
	if !strings.Contains(out, "no-ext heading") {
		t.Errorf("missing override:\n%s", out)
	}
}

// TestRenderExtended_MissingTemplateName asserts that extends="" is
// rejected with a clear error — this protects callers from passing
// through an unset author field.
func TestRenderExtended_MissingTemplateName(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	_, err = r.RenderExtended("", map[string]string{"heading": "x"}, extendsEnv())
	if err == nil {
		t.Fatal("expected error for empty extends, got nil")
	}
}

// TestRenderExtended_UnknownTemplate asserts that referencing a
// non-existent base template produces a meaningful error naming the
// missing path.
func TestRenderExtended_UnknownTemplate(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	_, err = r.RenderExtended("does-not-exist.pongo", nil, extendsEnv())
	if err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error missing template name: %v", err)
	}
}

// TestRenderExtended_UnknownBlockName asserts the documented
// drop-silently behaviour: a `blocks:` map naming a block the base
// template doesn't declare doesn't crash — the wrapper's
// `{% block %}` falls through to whatever pongo2 does for an
// unrecognised block name (the default fallthrough behaviour matches
// the base's default body, which is empty for unknown blocks).
//
// The author-visible signal is that the unknown block content simply
// doesn't appear in the output. We assert (a) no error, and (b) the
// known blocks still render normally so the unknown block didn't
// poison the rest of the composition.
func TestRenderExtended_UnknownBlockName(t *testing.T) {
	r, err := NewCachedAppRenderer(extendsAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderExtended("layout.pongo", map[string]string{
		"heading":  "real override",
		"nonsense": "should not appear",
	}, extendsEnv())
	if err != nil {
		t.Fatalf("RenderExtended: %v", err)
	}
	if !strings.Contains(out, "real override") {
		t.Errorf("missing known override:\n%s", out)
	}
	if strings.Contains(out, "should not appear") {
		t.Errorf("unknown block leaked into output:\n%s", out)
	}
}
