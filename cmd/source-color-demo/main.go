// Source-color demo: renders hand-crafted samples that mix templated
// and LLM-generated text, switching terminal background color at the
// boundary so the two are visually distinguishable.
//
// The colorize pass and theme palette live in the
// internal/render/sourcecolor package — this binary is a thin wrapper
// around that package so the demo cannot drift from production
// behaviour. The visual scheme is documented in docs/story-style.md §8.
//
// Run:
//
//	go run ./cmd/source-color-demo
//	go run ./cmd/source-color-demo -theme=high-contrast
//	go run ./cmd/source-color-demo -theme=light
//	go run ./cmd/source-color-demo -all
//	go run ./cmd/source-color-demo -fill-template
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"kitsoki/internal/render/sourcecolor"
)

// L wraps a string as LLM-sourced. The author of a demo sample reaches
// for L() exactly where the LLM operator would wrap its output in
// production — sourcecolor.Wrap is the single shared sentinel.
func L(s string) string { return sourcecolor.Wrap(s) }

var themes = map[string]sourcecolor.Theme{
	"dark":          sourcecolor.DarkTheme,
	"high-contrast": sourcecolor.HighContrastTheme,
	"light":         sourcecolor.LightTheme,
}

func themeNames() []string {
	out := make([]string, 0, len(themes))
	for k := range themes {
		out = append(out, k)
	}
	return out
}

type sample struct {
	title string
	body  string
}

func samples() []sample {
	return []sample{
		{
			"1. Pure template (cool only)",
			"Ticket #1234 — opened 3 hours ago by brad@acronis.com.\n" +
				"Severity: high.  Last activity: 12 minutes ago.",
		},
		{
			"2. Inline LLM inside template",
			"Ticket title is " + L("Database migration fails on backfill step") + ", reported in #ops.\n" +
				"Owner: " + L("ingest-team") + " — auto-assigned by the triage model.",
		},
		{
			"3. Block LLM inside template (multi-line warm band)",
			"Triage summary:\n" + L("\n"+
				"The migration creates a NOT NULL column before populating it.\n"+
				"Under concurrent writes the locking pattern blocks the backfill\n"+
				"and the deploy times out. Recommend reversing the order: add a\n"+
				"nullable column, backfill, then promote to NOT NULL once full.\n",
			) + "\n— end of summary —",
		},
		{
			"4. Nested LLM (LLM quoting earlier LLM output)",
			"The agent said " + L("I checked the prior plan, which stated: "+
				L("\"backfill before constraint\"")+" — confirmed.") +
				" So we proceed.",
		},
		{
			"5. Mixed: template scaffold with an LLM block inside a section",
			"## Incident notes\n" +
				"Date: 2026-05-20\n" +
				"Reporter: brad@acronis.com\n" +
				"\n" +
				"AI synthesis:\n" + L("\n"+
				"Root cause is the ordering of the schema change versus the\n"+
				"backfill job. The fix is two commits, not one: split the\n"+
				"migration and re-run the backfill before promoting the\n"+
				"constraint.\n",
			) + "\n" +
				"Next step: assign to the on-call.",
		},
	}
}

func render(w io.Writer, t sourcecolor.Theme, width int, fillTpl bool) {
	fmt.Fprintf(w, "\n=== theme=%s  width=%d  fill-template=%v ===\n",
		t.Name, width, fillTpl)
	for _, s := range samples() {
		fmt.Fprintf(w, "\n%s\n\n", s.title)
		painted := sourcecolor.Colorize(s.body, t, sourcecolor.Options{
			Width:        width,
			FillTemplate: fillTpl,
		})
		io.WriteString(w, painted)
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w)
}

func main() {
	themeName := flag.String("theme", "dark",
		"theme: "+strings.Join(themeNames(), "|"))
	width := flag.Int("width", 72, "width for block padding (cols)")
	fillTpl := flag.Bool("fill-template", false,
		"also pad template lines (solid cool band)")
	all := flag.Bool("all", false, "render every theme back-to-back")
	flag.Parse()

	if *all {
		for _, name := range []string{"dark", "high-contrast", "light"} {
			render(os.Stdout, themes[name], *width, *fillTpl)
		}
		return
	}

	t, ok := themes[*themeName]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown theme: %s (have: %s)\n",
			*themeName, strings.Join(themeNames(), ", "))
		os.Exit(2)
	}
	render(os.Stdout, t, *width, *fillTpl)
}
