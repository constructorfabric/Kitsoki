package elements

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	figure "github.com/common-nighthawk/go-figure"

	"kitsoki/internal/expr"
)

// Banner is a phase-marker block. Renders Source as figlet-style ASCII
// art in Color (defaulting to a neutral accent) so an operator skimming
// the transcript can find phase boundaries at a glance, followed by an
// optional Subtitle line in muted text.
//
// Why a typed element instead of a `code:` with pre-baked art? Pre-baked
// banners in YAML are fragile (one-off line wrapping, mis-counted
// columns, font drift when a phase is renamed). Generating the art at
// render time from a plain phase name keeps the authoring surface
// declarative — change `text: "REPRODUCING"` to `text: "REPRO"` and the
// banner re-renders.
type Banner struct {
	// Source is the phase text the renderer figlets. Required.
	Source string
	// Subtitle is the optional one-line caption shown beneath the art
	// (e.g. "Phase 1 / 7  ·  reproduce the bug").
	Subtitle string
	// Color is an optional CSS-style hex foreground for the art (the
	// subtitle stays unstyled so it doesn't compete visually). Defaults
	// to bannerDefaultColor when empty.
	Color string
}

// bannerFont is the go-figure font name used for the ASCII art. "small"
// is a 4-line block style: compact enough to render "IMPLEMENTING" in
// ~86 cols while staying recognisable as figlet output, distinct enough
// from `prose:` and `heading:` that it can't be mistaken for ordinary
// body text. If we ever want a denser style, "mini" is 3 lines tall.
const bannerFont = "small"

// bannerDefaultColor is the foreground used when the author leaves
// `color:` unset. Neutral emerald — matches the heading element so a
// banner-less older room still reads as the same visual family.
const bannerDefaultColor = "#10B981"

// bannerSubtitleColor is the muted foreground for the caption line.
// Lighter grey than the chrome so the eye lands on the art first.
const bannerSubtitleColor = "#9CA3AF"

// Render generates the ASCII art from Source, applies Color, and tacks
// on Subtitle (if any) on the next-but-one line. The output is layout-
// preserving — no reflow, no trimming of leading whitespace — so the
// figlet output renders character-for-character verbatim.
func (b Banner) Render(_ int, env expr.Env, rr ViewRenderer) (string, error) {
	source, err := renderLeaf(rr, b.Source, env)
	if err != nil {
		return "", err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}
	subtitle, err := renderLeaf(rr, b.Subtitle, env)
	if err != nil {
		return "", err
	}
	subtitle = strings.TrimSpace(subtitle)

	art := figure.NewFigure(source, bannerFont, true).String()
	art = strings.TrimRight(art, "\n")

	colour := b.Color
	if colour == "" {
		colour = bannerDefaultColor
	}
	artStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(colour)).Render(art)

	if subtitle == "" {
		return artStyled, nil
	}
	subtitleStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(bannerSubtitleColor)).
		Render(subtitle)
	// Blank line between art and subtitle so the caption reads as its
	// own visual beat — the dispatcher's inter-element spacing handles
	// the separation FROM the next element, but the intra-banner gap
	// belongs to this element.
	return artStyled + "\n\n" + subtitleStyled, nil
}
