package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

// testAppDir returns the path to the fixture app for renderer tests.
// The layout is internal/render/testdata/ + a views/ subdir, which makes
// the testdata dir itself the "appDir" passed to NewAppRenderer.
func testAppDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "views")); err != nil {
		t.Fatalf("missing testdata/views: %v", err)
	}
	return abs
}

func storeEnv() expr.Env {
	return expr.Env{
		World: map[string]any{
			"name":  "Matt's General Store",
			"money": 100,
			"oxen":  2,
			"food":  200,
		},
	}
}

func TestAppRenderer_Render_Inline(t *testing.T) {
	r, err := NewCachedAppRenderer(testAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.Render("Cash: ${{ world.money }}", storeEnv())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "Cash: $100" {
		t.Fatalf("Render inline: got %q", out)
	}
}

func TestAppRenderer_Render_FastPath(t *testing.T) {
	r, err := NewCachedAppRenderer(testAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	src := "no templating here"
	out, err := r.Render(src, storeEnv())
	if err != nil {
		t.Fatalf("Render fast path: %v", err)
	}
	if out != src {
		t.Fatalf("Render fast path: got %q want %q", out, src)
	}
}

func TestAppRenderer_RenderFile_Inheritance(t *testing.T) {
	r, err := NewCachedAppRenderer(testAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderFile("store.pongo", storeEnv())
	if err != nil {
		t.Fatalf("RenderFile: %v", err)
	}
	// Heading block override.
	if !strings.Contains(out, "Store: Matt's General Store") {
		t.Errorf("missing heading override:\n%s", out)
	}
	// Body block content.
	if !strings.Contains(out, "Cash: $100") {
		t.Errorf("missing body content:\n%s", out)
	}
	// Included partial rendered with env access.
	if !strings.Contains(out, "Oxen: 2") {
		t.Errorf("missing included partial (oxen):\n%s", out)
	}
	if !strings.Contains(out, "Food: 200") {
		t.Errorf("missing included partial (food):\n%s", out)
	}
	// Footer falls back to base default since store.pongo doesn't override.
	if !strings.Contains(out, "default footer") {
		t.Errorf("missing inherited footer:\n%s", out)
	}
}

func TestAppRenderer_RenderFile_Base(t *testing.T) {
	r, err := NewCachedAppRenderer(testAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderFile("base.pongo", expr.Env{})
	if err != nil {
		t.Fatalf("RenderFile base: %v", err)
	}
	if !strings.Contains(out, "Default Heading") {
		t.Errorf("base.pongo default heading missing:\n%s", out)
	}
	if !strings.Contains(out, "default body") {
		t.Errorf("base.pongo default body missing:\n%s", out)
	}
}

func TestAppRenderer_RenderFile_NotFound(t *testing.T) {
	r, err := NewCachedAppRenderer(testAppDir(t))
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	_, err = r.RenderFile("nope.pongo", expr.Env{})
	if err == nil {
		t.Fatal("expected error for missing template, got nil")
	}
	if !strings.Contains(err.Error(), "nope.pongo") {
		t.Fatalf("error missing template name: %v", err)
	}
}

// TestNewAppRenderer_MissingViewsDir asserts the Phase-H tolerance:
// an app with no views/ subdir builds a renderer that handles inline
// Render() calls (the fast path bypasses the loader), and RenderFile /
// {% extends %} / {% include %} calls fail loudly with a "no app
// views/ directory" error. This lets the orchestrator build a
// renderer uniformly for every app — apps that don't use file
// templates pay nothing for the absent views/ tree.
func TestNewAppRenderer_MissingViewsDir(t *testing.T) {
	dir := t.TempDir()
	// No views/ subdir.
	r, err := NewAppRenderer(dir)
	if err != nil {
		t.Fatalf("NewAppRenderer should tolerate missing views/: %v", err)
	}
	if r == nil {
		t.Fatal("renderer is nil")
	}
	// Inline rendering still works.
	out, err := r.Render("hello {{ world.who }}", expr.Env{World: map[string]any{"who": "world"}})
	if err != nil {
		t.Fatalf("inline Render: %v", err)
	}
	if out != "hello world" {
		t.Fatalf("inline Render: got %q", out)
	}
	// RenderFile must fail clearly.
	if _, err := r.RenderFile("anything.pongo", expr.Env{}); err == nil {
		t.Fatal("RenderFile should fail when views/ is absent")
	}
}

func TestNewAppRenderer_ViewsIsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file at the views/ path.
	viewsPath := filepath.Join(dir, "views")
	if err := os.WriteFile(viewsPath, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := NewAppRenderer(dir)
	if err == nil {
		t.Fatal("expected error when views/ is a regular file")
	}
}

// TestAppRenderer_UncachedReloads exercises the dev-mode behavior: edits
// to a template file should be picked up on the next RenderFile call
// without rebuilding the renderer.
func TestAppRenderer_UncachedReloads(t *testing.T) {
	dir := t.TempDir()
	viewsDir := filepath.Join(dir, "views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}
	tmpl := filepath.Join(viewsDir, "hot.pongo")
	if err := os.WriteFile(tmpl, []byte("v1: {{ world.x }}"), 0o644); err != nil {
		t.Fatalf("write tmpl: %v", err)
	}
	r, err := NewAppRenderer(dir)
	if err != nil {
		t.Fatalf("NewAppRenderer: %v", err)
	}
	env := expr.Env{World: map[string]any{"x": "hi"}}
	out, err := r.RenderFile("hot.pongo", env)
	if err != nil {
		t.Fatalf("RenderFile v1: %v", err)
	}
	if !strings.Contains(out, "v1: hi") {
		t.Fatalf("v1: got %q", out)
	}
	// Overwrite and re-render — uncached mode must see the new bytes.
	if err := os.WriteFile(tmpl, []byte("v2: {{ world.x }}"), 0o644); err != nil {
		t.Fatalf("rewrite tmpl: %v", err)
	}
	out, err = r.RenderFile("hot.pongo", env)
	if err != nil {
		t.Fatalf("RenderFile v2: %v", err)
	}
	if !strings.Contains(out, "v2: hi") {
		t.Fatalf("uncached reload failed: got %q", out)
	}
}

// TestAppRenderer_CachedDoesNotReload is the inverse — the cached variant
// must return the original content even after the file changes.
func TestAppRenderer_CachedDoesNotReload(t *testing.T) {
	dir := t.TempDir()
	viewsDir := filepath.Join(dir, "views")
	if err := os.MkdirAll(viewsDir, 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}
	tmpl := filepath.Join(viewsDir, "stable.pongo")
	if err := os.WriteFile(tmpl, []byte("v1"), 0o644); err != nil {
		t.Fatalf("write tmpl: %v", err)
	}
	r, err := NewCachedAppRenderer(dir)
	if err != nil {
		t.Fatalf("NewCachedAppRenderer: %v", err)
	}
	out, err := r.RenderFile("stable.pongo", expr.Env{})
	if err != nil {
		t.Fatalf("RenderFile v1: %v", err)
	}
	if out != "v1" {
		t.Fatalf("v1: got %q", out)
	}
	if err := os.WriteFile(tmpl, []byte("v2"), 0o644); err != nil {
		t.Fatalf("rewrite tmpl: %v", err)
	}
	out, err = r.RenderFile("stable.pongo", expr.Env{})
	if err != nil {
		t.Fatalf("RenderFile v2: %v", err)
	}
	if out != "v1" {
		t.Fatalf("cached should NOT reload: got %q want %q", out, "v1")
	}
}
