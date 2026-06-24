// offramp_outcome_test.go — rendering test for the SYNCHRONOUS ModeOffPath
// outcome produced by the agent off-ramp (an unroutable free-text utterance
// in a room declaring `agent_off_ramp:`). Distinct from the typed `/freeform`
// flow (offpath_test.go), this asserts handleTurnOutcome consumes a
// ModeOffPath *outcome* returned by a normal Turn: it renders the converse
// answer as an off-path-themed agent bubble, keeps the user in the SAME room
// with the SAME menu (state unchanged), and does NOT enter the async off-path
// input *view mode*.
package tui_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
	tuipkg "kitsoki/internal/tui"
)

// TestOffRampOutcomeRendersAnswerAndPreservesMenu drives a synthetic
// ModeOffPath TurnOutcome (the shape the off-ramp returns: an answer in
// View, unchanged NewState, the room's menu echoed in AllowedIntents)
// through the TUI's outcome-application switch and asserts:
//
//   - the converse answer is rendered into the transcript (off-path bubble);
//   - the model stays in ModeOnPath — it must NOT flip into the persistent
//     ModeOffPath *view mode* the typed /freeform flow uses;
//   - the room's menu survives (state unchanged ⇒ same actions advertised).
func TestOffRampOutcomeRendersAnswerAndPreservesMenu(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// The off-ramp answer the orchestrator already produced via converse.
	const answer = "The chandeliers were installed in 1898, but they have nothing to do with your cloak."

	// Mirror the orchestrator's ModeOffPath contract: Mode=ModeOffPath,
	// View=the answer, NewState=the unchanged resting room (foyer is
	// cloak's root), AllowedIntents=the room's same menu echoed unchanged,
	// no error code/message. (See internal/orchestrator/offpath.go:403.)
	out := &orchestrator.TurnOutcome{
		Mode:           orchestrator.ModeOffPath,
		View:           answer,
		NewState:       app.StatePath("foyer"),
		AllowedIntents: []string{"go", "look"},
	}

	next, _ := tuipkg.TriggerTurnOutcomeMsg(m, out, "tell me about the chandeliers", nil)
	rm, ok := tuipkg.ExtractRootModel(next)
	require.True(t, ok, "expected RootModel after off-ramp turnOutcomeMsg")

	// 1. The free-form answer renders into the transcript.
	transcript := tuipkg.GetTranscriptContent(rm)
	analyzer := tuipkg.NewRenderingAnalyzer(t, transcript)
	analyzer.AssertContains("chandeliers were installed in 1898")

	// 2. The model must stay on-path. A synchronous off-ramp answer is a
	// one-shot reply, NOT an entry into the persistent /freeform view mode
	// (which gates further input behind the async AskOffPath loop). If this
	// regresses, the user is stranded in off-path chat after a single
	// no-match.
	require.Equal(t, tuipkg.ModeOnPath, tuipkg.GetMode(rm),
		"a synchronous off-ramp outcome must NOT enter the async off-path view mode")

	// 3. The state is unchanged, so the room's menu must still be advertised.
	require.Equal(t, app.StatePath("foyer"), rm.CurrentStateForTest(),
		"the off-ramp leaves the resting state unchanged")
	menu := tuipkg.GetMenuPrimaryItems(rm)
	require.NotEmpty(t, menu,
		"the room's menu must survive an off-ramp answer (state unchanged); got: %v", menu)
}
