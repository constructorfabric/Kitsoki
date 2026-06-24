package orchestrator

// recorded_view_test.go pins recordedView: the trace must record the room
// narration with presentation ANSI stripped (so the bytes are deterministic
// across color profiles) while keeping the zero-width source-color sentinels
// (so consumers can still tell LLM-generated text from template text).

import (
	"strings"
	"testing"

	"kitsoki/internal/render/sourcecolor"
)

func TestRecordedView_StripsANSIKeepsSentinels(t *testing.T) {
	t.Parallel()
	// A view as lipgloss would render it to a colour terminal: a styled banner
	// (truecolor SGR) plus an LLM-wrapped value spliced in from a world var.
	styled := "\x1b[38;2;159;138;240mCLARIFYING\x1b[0m\nIdea: " +
		sourcecolor.Wrap("a Vue notes app") + "\nQuestions for you"

	got := recordedView(styled)

	if strings.Contains(got, "\x1b") {
		t.Fatalf("recordedView left ANSI escapes in the trace: %q", got)
	}
	if o, c := strings.Count(got, sourcecolor.LLMOpen), strings.Count(got, sourcecolor.LLMClose); o != 1 || c != 1 {
		t.Fatalf("recordedView dropped source-color sentinels: open=%d close=%d", o, c)
	}
	if want := "CLARIFYING\nIdea: a Vue notes app\nQuestions for you"; sourcecolor.Strip(got) != want {
		t.Fatalf("recordedView text = %q, want %q", sourcecolor.Strip(got), want)
	}
}

func TestRecordedView_DeterministicAcrossColorProfiles(t *testing.T) {
	t.Parallel()
	// The same narration rendered with colour (TTY) vs without (headless) must
	// record byte-identical views — that is the whole point of stripping.
	plain := "Idea: " + sourcecolor.Wrap("a Vue notes app")
	colored := "\x1b[38;2;16;185;129mIdea:\x1b[0m " + sourcecolor.Wrap("a Vue notes app")
	if recordedView(plain) != recordedView(colored) {
		t.Fatalf("recordedView not deterministic across color profiles:\n plain=%q\ncolor=%q",
			recordedView(plain), recordedView(colored))
	}
}
