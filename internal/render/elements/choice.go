package elements

import (
	"fmt"
	"regexp"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
)

// choicePlaceholderRegex matches `{name}` placeholders in a form-mode
// template body. Identifier-shaped names only — literal curly-brace
// content (e.g. `{$amount}`) is left alone so authors can still embed
// dollar-sign math text in prose. Mirrors internal/app/choice.go's
// choicePlaceholderRE so we stay consistent with the load-time
// validator (which already established the `{name}` ↔ fields entries
// cross-ref).
var choicePlaceholderRegex = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Choice is the static (non-interactive) renderer for `choice:` view
// elements. It produces the body that `kitsoki render` and the Jira /
// Bitbucket transports consume, and the same body the TUI's interactive
// widget overlays affordances on top of.
//
// The interactive widget overlays cursor / `[x]` / underline
// affordances on top of this layout; the rendering rules below are
// chosen so that overlay never has to re-measure column widths. See
// docs/stories/choice-widget.md for the author-facing cookbook.
//
// All three modes share the same skeleton:
//
//	<prompt>:        (optional, omitted with blank line when absent)
//
//	<body>           per-mode, mostly mirrors list.go's two-column block
//
//	[<footer>]       keybinding hint for the interactive widget
//
// Per-item / per-field `When` guards are evaluated against the supplied
// env BEFORE column measurement (mirrors list.go) so guarded-away rows
// don't reserve space for the survivors. Cross-references (intent /
// slot validity, form `{name}` ↔ fields) are NOT re-checked here —
// the loader owns that at load time.
//
// The zero value renders a bare footer (it defaults to "single" mode
// with no items); a usable Choice is built from a typed app.ViewElement
// by the dispatcher.
type Choice struct {
	Mode     string
	Prompt   string
	Items    []app.ChoiceItem
	Intent   string
	Slot     string
	Min      int
	MinSet   bool
	Max      int
	MaxSet   bool
	Template string
	Fields   []app.ChoiceField
}

// choiceItemRow is one already-substituted item, ready for layout.
// label is the post-pongo label, possibly with a `[placeholder]` /
// `(slot?)` suffix when the source item carries a Param.
type choiceItemRow struct {
	label string
	hint  string
}

// choiceCursorGutter is the column reserved for the interactive
// widget's `▸` cursor (Phase C). The static renderer emits two spaces
// here so the column geometry matches between the static and
// interactive forms — Phase C overlays `▸ ` in this gutter without
// shifting any other column.
const choiceCursorGutter = "  "

// choiceCheckboxGutter is the prefix every multi-mode item carries.
// Static rendering emits an empty `[ ]` (no selection at render
// time); Phase C overlays `[x]` for selected items by overwriting the
// inner character.
const choiceCheckboxGutter = "[ ] "

// choiceFooterSingle / Multi / Form are the keybinding-hint footers
// appended below the body. Phase C consumes the same strings (so the
// TUI's status bar wording stays in sync with what authors see in
// transport output).
const (
	choiceFooterSingle = "[↑/↓ move • Enter pick • Tab chat • Esc cancel]"
	choiceFooterMulti  = "[↑/↓ • Space toggle • Enter submit • Tab chat • Esc cancel]"
	choiceFooterForm   = "[Tab/↑↓ field • Enter submit • Esc cancel]"
)

// choiceFormUnderlineWidth is the minimum visible underline length for
// a form-mode field placeholder / default value. Authors expect a
// noticeable gap even for short defaults; the interactive widget
// (Phase C) will grow this on focus.
const choiceFormUnderlineWidth = 10

