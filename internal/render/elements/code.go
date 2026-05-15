package elements

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"kitsoki/internal/expr"
)

// Code is a monospace block that preserves layout exactly. Used for
// indented example commands, terminal output, anything where the
// author's whitespace IS the rendering intent.
//
// Code never reflows. If a line is longer than the viewport, the
// terminal handles the overflow (wrap, scroll, or truncate per its own
// settings) — better than the renderer chopping a hand-tuned indent.
//
// Bordered is an opt-in (default off). Per the proposal §8 open
// question, bare-monospace indent is the default Terminal-Room style;
// authors who want Glamour-style fenced framing can opt in.
type Code struct {
	Source   string
	Bordered bool
}

// codeBorderStyle is the optional thin border for `code` elements when
// Bordered is true. Author-visible setting; today no app opts in.
var codeBorderStyle = lipgloss.NewStyle().
	Border(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("#6B7280")).
	Padding(0, 1)

// Render expands pongo2 templates in the source and emits the result
// verbatim (no reflow, no trimming of leading whitespace).
func (c Code) Render(_ int, env expr.Env, rr ViewRenderer) (string, error) {
	body, err := renderLeaf(rr, c.Source, env)
	if err != nil {
		return "", err
	}
	// Trim only trailing newlines — leading whitespace is meaningful in
	// code (Terminal-Room example blocks are typically indented by two
	// spaces). A trailing \n from a YAML block scalar would otherwise
	// stack with the dispatcher's inter-element blank line.
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return "", nil
	}
	if c.Bordered {
		return codeBorderStyle.Render(body), nil
	}
	return body, nil
}
