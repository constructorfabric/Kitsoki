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
//
// The zero value (nil Items, empty Marker) is usable and renders to "";
// an empty Marker falls back to defaultMarker.
type List struct {
	Items  []app.ListItem
	Marker string
}

// defaultMarker is the bullet marker applied when Marker is empty.
//
// We use the Unicode bullet "•" rather than an ASCII "-" because the
// typed-element output is composed
// into a glamour-styled markdown document downstream (the TUI's
// transcript pane runs the entire view through glamour after the
// dispatcher pre-renders blocks — see element.go::RenderAll). When
// the marker is "-", glamour parses our output as a markdown list and
// re-flows the body at its own wrap width, collapsing the hint-column
// alignment we just baked in (continuation lines snap back to the
// marker indent, the hint column gets re-wrapped at glamour's
// possibly-narrower wrap budget, etc.).
//
// "•" is not a markdown list marker, so glamour leaves our line alone
// — column padding survives, inline code (`backtick`) chip styling
// still applies, hint continuations stay at the hint-column indent.
// Authors who want a different glyph (or the legacy "-") can still
// override via `list.marker:` in the YAML.
const defaultMarker = "•"

// gutterWidth is the column gap between the label column and the hint
// column in a two-column list. Two spaces matches the cloak / dev-story
// hand-tuned spacing this element exists to replace.
const gutterWidth = 2

// minHintWidth is the floor the two-column layout keeps for the hint
// column when there's at least one row with a hint. If padding every
// label to a single outlier's width would leave less than this for
// the hint, max_label is capped to (width - marker - gutter -
// minHintWidth) and rows whose label exceeds the cap render WITHOUT
// padding — their hint follows immediately after the label + gutter,
// and they don't align with the rest of the column. This keeps a
// single 49-char "stubs · group · of · labels" entry from squeezing
// every other row's hint column down to ~20 chars when the viewport
// is anywhere shy of huge.
const minHintWidth = 40

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
//
// max_label cap: when one outlier row's label would push the hint
// column below minHintWidth, the column is capped instead — so a
// single 49-char mega-label doesn't shrink every other row's hint
// down to 23 chars. Rows whose label exceeds the cap render in
// "overflow" mode: their label runs into the gutter (no padding),
// and the hint follows immediately after a single gutter gap. They
// don't align with the rest of the column — better than forcing the
// whole column narrow for every other row.
func renderTwoColumnList(rows []listRow, markerPrefix string, width int) string {
	indent := strings.Repeat(" ", visibleLen(markerPrefix))
	measuredMax := 0
	for _, r := range rows {
		if n := visibleLen(r.label); n > measuredMax {
			measuredMax = n
		}
	}
	// Effective label cap: never narrower than would leave less than
	// minHintWidth for the hint column. When measuredMax exceeds this
	// cap, the column aligns to the cap and outlier rows overflow.
	maxBudget := width - visibleLen(markerPrefix) - gutterWidth - minHintWidth
	maxLabel := measuredMax
	if maxBudget >= 1 && maxLabel > maxBudget {
		maxLabel = maxBudget
	}
	if maxLabel < 1 {
		maxLabel = 1
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
		labelLen := visibleLen(r.label)
		// Overflow rows: label is wider than the capped column. Don't
		// pad; emit the label as-is, then a single-space gutter, then
		// the hint (wrapped at the same hintWidth budget as everyone
		// else so the overflow row doesn't run off the right edge).
		overflow := labelLen > maxLabel
		var labelPadded string
		if overflow {
			labelPadded = r.label
		} else {
			labelPadded = r.label + strings.Repeat(" ", maxLabel-labelLen)
		}
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
