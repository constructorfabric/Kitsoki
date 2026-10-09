// operator_question.go — the inline widget that surfaces a forwarded agent
// question to the operator and collects the answer.
//
// operatorQuestionModel is opened when a TUIOperatorPrompter dispatches an
// operatorQuestionMsg (a dispatched `claude -p` agent forwarded an
// AskUserQuestion into kitsoki and is blocked, mid-turn, waiting for a human).
// It mirrors the surface of choiceWidgetModel — Open / Update / View / Close —
// and reuses the same lipgloss styles and key conventions (↑/↓ move, Space
// toggles a multi-select, Enter confirms, Esc cancels). The differences:
//
//   - It is driven by an ASYNC message arriving while ModeAwaitingLLM, not by a
//     typed view element. The in-flight turn does not complete on commit; the
//     answer is sent back over a channel and the same turn resumes.
//   - AskUserQuestion may carry up to four questions in one call. We present
//     them ONE AT A TIME (question 1 → Enter → question 2 …), accumulating
//     answers keyed by each question's text, and finalize after the last.
//
// The answer map matches what AskUserQuestion itself would have returned: a
// single-select question maps its text → the chosen option label or a custom
// text answer; a multi-select maps its text → the list of chosen labels.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/host"
)

// operatorQuestionResult is the return signal from operatorQuestionModel.Update.
// A nil result means "no decision yet — keep the widget open". A non-nil value
// means the caller should leave ModeOperatorQuestion and resume the turn.
//
//   - Cancel == true: Esc was pressed. The caller sends a nil answer back to the
//     prompter (the host maps that to an LLM-visible "proceed on your own").
//   - Cancel == false: every question is answered. Answers carries the full
//     map to hand back to the parked agent.
type operatorQuestionResult struct {
	Cancel  bool
	Answers map[string]any
}

// operatorQuestionModel is the inline question picker. RootModel keeps one
// instance and re-Opens it for each forwarded question batch; Close() resets it.
type operatorQuestionModel struct {
	active    bool
	questions []host.OperatorQuestion
	// answerCh is the channel the parked agent's Ask is blocked on. The
	// caller (updateOperatorQuestion) owns sending on it; the model carries it
	// so the lifecycle stays in one place.
	answerCh chan map[string]any

	idx      int          // index of the question currently on screen
	cursor   int          // option cursor within the current question
	selected map[int]bool // multi-select picks for the current question
	custom   string       // single-select custom answer buffer for current question
	answers  map[string]any
	errMsg   string
}

// newOperatorQuestionModel returns a zero-valued widget.
func newOperatorQuestionModel() operatorQuestionModel {
	return operatorQuestionModel{}
}

// IsActive reports whether the widget currently owns the keyboard.
func (m *operatorQuestionModel) IsActive() bool { return m.active }

// Close resets the widget to the inactive zero state. It does NOT touch
// answerCh's parked reader — the caller is responsible for sending an answer
// (or nil) before closing, so the agent never strands.
func (m *operatorQuestionModel) Close() {
	*m = operatorQuestionModel{}
}

// Open initialises the widget from a forwarded question batch and the channel
// the agent is blocked on. An empty batch is rejected (the host never forwards
// one, but the widget is defensive) so Open's caller can fall back cleanly.
func (m *operatorQuestionModel) Open(questions []host.OperatorQuestion, answerCh chan map[string]any) error {
	if len(questions) == 0 {
		return fmt.Errorf("operator question: empty question batch")
	}
	*m = operatorQuestionModel{
		active:    true,
		questions: questions,
		answerCh:  answerCh,
		selected:  map[int]bool{},
		answers:   map[string]any{},
	}
	return nil
}

// current returns the question on screen.
func (m *operatorQuestionModel) current() host.OperatorQuestion {
	return m.questions[m.idx]
}

