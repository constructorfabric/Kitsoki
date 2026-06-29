package tui_test

// Integration regressions for the choice-widget surface that lives in
// the surrounding TUI / orchestrator wiring (rather than the widget's
// own keymap). The widget's keyboard / view tests live in
// choice_widget_test.go — this file targets the seams the widget
// reaches THROUGH:
//
//   - viewWithoutChoice (shallow-copy semantics; nil / empty cases).
//   - /input slash command (pendingDraft snapshot/restore).
//   - handleTurnOutcome auto-focus (snapshots draft, opens widget,
//     flips mode to ModeChoosing).
//   - View() prompt suppression while ModeChoosing (the textarea must
//     not surface stale buffer text while the picker owns focus).
//   - /help lists /input in its catalogue.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
	tuipkg "kitsoki/internal/tui"
)

// ─── viewWithoutChoice ───────────────────────────────────────────────────────

// TestViewWithoutChoice locks in the helper's shallow-copy semantics.
// The auto-focus call sites pass the result to AppendAgentBodyTyped /
// AppendSystemTyped, which would render the static picker rows
// underneath the live widget if the choice element slipped through.
// Equally important: the helper must NOT mutate the caller's view —
// the orchestrator's renderer hands the same *app.View to multiple
// consumers (transcript, journal, replay).
func TestViewWithoutChoice(t *testing.T) {
	t.Parallel()

	t.Run("nil_input_returns_nil", func(t *testing.T) {
		require.Nil(t, tuipkg.ViewWithoutChoiceForTest(nil))
	})

	t.Run("empty_elements_returns_copy_with_zero_elements", func(t *testing.T) {
		in := &app.View{Source: "src"}
		out := tuipkg.ViewWithoutChoiceForTest(in)
		require.NotNil(t, out)
		require.Equal(t, 0, len(out.Elements))
		require.Equal(t, "src", out.Source,
			"non-element fields must survive the strip")
	})

	t.Run("strips_choice_elements_preserves_order", func(t *testing.T) {
		in := &app.View{
			Source: "raw",
			Elements: []app.ViewElement{
				{Kind: "prose", Source: "intro"},
				{Kind: "choice", ChoiceMode: "single"},
				{Kind: "heading", Source: "outro"},
			},
		}
		out := tuipkg.ViewWithoutChoiceForTest(in)
		require.NotNil(t, out)
		require.Equal(t, 2, len(out.Elements),
			"choice element must be filtered out")
		require.Equal(t, "prose", out.Elements[0].Kind)
		require.Equal(t, "heading", out.Elements[1].Kind)
		// Non-elements fields must survive.
		require.Equal(t, "raw", out.Source)

		// Shallow-copy contract: mutating the returned Elements slice
		// must NOT affect the caller's view.
		require.Equal(t, 3, len(in.Elements),
			"caller's Elements slice must be unchanged")
		require.Equal(t, "choice", in.Elements[1].Kind,
			"caller's choice element must still be present")
	})
}

// ─── /input slash command ────────────────────────────────────────────────────

// TestSlashInputRestoresPendingDraft pins the happy path. When a
// pendingDraft snapshot exists (the choice widget seized focus and
// stashed the textarea contents), /input must hydrate it back into
// the prompt and clear the snapshot so the next snapshot starts fresh.
func TestSlashInputRestoresPendingDraft(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	tuipkg.SetPendingDraftForTest(&m, "hello world")
	tuipkg.SetPromptValue(&m, "")

	next, _ := tuipkg.HandleSlashCommandForTest(m, "/input")
	require.Equal(t, "hello world", tuipkg.GetPromptValue(next),
		"/input must restore the snapshotted draft into the prompt")
	require.Equal(t, "", tuipkg.GetPendingDraftForTest(next),
		"/input must clear pendingDraft so the next widget can snapshot fresh")
}

// TestSlashInputNoopWhenEmpty pins the negative path. When no draft
// is queued, /input must NOT clobber the textarea (which might have
// fresh user input — surprising overwrite would be worse than the
// no-op it currently is). The system message detail is intentionally
// loose; we just check the prompt wasn't touched.
func TestSlashInputNoopWhenEmpty(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	tuipkg.SetPendingDraftForTest(&m, "")
	tuipkg.SetPromptValue(&m, "do not clobber me")

	next, _ := tuipkg.HandleSlashCommandForTest(m, "/input")
	require.Equal(t, "do not clobber me", tuipkg.GetPromptValue(next),
		"/input with no pendingDraft must leave the prompt untouched")
	require.Equal(t, "", tuipkg.GetPendingDraftForTest(next),
		"pendingDraft must remain empty (no-op path)")
}

// ─── handleTurnOutcome auto-focus ────────────────────────────────────────────

