package elements

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

// osc8 is the OSC 8 introducer; presence in the raw output proves a
// terminal hyperlink was emitted, absence proves it was not.
const osc8 = "\x1b]8;;"

// TestKV_OSC8Wrap_MarkdownValue verifies that a .md kv value is wrapped in
// an OSC 8 hyperlink AND that the VISIBLE text (escapes stripped) is
// byte-identical to the same row rendered without the change — so the
// column/width math is untouched. This is the load-bearing guarantee of the
// lean v1 (linkify-only-when-it-fits) approach.
//
// Reverting the linkify block in kv.go makes this test fail at the
// "expected OSC 8 escape" assertion (the raw output no longer contains the
// introducer), per the proposal's "assert it fails without the change".
func TestKV_OSC8Wrap_MarkdownValue(t *testing.T) {
	path := "docs/proposals/tui-md-links.md"
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Brief", Value: path},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The raw output must carry the OSC 8 escape...
	if !strings.Contains(out, osc8) {
		t.Fatalf("expected OSC 8 escape around %q, got raw:\n%q", path, out)
	}
	// ...pointing at the absolute file:// URL for the path...
	abs, _ := filepath.Abs(path)
	if !strings.Contains(out, "file://"+abs) {
		t.Errorf("expected file:// target %q in raw output:\n%q", "file://"+abs, out)
	}

	// ...and the VISIBLE text (escapes stripped) must equal the plain
	// render byte-for-byte.
	visible := ansi.Strip(out)
	wantVisible := "Brief:  " + path
	if visible != wantVisible {
		t.Errorf("visible text mismatch:\n got %q\nwant %q", visible, wantVisible)
	}
}

func TestKV_OSC8Wrap_MarkdownLinkURL(t *testing.T) {
	value := "[Kitsoku Issue #61](https://github.com/constructorfabric/Kitsoki/issues/61)"
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Issue", Value: value},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, osc8) {
		t.Fatalf("expected OSC 8 escape around %q, got raw:\n%q", value, out)
	}
	if !strings.Contains(out, "https://github.com/constructorfabric/Kitsoki/issues/61") {
		t.Fatalf("expected issue URL target in raw output:\n%q", out)
	}
	if visible := ansi.Strip(out); visible != "Issue:  Kitsoku Issue #61" {
		t.Fatalf("visible text mismatch:\n got %q\nwant %q", visible, "Issue:  Kitsoku Issue #61")
	}
}

func TestKV_OSC8Wrap_MarkdownLinkFileTarget(t *testing.T) {
	value := "[case-05 trace](.artifacts/dogfood-marathon/bug15-real-tui/traces/case-05.jsonl)"
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Trace", Value: value},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, osc8) {
		t.Fatalf("expected OSC 8 escape around %q, got raw:\n%q", value, out)
	}
	abs, _ := filepath.Abs(".artifacts/dogfood-marathon/bug15-real-tui/traces/case-05.jsonl")
	if !strings.Contains(out, "file://"+abs) {
		t.Fatalf("expected trace file target %q in raw output:\n%q", "file://"+abs, out)
	}
	if visible := ansi.Strip(out); visible != "Trace:  case-05 trace" {
		t.Fatalf("visible text mismatch:\n got %q\nwant %q", visible, "Trace:  case-05 trace")
	}
}

// TestKV_OSC8_NoEscapeForPlainText verifies ordinary non-link values emit no
// OSC 8 escape.
func TestKV_OSC8_NoEscapeForPlainText(t *testing.T) {
	kv := KV{
		Pairs: goyaml.MapSlice{
			{Key: "Note", Value: "docs/proposals/tui-md-links.txt"},
			{Key: "Cash", Value: "$42"},
		},
	}
	out, err := kv.Render(80, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(out, osc8) {
		t.Errorf("non-.md values must not be linkified, got raw:\n%q", out)
	}
}

// TestKV_OSC8_WidthUnaffected guards the width trap: a row whose value is a
// linkified .md path must wrap at the exact same visible columns as the same
// row rendered plain. We compare the escape-stripped output of a kv block
// containing a fitting .md path against an identical block whose value is a
// non-.md string of the same visible length — the visible layout must match.
func TestKV_OSC8_WidthUnaffected(t *testing.T) {
	const width = 80
	mdPath := "docs/x/brief.md"                       // 15 visible cols
	plain := strings.Repeat("y", len([]rune(mdPath))) // same visible width, no link

	linked, err := KV{Pairs: goyaml.MapSlice{
		{Key: "Brief", Value: mdPath},
	}}.Render(width, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render(linked): %v", err)
	}
	bare, err := KV{Pairs: goyaml.MapSlice{
		{Key: "Brief", Value: plain},
	}}.Render(width, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render(bare): %v", err)
	}

	// The linked render must carry an escape; the bare one must not.
	if !strings.Contains(linked, osc8) {
		t.Fatalf("expected linked render to contain OSC 8 escape:\n%q", linked)
	}
	if strings.Contains(bare, osc8) {
		t.Fatalf("expected bare render to contain no OSC 8 escape:\n%q", bare)
	}

	// Visible layout must be column-identical once the value glyphs are
	// normalized away: same line count and same column where the value
	// starts.
	lv := strings.Split(ansi.Strip(linked), "\n")
	bv := strings.Split(ansi.Strip(bare), "\n")
	if len(lv) != len(bv) {
		t.Fatalf("line count differs: linked=%d bare=%d\nlinked=%q\nbare=%q",
			len(lv), len(bv), lv, bv)
	}
	if got, want := strings.Index(lv[0], mdPath), strings.Index(bv[0], plain); got != want {
		t.Errorf("value column differs: linked=%d bare=%d", got, want)
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
