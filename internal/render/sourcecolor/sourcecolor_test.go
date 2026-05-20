package sourcecolor

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWrap_NonEmpty(t *testing.T) {
	t.Parallel()
	got := Wrap("hello")
	want := llmOpen + "hello" + llmClose
	if got != want {
		t.Fatalf("Wrap = %q, want %q", got, want)
	}
	if !IsWrapped(got) {
		t.Fatalf("IsWrapped(%q) = false, want true", got)
	}
}

func TestWrap_Empty(t *testing.T) {
	t.Parallel()
	if got := Wrap(""); got != "" {
		t.Fatalf("Wrap(\"\") = %q, want \"\"", got)
	}
}

func TestStrip_Roundtrip(t *testing.T) {
	t.Parallel()
	cases := []string{
		"plain text — no sentinels",
		Wrap("just one"),
		"prefix " + Wrap("middle") + " suffix",
		Wrap("outer " + Wrap("inner") + " more outer"),
		"multi\n" + Wrap("line\nllm") + "\nback",
	}
	for _, in := range cases {
		stripped := Strip(in)
		if strings.Contains(stripped, llmOpen) || strings.Contains(stripped, llmClose) {
			t.Errorf("Strip(%q) still contains sentinels: %q", in, stripped)
		}
	}
}

func TestSentinels_ZeroVisibleWidth(t *testing.T) {
	t.Parallel()
	// Each sentinel must consume zero visible columns. If this ever
	// regresses, any width-aware truncator could bisect a sentinel
	// and corrupt the source-tag stream.
	for name, s := range map[string]string{"open": llmOpen, "close": llmClose} {
		if w := ansi.StringWidth(s); w != 0 {
			t.Errorf("%s sentinel width = %d, want 0 (chars: %x)", name, w, []rune(s))
		}
	}
}

func TestSentinels_SurviveHardwrap(t *testing.T) {
	t.Parallel()
	// A user-visible scenario: a wide LLM span gets wrapped to a
	// narrow column by the transcript's queue() pass. The sentinels
	// must come through intact so Colorize can still find them.
	body := "prefix " + Wrap("the llm wrote some longer text that should wrap") + " suffix"
	wrapped := ansi.Hardwrap(body, 12, false)
	if !strings.Contains(wrapped, llmOpen) {
		t.Fatalf("hardwrap dropped open sentinel: %q", wrapped)
	}
	if !strings.Contains(wrapped, llmClose) {
		t.Fatalf("hardwrap dropped close sentinel: %q", wrapped)
	}
	// And after stripping the sentinels, the visible text must be
	// the same hardwrap a sentinel-free input would produce.
	plain := "prefix the llm wrote some longer text that should wrap suffix"
	plainWrapped := ansi.Hardwrap(plain, 12, false)
	if got := Strip(wrapped); got != plainWrapped {
		t.Fatalf("hardwrap+strip differs from plain hardwrap:\n got  = %q\n want = %q", got, plainWrapped)
	}
}

func TestColorize_NoSentinels_FastPath(t *testing.T) {
	t.Parallel()
	in := "no LLM here at all\nsecond line"
	got := Colorize(in, DarkTheme, Options{})
	if got != in {
		t.Fatalf("Colorize over plain text should return identity:\n got  = %q\n want = %q", got, in)
	}
}

func TestColorize_Inline(t *testing.T) {
	t.Parallel()
	in := "Title is " + Wrap("Hello, world") + "."
	got := Colorize(in, DarkTheme, Options{Width: 40})

	// Should contain template bg, switch to LLM bg around the wrapped
	// text, then switch back to template bg for the trailing ".".
	if !strings.Contains(got, DarkTheme.TplBG) {
		t.Errorf("missing template bg in output: %q", got)
	}
	if !strings.Contains(got, DarkTheme.LLMBG) {
		t.Errorf("missing LLM bg in output: %q", got)
	}
	// Inline span must NOT be padded — visible text is "Title is Hello, world."
	// which is 22 chars; if it had been padded to 40, the output (after
	// stripping ANSI) would be 40 wide.
	visible := ansi.Strip(got)
	if got := strings.TrimRight(visible, " "); got != "Title is Hello, world." {
		t.Errorf("inline got padded; visible = %q", visible)
	}
}

