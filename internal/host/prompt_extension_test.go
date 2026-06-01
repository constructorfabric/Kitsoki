package host_test

// prompt_extension_test.go — integration coverage for prompt extension at the
// oracle-handler boundary: a prompt renderer injected via WithPromptRenderer
// makes a story's prompt {% extends %} an overlay (or fill blocks) end-to-end
// through OracleAskHandler. We capture the rendered prompt the handler hands to
// the (stubbed) claude runner and assert on the COMBINED, post-extends text the
// LLM would have seen — not a function return — per CLAUDE.md's multi-system
// testing guidance.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/render"
)

// captureRunner returns a ClaudeRunner that records the stdin (the rendered
// prompt) it is handed, and the renderer-agnostic reply.
func captureRunner(captured *string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, stdin, _ string) (host.ClaudeRun, error) {
		*captured = stdin
		return host.ClaudeRun{Stdout: "ok", ExitCode: 0}, nil
	}
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestOracleAsk_OverlayExtendsThroughHandler is the end-to-end proof: the
// effect's prompt_path is unchanged (prompts/diagnose.md) but with an overlay
// injected via WithPromptRenderer the handler resolves it overlay-first, the
// overlay {% extends %} the story base, and the rendered prompt the runner
// receives carries both the inherited structural text and the overlay's filled
// block.
func TestOracleAsk_OverlayExtendsThroughHandler(t *testing.T) {
	story := t.TempDir()
	overlay := t.TempDir()

	writeFile(t, story, "prompts/diagnose.md",
		"STRUCTURAL HEADER\n"+
			"{% block spec_project %}{% endblock %}\n"+
			"STRUCTURAL FOOTER")
	writeFile(t, overlay, "prompts/diagnose.md",
		`{% extends "@story/prompts/diagnose.md" %}`+"\n"+
			"{% block spec_project %}ACME PROJECT CONTEXT{% endblock %}")

	pr, err := render.NewPromptRenderer(render.PromptPath{Story: story, Overlay: overlay}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}

	var captured string
	ctx := host.WithClaudeRunner(context.Background(), captureRunner(&captured))
	ctx = host.WithPromptRenderer(ctx, pr)

	res, err := host.OracleAskHandler(ctx, map[string]any{"prompt_path": "prompts/diagnose.md"})
	if err != nil {
		t.Fatalf("handler Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler Result.Error: %s", res.Error)
	}
	for _, want := range []string{"STRUCTURAL HEADER", "STRUCTURAL FOOTER", "ACME PROJECT CONTEXT"} {
		if !strings.Contains(captured, want) {
			t.Errorf("rendered prompt missing %q; got:\n%s", want, captured)
		}
	}
}

// TestOracleAsk_NoRenderer_ExtendsFails is the guard that the wiring is doing
// the work: the SAME overlay prompt (which {% extends %} a story base) rendered
// WITHOUT a prompt renderer in ctx — the legacy render.Pongo path with no
// template root — cannot resolve the extends and errors. If this ever passes,
// the feature has silently stopped depending on the injected renderer.
func TestOracleAsk_NoRenderer_ExtendsFails(t *testing.T) {
	dir := t.TempDir()
	// A prompt that extends another template; with no loader this can't resolve.
	writeFile(t, dir, "p.md", `{% extends "@story/base.md" %}`+"\n"+`{% block x %}y{% endblock %}`)

	var captured string
	ctx := host.WithClaudeRunner(context.Background(), captureRunner(&captured))
	// No WithPromptRenderer — legacy path.

	res, err := host.OracleAskHandler(ctx, map[string]any{"prompt_path": filepath.Join(dir, "p.md")})
	if err != nil {
		t.Fatalf("handler Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected a render error for {%% extends %%} with no prompt renderer, got success; captured=%q", captured)
	}
	if !strings.Contains(res.Error, "render prompt") {
		t.Fatalf("expected a render error, got %q", res.Error)
	}
}

// TestOracleAsk_NoOverlay_Inert: with a renderer but no overlay, a plain prompt
// renders to exactly its content (args substituted) — the backward-compat
// guarantee at the handler boundary.
func TestOracleAsk_NoOverlay_Inert(t *testing.T) {
	story := t.TempDir()
	writeFile(t, story, "prompts/p.md", "Plain prompt for {{ args.who }}.")

	pr, err := render.NewPromptRenderer(render.PromptPath{Story: story}, true)
	if err != nil {
		t.Fatalf("NewPromptRenderer: %v", err)
	}
	var captured string
	ctx := host.WithClaudeRunner(context.Background(), captureRunner(&captured))
	ctx = host.WithPromptRenderer(ctx, pr)

	res, err := host.OracleAskHandler(ctx, map[string]any{
		"prompt_path": "prompts/p.md",
		"args":        map[string]any{"who": "acme"},
	})
	if err != nil {
		t.Fatalf("handler Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("handler Result.Error: %s", res.Error)
	}
	if want := "Plain prompt for acme."; captured != want {
		t.Fatalf("inert render through handler: got %q want %q", captured, want)
	}
}
