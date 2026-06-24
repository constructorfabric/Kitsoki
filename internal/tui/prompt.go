// Prompt construction & layout helpers — isolates the bubbles/textarea
// wiring so the rest of tui.go can stay focused on mode/state plumbing.
//
// The TUI prompt is a multi-line textarea that:
//   - wraps long input visually as the user types (no horizontal scroll),
//   - grows its rendered height downward (1 row when empty, +1 row per
//     wrapped/literal line up to a sensible cap),
//   - submits on plain Enter (handled by the root model BEFORE textarea
//     sees the key; we just keep textarea's InsertNewline binding away
//     from the bare "enter" key),
//   - inserts a literal newline on Alt+Enter / Ctrl+J (terminal-portable
//     "shift+enter" alternatives) and on the bash-style `\<Enter>` line-
//     continuation idiom (the root model strips a trailing "\" and
//     dispatches a manual newline insert).
//
// The textarea owns its own per-line prompt via SetPromptFunc so the
// "> " (or "» ") prefix renders ONCE at the top-left of the input block;
// continuation rows are padded to the same 2-column visual indent so the
// text reads as a contiguous wrapped paragraph. ShowLineNumbers is off,
// EndOfBufferCharacter is a space, and the cursor-line background is
// suppressed so the textarea looks like a single flowing input rather
// than a chunky editor pane.

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// promptPrefixOnPath is the "> " marker on lines 0 of the on-path
	// (and off-path) prompt. The width is fixed at promptPrefixCols.
	promptPrefixOnPath = "> "
	// promptPrefixMeta is the "» " marker on line 0 of the meta-mode prompt.
	promptPrefixMeta = "» "
	// promptPrefixCols is the visual width reserved for the per-line
	// prompt prefix. Both "> " and "» " are 2 columns wide so we keep a
	// single constant.
	promptPrefixCols = 2
	// promptSafetyMargin is the column count we reserve at the right
	// edge so the cursor and any terminal-edge quirks don't push past
	// the last column. Matches the legacy textinput layout (which had
	// the same margin) so tests asserting "width 80 → prompt width 76"
	// stay valid.
	promptSafetyMargin = 2
	// promptMinWidth is the inner-content width floor — narrow
	// terminals can't render a usable prompt below this. Stays at 20
	// to preserve the existing TestTUIPromptWidthHonoursMinimum
	// expectation.
	promptMinWidth = 20
	// promptMinHeight is the minimum number of rendered rows. 1 keeps
	// the empty prompt visually flat (placeholderView renders exactly
	// m.height rows; we want one row of placeholder, not a multi-row
	// box).
	promptMinHeight = 1
	// promptMaxHeight caps the dynamic growth so a runaway paste
	// doesn't eat the whole screen. Matches Claude Code's "tall enough
	// for a paragraph, not a document" feel.
	promptMaxHeight = 8
	// promptCharLimit preserves the historical textinput limit so a
	// rogue paste can't blow out memory. Comfortably larger than the
	// 8-row visual cap × ~80 chars/row.
	promptCharLimit = 4096
)

// newPromptTextarea builds the multi-line input model used at the bottom
// of the TUI. The returned model:
//   - is focused,
//   - has its "InsertNewline" binding REMOVED from plain Enter so the
//     root model's Enter handler can submit cleanly,
//   - keeps an "alt+enter" / "ctrl+j" alias for literal-newline inserts,
//   - hides line numbers and the cursor-line highlight,
//   - renders "> " at the head of line 0 and 2 spaces on continuation rows.
//
// Width/height are left at textarea defaults; resize() will set both via
// SetWidth and SetHeight before the first View().
func newPromptTextarea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "describe what you want, or /help"
	ta.CharLimit = promptCharLimit
	ta.ShowLineNumbers = false
	ta.MaxHeight = promptMaxHeight
	ta.EndOfBufferCharacter = ' '

	// Per-line prefix: "> " on row 0, 2 spaces on continuation rows.
	// SetPromptFunc with promptWidth=2 pads short returns to 2 cols
	// (textarea.go getPromptString left-pads with spaces), so returning
	// "" on rows 1+ produces "  " — visually a clean continuation
	// indent without a repeated marker.
	ta.SetPromptFunc(promptPrefixCols, func(lineIdx int) string {
		if lineIdx == 0 {
			return promptPrefixOnPath
		}
		return ""
	})

	// Re-bind InsertNewline OFF plain Enter. Plain Enter never reaches
	// the textarea (the root model intercepts it first), but keeping
	// "enter" inside the textarea's InsertNewline binding would mean
	// any future code path that DOES forward Enter (or a stray
	// tea.KeyEnter that slips past) would silently insert a newline
	// instead of submitting. We allow alt+enter and ctrl+j as portable
	// fallbacks for terminals that don't differentiate shift+enter
	// from enter.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "insert newline"),
	)

	// Suppress the bubbles default cursor-line background so the input
	// renders as plain text (no two-tone striping when the cursor
	// moves between wrapped rows). Foreground stays default so the
	// cursor itself is still visible.
	flatLine := lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = flatLine
	ta.BlurredStyle.CursorLine = flatLine
	ta.FocusedStyle.CursorLineNumber = flatLine
	ta.BlurredStyle.CursorLineNumber = flatLine

	// Apply the on-path prompt color so "> " renders in the same
	// violet/bold as the legacy promptStyle. Bold via the underlying
	// lipgloss style so it composes with focused/blurred variants.
	ta.FocusedStyle.Prompt = promptStyle
	ta.BlurredStyle.Prompt = promptStyle

	ta.Focus()
	return ta
}