func TestColorize_Block_PadsToWidth(t *testing.T) {
	t.Parallel()
	body := Wrap("line one\nline two\nline three")
	got := Colorize(body, DarkTheme, Options{Width: 20})

	stripped := ansi.Strip(got)
	lines := strings.Split(stripped, "\n")
	for i, line := range lines {
		// Strip trailing whitespace ONLY if there's content; empty
		// lines have nothing to assert.
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) != 20 {
			t.Errorf("line %d not padded to 20: len=%d %q", i, len(line), line)
		}
	}
}

func TestColorize_Block_RectangleWhenWidthZero(t *testing.T) {
	t.Parallel()
	// With Width=0, the block should pad to the longest line in the
	// block — a content-shaped rectangle, not edge-to-edge.
	body := Wrap("short\nmuch longer line\nmid")
	got := Colorize(body, DarkTheme, Options{Width: 0})
	stripped := ansi.Strip(got)
	lines := strings.Split(stripped, "\n")
	const want = 16 // len("much longer line")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) != want {
			t.Errorf("line %d not padded to %d: len=%d %q", i, want, len(line), line)
		}
	}
}

func TestColorize_Nested(t *testing.T) {
	t.Parallel()
	// Pattern: agent quoting an earlier LLM line — an LLM span
	// containing another LLM span. Both layers are warm bg in the
	// current design, but the inner exit must restore the outer's
	// LLM bg (not the template bg) so the warm band stays solid.
	in := "prefix " + Wrap("outer "+Wrap("inner")+" more") + " suffix"
	got := Colorize(in, DarkTheme, Options{Width: 80})

	// The transition sequence we expect: tpl → llm → llm (re-emit on
	// inner open, harmless) → llm (on inner close, pop restores
	// outer's llm bg) → tpl (on outer close).
	//
	// Find the *last* tpl-bg switch in the output: it must come
	// AFTER the close of the outer span. If the outer close
	// incorrectly emitted tpl (instead of going back through llm
	// first), the structure would invert.
	lastTpl := strings.LastIndex(got, DarkTheme.TplBG)
	lastLlm := strings.LastIndex(got, DarkTheme.LLMBG)
	if lastTpl <= lastLlm {
		t.Errorf("last tpl-bg (%d) should follow last llm-bg (%d): %q", lastTpl, lastLlm, got)
	}

	// Visible text round-trips through Strip.
	if want, got := "prefix outer inner more suffix", Strip(in); got != want {
		t.Errorf("Strip after wrap: got %q want %q", got, want)
	}
}

func TestColorize_LinesStartFreshly(t *testing.T) {
	t.Parallel()
	// Every line must re-emit the active bg at its start so terminal
	// renderers that issue per-line clears (or a stray downstream
	// reset) don't drop the band on subsequent lines.
	body := Wrap("\nfirst\nsecond\nthird\n")
	got := Colorize(body, DarkTheme, Options{Width: 30})

	// Count occurrences of the LLM bg switch — should be ≥ once per
	// content line (the open sentinel emits it, and every subsequent
	// line re-emits at start).
	n := strings.Count(got, DarkTheme.LLMBG)
	if n < 4 {
		t.Errorf("expected ≥ 4 LLM-bg emissions (one per line in the block), got %d in %q", n, got)
	}
}

func TestColorize_ShoulderRows(t *testing.T) {
	t.Parallel()
	// A block written as L("\nfoo\n") yields two "empty" line slots:
	// one with just the open sentinel (above "foo") and one with
	// just the close sentinel (below). Both must pad to width with
	// the warm bg, so the block has solid top and bottom edges
	// instead of ragged corners.
	body := "before\n" + Wrap("\nfoo\n") + "\nafter"
	got := Colorize(body, DarkTheme, Options{Width: 12})
	stripped := ansi.Strip(got)
	lines := strings.Split(stripped, "\n")

	// Layout (visible widths after Strip):
	//   "before"           — 6 chars, template (no pad)
	//   ""                 — shoulder above "foo", padded to 12 (warm)
	//   "foo"              — content, padded to 12 (warm)
	//   ""                 — shoulder below "foo", padded to 12 (warm)
	//   "after"            — 5 chars, template (no pad)
	wantWidths := []int{6, 12, 12, 12, 5}
	if len(lines) != len(wantWidths) {
		t.Fatalf("line count: got %d want %d\nstripped=%q", len(lines), len(wantWidths), stripped)
	}
	for i, w := range wantWidths {
		if got := len(lines[i]); got != w {
			t.Errorf("line %d width: got %d want %d: %q", i, got, w, lines[i])
		}
	}
}