// Render lays out the choice element at the supplied width.
func (c Choice) Render(width int, env expr.Env, rr ViewRenderer) (string, error) {
	prompt, err := renderLeaf(rr, c.Prompt, env)
	if err != nil {
		return "", fmt.Errorf("choice.prompt: %w", err)
	}
	prompt = strings.TrimSpace(prompt)

	var body string
	var footer string
	var promptSuffix string
	switch c.Mode {
	case "multi":
		body, promptSuffix, err = c.renderMulti(width, env, rr)
		footer = choiceFooterMulti
	case "form":
		body, err = c.renderForm(width, env, rr)
		footer = choiceFooterForm
	default:
		// "single" is the default mode, matching the unmarshal-time
		// default-fill in internal/app/choice.go. We treat any
		// unrecognized mode as single rather than erroring because the
		// loader's schema validator already rejected the bad mode before
		// we got here.
		body, err = c.renderSingle(width, env, rr)
		footer = choiceFooterSingle
	}
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	if prompt != "" {
		// Trailing ":" reads as native and matches the heading
		// affordance without pulling in lipgloss styling (the
		// interactive widget applies styling on top). The optional
		// multi-mode bounds suffix sits between the prompt body and
		// the colon (e.g. "Select symptoms (1-5):").
		sb.WriteString(prompt)
		if promptSuffix != "" {
			sb.WriteByte(' ')
			sb.WriteString(promptSuffix)
		}
		if !strings.HasSuffix(prompt, ":") {
			sb.WriteByte(':')
		}
		// Blank line before the body keeps the prompt visually
		// distinct from the rows.
		sb.WriteString("\n\n")
	} else if promptSuffix != "" {
		// No prompt but bounds were set — surface the bounds alone
		// so the author's constraint is visible.
		sb.WriteString(promptSuffix)
		sb.WriteString("\n\n")
	}
	sb.WriteString(body)
	// Blank line before the footer keeps the keybinding hint visually
	// detached from the body. When the body is empty (all items
	// guarded away), we still emit the footer alone — authors can
	// fall through to the keybinding cue rather than seeing a blank
	// widget.
	if body != "" {
		sb.WriteString("\n\n")
	}
	sb.WriteString(footer)
	return clampChoiceRenderLines(sb.String(), width), nil
}

// renderSingle lays out single-mode items. Cursor gutter + label
// (padded to the longest label) + gutter + hint, mirroring list.go's
// two-column block. Items without a hint render as bare rows in the
// same column geometry so the cursor never jumps horizontally between
// rows (matters for Phase C's overlay).
func (c Choice) renderSingle(width int, env expr.Env, rr ViewRenderer) (string, error) {
	rows, anyHint, err := c.collectItemRows(env, rr, false)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return layoutChoiceRows(rows, choiceCursorGutter, width, anyHint), nil
}

// renderMulti lays out multi-mode items. The cursor gutter is
// followed by `[ ] `, then the label/hint pair as in single mode.
// The second return value is the optional `(min–max)` suffix the
// caller grafts onto the prompt header. Suffix is
// empty when the author left both bounds defaulted.
func (c Choice) renderMulti(width int, env expr.Env, rr ViewRenderer) (string, string, error) {
	rows, anyHint, err := c.collectItemRows(env, rr, true)
	if err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		return "", c.multiBounds(0), nil
	}
	// Combine the cursor + checkbox into one prefix so the column
	// math in layoutChoiceRows treats them as a single gutter.
	prefix := choiceCursorGutter + choiceCheckboxGutter
	body := layoutChoiceRows(rows, prefix, width, anyHint)
	return body, c.multiBounds(len(rows)), nil
}

// multiBounds renders the "(min–max)" suffix for multi-mode prompts.
// Returns the empty string when neither bound was set explicitly
// (the default is min=0, max=len(visible)) — the suffix
// only adds noise when the author didn't constrain the selection.
func (c Choice) multiBounds(visibleCount int) string {
	// Nothing visible to pick from → suppress the bounds suffix
	// entirely. Rendering "(1–0)" would be nonsense, and a bare
	// "(1)" suggests one pickable item where there are none.
	if visibleCount == 0 {
		return ""
	}
	min := 0
	max := visibleCount
	if c.MinSet {
		min = c.Min
	}
	if c.MaxSet {
		max = c.Max
	}
	if !c.MinSet && !c.MaxSet {
		return ""
	}
	if min == max {
		return fmt.Sprintf("(%d)", min)
	}
	// En-dash (U+2013) renders the range as "(1–5)" to match the
	// "Select symptoms (1–5)" convention. Authors who want ASCII can
	// override the prompt directly.
	return fmt.Sprintf("(%d–%d)", min, max)
}

