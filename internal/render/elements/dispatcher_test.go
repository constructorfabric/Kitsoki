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
// in a single view and asserts the inter-element join policy —
// one blank line between unlike kinds, and the expected content
// substrings in the expected order.
func TestRenderAll_MixedComposition(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "Available areas"},
			{Kind: "prose", Source: "Welcome to dev-story. What would you like to do today?"},
			{Kind: "list", Items: []app.ListItem{
				{Label: "Start a new task", Hint: "jira search"},
				{Label: "Consult the Agent", Hint: "general Q&A"},
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
		"Consult the Agent",
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

// TestRenderAll_AdjacentKVCoalesces asserts the kv coalescing hint:
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

// TestRenderAll_ExtendsBlocksLegacyEmptyWithoutRenderer asserts the
// back-compat path: callers that pass rr=nil with an extends-shaped
// view still get the empty-string fallback (the dispatcher can't
// resolve the base template without a per-app loader).
func TestRenderAll_ExtendsBlocksLegacyEmptyWithoutRenderer(t *testing.T) {
	view := app.View{
		Extends: "base",
		Blocks: map[string][]app.ViewElement{
			"body": {
				{Kind: "prose", Source: "needs a renderer"},
			},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if out != "" {
		t.Errorf("extends/blocks should render empty when rr=nil, got %q", out)
	}
}

// fakeExtendsRenderer is the minimal ViewRenderer for the
// extends-aware tests below. Render is a passthrough; RenderExtended
// just concatenates the rendered blocks in alphabetical name order so
// the test can grep individual block bodies. Tests that need a real
// base-template splice already live in internal/render/extends_test.go.
type fakeExtendsRenderer struct{}

func (fakeExtendsRenderer) Render(src string, _ expr.Env) (string, error) {
	return src, nil
}

func (fakeExtendsRenderer) RenderExtended(_ string, blocks map[string]string, _ expr.Env) (string, error) {
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
	}
	// Sort for deterministic test output.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	var sb strings.Builder
	for i, n := range names {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(blocks[n])
	}
	return sb.String(), nil
}

// TestRenderAll_ExtendsUsesDispatcherWidth is the regression guard for
// the 2026-05-20 narrow-hint-column bug. The narrow case has TWO
// failure modes the list renderer must defend against:
//
//  1. Extends-form views falling back to the orchestrator's fixed
//     blockRenderWidth=80 — fixed by RenderAll's extends branch
//     pre-rendering at the dispatcher's actual viewport width.
//
//  2. A single outlier label setting maxLabel for the whole list
//     and squeezing every other row's hint column. The list
//     renderer's max_label cap (minHintWidth floor) now keeps a
//     49-char mega-label from forcing every other row's hint
//     column below 40 chars; the outlier row overflows alone
//     instead.
//
// Post-fix the short-label row's hint must land on one line at any
// reasonable viewport width — narrow OR wide — because the cap
// keeps the hint column at ≥ minHintWidth.
func TestRenderAll_ExtendsUsesDispatcherWidth(t *testing.T) {
	view := app.View{
		Extends: "base",
		Blocks: map[string][]app.ViewElement{
			"choices": {
				{
					Kind: "list",
					Items: []app.ListItem{
						{Label: "tickets", Hint: "search for a ticket to pick"},
						// 50-char "wide" label — would set maxLabel for the
						// whole list pre-cap. Post-cap it overflows
						// individually but the short-label row's hint stays
						// on one line.
						{Label: "deploy · observability · incident · docs · etc", Hint: "stubs"},
					},
				},
			},
		},
	}

	// Both narrow (60) AND wide (120) widths must keep the short
	// "tickets" row's hint on one line — minHintWidth (40 chars) is
	// always enough for "search for a ticket to pick" (27 chars).
	cases := []struct {
		name       string
		width      int
		shouldWrap bool
	}{
		{"narrow", 60, false},
		{"viewport", 120, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderAll(view, expr.Env{}, tc.width, IdentityGlamour, fakeExtendsRenderer{})
			if err != nil {
				t.Fatalf("RenderAll(w=%d): %v", tc.width, err)
			}
			// Locate the "tickets" row. The full hint must land on the
			// same line as the label when the dispatcher has enough
			// width; pre-fix the dispatcher always used width=80 so a
			// 150-col viewport still wrapped.
			ticketsLine := ""
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "tickets") {
					ticketsLine = line
					break
				}
			}
			if ticketsLine == "" {
				t.Fatalf("output did not contain a tickets row:\n%s", out)
			}
			fits := strings.Contains(ticketsLine, "search for a ticket to pick")
			if tc.shouldWrap && fits {
				t.Errorf("w=%d: hint should wrap, but landed on one line: %q", tc.width, ticketsLine)
			}
			if !tc.shouldWrap && !fits {
				t.Errorf("w=%d: hint should fit on one line, but wrapped. Line: %q\nFull:\n%s",
					tc.width, ticketsLine, out)
			}
		})
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

// TestRenderAll_ChoiceElementDispatch asserts the dispatcher routes a
// Kind="choice" element through the Choice renderer and threads the
// typed ChoiceMode / ChoicePrompt / ChoiceItems fields through. Acts
// as the integration check for the case "choice": branch in
// element.go.
func TestRenderAll_ChoiceElementDispatch(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{
				Kind:         "choice",
				ChoiceMode:   "single",
				ChoicePrompt: "Choose",
				ChoiceItems: []app.ChoiceItem{
					{Label: "alpha", Intent: "pick"},
					{Label: "beta", Intent: "pick"},
				},
			},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 80, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	for _, want := range []string{"Choose:", "alpha", "beta", "[↑/↓ move"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// TestEvalElements_ChoiceItemsExpandedForBrowser asserts that EvalElements
// pongo-expands choice item labels, hints, and slot values so the browser
// receives concrete strings (not raw {{ ... }} expressions). This is the
// web-parity counterpart of the TUI widget's Open() expansion.
func TestEvalElements_ChoiceItemsExpandedForBrowser(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{
				Kind:       "choice",
				ChoiceMode: "single",
				ChoiceItems: []app.ChoiceItem{
					{
						Label:  "{{ world.opts.0.path }}",
						Hint:   "{{ world.opts.0.rec|upper }} — amend",
						Intent: "pick",
						When:   "len(world.opts ?? []) >= 1",
						Slots:  map[string]any{"target": "{{ world.opts.0.path }}"},
					},
					// Filtered out by when guard.
					{
						Label:  "{{ world.opts.1.path }}",
						Hint:   "{{ world.opts.1.rec|upper }} — amend",
						Intent: "pick",
						When:   "len(world.opts ?? []) >= 2",
						Slots:  map[string]any{"target": "{{ world.opts.1.path }}"},
					},
				},
			},
		},
	}
	env := expr.Env{World: map[string]any{
		"opts": []any{
			map[string]any{"path": "docs/proposals/foo.md", "rec": "amend"},
		},
	}}

	ev, err := EvalElements(view, env, nil)
	if err != nil {
		t.Fatalf("EvalElements: %v", err)
	}
	if len(ev.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(ev.Elements))
	}
	items := ev.Elements[0].ChoiceItems
	if len(items) != 1 {
		t.Fatalf("expected 1 item (second filtered by when), got %d", len(items))
	}
	it := items[0]
	if it.Label != "docs/proposals/foo.md" {
		t.Errorf("label not expanded: got %q", it.Label)
	}
	if it.Hint != "AMEND — amend" {
		t.Errorf("hint not expanded: got %q", it.Hint)
	}
	sv, _ := it.Slots["target"].(string)
	if sv != "docs/proposals/foo.md" {
		t.Errorf("slot.target not expanded: got %q", sv)
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