func TestColorize_FillTemplate(t *testing.T) {
	t.Parallel()
	// FillTemplate=true must pad plain template lines too, so the
	// entire view is a solid cool band.
	in := "short\nalso short"
	got := Colorize(in, DarkTheme, Options{Width: 25, FillTemplate: true})
	stripped := ansi.Strip(got)
	for i, line := range strings.Split(stripped, "\n") {
		if len(line) != 25 {
			t.Errorf("template line %d not padded to 25: len=%d %q", i, len(line), line)
		}
	}
}

func TestColorize_PreservesTextContent(t *testing.T) {
	t.Parallel()
	// After Colorize, stripping all ANSI codes should give back the
	// original visible text (modulo block padding spaces). Verifies
	// no character data is lost or duplicated.
	in := "prefix " + Wrap("hello world") + " suffix"
	got := Colorize(in, DarkTheme, Options{Width: 80})
	stripped := ansi.Strip(got)
	stripped = strings.TrimRight(stripped, " ")
	if want := Strip(in); stripped != want {
		t.Errorf("visible text mismatch:\n got  = %q\n want = %q", stripped, want)
	}
}

func TestColorize_ReEmitsBgAfterEmbeddedResets(t *testing.T) {
	t.Parallel()
	// Glamour/lipgloss end every styled chunk with "\x1b[0m". Without
	// reset-aware re-emission, our LLM bg would die at the first
	// embedded reset and the rest of the span would render with the
	// terminal's default bg — exactly the "bg only on empty lines"
	// symptom we saw in the real TUI.
	in := "prefix " + Wrap("warm \x1b[1mbold-bit\x1b[0m still warm") + " suffix"
	got := Colorize(in, DarkTheme, Options{Width: 80})

	// After the embedded "\x1b[0m" the active bg must be the LLM bg
	// again (we're still inside the LLM span at that point).
	idx := strings.Index(got, "\x1b[0m")
	if idx < 0 {
		t.Fatalf("no embedded reset in output: %q", got)
	}
	after := got[idx+len("\x1b[0m"):]
	if !strings.HasPrefix(after, DarkTheme.LLMBG) {
		t.Errorf("LLM bg not re-emitted after embedded reset; got %q…", clip(after, 60))
	}
}

func TestColorize_ReEmitsBgAfterDefaultBgEscape(t *testing.T) {
	t.Parallel()
	// "\x1b[49m" sets the bg to terminal default — equally fatal to
	// our band as a full reset. Must re-emit the active bg.
	in := Wrap("warm \x1b[49mclear\x1b[39m warm")
	got := Colorize(in, DarkTheme, Options{Width: 40})

	idx := strings.Index(got, "\x1b[49m")
	if idx < 0 {
		t.Fatalf("no \\x1b[49m in output: %q", got)
	}
	after := got[idx+len("\x1b[49m"):]
	if !strings.HasPrefix(after, DarkTheme.LLMBG) {
		t.Errorf("LLM bg not re-emitted after [49m: %q…", clip(after, 60))
	}
}

func TestParseEscape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		n    int
	}{
		{"\x1b[0m abc", "\x1b[0m", 4},
		{"\x1b[48;2;1;2;3m...", "\x1b[48;2;1;2;3m", 13},
		{"\x1b[mxyz", "\x1b[m", 3},
		{"\x1bc", "\x1b", 1}, // non-CSI: consume ESC only
		{"x", "x", 1},        // not an escape; degenerate but defined
	}
	for _, c := range cases {
		seq, n := parseEscape(c.in)
		if seq != c.want || n != c.n {
			t.Errorf("parseEscape(%q) = (%q, %d), want (%q, %d)", c.in, seq, n, c.want, c.n)
		}
	}
}