// Update consumes one tea.Msg and returns the next widget state plus an optional
// result. tea.Cmd is reserved for symmetry with choiceWidgetModel (always nil —
// the widget is synchronous).
func (m operatorQuestionModel) Update(msg tea.Msg) (operatorQuestionModel, tea.Cmd, *operatorQuestionResult) {
	if !m.active {
		return m, nil, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, nil
	}
	q := m.current()
	switch key.Type {
	case tea.KeyEsc:
		return m, nil, &operatorQuestionResult{Cancel: true}
	case tea.KeyUp:
		m.errMsg = ""
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil, nil
	case tea.KeyDown:
		m.errMsg = ""
		if m.cursor < maxOperatorQuestionCursor(q) {
			m.cursor++
		}
		return m, nil, nil
	case tea.KeySpace:
		// Space toggles in multi-select; on the single-select custom row it
		// edits the custom answer. Other single-select rows still commit with
		// Enter, matching the choice widget.
		if q.MultiSelect && len(q.Options) > 0 {
			m.selected[m.cursor] = !m.selected[m.cursor]
			m.errMsg = ""
		} else if isOperatorQuestionCustomCursor(q, m.cursor) {
			m.custom += " "
			m.errMsg = ""
		}
		return m, nil, nil
	case tea.KeyBackspace:
		if isOperatorQuestionCustomCursor(q, m.cursor) && m.custom != "" {
			runes := []rune(m.custom)
			m.custom = string(runes[:len(runes)-1])
			m.errMsg = ""
		}
		return m, nil, nil
	case tea.KeyRunes:
		if isOperatorQuestionCustomCursor(q, m.cursor) {
			m.custom += string(key.Runes)
			m.errMsg = ""
		}
		return m, nil, nil
	case tea.KeyEnter:
		return m.commitCurrent(q)
	}
	return m, nil, nil
}

// commitCurrent records the answer for the on-screen question and either
// advances to the next question or, when the last is answered, returns the
// finalized answer map.
func (m operatorQuestionModel) commitCurrent(q host.OperatorQuestion) (operatorQuestionModel, tea.Cmd, *operatorQuestionResult) {
	if len(q.Options) == 0 {
		// No options to pick — nothing sensible to answer; treat Enter as a
		// skip so the operator is never trapped on a malformed question.
		m.answers[q.Question] = ""
	} else if q.MultiSelect {
		labels := make([]string, 0, len(q.Options))
		for i, opt := range q.Options {
			if m.selected[i] {
				labels = append(labels, opt.Label)
			}
		}
		if len(labels) == 0 {
			m.errMsg = "select at least one (Space to toggle)"
			return m, nil, nil
		}
		m.answers[q.Question] = labels
	} else if isOperatorQuestionCustomCursor(q, m.cursor) {
		answer := strings.TrimSpace(m.custom)
		if answer == "" {
			m.errMsg = "type a custom answer"
			return m, nil, nil
		}
		m.answers[q.Question] = answer
	} else {
		m.answers[q.Question] = q.Options[m.cursor].Label
	}

	// Advance to the next question, or finalize.
	if m.idx < len(m.questions)-1 {
		m.idx++
		m.cursor = 0
		m.selected = map[int]bool{}
		m.custom = ""
		m.errMsg = ""
		return m, nil, nil
	}
	return m, nil, &operatorQuestionResult{Answers: m.answers}
}

// View renders the on-screen question at the supplied width, reusing the choice
// widget's styles so the two surfaces read identically.
func (m *operatorQuestionModel) View(width int) string {
	if !m.active {
		return ""
	}
	if width < 20 {
		width = 20
	}
	q := m.current()

	var sb strings.Builder
	// Header line: agent badge + the short category + a progress counter when
	// the batch holds more than one question.
	header := "🤖 Agent question"
	if q.Header != "" {
		header += " — " + q.Header
	}
	if len(m.questions) > 1 {
		header += fmt.Sprintf("  (%d/%d)", m.idx+1, len(m.questions))
	}
	sb.WriteString(choiceHintStyle.Render(header))
	sb.WriteString("\n")

	if q.Question != "" {
		sb.WriteString(choicePromptStyle.Render(q.Question))
		if q.MultiSelect {
			sb.WriteString(" ")
			sb.WriteString(choiceHintStyle.Render("(choose any)"))
		}
		sb.WriteString("\n\n")
	}

	sb.WriteString(m.renderOptions(q))

	sb.WriteString("\n\n")
	sb.WriteString(m.renderFooter(q))
	return sb.String()
}