// collectItemRows walks c.Items, evaluates per-item When guards,
// pongo-expands Label / Hint / Param placeholders, and returns the
// surviving rows in author order. When multi == false, single-mode
// param hints are appended to the label.
func (c Choice) collectItemRows(env expr.Env, rr ViewRenderer, multi bool) ([]choiceItemRow, bool, error) {
	rows := make([]choiceItemRow, 0, len(c.Items))
	anyHint := false
	for i, it := range c.Items {
		keep, err := evalWhen(it.When, env)
		if err != nil {
			return nil, false, fmt.Errorf("choice.items[%d] when: %w", i, err)
		}
		if !keep {
			continue
		}
		labelSrc := it.Label
		if multi && labelSrc == "" {
			// Multi mode: label defaults to the item value.
			labelSrc = it.Value
		}
		label, err := renderLeaf(rr, labelSrc, env)
		if err != nil {
			return nil, false, fmt.Errorf("choice.items[%d] label: %w", i, err)
		}
		hint, err := renderLeaf(rr, it.Hint, env)
		if err != nil {
			return nil, false, fmt.Errorf("choice.items[%d] hint: %w", i, err)
		}
		label = strings.TrimRight(label, " \t")
		hint = strings.TrimSpace(hint)

		if !multi && it.Param != nil {
			// Append the param affordance to the label. Prefer the
			// placeholder when present (mirrors the inline text the
			// interactive widget will show); otherwise fall back to
			// the slot name with a trailing "?" so the row reads as
			// a question.
			placeholder, err := renderLeaf(rr, it.Param.Placeholder, env)
			if err != nil {
				return nil, false, fmt.Errorf("choice.items[%d] param.placeholder: %w", i, err)
			}
			placeholder = strings.TrimSpace(placeholder)
			if placeholder != "" {
				label = label + " [" + placeholder + "]"
			} else if it.Param.Slot != "" {
				label = label + " (" + it.Param.Slot + "?)"
			}
		}

		if hint != "" {
			anyHint = true
		}
		rows = append(rows, choiceItemRow{label: label, hint: hint})
	}
	return rows, anyHint, nil
}

// layoutChoiceRows renders a slice of choice rows at the supplied
// width with the supplied per-row prefix. When anyHint is true, the
// label column is padded to the longest label and a two-space gutter
// separates label from hint (mirroring list.go::renderTwoColumnList).
// When anyHint is false, rows render as `<prefix><label>` with no
// trailing alignment.
func layoutChoiceRows(rows []choiceItemRow, prefix string, width int, anyHint bool) string {
	if width < 1 {
		width = 1
	}
	prefixWidth := visibleLen(prefix)
	budget := width - prefixWidth
	if budget <= 0 {
		return truncateChoiceText(prefix, width)
	}
	if !anyHint {
		var sb strings.Builder
		for i, r := range rows {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(prefix)
			sb.WriteString(truncateChoiceText(r.label, budget))
		}
		return sb.String()
	}
	maxLabel := 0
	for _, r := range rows {
		if n := visibleLen(r.label); n > maxLabel {
			maxLabel = n
		}
	}
	maxLabelBudget := budget - gutterWidth - minHintWidth
	if maxLabelBudget >= choiceMinLabelColumnWidth && maxLabel > maxLabelBudget {
		maxLabel = maxLabelBudget
	}
	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(prefix)
		label := truncateChoiceText(r.label, maxLabel)
		labelWidth := visibleLen(label)
		if r.hint == "" {
			sb.WriteString(truncateChoiceText(label, budget))
			continue
		}
		if labelWidth+gutterWidth >= budget {
			sb.WriteString(truncateChoiceText(label, budget))
			continue
		}
		pad := maxLabel - labelWidth
		if pad < 0 {
			pad = 0
		}
		hintBudget := budget - labelWidth - pad - gutterWidth
		if hintBudget <= 0 {
			sb.WriteString(truncateChoiceText(label, budget))
			continue
		}
		sb.WriteString(label)
		sb.WriteString(strings.Repeat(" ", pad+gutterWidth))
		sb.WriteString(truncateChoiceText(r.hint, hintBudget))
	}
	return sb.String()
}

const choiceMinLabelColumnWidth = 8

func clampChoiceRenderLines(s string, width int) string {
	if width < 1 {
		width = 1
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = truncateChoiceText(line, width)
	}
	return strings.Join(lines, "\n")
}

func truncateChoiceText(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if visibleLen(s) <= width {
		return s
	}
	runes := []rune(s)
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}

