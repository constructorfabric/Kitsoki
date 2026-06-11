package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveDecidePrompt_PromptAsPath is the regression for the decide-vs-task
// background-render asymmetry: the bugfix story passes a FILE PATH via `prompt:`
// (a pre-rendered prompt path bound from a prior host.run). The file holds a
// template referencing {{ args.ticket }} and {{ args.context.bug_description }}.
//
// Before the fix, resolveDecidePrompt treated `prompt:` as literal inline text,
// so it emitted the path string verbatim and never read/rendered the file — the
// dispatched prompt either carried the bare path or (when the file leaked
// through another seam) literal {{ args.context.* }} placeholders. The task path
// (resolveTaskContextPrompt) reads `prompt:` as a file, which is why it rendered
// correctly. This asserts decide now matches: `prompt:` is path-first.
func TestResolveDecidePrompt_PromptAsPath(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "03-fix-proposal.txt")
	body := "Ticket: {{ args.ticket }}\nDesc: {{ args.context.bug_description }}\n"
	if err := os.WriteFile(pf, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	args := map[string]any{
		"prompt": pf, // path passed via prompt:, exactly as the story does
		"args": map[string]any{
			"ticket": "PROJ-1234",
			"context": map[string]any{
				// Jira-shaped leaf: newlines, markdown, embedded quotes.
				"bug_description": "Grid shows 10 not 20\n# Repro\n\"quoted\"",
			},
		},
	}

	rendered, errMsg := resolveDecidePrompt(context.Background(), args)
	if errMsg != "" {
		t.Fatalf("resolveDecidePrompt returned error: %s", errMsg)
	}
	if strings.Contains(rendered, "{{") {
		t.Fatalf("rendered prompt still contains literal templates: %q", rendered)
	}
	if !strings.Contains(rendered, "PROJ-1234") || !strings.Contains(rendered, "Grid shows 10") {
		t.Fatalf("substituted values missing: %q", rendered)
	}
}

// TestResolveDecidePrompt_PromptInlineStillWorks guards the legitimate inline
// use: a `prompt:` that is genuine prompt TEXT (not a path) must still be used
// verbatim-then-rendered, not swallowed by the path-first probe.
func TestResolveDecidePrompt_PromptInlineStillWorks(t *testing.T) {
	args := map[string]any{
		"prompt": "Decide on ticket {{ args.ticket }} now.",
		"args": map[string]any{
			"ticket": "PROJ-9",
		},
	}
	rendered, errMsg := resolveDecidePrompt(context.Background(), args)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if rendered != "Decide on ticket PROJ-9 now." {
		t.Fatalf("inline prompt mis-rendered: %q", rendered)
	}
}

// TestResolveDecidePrompt_PromptPathExplicit confirms the explicit prompt_path:
// branch is unchanged (a missing file is still a hard error).
func TestResolveDecidePrompt_PromptPathExplicit(t *testing.T) {
	_, errMsg := resolveDecidePrompt(context.Background(), map[string]any{
		"prompt_path": "/no/such/decide/prompt/file.txt",
	})
	if !strings.Contains(errMsg, "read prompt") {
		t.Fatalf("expected read-prompt error for missing prompt_path, got %q", errMsg)
	}
}