// ChromeView bounds the forwarded operator question when it occupies
// transcript.LiveLine in the normal-screen renderer. The full View remains the
// committed transcript body; this projection keeps the active cursor visible
// and prevents resize repaints from pushing stale question rows to scrollback.
func (m *operatorQuestionModel) ChromeView(width, maxRows int) string {
	full := m.View(width)
	if full == "" {
		return ""
	}
	if maxRows <= 0 {
		return ""
	}
	lines := clampLiveOverlayRows(strings.Split(full, "\n"), width)
	if len(lines) <= maxRows {
		return strings.Join(lines, "\n")
	}
	cursor := -1
	for i, line := range lines {
		if strings.Contains(line, "▸") {
			cursor = i
			break
		}
	}
	if cursor < 0 {
		cursor = 0
	}
	return strings.Join(windowLiveOverlayRows(lines, cursor, maxRows, width), "\n")
}

func (m *operatorQuestionModel) renderOptions(q host.OperatorQuestion) string {
	if len(q.Options) == 0 {
		return "  (no options — press Enter to skip)"
	}
	var sb strings.Builder
	for i, opt := range q.Options {
		if i > 0 {
			sb.WriteByte('\n')
		}
		// Cursor gutter.
		if i == m.cursor {
			sb.WriteString(choiceCursorStyle.Render("▸ "))
		} else {
			sb.WriteString("  ")
		}
		// Multi-select draws a checkbox; single-select a plain label.
		if q.MultiSelect {
			if m.selected[i] {
				sb.WriteString(choiceCheckedStyle.Render("[x] "))
			} else {
				sb.WriteString("[ ] ")
			}
		}
		sb.WriteString(opt.Label)
		if opt.Description != "" {
			sb.WriteString("  ")
			sb.WriteString(choiceHintStyle.Render(opt.Description))
		}
	}
	if operatorQuestionSupportsCustom(q) {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		if isOperatorQuestionCustomCursor(q, m.cursor) {
			sb.WriteString(choiceCursorStyle.Render("▸ "))
		} else {
			sb.WriteString("  ")
		}
		sb.WriteString("Custom answer")
		sb.WriteString("  ")
		custom := strings.TrimSpace(m.custom)
		if custom == "" {
			sb.WriteString(choiceHintStyle.Render("type your own response"))
		} else {
			sb.WriteString(custom)
		}
	}
	return sb.String()
}

func (m *operatorQuestionModel) renderFooter(q host.OperatorQuestion) string {
	var hint string
	if q.MultiSelect {
		hint = "[↑/↓ • Space toggle • Enter confirm • Esc let agent decide]"
	} else if operatorQuestionSupportsCustom(q) {
		hint = "[↑/↓ move • type on Custom answer • Enter confirm • Esc let agent decide]"
	} else {
		hint = "[↑/↓ move • Enter confirm • Esc let agent decide]"
	}
	rendered := choiceFooterStyle.Render(hint)
	if m.errMsg != "" {
		rendered = choiceErrorStyle.Render("("+m.errMsg+")") + "\n" + rendered
	}
	return rendered
}

func operatorQuestionSupportsCustom(q host.OperatorQuestion) bool {
	return !q.MultiSelect && len(q.Options) > 0
}

func isOperatorQuestionCustomCursor(q host.OperatorQuestion, cursor int) bool {
	return operatorQuestionSupportsCustom(q) && cursor == len(q.Options)
}

func maxOperatorQuestionCursor(q host.OperatorQuestion) int {
	if operatorQuestionSupportsCustom(q) {
		return len(q.Options)
	}
	if len(q.Options) == 0 {
		return 0
	}
	return len(q.Options) - 1
}
