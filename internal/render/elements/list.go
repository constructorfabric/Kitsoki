package elements

import (
	"fmt"
	"strings"

	"github.com/muesli/reflow/wordwrap"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// List is a bulleted list with an optional aligned hint column.
//
// When any item carries a non-empty Hint, the renderer lays the list out
// as a two-column block: marker + label padded to the longest label
// width, two spaces of gutter, hint column reflowing into the remaining
// width. When no item has a hint, the renderer emits a bare list — one
// marker-prefixed line per item, with the label reflowing into the
// available width.
//
// Items whose `when:` guard evaluates to false are filtered out BEFORE
// the column-width measurement, so a guard'd-away long row doesn't
// reserve a wide column for the survivors.
type List struct {
	Items  []app.ListItem
	Marker string
}

// defaultMarker is the bullet marker applied when Marker is empty. The
// proposal §2.2 spec ("Optional. Default: \"-\"") puts this at the
// element-render layer, not at YAML load — authors who write `marker: ""`
// explicitly still get the default. (If we ever want a bare bulletless
// list, we can introduce `bare: true` or accept a sentinel.)
const defaultMarker = "-"

// gutterWidth is the column gap between the label column and the hint
// column in a two-column list. Two spaces matches the cloak / dev-story
// hand-tuned spacing that the proposal §1 motivates replacing.
const gutterWidth = 2

// listRow is one already-substituted list entry, ready for layout. Held
// at file scope so the bare / two-column helpers can share the type.
type listRow struct {
	label string
	hint  string
}

// Render lays out the list at the supplied width.
func (l List) Render(width int, env expr.Env, rr ViewRenderer) (string, error) {
	if len(l.Items) == 0 {
		return "", nil
	}
	marker := l.Marker
	if marker == "" {
		marker = defaultMarker
	}
	// Marker prefix consumed by every row: "<marker><space>".
	markerPrefix := marker + " "

	// Step 1 — filter out guard'd-away items and pongo-expand the
	// survivors' Label / Hint. The filter must precede column
	// measurement: if a long-labeled row is suppressed, its width must
	// not size the column for the rows that do render.
	rows := make([]listRow, 0, len(l.Items))
	anyHint := false
	for i, it := range l.Items {
		keep, err := evalWhen(it.When, env)
		if err != nil {
			return "", fmt.Errorf("list.items[%d] when: %w", i, err)
		}
		if !keep {
			continue
		}
		label, err := renderLeaf(rr, it.Label, env)
		if err != nil {
			return "", fmt.Errorf("list.items[%d] label: %w", i, err)
		}
		hint, err := renderLeaf(rr, it.Hint, env)
		if err != nil {
			return "", fmt.Errorf("list.items[%d] hint: %w", i, err)
		}
		label = strings.TrimRight(label, " \t")
		hint = strings.TrimSpace(hint)
		if hint != "" {
			anyHint = true
		}
		rows = append(rows, listRow{label: label, hint: hint})
	}
	if len(rows) == 0 {
		return "", nil
	}

	// Step 2 — layout.
	if !anyHint {
		return renderBareList(rows, markerPrefix, width), nil
	}
	return renderTwoColumnList(rows, markerPrefix, width), nil
}

// renderBareList emits one marker-prefixed line per row, reflowing
// labels that overflow the available width onto continuation lines
// indented by markerPrefix width so the bullet visually owns its block.
func renderBareList(rows []listRow, markerPrefix string, width int) string {
	indent := strings.Repeat(" ", visibleLen(markerPrefix))
	avail := width - len(indent)
	if avail < 1 {
		avail = 1
	}
	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		wrapped := wordwrap.String(r.label, avail)
		lines := strings.Split(wrapped, "\n")
		for j, line := range lines {
			if j == 0 {
				sb.WriteString(markerPrefix)
			} else {
				sb.WriteByte('\n')
				sb.WriteString(indent)
			}
			sb.WriteString(line)
		}
	}
	return sb.String()
}

// renderTwoColumnList lays out the rows with the label column sized to
// the longest label (so the colon column is aligned), gutterWidth spaces
// of gap, and the hint reflowed into whatever width remains. Rows with
// no hint render the label alone, no trailing gutter.
func renderTwoColumnList(rows []listRow, markerPrefix string, width int) string {
	indent := strings.Repeat(" ", visibleLen(markerPrefix))
	maxLabel := 0
	for _, r := range rows {
		if n := visibleLen(r.label); n > maxLabel {
			maxLabel = n
		}
	}
	// Width budget for the hint column = total - marker - label - gutter.
	hintWidth := width - visibleLen(markerPrefix) - maxLabel - gutterWidth
	if hintWidth < 8 {
		// Floor — give the hint column at least 8 chars even on narrow
		// terminals. The label column will get truncated by the terminal
		// rather than the renderer, which is preferable to a hint column
		// that wraps every word.
		hintWidth = 8
	}

	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		labelPadded := r.label + strings.Repeat(" ", maxLabel-visibleLen(r.label))
		if r.hint == "" {
			sb.WriteString(markerPrefix)
			sb.WriteString(strings.TrimRight(labelPadded, " "))
			continue
		}
		hintWrapped := wordwrap.String(r.hint, hintWidth)
		hintLines := strings.Split(hintWrapped, "\n")
		for j, hl := range hintLines {
			if j == 0 {
				sb.WriteString(markerPrefix)
				sb.WriteString(labelPadded)
				sb.WriteString(strings.Repeat(" ", gutterWidth))
				sb.WriteString(hl)
			} else {
				sb.WriteByte('\n')
				// Continuation lines align under the hint column.
				sb.WriteString(indent)
				sb.WriteString(strings.Repeat(" ", maxLabel+gutterWidth))
				sb.WriteString(hl)
			}
		}
	}
	return sb.String()
}

// visibleLen returns the display width of s. Today's authored strings
// are plain ASCII / single-rune Unicode (no ANSI escapes — those are
// added by the styling pass, not by authors), so a rune count is
// correct. If we ever stuff styled runs into Source fields we'll need
// reflow/ansi.PrintableRuneWidth instead.
func visibleLen(s string) int {
	return len([]rune(s))
}
