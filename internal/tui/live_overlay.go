package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// liveOverlayPlacement identifies which part of composeChromeParts owns a
// variable-height interactive surface. Prompt overlays replace promptLine;
// live overlays occupy transcript.LiveLine above the ordinary prompt.
type liveOverlayPlacement int

const (
	liveOverlayPrompt liveOverlayPlacement = iota
	liveOverlayTranscriptLine

	// liveOverlayStableTerminalRows is the terminal-row count produced by the
	// maintained 760x430 xterm.js regression viewport. The overlay ceiling is
	// derived from this target minus every sibling chrome row actually present,
	// so optional operation/banner/live/footer rows lower the initial wide-screen
	// footprint too. Shorter terminals still use their exact current height;
	// there is intentionally no minimum floor.
	liveOverlayStableTerminalRows = 10
)

// liveOverlayRowsFor returns the physical-row budget left after rendering
// every sibling chrome part that will share the normal-screen live region.
// There is deliberately no minimum: at tiny heights, forcing even a four-row
// overlay can make the live region taller than the terminal and push stale
// frames into native scrollback.
func (m RootModel) liveOverlayRowsFor(placement liveOverlayPlacement) int {
	if m.height <= 0 {
		return 0
	}
	width := m.width
	if width < 1 {
		width = 1
	}

	budgetModel := m
	promptLine := ""
	if placement == liveOverlayTranscriptLine {
		// The overlay itself is the current live line, so remove it from the
		// sibling measurement. The ordinary prompt remains below it.
		budgetModel.transcript.liveLine = ""
		promptLine, _ = composePromptAndBanner(budgetModel)
	}
	bannerLine := budgetModel.inbox.ActionRequiredBanner()
	siblings := joinChromeParts(composeChromeParts(budgetModel, width, promptLine, bannerLine))
	terminalRows := m.height
	if terminalRows > liveOverlayStableTerminalRows {
		terminalRows = liveOverlayStableTerminalRows
	}
	available := terminalRows - lipgloss.Height(siblings)
	if available < 0 {
		return 0
	}
	return available
}

func (m RootModel) liveOverlayRenderRows(placement liveOverlayPlacement) int {
	available := m.liveOverlayRowsFor(placement)
	if m.liveOverlayRowLimitSet && m.liveOverlayRowLimit > 0 && m.liveOverlayRowLimit < available {
		return m.liveOverlayRowLimit
	}
	return available
}

func (m *RootModel) resetLiveOverlayRowLimit(placement liveOverlayPlacement) {
	m.liveOverlayRowLimit = m.liveOverlayRowsFor(placement)
	m.liveOverlayRowLimitSet = true
}

func (m *RootModel) tightenLiveOverlayRowLimit() bool {
	placement, ok := m.activeLiveOverlayPlacement()
	if !ok {
		return false
	}
	available := m.liveOverlayRowsFor(placement)
	if !m.liveOverlayRowLimitSet || m.liveOverlayRowLimit <= 0 || available < m.liveOverlayRowLimit {
		m.liveOverlayRowLimit = available
		m.liveOverlayRowLimitSet = true
		return true
	}
	return false
}

func (m RootModel) activeLiveOverlayPlacement() (liveOverlayPlacement, bool) {
	switch m.mode {
	case ModeChoosing, ModeMenu, ModeMetaSessions, ModeStorySelector:
		return liveOverlayPrompt, true
	case ModeOperatorQuestion:
		return liveOverlayTranscriptLine, true
	default:
		return liveOverlayPrompt, false
	}
}