func TestResetsBackground(t *testing.T) {
	t.Parallel()
	yes := []string{"\x1b[m", "\x1b[0m", "\x1b[0;0m", "\x1b[49m", "\x1b[1;49;7m"}
	for _, s := range yes {
		if !resetsBackground(s) {
			t.Errorf("resetsBackground(%q) = false, want true", s)
		}
	}
	no := []string{"\x1b[1m", "\x1b[38;5;205m", "\x1b[48;2;1;2;3m", "not-an-escape", "\x1b[H"}
	for _, s := range no {
		if resetsBackground(s) {
			t.Errorf("resetsBackground(%q) = true, want false", s)
		}
	}
}

// clip returns up to n bytes of s as a printable shorthand for error
// messages — strings.Builder output is full of escapes and hard to
// eyeball in a long single line.
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func TestWrapTree(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"title": "Hello",
		"body":  "multi\nline\ntext",
		"meta": map[string]any{
			"author":     "claude",
			"confidence": 0.83,
			"tags":       []any{"alpha", "beta"},
		},
		"empty": "",
		"count": 42,
		"ok":    true,
		"nada":  nil,
	}
	out := WrapTree(in).(map[string]any)

	// String leaves at any depth are wrapped.
	if got := out["title"].(string); got != Wrap("Hello") {
		t.Errorf("title not wrapped: %q", got)
	}
	if got := out["body"].(string); got != Wrap("multi\nline\ntext") {
		t.Errorf("body not wrapped: %q", got)
	}
	if got := out["meta"].(map[string]any)["author"].(string); got != Wrap("claude") {
		t.Errorf("meta.author not wrapped: %q", got)
	}
	tags := out["meta"].(map[string]any)["tags"].([]any)
	if got := tags[0].(string); got != Wrap("alpha") {
		t.Errorf("tags[0] not wrapped: %q", got)
	}
	if got := tags[1].(string); got != Wrap("beta") {
		t.Errorf("tags[1] not wrapped: %q", got)
	}

	// Empty strings stay empty (Wrap is a no-op on "").
	if got := out["empty"].(string); got != "" {
		t.Errorf("empty string should stay empty: %q", got)
	}

	// Non-string scalars are returned as-is.
	if got := out["count"]; got != 42 {
		t.Errorf("count mutated: %v", got)
	}
	if got := out["ok"]; got != true {
		t.Errorf("ok mutated: %v", got)
	}
	if got := out["nada"]; got != nil {
		t.Errorf("nada mutated: %v", got)
	}
	if got := out["meta"].(map[string]any)["confidence"]; got != 0.83 {
		t.Errorf("confidence mutated: %v", got)
	}

	// Original map is NOT mutated.
	if got := in["title"].(string); got != "Hello" {
		t.Errorf("WrapTree mutated input map: %q", got)
	}
}

func TestLongestBlockLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"no llm", "no llm here", 0},
		{"single line inline", "pre " + Wrap("xyz") + " post", 3},
		{"multi line", Wrap("short\nmuch longer\nmid"), 11},
		{"trailing newline", Wrap("aaa\nbb\n"), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := longestBlockLine(c.in); got != c.want {
				t.Errorf("longestBlockLine(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestRuneVisibleWidth_ZeroWidthClasses(t *testing.T) {
	t.Parallel()
	zero := []rune{
		'\x00', '\x1f',
		0x200B, 0x200C, 0x200D,
		0x2060, 0x2061, 0x2062, 0x2063, 0x2064,
		0xFE00, 0xFE0F,
		0xFEFF,
	}
	for _, r := range zero {
		if w := runeVisibleWidth(r); w != 0 {
			t.Errorf("runeVisibleWidth(%U) = %d, want 0", r, w)
		}
	}
	if w := runeVisibleWidth('A'); w != 1 {
		t.Errorf("runeVisibleWidth('A') = %d, want 1", w)
	}
}
