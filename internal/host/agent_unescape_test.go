package host

import (
	"reflect"
	"strings"
	"testing"
)

// TestUnescapeOverEscapedStrings_FixesMarkdownBody covers the dogfood
// symptom that motivated the helper: claude submitted a multi-line
// markdown body but JSON-encoded the newlines as literal `\n` text
// (over-escaping). The validator preserves the bytes; Unmarshal then
// returns a string with literal `\n` substrings. The helper rewrites
// those back to real newlines so the view renders correctly.
func TestUnescapeOverEscapedStrings_FixesMarkdownBody(t *testing.T) {
	in := map[string]any{
		"summary_markdown": `## Title\n\n### Section\n\nbody line one\nbody line two\nbody line three`,
	}
	got := unescapeOverEscapedStrings(in).(map[string]any)
	want := "## Title\n\n### Section\n\nbody line one\nbody line two\nbody line three"
	if got["summary_markdown"] != want {
		t.Fatalf("expected unescape, got %q", got["summary_markdown"])
	}
}

// TestUnescapeOverEscapedStrings_LeavesShortIncidentalEscapesAlone
// guards against the false-positive case: a short docstring that
// happens to mention `\n` (e.g. "use \n as the separator") should NOT
// be rewritten. We require >= 3 occurrences before unescaping.
func TestUnescapeOverEscapedStrings_LeavesShortIncidentalEscapesAlone(t *testing.T) {
	in := `Use \n as the separator.`
	got := unescapeOverEscapedStrings(in).(string)
	if got != in {
		t.Fatalf("expected pass-through for ≤2 escapes, got %q", got)
	}
}

// TestUnescapeOverEscapedStrings_WalksNestedStructures ensures the
// helper recurses through maps and slices — schemas typically nest
// markdown fields inside an object, sometimes inside an array of
// sub-artifacts.
func TestUnescapeOverEscapedStrings_WalksNestedStructures(t *testing.T) {
	in := map[string]any{
		"top": map[string]any{
			"body": `line1\nline2\nline3\nline4`,
		},
		"items": []any{
			map[string]any{"text": `a\nb\nc\nd`},
		},
		"untouched": "no escapes here",
	}
	got := unescapeOverEscapedStrings(in).(map[string]any)
	wantBody := "line1\nline2\nline3\nline4"
	if got["top"].(map[string]any)["body"] != wantBody {
		t.Fatalf("nested body not unescaped: %q", got["top"].(map[string]any)["body"])
	}
	gotItem := got["items"].([]any)[0].(map[string]any)
	if gotItem["text"] != "a\nb\nc\nd" {
		t.Fatalf("array element not unescaped: %q", gotItem["text"])
	}
	if got["untouched"] != "no escapes here" {
		t.Fatalf("untouched field changed: %q", got["untouched"])
	}
}

// TestUnescapeOverEscapedStrings_PreservesNonStringValues makes sure
// numbers, bools, and nil flow through unchanged. The helper used to
// reflect.DeepEqual the input as the contract for non-string scalars
// before the switch-based rewrite; this test pins that contract.
func TestUnescapeOverEscapedStrings_PreservesNonStringValues(t *testing.T) {
	in := map[string]any{
		"n":   float64(42),
		"b":   true,
		"nil": nil,
	}
	got := unescapeOverEscapedStrings(in)
	if !reflect.DeepEqual(in, got) {
		t.Fatalf("non-string scalars changed: got %#v", got)
	}
}

// TestMaybeUnescapeString_TabAndCR mirrors the markdown case for the
// other two whitespace escapes we rewrite. Real-world claude payloads
// occasionally over-escape \t in code blocks; \r is rare but cheap to
// cover.
func TestMaybeUnescapeString_TabAndCR(t *testing.T) {
	in := `a\tb\tc\td`
	got := maybeUnescapeString(in)
	if got != "a\tb\tc\td" {
		t.Fatalf("tab unescape failed: %q", got)
	}
	in = strings.Repeat(`x\r`, 3) + "y"
	got = maybeUnescapeString(in)
	want := strings.Repeat("x\r", 3) + "y"
	if got != want {
		t.Fatalf("cr unescape failed: %q", got)
	}
}
