package sourcecolor

import (
	"strings"
	"testing"
)

func TestVisibleLen_IgnoresSentinels(t *testing.T) {
	t.Parallel()
	// Sentinels are zero-width: a wrapped 5-char value is 5 visible runes.
	if got := VisibleLen(Wrap("hello")); got != 5 {
		t.Fatalf("VisibleLen(Wrap(hello)) = %d, want 5", got)
	}
	if got := VisibleLen("plain"); got != 5 {
		t.Fatalf("VisibleLen(plain) = %d, want 5", got)
	}
	// Multibyte runes count as one each.
	if got := VisibleLen("café " + Wrap("●")); got != 6 {
		t.Fatalf("VisibleLen = %d, want 6", got)
	}
}

func TestTruncate_PreservesSentinelBalance(t *testing.T) {
	t.Parallel()
	// The real bug: a value wrapped in LLM sentinels, truncated short, must not
	// drop its closing sentinel — otherwise Colorize bleeds the band onward.
	idea := Wrap("A Vue 3 web notes app for power users who need to capture, organize, and search everything with AI")
	got := Truncate(idea, 80)
	if o, c := strings.Count(got, llmOpen), strings.Count(got, llmClose); o != 1 || c != 1 {
		t.Fatalf("Truncate dropped sentinel balance: open=%d close=%d (want 1/1)\n%q", o, c, got)
	}
	if VisibleLen(got) != 80 {
		t.Fatalf("VisibleLen(Truncate(_,80)) = %d, want 80", VisibleLen(got))
	}
	if !strings.HasSuffix(Strip(got), "...") {
		t.Fatalf("expected ellipsis suffix, got %q", Strip(got))
	}
	// The whole span is still flagged LLM (open before text, close at the end).
	if !strings.HasPrefix(got, llmOpen) || !strings.HasSuffix(got, llmClose) {
		t.Fatalf("span not wrapped end-to-end: %q", got)
	}
}

func TestTruncate_NoTruncationWhenWithinBudget(t *testing.T) {
	t.Parallel()
	in := Wrap("short")
	if got := Truncate(in, 80); got != in {
		t.Fatalf("Truncate within budget changed input: %q -> %q", in, got)
	}
}

func TestTruncate_SentinelFreeMatchesPongoSemantics(t *testing.T) {
	t.Parallel()
	// For sentinel-free input the result must match pongo2's truncatechars:
	// n>=3 → runes[:n-3]+"...", n<3 → hard cut, n>=len → unchanged.
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abcdefghij", 5, "ab..."},
		{"abcdefghij", 3, "..."},
		{"abcdefghij", 2, "ab"},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghij", 20, "abcdefghij"},
		{"héllo wörld", 6, "hél..."},
	}
	for _, c := range cases {
		if got := Truncate(c.in, c.n); got != c.want {
			t.Errorf("Truncate(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestTruncate_BalancedSpanStaysBalanced(t *testing.T) {
	t.Parallel()
	// Template prefix + a closed LLM span + template suffix, truncated mid-span:
	// the kept portion's open span must be re-closed at the cut.
	s := "Idea: " + Wrap("a long generated idea that runs well past the cut") + " (end)"
	got := Truncate(s, 20)
	if o, c := strings.Count(got, llmOpen), strings.Count(got, llmClose); o != c {
		t.Fatalf("unbalanced after truncation: open=%d close=%d\n%q", o, c, got)
	}
}
