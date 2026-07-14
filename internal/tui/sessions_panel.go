package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/metamode"
)

// sessionsPanelModel is the foyer "meta sessions" overlay. See
// docs/stories/meta-mode.md §8 "Chat persistence". It lists every active meta chat the
// controller can see — including cross-app `self` chats merged in
// by Controller.ListChats — and lets the user pick one to resume
// without typing `/meta resume <id>`.
//
// Activation: the Esc menu's "Meta sessions" entry calls
// sessionsPanelLoadCmd, which runs ListChats off the UI goroutine
// and emits sessionsPanelLoadedMsg. The handler Open()s this model
// with the loaded rows and switches the RootModel to ModeMetaSessions
// so the overlay receives input.
//
// Interaction:
//   - ↑/↓ (or j/k) move selection
//   - Enter resumes the highlighted chat (emits sessionsPanelChoiceMsg)
//   - Esc / q closes the overlay without resuming
//
// Layout reuses metaListColumns / metaListingCells from tui.go so the
// header row and per-row cell composition stay consistent with the
// inline `/meta list` output.
type sessionsPanelModel struct {
	active   bool
	listings []metamode.ChatListing
	selected int
}

// sessionsPanelChoiceMsg is emitted when the user picks a row.
// Carries the chosen chat's ID and mode name so the handler can
// dispatch into metaResumeCmd unchanged.
type sessionsPanelChoiceMsg struct {
	chatID   string
	modeName string
}

// sessionsPanelLoadedMsg is emitted after the asynchronous
// Controller.ListChats call returns. err is non-nil on failure; the
// overlay is not opened in that case and the caller surfaces the
// error via the transcript instead.
type sessionsPanelLoadedMsg struct {
	listings []metamode.ChatListing
	err      error
}

// newSessionsPanelModel returns a closed model. Open() is the
// activation entrypoint once listings are available.
func newSessionsPanelModel() sessionsPanelModel {
	return sessionsPanelModel{}
}

// Open activates the overlay with the given listings. The selection
// resets to the first row; empty listings still produce an active
// model that renders an "(no meta sessions)" body — the panel becomes
// dismissable via Esc rather than silently no-oping.
func (m *sessionsPanelModel) Open(listings []metamode.ChatListing) {
	m.active = true
	m.listings = listings
	m.selected = 0
}

// Close deactivates the overlay and drops the cached listings so a
// re-open via the menu always re-queries the controller (no stale
// rows hiding behind a closed panel).
func (m *sessionsPanelModel) Close() {
	m.active = false
	m.listings = nil
	m.selected = 0
}

// IsActive reports whether the overlay is currently visible.
func (m sessionsPanelModel) IsActive() bool { return m.active }

func (m sessionsPanelModel) Init() tea.Cmd { return nil }

// Update handles arrow nav, numeric hotkeys, Enter, and Esc/q.
// Returns the (possibly mutated) model plus an optional tea.Cmd that
// emits sessionsPanelChoiceMsg on selection.
func (m sessionsPanelModel) Update(msg tea.Msg) (sessionsPanelModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.String() {
	case "esc", "q":
		m.active = false
		return m, nil

	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil

	case "down", "j":
		if m.selected < len(m.listings)-1 {
			m.selected++
		}
		return m, nil

	case "enter":
		if len(m.listings) == 0 {
			// Nothing to pick — Enter just closes the panel.
			m.active = false
			return m, nil
		}
		chosen := m.listings[m.selected]
		m.active = false
		return m, func() tea.Msg {
			return sessionsPanelChoiceMsg{chatID: chosen.ID, modeName: chosen.ModeName}
		}
	}

	// Numeric hotkeys for the first nine rows.
	for i := 1; i <= len(m.listings) && i <= 9; i++ {
		if keyMsg.String() == fmt.Sprintf("%d", i) {
			chosen := m.listings[i-1]
			m.active = false
			m.selected = i - 1
			return m, func() tea.Msg {
				return sessionsPanelChoiceMsg{chatID: chosen.ID, modeName: chosen.ModeName}
			}
		}
	}
	return m, nil
}

// View renders the overlay. Returns "" when inactive.
func (m sessionsPanelModel) View() string {
	if !m.active {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("meta sessions (↑/↓ to move, Enter to resume, Esc to close)\n\n")
	if len(m.listings) == 0 {
		sb.WriteString("(no active meta sessions — start one with /meta <mode>)\n")
		return sb.String()
	}

	header, rows := m.tableRows()
	sb.WriteString(header)
	sb.WriteByte('\n')
	sb.WriteString(strings.Join(rows, "\n"))
	sb.WriteByte('\n')
	return sb.String()
}

// ChromeView renders the sessions panel through the shared normal-screen row
// budget. The title and column labels stay fixed when space allows; the
// selected logical session remains visible through the window.
func (m sessionsPanelModel) ChromeView(width, maxRows int) string {
	if !m.active {
		return ""
	}
	title := "meta sessions (↑/↓ to move, Enter to resume, Esc to close)"
	if len(m.listings) == 0 {
		return renderLiveOverlay([]string{title}, []string{
			"(no active meta sessions — start one with /meta <mode>)",
		}, 0, width, maxRows)
	}
	header, rows := m.tableRows()
	return renderLiveOverlay([]string{title, header}, rows, m.selected, width, maxRows)
}

func (m sessionsPanelModel) tableRows() (string, []string) {
	headers := metaListColumns()
	cellsByRow := make([][]string, 0, len(m.listings))
	for _, listing := range m.listings {
		cellsByRow = append(cellsByRow, metaListingCells(listing))
	}
	widths := computeColumnWidths(headers, cellsByRow)

	var header strings.Builder
	header.WriteString("  ")
	for i, text := range headers {
		header.WriteString(padRight(text, widths[i]))
		if i < len(headers)-1 {
			header.WriteString("  ")
		}
	}

	rows := make([]string, 0, len(cellsByRow))
	for i, cells := range cellsByRow {
		var row strings.Builder
		marker := "  "
		if i == m.selected {
			marker = "▸ "
		}
		row.WriteString(marker)
		for j, cellText := range cells {
			cell := padRight(cellText, widths[j])
			if i == m.selected {
				cell = menuItemSelectedStyle.Render(cell)
			} else {
				cell = menuItemStyle.Render(cell)
			}
			row.WriteString(cell)
			if j < len(cells)-1 {
				row.WriteString("  ")
			}
		}
		rows = append(rows, row.String())
	}
	return header.String(), rows
}

// computeColumnWidths returns the per-column max display width across
// the header and every row. Cells are runes-counted (so multibyte
// characters in the preview don't blow out the layout). Uses
// transcript.go's runeLen / padRight helpers — no separate width
// math for this surface.
func computeColumnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runeLen(h)
	}
	for _, row := range rows {
		for j, cell := range row {
			if j >= len(widths) {
				break
			}
			if w := runeLen(cell); w > widths[j] {
				widths[j] = w
			}
		}
	}
	return widths
}
