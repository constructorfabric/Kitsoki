package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/expr"
)

// writePrompt writes a prompt file under dir/rel, creating parent dirs.
func writePrompt(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", p, err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
}

func argsEnv(m map[string]any) expr.Env { return expr.Env{Args: m} }

// TestPromptRenderer_NoOverlay_Inert is the backward-compat guard at the render
// layer: a story prompt with no blocks and no overlay renders exactly the file
// content (with args substituted) — the same bytes the legacy render.Pongo
// path produced. If this drifts, every existing story's prompts drift.
func TestPromptRenderer_NoOverlay_Inert(t *testing.T) {
	story := t.TempDir()
	writePrompt(t, story, "prompts/diagnose.md", "Diagnose {{ args.bug }} in {{ args.repo }}.")

	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/diagnose.md", argsEnv(map[string]any{"bug": "NPE", "repo": "acme"}))
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if want := "Diagnose NPE in acme."; out != want {
		t.Fatalf("inert render: got %q want %q", out, want)
	}
}

// TestPromptRenderer_ProvisionalDefault_NoOverlay: a base that ships a spec_
// block with a working default renders that default when no overlay overrides
// it.
func TestPromptRenderer_ProvisionalDefault_NoOverlay(t *testing.T) {
	story := t.TempDir()
	writePrompt(t, story, "prompts/diagnose.md",
		"Header.\n{% block spec_rubric %}generic rubric{% endblock %}\nFooter.")

	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/diagnose.md", expr.Env{})
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	for _, want := range []string{"Header.", "generic rubric", "Footer."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestPromptRenderer_OverlayExtendsAndOverrides is the headline mechanism: an
// overlay file extends the story base via @story and fills/overrides a spec_
// block, while a bare reference (prompts/diagnose.md) resolves overlay-first.
// The reviewer-flagged pongo2 constraint lives or dies here.
func TestPromptRenderer_OverlayExtendsAndOverrides(t *testing.T) {
	story := t.TempDir()
	overlay := t.TempDir()

	writePrompt(t, story, "prompts/diagnose.md",
		"Structural header.\n"+
			"{% block spec_project %}{% endblock %}\n"+
			"{% block spec_rubric %}generic rubric{% endblock %}\n"+
			"Structural footer.")

	// Overlay shadows the same bare name and extends the base it shadows.
	writePrompt(t, overlay, "prompts/diagnose.md",
		`{% extends "@story/prompts/diagnose.md" %}`+"\n"+
			"{% block spec_project %}Acme repo: gofmt, no naked returns.{% endblock %}\n"+
			"{% block spec_rubric %}strict rubric{% endblock %}")

	r, err := NewPromptRenderer(PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	// Bare name resolves overlay-first.
	resolved, ok := r.ResolvePromptName("prompts/diagnose.md")
	if !ok {
		t.Fatal("ResolvePromptName: not found")
	}
	if !strings.HasPrefix(resolved, overlay) {
		t.Fatalf("bare name should resolve overlay-first; got %q (overlay %q)", resolved, overlay)
	}

	out, err := r.RenderPrompt("prompts/diagnose.md", expr.Env{})
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	// Structural parts inherited from the base.
	for _, want := range []string{"Structural header.", "Structural footer."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing inherited structural text %q in:\n%s", want, out)
		}
	}
	// Hole filled + provisional default overridden.
	if !strings.Contains(out, "Acme repo: gofmt") {
		t.Errorf("hole spec_project not filled from overlay:\n%s", out)
	}
	if !strings.Contains(out, "strict rubric") {
		t.Errorf("provisional spec_rubric not overridden from overlay:\n%s", out)
	}
	if strings.Contains(out, "generic rubric") {
		t.Errorf("overlay should have overridden the provisional default:\n%s", out)
	}
}

// TestPromptRenderer_SharedInclude: a base includes a shared fragment via the
// @shared namespace.
func TestPromptRenderer_SharedInclude(t *testing.T) {
	story := t.TempDir()
	sharedDir := filepath.Join(story, "prompts", "_shared")

	writePrompt(t, story, "prompts/_shared/safety.md", "SAFETY: read-only.")
	writePrompt(t, story, "prompts/diagnose.md",
		`Top.`+"\n"+`{% include "@shared/safety.md" %}`)

	r, err := NewPromptRenderer(PromptPath{Story: story, Shared: []string{sharedDir}}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/diagnose.md", expr.Env{})
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(out, "SAFETY: read-only.") {
		t.Errorf("@shared include not resolved:\n%s", out)
	}
}

// TestPromptRenderer_CoalescePreserved: the ?? operator works in a prompt
// loaded through the search-path loader, matching inline Pongo behavior.
func TestPromptRenderer_CoalescePreserved(t *testing.T) {
	story := t.TempDir()
	writePrompt(t, story, "prompts/p.md", `Repo: {{ args.repo ?? "(unknown)" }}`)

	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/p.md", argsEnv(map[string]any{}))
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if want := "Repo: (unknown)"; out != want {
		t.Fatalf("coalesce: got %q want %q", out, want)
	}
}

func TestPromptRenderer_Errors(t *testing.T) {
	story := t.TempDir()
	writePrompt(t, story, "prompts/p.md", "ok")
	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}

	if _, ok := r.ResolvePromptName("prompts/missing.md"); ok {
		t.Error("missing prompt should not resolve")
	}
	if _, err := r.RenderPrompt("prompts/missing.md", expr.Env{}); err == nil {
		t.Error("RenderPrompt of missing file should error")
	}
	// Path traversal rejected.
	if _, ok := r.ResolvePromptName("../escape.md"); ok {
		t.Error("`..` reference should be rejected")
	}
}

// TestPromptRenderer_SelfExtendsErrorsNotCrash: an overlay file that extends
// its own bare name (instead of @story/…) resolves overlay-first back to
// itself. pongo2 has no cycle guard and would stack-overflow (fatal); the
// loader must turn it into an ordinary error.
func TestPromptRenderer_SelfExtendsErrorsNotCrash(t *testing.T) {
	story := t.TempDir()
	overlay := t.TempDir()
	writePrompt(t, story, "prompts/x.md", "base {% block b %}d{% endblock %}")
	// Footgun: bare self-extends rather than @story/.
	writePrompt(t, overlay, "prompts/x.md",
		`{% extends "prompts/x.md" %}`+"\n"+"{% block b %}o{% endblock %}")

	r, err := NewPromptRenderer(PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	_, err = r.RenderPrompt("prompts/x.md", expr.Env{})
	if err == nil {
		t.Fatal("expected a self-reference error, got nil")
	}
	if !strings.Contains(err.Error(), "self-reference") {
		t.Fatalf("expected self-reference error, got: %v", err)
	}
}

// TestPromptRenderer_AbsoluteReference: an absolute prompt reference (e.g. an
// imported sub-app prompt rebased absolute at fold time) is used as-is, not
// misjoined under the story root.
func TestPromptRenderer_AbsoluteReference(t *testing.T) {
	story := t.TempDir()
	other := t.TempDir()
	abs := filepath.Join(other, "imported.md")
	if err := os.WriteFile(abs, []byte("imported body"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	got, ok := r.ResolvePromptName(abs)
	if !ok || got != abs {
		t.Fatalf("absolute reference should resolve to itself; got %q ok=%v", got, ok)
	}
}

// TestPromptRenderer_ImportExtend: a parent override prompt extends an
// imported child story's base via @import/<alias>/… — the overlay-extend form
// of imports.overrides.prompts.
func TestPromptRenderer_ImportExtend(t *testing.T) {
	parent := t.TempDir()
	child := t.TempDir()

	// Child (imported) base prompt with a spec_ hole.
	writePrompt(t, child, "prompts/scout.md",
		"Child scout brief.\n{% block spec_locale %}{% endblock %}")
	// Parent override file extends the imported child base and fills the hole.
	writePrompt(t, parent, "prompts/scout_override.md",
		`{% extends "@import/trail/prompts/scout.md" %}`+"\n"+
			"{% block spec_locale %}Oregon Trail, 1848.{% endblock %}")

	r, err := NewPromptRenderer(PromptPath{
		Story:   parent,
		Imports: map[string]string{"trail": child},
	}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/scout_override.md", expr.Env{})
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(out, "Child scout brief.") {
		t.Errorf("missing inherited child base text:\n%s", out)
	}
	if !strings.Contains(out, "Oregon Trail, 1848.") {
		t.Errorf("override did not fill the imported base's hole:\n%s", out)
	}

	// Unknown alias errors clearly.
	writePrompt(t, parent, "prompts/bad.md", `{% extends "@import/nope/prompts/scout.md" %}x`)
	if _, err := r.RenderPrompt("prompts/bad.md", expr.Env{}); err == nil {
		t.Error("expected error for unknown import alias")
	}
}

// TestPromptRenderer_SpecProvenance: with an overlay overriding one of two
// spec_ blocks, provenance reports one overridden and one still-defaulted.
func TestPromptRenderer_SpecProvenance(t *testing.T) {
	story := t.TempDir()
	overlay := t.TempDir()
	writePrompt(t, story, "prompts/x.md",
		"{% block spec_a %}da{% endblock %}\n{% block spec_b %}db{% endblock %}\n{% block structural %}s{% endblock %}")
	writePrompt(t, overlay, "prompts/x.md",
		`{% extends "@story/prompts/x.md" %}`+"\n"+"{% block spec_a %}override-a{% endblock %}")

	r, err := NewPromptRenderer(PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	defaulted, overridden := r.SpecProvenance("prompts/x.md")
	if len(overridden) != 1 || overridden[0] != "spec_a" {
		t.Errorf("overridden: want [spec_a], got %v", overridden)
	}
	if len(defaulted) != 1 || defaulted[0] != "spec_b" {
		t.Errorf("defaulted: want [spec_b], got %v", defaulted)
	}

	// No overlay → no provenance.
	r2, _ := NewPromptRenderer(PromptPath{Story: story}, true)
	if d, o := r2.SpecProvenance("prompts/x.md"); d != nil || o != nil {
		t.Errorf("no-overlay provenance should be nil, got d=%v o=%v", d, o)
	}
}

// TestExampleOverlay_RendersAgainstBugfix renders the shipped example project
// overlay (docs/recipes/prompt-overlay-example) against the real bugfix base
// prompt, guarding the recipe from drift: the overlay must extend the base
// (inherit its structural text) and fill the spec_ blocks.
func TestExampleOverlay_RendersAgainstBugfix(t *testing.T) {
	story, err := filepath.Abs(filepath.Join("..", "..", "stories", "bugfix"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	overlay, err := filepath.Abs(filepath.Join("..", "..", "docs", "recipes", "prompt-overlay-example"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if !fileExists(filepath.Join(story, "prompts", "reproducing_executing.md")) {
		t.Skip("bugfix base prompt not present")
	}
	r, err := NewPromptRenderer(PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/reproducing_executing.md",
		argsEnv(map[string]any{"ticket_id": "ACME-1", "ticket_title": "x", "workdir": "/w"}))
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	// Inherited structural text from the base.
	if !strings.Contains(out, "produce evidence") {
		t.Errorf("overlay did not inherit base structural text:\n%s", out)
	}
	// Specialized blocks from the overlay.
	if !strings.Contains(out, "Acme") {
		t.Errorf("overlay spec_ blocks not applied:\n%s", out)
	}
	// Provenance: both spec_ blocks overridden by the overlay.
	defaulted, overridden := r.SpecProvenance("prompts/reproducing_executing.md")
	if len(defaulted) != 0 {
		t.Errorf("expected no defaulted blocks, got %v", defaulted)
	}
	if len(overridden) != 2 {
		t.Errorf("expected 2 overridden blocks, got %v", overridden)
	}
}

// TestMigratedOregonSwap_RendersViaImportExtend renders the real oregon-trail
// scout override against the real frontier_event base via @import/frontier,
// guarding the migrated swap→extend from drift (flows stub the agent and
// don't render prompts, so this is the only render-syntax guard for it).
func TestMigratedOregonSwap_RendersViaImportExtend(t *testing.T) {
	oregon, _ := filepath.Abs(filepath.Join("..", "..", "stories", "oregon-trail"))
	frontier, _ := filepath.Abs(filepath.Join("..", "..", "stories", "frontier_event"))
	override := filepath.Join(oregon, "prompts", "scout_brief_trail.md")
	if !fileExists(override) {
		t.Skip("oregon-trail override not present")
	}
	r, err := NewPromptRenderer(PromptPath{
		Story:   oregon,
		Imports: map[string]string{"frontier": frontier},
	}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.RenderPrompt("prompts/scout_brief_trail.md", expr.Env{})
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(out, "sweat-stained") {
		t.Errorf("migrated override did not render its overridden block:\n%s", out)
	}
}

// TestPromptRenderer_DevStoryLandingInline guards the host-agent runtime path:
// prompt files are read into memory and rendered through AppRenderer.Render.
// That path uses pongo2's generated inline name ("template<hex>"), so it can
// fail differently from ValidatePrompt / RenderPrompt, which load by filename.
func TestPromptRenderer_DevStoryLandingInline(t *testing.T) {
	story, _ := filepath.Abs(filepath.Join("..", "..", "stories", "dev-story"))
	prompt := filepath.Join(story, "prompts", "landing.md")
	if !fileExists(prompt) {
		t.Skip("dev-story landing prompt not present")
	}
	src, err := os.ReadFile(prompt)
	if err != nil {
		t.Fatalf("read landing prompt: %v", err)
	}
	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	out, err := r.Render(string(src), expr.Env{Args: map[string]any{"request": "check status"}})
	if err != nil {
		t.Fatalf("render landing prompt inline: %v", err)
	}
	if !strings.Contains(out, "check status") {
		t.Fatalf("rendered prompt missing request:\n%s", out)
	}
}

// TestPromptRenderer_ValidatePrompt: ValidatePrompt parses without executing
// and catches a missing file and an unresolved {% extends %} target.
func TestPromptRenderer_ValidatePrompt(t *testing.T) {
	story := t.TempDir()
	writePrompt(t, story, "prompts/ok.md", "fine {% block b %}d{% endblock %}")
	writePrompt(t, story, "prompts/bad.md", `{% extends "@story/prompts/nope.md" %}`)
	r, err := NewPromptRenderer(PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	if err := r.ValidatePrompt("prompts/ok.md"); err != nil {
		t.Errorf("ok prompt should validate, got: %v", err)
	}
	if err := r.ValidatePrompt("prompts/bad.md"); err == nil {
		t.Error("unresolved extends should fail validation")
	}
	if err := r.ValidatePrompt("prompts/missing.md"); err == nil {
		t.Error("missing prompt should fail validation")
	}
}

// TestPromptRenderer_OverrideIssues: an overlay that overrides a typo'd block
// name (not in the base) is flagged; a correct override is not.
func TestPromptRenderer_OverrideIssues(t *testing.T) {
	story := t.TempDir()
	overlay := t.TempDir()
	writePrompt(t, story, "prompts/x.md", "{% block spec_real %}d{% endblock %}")
	// Overlay overrides a real block AND a typo'd one.
	writePrompt(t, overlay, "prompts/x.md",
		`{% extends "@story/prompts/x.md" %}`+"\n"+
			"{% block spec_real %}ok{% endblock %}\n"+
			"{% block spec_typo %}oops{% endblock %}")

	r, err := NewPromptRenderer(PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	dead := r.OverrideIssues("prompts/x.md")
	if len(dead) != 1 || dead[0] != "spec_typo" {
		t.Fatalf("want [spec_typo] flagged as dead, got %v", dead)
	}

	// No overlay → the base doesn't extend anything → no issues.
	r2, _ := NewPromptRenderer(PromptPath{Story: story}, true)
	if d := r2.OverrideIssues("prompts/x.md"); len(d) != 0 {
		t.Errorf("base prompt should have no override issues, got %v", d)
	}
}

func TestNewPromptRenderer_RequiresStory(t *testing.T) {
	if _, err := NewPromptRenderer(PromptPath{}, true); err == nil {
		t.Fatal("expected error when Story root is empty")
	}
}

func TestEnumerateSpecBlocks(t *testing.T) {
	dir := t.TempDir()
	writePrompt(t, dir, "p.md",
		"{% block spec_hole %}{% endblock %}\n"+
			"{% block spec_default %}working default{% endblock %}\n"+
			"{% block structural %}not a spec surface{% endblock %}")

	blocks, err := EnumerateSpecBlocks([]string{filepath.Join(dir, "p.md")})
	if err != nil {
		t.Fatalf("EnumerateSpecBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("want 2 spec_ blocks (structural omitted), got %d: %+v", len(blocks), blocks)
	}
	// Sorted by name: spec_default, spec_hole.
	if blocks[0].Name != "spec_default" || blocks[0].Hole || blocks[0].Default != "working default" {
		t.Errorf("spec_default wrong: %+v", blocks[0])
	}
	if blocks[1].Name != "spec_hole" || !blocks[1].Hole || blocks[1].Default != "" {
		t.Errorf("spec_hole wrong: %+v", blocks[1])
	}
}
