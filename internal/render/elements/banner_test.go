package elements

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// stripANSI removes ANSI SGR escapes so tests can assert on visible
// content without knowing the active colour profile. Mirrors the
// helper in transcript.go (kept inline here so the elements package
// stays free of TUI imports).
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
				j++
			}
			if j < len(s) {
				i = j // skip past the final byte of the SGR sequence
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func init() {
	// Pin lipgloss to TrueColor so the styled-output tests get a stable
	// ANSI envelope across run environments (CI sometimes detects no TTY
	// and Render returns plain bytes; this keeps the colour tests
	// reproducible). Mirrors the helper in internal/tui/view_chrome_test.go.
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// TestBanner_RendersFigletArtFromText covers the happy path: a non-
// empty text produces multi-line figlet output, layout preserved.
func TestBanner_RendersFigletArtFromText(t *testing.T) {
	out, err := Banner{Source: "REPRODUCING"}.Render(120, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plain := stripANSI(out)
	lines := strings.Split(plain, "\n")
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines of figlet art; got %d:\n%s", len(lines), plain)
	}
	// Every line is non-empty (the "small" figlet font has no empty
	// rows for a single-word source). A blank line would mean the
	// art mid-renders — usually a sign the font went missing.
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			t.Errorf("line %d unexpectedly blank in figlet art:\n%s", i, plain)
		}
	}
}

// TestBanner_AppliesColor asserts the Color field flows through to
// lipgloss: a known hex value lands in the output as a TrueColor SGR
// sequence (e.g. "38;2;6;182;212" for #06B6D4 cyan).
func TestBanner_AppliesColor(t *testing.T) {
	out, err := Banner{Source: "REPRODUCING", Color: "#06B6D4"}.Render(120, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// 0x06=6, 0xB6=182, 0xD4≈212 in decimal. lipgloss/termenv may
	// round the B channel by ±1 when round-tripping the colour
	// through their internal palette, so we anchor on R and G
	// (which never quantize-shift for TrueColor profiles).
	if !strings.Contains(out, "38;2;6;182;") {
		t.Errorf("expected #06B6D4 TrueColor escape in output; got: %q", out)
	}
}

// TestBanner_DefaultColorWhenColorEmpty asserts the bannerDefaultColor
// fallback fires when Color is unset.
func TestBanner_DefaultColorWhenColorEmpty(t *testing.T) {
	out, err := Banner{Source: "DONE"}.Render(120, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	// bannerDefaultColor = #10B981 → 16, 185, 129. Anchor on R+G
	// (see TestBanner_AppliesColor for the rationale).
	if !strings.Contains(out, "38;2;16;185;") {
		t.Errorf("expected default colour escape; got: %q", out)
	}
}

// TestBanner_AppendsSubtitle asserts the subtitle lands beneath the
// art on a separate visual beat (one blank line between).
func TestBanner_AppendsSubtitle(t *testing.T) {
	out, err := Banner{
		Source:   "DONE",
		Subtitle: "Phase 7 / 7  ·  close-out artifact",
	}.Render(120, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "Phase 7 / 7  ·  close-out artifact") {
		t.Errorf("subtitle missing from output:\n%s", plain)
	}
	// Subtitle must be on a separate line from the art's last line.
	lines := strings.Split(plain, "\n")
	subtitleIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "Phase 7") {
			subtitleIdx = i
			break
		}
	}
	if subtitleIdx < 1 {
		t.Fatalf("subtitle not found in any line: %v", lines)
	}
	// One blank line of separation between art and caption.
	if strings.TrimSpace(lines[subtitleIdx-1]) != "" {
		t.Errorf("expected blank line before subtitle; got %q", lines[subtitleIdx-1])
	}
}

// TestBanner_EmptyTextRendersEmpty asserts the dispatcher contract:
// an empty source skips the element entirely (so a guard'd-off banner
// doesn't leak whitespace).
func TestBanner_EmptyTextRendersEmpty(t *testing.T) {
	out, err := Banner{Source: ""}.Render(120, expr.Env{}, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "" {
		t.Errorf("empty text should render empty, got %q", out)
	}
}

// TestBanner_DispatchedFromTypedView is the integration test: a View
// with a banner element flows through RenderAll and lands as figlet
// art in the composed output.
func TestBanner_DispatchedFromTypedView(t *testing.T) {
	view := app.View{
		Elements: []app.ViewElement{
			{
				Kind:     "banner",
				Source:   "TESTING",
				Subtitle: "Phase 4 / 7",
				Color:    "#F59E0B",
			},
			{Kind: "prose", Source: "body text"},
		},
	}
	out, err := RenderAll(view, expr.Env{}, 120, IdentityGlamour, nil)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "Phase 4 / 7") {
		t.Errorf("subtitle missing from dispatcher output:\n%s", plain)
	}
	if !strings.Contains(plain, "body text") {
		t.Errorf("subsequent prose element missing from output:\n%s", plain)
	}
	// The amber colour escape must be present somewhere. Anchor on
	// R+G (see TestBanner_AppliesColor for the rounding rationale).
	if !strings.Contains(out, "38;2;245;158;") {
		t.Errorf("expected #F59E0B amber escape in dispatched output")
	}
}