// renderLiveOverlay renders logical rows through one shared, width-safe,
// cursor-following window. header rows stay fixed when space allows; when the
// terminal is tiny they yield space to the selected row and overflow hint.
func renderLiveOverlay(header, rows []string, selected, width, maxRows int) string {
	if maxRows <= 0 {
		return ""
	}
	if width < 1 {
		width = 1
	}
	header = clampLiveOverlayRows(header, width)
	rows = clampLiveOverlayRows(rows, width)

	if len(rows) == 0 {
		if len(header) > maxRows {
			header = header[:maxRows]
		}
		return strings.Join(header, "\n")
	}

	// Preserve at least the selected row. When rows are hidden and two body
	// rows fit, reserve the second for an ↑/↓ affordance rather than retaining
	// decorative header chrome.
	minBodyRows := 1
	if len(rows) > 1 && maxRows > 1 {
		minBodyRows = 2
	}
	maxHeaderRows := maxRows - minBodyRows
	if maxHeaderRows < 0 {
		maxHeaderRows = 0
	}
	if len(header) > maxHeaderRows {
		header = header[:maxHeaderRows]
	}

	bodyBudget := maxRows - len(header)
	body := windowLiveOverlayRows(rows, selected, bodyBudget, width)
	out := make([]string, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	if len(out) > maxRows {
		out = out[:maxRows]
	}
	return strings.Join(out, "\n")
}

// windowLiveOverlayRows keeps selected visible and uses a stable physical row
// count whenever the logical list is larger than maxRows. Absolute labels are
// part of the caller-supplied rows, so windowing never renumbers hotkeys.
func windowLiveOverlayRows(rows []string, selected, maxRows, width int) []string {
	if maxRows <= 0 || len(rows) == 0 {
		return nil
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(rows) {
		selected = len(rows) - 1
	}
	if len(rows) <= maxRows {
		return rows
	}
	if maxRows == 1 {
		return []string{rows[selected]}
	}
	if maxRows == 2 {
		above := selected
		below := len(rows) - selected - 1
		hint := liveOverlayOverflowSummary(above, below, width)
		if below == 0 && above > 0 {
			return []string{hint, rows[selected]}
		}
		return []string{rows[selected], hint}
	}

	var start, bodyRows int
	switch {
	case selected < maxRows-1:
		// Top window: no ↑ indicator, so all but one row can hold entries.
		bodyRows = maxRows - 1
		start = 0
	case selected >= len(rows)-(maxRows-1):
		// Bottom window: no ↓ indicator.
		bodyRows = maxRows - 1
		start = len(rows) - bodyRows
	default:
		// Middle window: reserve both indicators before centering. Computing
		// the body first and centering afterward can introduce a new ↑ row
		// and accidentally return maxRows+1.
		bodyRows = maxRows - 2
		start = selected - bodyRows/2
		if start < 1 {
			start = 1
		}
		maxStart := len(rows) - bodyRows - 1
		if start > maxStart {
			start = maxStart
		}
	}
	end := start + bodyRows
	above := start
	below := len(rows) - end

	out := make([]string, 0, maxRows)
	if above > 0 {
		out = append(out, liveOverlayOverflowLine("↑", above, width))
	}
	out = append(out, rows[start:end]...)
	if below > 0 {
		out = append(out, liveOverlayOverflowLine("↓", below, width))
	}
	return out
}

func liveOverlayOverflowLine(direction string, count, width int) string {
	line := fmt.Sprintf("  %s %d more", direction, count)
	return choiceHintStyle.Render(truncateFrameCell(line, width))
}

func liveOverlayOverflowSummary(above, below, width int) string {
	var parts []string
	if above > 0 {
		parts = append(parts, fmt.Sprintf("↑ %d more", above))
	}
	if below > 0 {
		parts = append(parts, fmt.Sprintf("↓ %d more", below))
	}
	return choiceHintStyle.Render(truncateFrameCell("  "+strings.Join(parts, " · "), width))
}

func clampLiveOverlayRows(rows []string, width int) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		// A logical overlay row must always be one physical terminal row.
		row = strings.ReplaceAll(row, "\r", " ")
		row = strings.ReplaceAll(row, "\n", " ")
		out[i] = truncateFrameCell(row, width)
	}
	return out
}
