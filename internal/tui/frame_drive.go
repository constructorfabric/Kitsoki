package tui

import (
	"kitsoki/internal/orchestrator"
)

// ApplyTurnOutcome folds a completed TurnOutcome into the model exactly the
// way the live TUI does when a turn settles, returning the updated model. It
// is the production seam a headless driver (kitsoki drive) calls between
// orch.Turn and ComposeFrame so the still frame it emits is the same paint a
// human would have seen — the room body, current state, menu, and mode all
// advance through the one canonical handleTurnOutcome path rather than a
// re-derived lookalike.
//
// The tea.Cmd handleTurnOutcome returns drives asynchronous live-UI
// follow-ups (queue draining, spinner ticks, auto-action prints); a headless
// single-still caller has no event loop to run them on, so it is intentionally
// discarded. The synchronous model mutations — currentState, transcript body,
// menu, location, mode — are all applied before the command is returned, so
// the composed frame is faithful without it.
//
// input is the free-text utterance that produced this outcome (echoed in the
// rejection/clarification branches); err is the orchestrator error, if any
// (rendered as an error body, mirroring the live TUI).
func (m RootModel) ApplyTurnOutcome(out *orchestrator.TurnOutcome, input string, err error) RootModel {
	updated, _ := m.handleTurnOutcome(turnOutcomeMsg{outcome: out, input: input, err: err})
	rm, _ := updated.(RootModel)
	return rm
}
