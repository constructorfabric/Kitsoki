package elements

import (
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/expr"
)

func TestKV_ColonAlignment(t *testing.T) {
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Cash", Value: "$42"},
			{Key: "Oxen", Value: "3"},
			{Key: "Spare wheels", Value: "2"},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), out)
	}
	// "Spare wheels" is the longest key at 12 chars. The colon must
	// sit immediately after every key, so the value column starts at
	// index 12 + 1 ("len(key)+colon") + 2 (separator) = 15 for every
	// row.
	for i, line := range lines {
		// First non-space character after the colon padding should
		// align across rows. Easiest check: find the index of the
		// value.
		want := []string{"$42", "3", "2"}[i]
		idx := strings.Index(line, want)
		if idx != 15 {
			t.Errorf("row %d (%q): value at col %d; expected 15", i, line, idx)
		}
	}
}

func TestKV_PreservesOrder(t *testing.T) {
	// MapSlice is order-preserving; verify our renderer doesn't
	// accidentally sort by key.
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Zulu", Value: "z"},
			{Key: "Alpha", Value: "a"},
			{Key: "Mike", Value: "m"},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "Zulu:") {
		t.Errorf("row 0 should start with Zulu, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "Alpha:") {
		t.Errorf("row 1 should start with Alpha, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "Mike:") {
		t.Errorf("row 2 should start with Mike, got %q", lines[2])
	}
}

func TestKV_PongoInterpolationInValues(t *testing.T) {
	env := expr.Env{World: map[string]any{"money": int64(120), "oxen": int64(2)}}
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Cash", Value: "${{ world.money }}"},
			{Key: "Oxen", Value: "{{ world.oxen }}"},
		},
	}
	out, err := kv.Render(80, env, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "$120") {
		t.Errorf("cash interpolation failed:\n%s", out)
	}
	if !strings.Contains(out, "Oxen:") || !strings.Contains(out, "2") {
		t.Errorf("oxen interpolation failed:\n%s", out)
	}
}

func TestKV_EmptyPairsReturnsEmpty(t *testing.T) {
	kv := KV{Pairs: nil}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("empty kv should return empty, got %q", out)
	}
}

func TestKV_LongValueReflows(t *testing.T) {
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Note", Value: "this is a much longer value that will need to wrap when the available column is narrow"},
		},
	}
	// Narrow width forces wrap.
	out, err := kv.Render(40, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Errorf("expected wrap to multiple lines at width 40, got %d:\n%s", len(lines), out)
	}
	// Continuation lines should indent under the value column.
	// Key "Note" → "Note:" then two-space separator: value column at 7.
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, "       ") {
			t.Errorf("continuation line %d not indented under value column: %q", i+1, line)
		}
	}
}
