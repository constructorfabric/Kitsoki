package tui

import (
	"strings"
	"testing"

	"kitsoki/internal/render/sourcecolor"
)

// TestSourceColorThemeFor pins the terminal-background → palette mapping
// behind the transcript's source-color bands. A light terminal must get
// the pale LightTheme; dark and notty keep DarkTheme.
//
// Regression guard: FlushPending previously hard-coded DarkTheme, so an
// LLM-sourced run painted a dark bronze band even on a light terminal
// (dark-mode background with no dark background). This test fails if the
// selection reverts to a single hard-coded theme.
func TestSourceColorThemeFor(t *testing.T) {
	cases := []struct {
		style string
		want  sourcecolor.Theme
	}{
		{"light", sourcecolor.LightTheme},
		{"dark", sourcecolor.DarkTheme},
		{"notty", sourcecolor.DarkTheme},
		{"", sourcecolor.DarkTheme},
	}
	for _, c := range cases {
		if got := sourceColorThemeFor(c.style); got.Name != c.want.Name {
			t.Errorf("sourceColorThemeFor(%q) = %q, want %q", c.style, got.Name, c.want.Name)
		}
	}
}

// TestLightTerminalGetsPaleLLMBand verifies the end effect: an LLM-wrapped
// run colorized under the light palette carries the pale (#fff4e0) band,
// NOT the dark bronze (#5c3e28) one. This is the user-visible property —
// LLM output must not get a dark background on a light terminal.
func TestLightTerminalGetsPaleLLMBand(t *testing.T) {
	wrapped := sourcecolor.Wrap("analyst reply")

	light := sourcecolor.Colorize(wrapped, sourceColorThemeFor("light"), sourcecolor.Options{})
	if !strings.Contains(light, "48;2;255;244;224") { // #fff4e0 pale
		t.Errorf("light terminal: expected pale LLM band (#fff4e0); got %q", light)
	}
	if strings.Contains(light, "48;2;92;62;40") { // #5c3e28 dark bronze
		t.Errorf("light terminal: must NOT carry the dark bronze band; got %q", light)
	}

	dark := sourcecolor.Colorize(wrapped, sourceColorThemeFor("dark"), sourcecolor.Options{})
	if !strings.Contains(dark, "48;2;92;62;40") {
		t.Errorf("dark terminal: expected bronze LLM band (#5c3e28); got %q", dark)
	}
}
