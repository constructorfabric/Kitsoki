package elements

import (
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// TestRenderAll_EmptyViewReturnsEmpty asserts the zero View renders
// without error and emits no output. This is the contract that
// transcriptModel's call sites rely on — they call renderView on every
// turn, even when the orchestrator returned no view payload.
func TestRenderAll_EmptyViewReturnsEmpty(t *testing.T) {
	out, err := RenderAll(app.View{}, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if out != "" {
		t.Errorf("empty view should render empty, got %q", out)
	}
}

// TestRenderAll_MixedComposition exercises the full element-kind menu
// in a single view and asserts the join policy from proposal §5.3 —
// one blank line between unlike kinds, and the expected content
// substrings in the expected order.
func TestRenderAll_MixedComposition(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "Available areas"},
			{Kind: "prose", Source: "Welcome to dev-story. What would you like to do today?"},
			{Kind: "list", Items: []app.ListItem{
				{Label: "Start a new task", Hint: "jira search"},
				{Label: "Consult the Oracle", Hint: "general Q&A"},
			}},
			{Kind: "kv", Pairs: goyaml.MapSlice{
				{Key: "Inbox", Value: "3 unread"},
			}},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	wantSubstrings := []string{
		"Available areas",
		"Welcome to dev-story",
		"Start a new task",
		"jira search",
		"Consult the Oracle",
		"general Q&A",
		"Inbox:",
		"3 unread",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}

	// Verify the ordering: each substring must appear before the next.
	prev := -1
	for _, s := range wantSubstrings {
		idx := strings.Index(out, s)
		if idx < prev {
			t.Errorf("substring %q appears before its predecessor in output:\n%s", s, out)
		}
		prev = idx
	}
}

// TestRenderAll_BlankLineBetweenUnlikeKinds asserts that unlike-kind
// neighbours are separated by exactly one blank line (== "\n\n" in the
// joined output).
func TestRenderAll_BlankLineBetweenUnlikeKinds(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "Heading"},
			{Kind: "prose", Source: "Paragraph."},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	// Two segments joined by exactly one blank line.
	if !strings.Contains(out, "\n\n") {
		t.Errorf("expected blank line between heading and prose:\n%s", out)
	}
}

// TestRenderAll_AdjacentKVCoalesces asserts the §5.3 coalescing hint:
// two `kv` elements in a row read as one block (no blank line
// between).
func TestRenderAll_AdjacentKVCoalesces(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "kv", Pairs: goyaml.MapSlice{{Key: "A", Value: "1"}}},
			{Kind: "kv", Pairs: goyaml.MapSlice{{Key: "B", Value: "2"}}},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	// Two kv elements glued with a single newline (no blank between).
	if strings.Contains(out, "\n\n") {
		t.Errorf("adjacent kv elements must coalesce (no blank line):\n%s", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines for two coalesced kv rows, got %d:\n%s", len(lines), out)
	}
}

// TestRenderAll_WhenFiltersElement asserts the dispatcher drops a whole
// element when its `when:` guard fails, and that no blank line stub is
// left behind.
func TestRenderAll_WhenFiltersElement(t *testing.T) {
	env := expr.Env{World: map[string]any{"show": false}}
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", Source: "always visible"},
			{Kind: "prose", Source: "hidden", When: "world.show"},
			{Kind: "prose", Source: "also always visible"},
		},
	}
	out, err := RenderAll(view, env, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("filtered element appeared:\n%s", out)
	}
	if !strings.Contains(out, "always visible") || !strings.Contains(out, "also always visible") {
		t.Errorf("survivors missing:\n%s", out)
	}
	// Survivors joined by exactly one blank line, not two (no
	// vacant slot from the filtered element).
	if strings.Count(out, "\n\n") != 1 {
		t.Errorf("expected exactly one blank line in output, got %d:\n%s", strings.Count(out, "\n\n"), out)
	}
}

// TestRenderAll_ExtendsBlocksPlaceholder asserts that the extends /
// blocks form renders the empty string in Phase D — Phase H will wire
// real resolution. Authoring this surface before Phase H lands shows a
// clear empty body rather than a malformed dump.
func TestRenderAll_ExtendsBlocksPlaceholder(t *testing.T) {
	view := app.View{
		Extends: "base",
		Blocks: map[string][]app.ViewElement{
			"body": {
				{Kind: "prose", Source: "ignored in Phase D"},
			},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if out != "" {
		t.Errorf("extends/blocks should render empty in Phase D, got %q", out)
	}
}

// TestRenderAll_WidthAffectsLayout exercises the dispatcher at multiple
// widths and confirms the prose / list / kv elements reflow without
// breaking. Specifically asserts that no line exceeds the requested
// width for prose-only content (lists / kv have soft-floor exceptions
// noted in their renderer docs).
func TestRenderAll_WidthAffectsLayout(t *testing.T) {
	long := strings.Repeat("word ", 30)
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", Source: long},
		},
	}
	for _, width := range []int{40, 80, 120} {
		out, err := RenderAll(view, expr.Env{}, width, IdentityGlamour, nil)
		if err != nil {
			t.Fatalf("RenderAll(w=%d): %v", width, err)
		}
		for _, line := range strings.Split(out, "\n") {
			if n := len([]rune(line)); n > width {
				t.Errorf("width=%d: line is %d chars: %q", width, n, line)
			}
		}
	}
}

// TestRenderAll_UnknownKindErrors asserts that an unknown element kind
// produces an error rather than silently rendering nothing — the
// loader validates known kinds, so an unknown one in the dispatch path
// means a programming bug elsewhere and we want it surfaced.
func TestRenderAll_UnknownKindErrors(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "wat", Source: "x"},
		},
	}
	_, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "wat") {
		t.Errorf("error should name the offending kind, got %v", err)
	}
}

// TestRenderAll_WhenGuardCacheIsPerSource asserts the guard cache
// keys on the raw expression source — repeated guards across rooms
// compile once. We exercise this indirectly by running the same guard
// expression twice against different envs and asserting we get the
// expected results (which requires the cached program to evaluate
// correctly against fresh envs, not the env it was compiled against).
func TestRenderAll_WhenGuardCacheIsPerSource(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "prose", Source: "x", When: "world.flag"},
		},
	}
	out, err := RenderAll(view, expr.Env{World: map[string]any{"flag": true}}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll(flag=true): %v", err)
	}
	if !strings.Contains(out, "x") {
		t.Errorf("flag=true should keep element, got %q", out)
	}
	out, err = RenderAll(view, expr.Env{World: map[string]any{"flag": false}}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll(flag=false): %v", err)
	}
	if strings.Contains(out, "x") {
		t.Errorf("flag=false should drop element, got %q", out)
	}
}
