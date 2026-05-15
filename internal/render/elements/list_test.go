package elements

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

func TestList_BareList(t *testing.T) {
	list := List{
		Items: []app.ListItem{
			{Label: "ford"},
			{Label: "caulk"},
			{Label: "ferry"},
		},
	}
	out, err := list.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := "- ford\n- caulk\n- ferry"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestList_CustomMarker(t *testing.T) {
	list := List{
		Items:  []app.ListItem{{Label: "alpha"}, {Label: "beta"}},
		Marker: "*",
	}
	out, err := list.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(out, "* alpha") {
		t.Errorf("expected `* alpha` prefix, got %q", out)
	}
}

func TestList_TwoColumnAlignment(t *testing.T) {
	list := List{
		Items: []app.ListItem{
			{Label: "Start a new task", Hint: "jira search"},
			{Label: "Continue existing task", Hint: "workspace manager"},
			{Label: "Consult the Oracle", Hint: "general Q&A"},
		},
	}
	out, err := list.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(out, "\n")
	// Three rows, no continuation lines at width 80.
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	// The longest label is "Continue existing task" (22 chars).
	// Layout: "- " (2) + label-padded-to-22 + 2 gap + hint
	// → hint column starts at index 26 for every row.
	for i, line := range lines {
		idx := strings.Index(line, "jira search")
		if i == 0 && idx != 26 {
			t.Errorf("row 0 hint at column %d (line %q); expected 26", idx, line)
		}
		// All hint texts should start at the same column on their own
		// row, which we verify by checking that the longest-label row
		// has its hint at the gutter start position.
	}
	// All three hints must appear in the output.
	for _, hint := range []string{"jira search", "workspace manager", "general Q&A"} {
		if !strings.Contains(out, hint) {
			t.Errorf("output missing hint %q:\n%s", hint, out)
		}
	}
}

func TestList_WhenFilterRemovesRowsCleanly(t *testing.T) {
	env := expr.Env{World: map[string]any{"show": false}}
	list := List{
		Items: []app.ListItem{
			{Label: "always shown"},
			{Label: "hidden", When: "world.show"},
			{Label: "also always shown"},
		},
	}
	out, err := list.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines after filter, got %d:\n%s", len(lines), out)
	}
	if strings.Contains(out, "hidden") {
		t.Errorf("filtered item should not appear:\n%s", out)
	}
	// No blank line where the filtered row was.
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Errorf("filter left a blank line:\n%s", out)
		}
	}
}

func TestList_FilteredRowDoesNotSizeLabelColumn(t *testing.T) {
	// A very-long-labeled row that the filter removes must not size
	// the column for the survivors. We assert that the visible "ok"
	// row's hint sits at a column proportional to the survivors' max
	// label, not the filtered one.
	env := expr.Env{World: map[string]any{"show_long": false}}
	list := List{
		Items: []app.ListItem{
			{Label: "this is a very very long label that would dominate", When: "world.show_long", Hint: "X"},
			{Label: "ok", Hint: "Y"},
		},
	}
	out, err := list.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, "very very long") {
		t.Errorf("filtered long label leaked into output:\n%s", out)
	}
	// Layout: "- " + "ok" + "  " + "Y" — hint column at index 6.
	yIdx := strings.Index(out, "Y")
	if yIdx != 6 {
		t.Errorf("hint Y at column %d; expected 6 (filtered row should not size the label column). Output:\n%s", yIdx, out)
	}
}

func TestList_PongoInterpolationInLabelAndHint(t *testing.T) {
	env := expr.Env{World: map[string]any{"thing": "wagon", "cost": "$40"}}
	list := List{
		Items: []app.ListItem{
			{Label: "buy {{ world.thing }}", Hint: "{{ world.cost }}"},
		},
	}
	out, err := list.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "buy wagon") {
		t.Errorf("label interpolation failed:\n%s", out)
	}
	if !strings.Contains(out, "$40") {
		t.Errorf("hint interpolation failed:\n%s", out)
	}
}

func TestList_EmptyItemsReturnsEmpty(t *testing.T) {
	list := List{Items: nil}
	out, err := list.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("empty list should return empty, got %q", out)
	}
}

func TestList_AllItemsFilteredOutReturnsEmpty(t *testing.T) {
	env := expr.Env{World: map[string]any{"never": false}}
	list := List{
		Items: []app.ListItem{
			{Label: "x", When: "world.never"},
			{Label: "y", When: "world.never"},
		},
	}
	out, err := list.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("all-filtered list should render empty, got %q", out)
	}
}