// renderForm lays out form-mode content. The Template body has its
// `{name}` placeholders replaced with `_<value>_` runs sized to at
// least choiceFormUnderlineWidth so Phase C can grow them on focus
// without re-flowing surrounding text. Readonly fields evaluate
// their Expr against env; writable fields show their default (or
// placeholder, or `<type>` as a last resort).
func (c Choice) renderForm(width int, env expr.Env, rr ViewRenderer) (string, error) {
	// Build a lookup keyed by field name. Fields whose When guard
	// fails are dropped — the template substitution treats them as
	// missing and emits a bare-underscore stub so the layout
	// doesn't collapse. (Authors who want a guarded field to vanish
	// from the template should guard the whole element, not the
	// field.)
	fieldByName := make(map[string]app.ChoiceField, len(c.Fields))
	for _, f := range c.Fields {
		keep, err := evalWhen(f.When, env)
		if err != nil {
			return "", fmt.Errorf("choice.fields.%s when: %w", f.Name, err)
		}
		if !keep {
			continue
		}
		fieldByName[f.Name] = f
	}

	// Substitute the template body with field-derived strings.
	// We expand pongo on the template body FIRST so {{ world.x }}
	// resolves; then walk the result and replace `{name}` literals.
	// Order matters: a pongo expression that emits `{name}` should
	// NOT be re-substituted (that would defeat the load-time
	// validator's `{name}` ↔ fields check). The
	// `{name}` placeholders are author-source-level, not runtime.
	// Concretely: we apply the literal-`{name}` pass on the
	// AUTHOR template, then pongo-expand the resulting body. That
	// way both the underline padding and any author-supplied
	// `{{ ... }}` adjacent to a `{name}` survive verbatim.
	bodySrc := c.Template
	bodySrc = choicePlaceholderRegex.ReplaceAllStringFunc(bodySrc, func(match string) string {
		name := match[1 : len(match)-1] // strip { and }
		f, ok := fieldByName[name]
		if !ok {
			// Loader guarantees every {name} has a fields entry; a
			// guarded-away field falls into this branch. Emit an
			// underline-only stub so the line still reads.
			return formUnderline("")
		}
		val := formFieldValue(f, env)
		out := formUnderline(val)
		if u := strings.TrimSpace(f.Unit); u != "" {
			out = out + " " + u
		}
		return out
	})

	body, err := renderLeaf(rr, bodySrc, env)
	if err != nil {
		return "", fmt.Errorf("choice.template: %w", err)
	}

	// Indent the body two spaces so the form reads as a block under
	// the prompt — the form reads as an indented block. We DON'T
	// reflow inside the indent: form templates are author-laid-out
	// (one logical sentence per line) and the underlines must keep
	// their character count to communicate field width.
	body = strings.TrimRight(body, "\n")
	if body == "" {
		// Every field is when:-guarded away (or the template was
		// empty after substitution). Don't return "" — the
		// Render wrapper would then suppress the blank-line-then-
		// footer separator and emit a widget with no footer, which
		// disagrees with single/multi mode's empty-state behaviour.
		// Emit a visible "(no fields visible)" stub instead so the
		// footer surfaces and authors can see the empty state.
		return "  (no fields visible)", nil
	}
	var sb strings.Builder
	for i, line := range strings.Split(body, "\n") {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString("  ")
		sb.WriteString(line)
	}
	_ = width // form mode is not width-reflowed; see comment above.
	return sb.String(), nil
}

// formFieldValue picks the static string to render inside a form
// field's underline. Order of precedence: readonly fields evaluate
// their Expr; writable fields show their Default (stringified);
// failing that, their Placeholder. Anything else falls back to the
// field name so the template still reads.
func formFieldValue(f app.ChoiceField, env expr.Env) string {
	if f.Readonly && strings.TrimSpace(f.Expr) != "" {
		p, err := expr.Compile(f.Expr)
		if err == nil {
			v, err := expr.EvalAny(p, env)
			if err == nil && v != nil {
				return fmt.Sprintf("%v", v)
			}
		}
		// Compile / eval failures fall through to the placeholder /
		// name fallback — the loader's compile-pass already
		// surfaced syntax errors, and a runtime failure (undefined
		// var) shouldn't crash a static render.
	}
	if f.Default != nil {
		switch d := f.Default.(type) {
		case string:
			if d != "" {
				return d
			}
		default:
			return fmt.Sprintf("%v", d)
		}
	}
	if f.Placeholder != "" {
		return f.Placeholder
	}
	return f.Name
}

// formUnderline returns the `_<value>_` underline rendering for a
// form field. The leading and trailing underscores are part of the
// visual treatment (Phase C will replace them with lipgloss
// .Underline()); the static form picks character-only affordances
// so the output is greppable from transport logs.
//
// Pad to choiceFormUnderlineWidth total characters so short values
// don't read as cramped.
func formUnderline(value string) string {
	v := strings.TrimSpace(value)
	padLen := choiceFormUnderlineWidth - visibleLen(v)
	if padLen < 0 {
		padLen = 0
	}
	return "_" + v + strings.Repeat("_", padLen) + "_"
}