// setPromptPrefix re-points the textarea's per-line prompt function. Use
// when entering/leaving a mode that changes the marker:
//   - on-path / off-path  → "> "
//   - meta-mode            → "» "
//
// Style (color) follows via setPromptStyle; the two are kept separate so
// the off-path mode (amber prefix) doesn't have to also override the
// prefix glyph.
func setPromptPrefix(m *textarea.Model, head string) {
	if head == "" {
		head = promptPrefixOnPath
	}
	m.SetPromptFunc(promptPrefixCols, func(lineIdx int) string {
		if lineIdx == 0 {
			return head
		}
		return ""
	})
}

// setPromptStyle swaps the lipgloss.Style applied to the per-line prefix.
// Off-path mode uses promptOffPathStyle (amber); meta and on-path use the
// default promptStyle (violet).
func setPromptStyle(m *textarea.Model, s lipgloss.Style) {
	m.FocusedStyle.Prompt = s
	m.BlurredStyle.Prompt = s
}

// promptVisualHeight returns the number of terminal rows the textarea
// will occupy when rendered. It walks the current value, dividing each
// logical line by the inner content width to count wrapped rows, then
// clamps to [promptMinHeight, promptMaxHeight]. The result drives
// SetHeight so the input grows downward as the user types.
//
// The calculation uses rune count, which slightly under-counts for
// double-width runes; the SetHeight clamp at promptMaxHeight prevents
// runaway and the textarea's internal viewport handles any minor
// off-by-one by scrolling. We intentionally avoid calling the bubbles
// memoizedWrap path — it's unexported and would require importing
// internal packages.
func promptVisualHeight(value string, innerWidth int) int {
	if innerWidth <= 0 {
		innerWidth = 1
	}
	if value == "" {
		return promptMinHeight
	}
	total := 0
	for _, line := range strings.Split(value, "\n") {
		runes := []rune(line)
		if len(runes) == 0 {
			total++
			continue
		}
		total += (len(runes) + innerWidth - 1) / innerWidth
	}
	if total < promptMinHeight {
		total = promptMinHeight
	}
	if total > promptMaxHeight {
		total = promptMaxHeight
	}
	return total
}

// shouldSubmitOnEnter reports whether a bare Enter should submit the
// current prompt value or be treated as a soft newline. The rule mirrors
// bash-style line continuation: if the value ends with an UNESCAPED
// backslash, Enter inserts a newline (after stripping the trailing "\")
// instead of submitting. This lets users compose multi-line input on
// terminals that don't surface alt+enter / ctrl+j (or that swallow them
// for other shortcuts).
//
// Returns (submit, valueAfter):
//   - submit=true  → caller submits valueAfter (== current value).
//   - submit=false → caller calls textarea.SetValue(valueAfter) then
//     InsertString("\n") to convert the "\<Enter>" into a real newline.
func shouldSubmitOnEnter(value string) (submit bool, valueAfter string) {
	// Count consecutive trailing backslashes — an odd count means the
	// last "\" is unescaped (i.e. a real continuation marker), an even
	// count means the user typed "\\" to escape the backslash and
	// wants to submit normally.
	n := 0
	for i := len(value) - 1; i >= 0 && value[i] == '\\'; i-- {
		n++
	}
	if n%2 == 1 {
		return false, value[:len(value)-1]
	}
	return true, value
}
