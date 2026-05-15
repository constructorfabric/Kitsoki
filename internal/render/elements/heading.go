package elements

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/expr"
)

// headingStyle is the visual treatment for `heading:` elements. Bold +
// accent (emerald) — section breaks without shouting. Matches the
// menuItemStyle tone in internal/tui/styles.go so headings read as part
// of the same visual language as the menu's action labels.
//
// Defined here (not in styles.go) because the elements package must
// stand alone — see Phase D bullet "Files you may NOT touch" in the
// brief. Once the colour palette is centralised under internal/tui we
// can re-import from there.
var headingStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#10B981"))

// Heading is a styled section break. Not bulleted, not prefixed. The
// dispatcher pairs it with a trailing blank line via the standard inter-
// element spacing.
type Heading struct {
	Source string
}

// Render expands pongo2 templates in the heading text and styles the
// result. An empty heading yields the empty string so the dispatcher
// drops it without emitting an extra blank line.
func (h Heading) Render(_ int, env expr.Env, rr ViewRenderer) (string, error) {
	body, err := renderLeaf(rr, h.Source, env)
	if err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}
	return headingStyle.Render(body), nil
}