// TestHandleTurnOutcomeAutoFocusSnapshotsDraft drives a turn outcome
// whose typed view carries a choice element through the TUI's
// turnOutcomeMsg handler. The handler must:
//
//   - Snapshot whatever was in the prompt into pendingDraft.
//   - Clear the prompt buffer.
//   - Open the choice widget (IsActive() == true).
//   - Flip the mode to ModeChoosing.
//
// Without the snapshot, the textarea would still hold the user's
// pre-widget composition behind the picker (and View() would
// pass keystrokes to the picker, not the buffer — meaning typed
// letters would be silently absorbed and lost).
func TestHandleTurnOutcomeAutoFocusSnapshotsDraft(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// Pre-fill the prompt to simulate a half-typed user message.
	rm, _ := tuipkg.ExtractRootModel(m)
	tuipkg.SetPromptValue(&rm, "draft text")
	m = rm

	// Synthesize a TurnOutcome carrying a typed view whose only
	// interactive element is a single-mode choice. NewState is set
	// to "foyer" (cloak's root state) so updateMenuFromAllowed /
	// updateLocation can resolve against a real state path.
	choiceView := &app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "Pick one"},
			{
				Kind:         "choice",
				ChoiceMode:   "single",
				ChoicePrompt: "Ready?",
				ChoiceItems: []app.ChoiceItem{
					{Label: "Yes", Intent: "go"},
					{Label: "No", Intent: "look"},
				},
			},
		},
	}
	out := &orchestrator.TurnOutcome{
		Mode:      orchestrator.ModeTransitioned,
		View:      "Pick one",
		TypedView: choiceView,
		NewState:  app.StatePath("foyer"),
	}

	next, _ := tuipkg.TriggerTurnOutcomeMsg(m, out, "begin", nil)
	rm, ok := tuipkg.ExtractRootModel(next)
	require.True(t, ok, "expected RootModel after turnOutcomeMsg")

	require.Equal(t, tuipkg.ModeChoosing, tuipkg.GetMode(rm),
		"choice element in TypedView must auto-flip mode to ModeChoosing")
	require.True(t, tuipkg.ChoiceWidgetIsActive(rm),
		"choice widget must seize focus on the auto-focus path")
	require.Equal(t, "", tuipkg.GetPromptValue(rm),
		"prompt must be cleared so keystrokes route to the widget cleanly")
	require.Equal(t, "draft text", tuipkg.GetPendingDraftForTest(rm),
		"pre-widget prompt content must be snapshotted to pendingDraft "+
			"so /input can restore it later")
}

func TestHandleTurnOutcomeClosesStaleChoiceWhenNextViewHasNoChoice(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	choiceView := &app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "Pick one"},
			{
				Kind:         "choice",
				ChoiceMode:   "single",
				ChoicePrompt: "Ready?",
				ChoiceItems: []app.ChoiceItem{
					{Label: "Stale action", Intent: "go"},
				},
			},
		},
	}
	first := &orchestrator.TurnOutcome{
		Mode:      orchestrator.ModeTransitioned,
		View:      "Pick one",
		TypedView: choiceView,
		NewState:  app.StatePath("foyer"),
	}
	next, _ := tuipkg.TriggerTurnOutcomeMsg(m, first, "begin", nil)
	rm, ok := tuipkg.ExtractRootModel(next)
	require.True(t, ok)
	require.True(t, tuipkg.ChoiceWidgetIsActive(rm))

	noChoiceView := &app.View{
		Elements: []app.ViewElement{
			{Kind: "heading", Source: "No picker here"},
			{Kind: "prose", Source: "Destination body"},
		},
	}
	second := &orchestrator.TurnOutcome{
		Mode:      orchestrator.ModeTransitioned,
		View:      "No picker here\n\nDestination body",
		TypedView: noChoiceView,
		NewState:  app.StatePath("foyer"),
	}
	next, _ = tuipkg.TriggerTurnOutcomeMsg(rm, second, "continue", nil)
	rm, ok = tuipkg.ExtractRootModel(next)
	require.True(t, ok)

	require.Equal(t, tuipkg.ModeOnPath, tuipkg.GetMode(rm),
		"a destination view without a choice must restore normal input mode")
	require.False(t, tuipkg.ChoiceWidgetIsActive(rm),
		"previous room's choice widget must not remain active")
	frame := tuipkg.ComposeFrame(&rm, 100, 30)
	require.NotContains(t, frame.Text, "Stale action",
		"old choice rows must not render over the destination frame")
	require.Contains(t, frame.Text, "Destination body")
}

// ─── View() suppresses textarea during ModeChoosing ──────────────────────────

// TestViewHidesPromptDuringChoosing locks in the View() suppression:
// while the picker owns focus, the textarea is inert and any residual
// buffer content must NOT leak into the rendered output. The
// placeholder line ("(picker active …)") replaces the textarea row.
//
// This guards against a class of bug where a stale buffer value
// remains visible behind the picker — confusing the user into thinking
// their typed letters are landing in the textarea when they are
// actually being absorbed by the widget keymap.
func TestViewHidesPromptDuringChoosing(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, _ := tuipkg.ExtractRootModel(m)
	rm = tuipkg.ResizeRootModel(rm, 100, 32)

	tuipkg.SetModeForTest(&rm, tuipkg.ModeChoosing)
	tuipkg.SetPromptValue(&rm, "buffered text")

	rendered := rm.View()
	require.Contains(t, rendered, "picker active",
		"ModeChoosing must show the picker-active placeholder line; got:\n%s",
		rendered)
	require.NotContains(t, rendered, "buffered text",
		"ModeChoosing must NOT leak the stale textarea buffer into View(); got:\n%s",
		rendered)
}

// ─── /help lists /input ──────────────────────────────────────────────────────

// TestHelpListsInputCommand pins that the help catalogue advertises
// /input. Without the entry users would not discover the restore
// affordance, and the snapshot-on-auto-focus behaviour would feel like
// a silent data loss bug.
func TestHelpListsInputCommand(t *testing.T) {
	t.Parallel()

	orch, sid := setupCloak(t)
	m, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	body, _, _ := tuipkg.HelpCommand{}.Run(m, nil)
	require.True(t, strings.Contains(body, "/input"),
		"/help must list /input so users discover the draft-restore affordance; got:\n%s",
		body)
}
