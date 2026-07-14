// choice_widget_test.go — keyboard-driven tests for the Phase C
// inline interactive choice widget.
//
// We exercise the widget directly via the TestChoiceWidget wrapper
// (export_test.go) rather than spinning up a full RootModel + teatest
// pipeline. The widget is a self-contained Bubble Tea-shaped model;
// the integration with RootModel.updateChoosing is covered separately
// by the broader TUI tests when needed. Keeping the unit tests tight
// (synchronous Update / SendKey) keeps each case well under the fast-
// test budget.

package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/tui"
)

// ---- single-mode tests -------------------------------------------------------

// makeSingleElement constructs a single-mode choice ViewElement with
// two trivial items. Used as the baseline shape for single-mode tests
// — variants overlay When guards / Param structs as needed.
func makeSingleElement() app.ViewElement {
	return app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "single",
		ChoicePrompt: "Choose a profession",
		ChoiceItems: []app.ChoiceItem{
			{
				Label:  "Banker",
				Intent: "pick_profession",
				Slots:  map[string]any{"profession": "banker"},
			},
			{
				Label:  "Carpenter",
				Intent: "pick_profession",
				Slots:  map[string]any{"profession": "carpenter"},
			},
		},
	}
}

func TestChoiceSingleEnterDispatchesIntent(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeSingleElement(), nil))
	require.True(t, w.IsActive())
	require.Equal(t, 2, w.ItemCount())
	require.Equal(t, 0, w.Cursor())

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit, "Enter on a single item should commit")
	require.False(t, commit.Cancel, "Enter is not a cancellation")
	require.Equal(t, "pick_profession", commit.Intent)
	require.Equal(t, "banker", commit.Slots["profession"])
}

func TestChoiceSingleArrowsMoveCursor(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeSingleElement(), nil))

	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	require.Equal(t, 1, w.Cursor(), "Down should advance the cursor")

	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	require.Equal(t, 1, w.Cursor(), "Down at the end should clamp")

	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyUp}))
	require.Equal(t, 0, w.Cursor(), "Up should retreat the cursor")

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "banker", commit.Slots["profession"])
}

func TestChoiceSingleEscCancels(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeSingleElement(), nil))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, commit)
	require.True(t, commit.Cancel)
	require.False(t, commit.ToChat, "Esc is a pure cancel, not a chat off-ramp")
}

func TestChoiceSinglePrintableIsIgnored(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeSingleElement(), nil))

	// Printable letters used to dismiss the picker via a "letter →
	// chat" defocus shortcut, but that turned out to be a footgun (a
	// stray keystroke would dismiss). Letters are now ignored; the
	// user dismisses with Tab (ToChat) or Esc.
	commit := w.SendRune('h')
	require.Nil(t, commit, "printable letters no longer dismiss the picker")
	require.True(t, w.IsActive())
}

func TestChoiceSingleTabRequestsChat(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeSingleElement(), nil))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyTab})
	require.NotNil(t, commit)
	require.True(t, commit.Cancel, "Tab cancels the widget")
	require.True(t, commit.ToChat, "Tab signals an explicit off-ramp to chat")
}

// ---- single + param ---------------------------------------------------------

func TestChoiceSingleParamEntersParamMode(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{
			{
				Label:  "Generate names from a theme",
				Intent: "generate_names",
				Param: &app.ChoiceParam{
					Slot: "theme",
					Type: "string",
				},
			},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	// Ergonomic shortcut: when the widget has exactly one item AND
	// that item has a param, Open auto-enters paramMode so the user
	// doesn't have to press Enter on the only available row first.
	require.True(t, w.ParamMode(), "single-item-with-param widget should auto-enter paramMode at Open")

	// Type the slot value, then Enter commits.
	for _, r := range "norse mythology" {
		if r == ' ' {
			require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
			continue
		}
		require.Nil(t, w.SendRune(r))
	}
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "generate_names", commit.Intent)
	require.Equal(t, "norse mythology", commit.Slots["theme"])
}

func TestChoiceSingleParamRequiredEmptyShowsError(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{
			{
				Label:  "Pick theme",
				Intent: "generate_names",
				Param: &app.ChoiceParam{
					Slot:     "theme",
					Type:     "string",
					Required: true,
				},
			},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyEnter}))
	require.True(t, w.ParamMode())

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit, "empty required param keeps widget open")
	require.True(t, w.IsActive())
	require.Contains(t, w.ErrMsg(), "required")
}

// ---- multi mode ------------------------------------------------------------

func makeMultiElement() app.ViewElement {
	return app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "multi",
		ChoicePrompt: "Select symptoms",
		ChoiceIntent: "report_symptoms",
		ChoiceSlot:   "symptoms",
		ChoiceItems: []app.ChoiceItem{
			{Value: "fever", Label: "Fever"},
			{Value: "cough", Label: "Cough"},
			{Value: "fatigue", Label: "Fatigue"},
		},
	}
}

func TestChoiceMultiSpaceTogglesEnterCommits(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeMultiElement(), nil))

	// Select item 0 (fever) and item 2 (fatigue).
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "report_symptoms", commit.Intent)
	values, ok := commit.Slots["symptoms"].([]string)
	require.True(t, ok, "slot should be []string")
	require.Equal(t, []string{"fever", "fatigue"}, values)
}

func TestChoiceMultiMinViolationShowsError(t *testing.T) {
	el := makeMultiElement()
	el.ChoiceMin = 2
	el.ChoiceMinSet = true
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	// Select only one item, then try to submit.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit, "under-min should keep widget open")
	require.Contains(t, w.ErrMsg(), "at least 2")
}

func TestChoiceMultiMaxViolationShowsError(t *testing.T) {
	el := makeMultiElement()
	el.ChoiceMax = 1
	el.ChoiceMaxSet = true
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit)
	require.Contains(t, w.ErrMsg(), "at most 1")
}

// ---- form mode --------------------------------------------------------------

func makeFormElement() app.ViewElement {
	return app.ViewElement{
		Kind:           "choice",
		ChoiceMode:     "form",
		ChoiceIntent:   "propose_purchase",
		ChoiceTemplate: "Buy {items} for ${total_cost}.",
		ChoiceFields: []app.ChoiceField{
			{Name: "items", Type: "string", Required: true},
			{Name: "total_cost", Type: "int", Default: 0},
		},
	}
}

func TestChoiceFormTabCyclesAndEnterCommits(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeFormElement(), nil))
	require.Equal(t, 0, w.FieldCursor())

	// Type the items value (skipping spaces / special chars for brevity).
	for _, r := range "oxen=4" {
		require.Nil(t, w.SendRune(r))
	}

	// Tab to the next field, type the total_cost.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyTab}))
	require.Equal(t, 1, w.FieldCursor())
	// Clear the default first by backspacing.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyBackspace}))
	for _, r := range "1200" {
		require.Nil(t, w.SendRune(r))
	}

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "propose_purchase", commit.Intent)
	require.Equal(t, "oxen=4", commit.Slots["items"])
	require.Equal(t, 1200, commit.Slots["total_cost"])
}

func TestChoiceFormRequiredEmptyShowsError(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeFormElement(), nil))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit, "empty required field keeps widget open")
	require.Contains(t, w.ErrMsg(), "items")
	require.Contains(t, w.ErrMsg(), "required")
}

func TestChoiceFormIntCoercionRejectsNonInt(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeFormElement(), nil))

	// Fill items.
	for _, r := range "x" {
		require.Nil(t, w.SendRune(r))
	}
	// Tab to total_cost, replace default with non-int.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyTab}))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyBackspace}))
	for _, r := range "abc" {
		require.Nil(t, w.SendRune(r))
	}

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit)
	require.Contains(t, w.ErrMsg(), "total_cost")
}

func TestChoiceFormEscCancels(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeFormElement(), nil))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, commit)
	require.True(t, commit.Cancel)
}

// ---- When-filtering --------------------------------------------------------

func TestChoiceWhenFalseItemFilteredOut(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{
			{Label: "Always", Intent: "pick_a", When: "true"},
			{Label: "Hidden", Intent: "pick_b", When: "world.flag == 'on'"},
			{Label: "Shown", Intent: "pick_c", When: "world.flag == 'off'"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, map[string]any{"flag": "off"}))
	require.Equal(t, 2, w.ItemCount(),
		"Hidden item with when=world.flag=='on' should be filtered out")

	// The second visible item must be the 'Shown' row; Enter on it
	// fires pick_c.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "pick_c", commit.Intent)
}

func TestChoiceWhenMissingNestedPathHidesItem(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{
			{
				Label:  "hidden route",
				Intent: "take_route",
				When:   "(world.landing_note.route.intent ?? '') != ''",
			},
			{Label: "visible", Intent: "go_visible"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, map[string]any{"landing_note": nil}))
	require.Equal(t, 1, w.ItemCount(), "missing nested optional path should hide the guarded row, not fail Open")
	require.NotContains(t, ansi.Strip(w.View(80)), "hidden route")
	require.Contains(t, ansi.Strip(w.View(80)), "visible")
}

// ---- View rendering smoke --------------------------------------------------

func TestChoiceViewRendersCursorAndCheckbox(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeMultiElement(), nil))
	w.SendKey(tea.KeyMsg{Type: tea.KeySpace})
	out := w.View(80)
	// Cursor + checkbox markers are present.
	require.Contains(t, out, "▸")
	require.Contains(t, out, "[x]")
	require.Contains(t, out, "[ ]")
	// Footer hint is rendered.
	require.True(t, strings.Contains(out, "Space") || strings.Contains(out, "submit"),
		"multi-mode footer should mention Space/submit")
}

func TestChoiceViewLinesFitWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		el   app.ViewElement
	}{
		{
			name: "single",
			el: app.ViewElement{
				Kind:       "choice",
				ChoiceMode: "single",
				ChoiceItems: []app.ChoiceItem{
					{
						Label:  "prd",
						Hint:   "author a PRD, then carry it into design without wrapping the live row",
						Intent: "go_prd",
					},
					{
						Label:  "very long action label that should not force wrapping",
						Hint:   "a long explanatory hint that must be clipped",
						Intent: "go_long",
					},
				},
			},
		},
		{
			name: "multi",
			el: app.ViewElement{
				Kind:         "choice",
				ChoiceMode:   "multi",
				ChoiceIntent: "choose",
				ChoiceSlot:   "items",
				ChoiceItems: []app.ChoiceItem{
					{Label: "coverage", Value: "coverage", Hint: "add deterministic regression coverage before shipping"},
					{Label: "docs", Value: "docs", Hint: "update the narrative docs after the implementation lands"},
				},
			},
		},
		{
			name: "form",
			el: app.ViewElement{
				Kind:         "choice",
				ChoiceMode:   "form",
				ChoiceIntent: "submit",
				ChoiceFields: []app.ChoiceField{
					{Name: "summary", Type: "string", Placeholder: "short summary", Hint: "this hint is intentionally too long for a narrow terminal"},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width = 40
			w := tui.NewTestChoiceWidget()
			require.NoError(t, w.OpenChoice(tc.el, nil))
			out := w.View(width)
			for i, line := range strings.Split(out, "\n") {
				require.LessOrEqualf(t, ansi.StringWidth(line), width,
					"line %d exceeds width %d (visible=%d): %q\n%s",
					i, width, ansi.StringWidth(line), ansi.Strip(line), ansi.Strip(out))
			}
		})
	}
}

func TestChoiceClosedViewEmpty(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.Equal(t, "", w.View(80), "inactive widget renders empty")
}

// ---- Pinned regressions: surfaced via interactive smoke testing ----------
//
// The behaviours below are real bugs we tripped over and corrected today;
// the tests here pin the corrected widget behaviour so a future change
// can't silently regress it. Each case maps to a numbered behaviour in
// the regression checklist for traceability.

// 1. Auto-paramMode at Open for single-item-with-param --------------------

func TestChoiceSingleItemWithParamAutoEntersParamMode(t *testing.T) {
	t.Run("string param: paramMode true, buffer empty", func(t *testing.T) {
		el := app.ViewElement{
			Kind:       "choice",
			ChoiceMode: "single",
			ChoiceItems: []app.ChoiceItem{{
				Label:  "Generate names",
				Intent: "generate_names",
				Param: &app.ChoiceParam{
					Slot: "theme",
					Type: "string",
				},
			}},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, nil))
		require.True(t, w.ParamMode(), "single-item-with-param auto-enters paramMode")
		require.Equal(t, "", w.ParamBuf(),
			"non-enum param should start with an empty buffer (placeholder shows hint)")
	})

	t.Run("enum param: paramMode true, buffer seeded with Values[0]", func(t *testing.T) {
		el := app.ViewElement{
			Kind:       "choice",
			ChoiceMode: "single",
			ChoiceItems: []app.ChoiceItem{{
				Label:  "Pick mood",
				Intent: "set_mood",
				Param: &app.ChoiceParam{
					Slot:   "mood",
					Type:   "enum",
					Values: []string{"calm", "tense", "elated"},
				},
			}},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, nil))
		require.True(t, w.ParamMode())
		require.Equal(t, "calm", w.ParamBuf(),
			"enum param should seed the buffer with Values[0] so Space-cycle has a starting point")
	})

	t.Run("two items (one with param): does NOT auto-enter paramMode", func(t *testing.T) {
		el := app.ViewElement{
			Kind:       "choice",
			ChoiceMode: "single",
			ChoiceItems: []app.ChoiceItem{
				{
					Label:  "Just pick",
					Intent: "just_pick",
				},
				{
					Label:  "Pick with theme",
					Intent: "pick_with_theme",
					Param: &app.ChoiceParam{
						Slot: "theme",
						Type: "string",
					},
				},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, nil))
		require.False(t, w.ParamMode(),
			"with >1 item, paramMode must stay false at Open even when one item has a param")
	})
}

// 2. Enum / bool form fields ignore printable letters --------------------

func TestChoiceFormEnumIgnoresPrintable(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "pick_mood",
		ChoiceFields: []app.ChoiceField{
			{Name: "mood", Type: "enum", Values: []string{"calm", "tense"}, Default: "calm"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	before := w.FieldBuffers()
	require.Equal(t, []string{"calm"}, before)

	for _, r := range []rune{'a', 'b', 'c', 'z'} {
		commit := w.SendRune(r)
		require.Nil(t, commit, "enum field must stay open on printable %q", r)
	}
	require.Equal(t, before, w.FieldBuffers(),
		"enum field buffer must be unchanged after printable runes")
	require.True(t, w.IsActive())
}

func TestChoiceFormBoolIgnoresPrintable(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "confirm",
		ChoiceFields: []app.ChoiceField{
			{Name: "agree", Type: "bool", Default: "false"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	before := w.FieldBuffers()
	require.Equal(t, []string{"false"}, before)

	for _, r := range []rune{'a', 't', 'r', 'u', 'e'} {
		commit := w.SendRune(r)
		require.Nil(t, commit, "bool field must stay open on printable %q", r)
	}
	require.Equal(t, before, w.FieldBuffers(),
		"bool field buffer must be unchanged after printable runes")
	require.True(t, w.IsActive())
}

func TestChoiceFormStringAcceptsPrintable(t *testing.T) {
	// Negative case for #2 — we don't over-block. A string field still
	// accepts printable runes (the regression we pinned only locks
	// enum/bool out, not string).
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "named",
		ChoiceFields: []app.ChoiceField{
			{Name: "name", Type: "string"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	for _, r := range "abc" {
		require.Nil(t, w.SendRune(r))
	}
	require.Equal(t, []string{"abc"}, w.FieldBuffers(),
		"string field must still accept printable letters")
}

// 3. Bool field Space toggles true/false ---------------------------------

func TestChoiceFormBoolSpaceTogglesTrueFalse(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "confirm",
		ChoiceFields: []app.ChoiceField{
			{Name: "agree", Type: "bool"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	require.Equal(t, []string{""}, w.FieldBuffers(),
		"empty buffer is the initial state when no default is set")

	// Empty buffer (doesn't lowercase to "true") → "true"
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Equal(t, []string{"true"}, w.FieldBuffers())

	// "true" → "false"
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Equal(t, []string{"false"}, w.FieldBuffers())

	// "false" → "true" (anything-not-true)
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Equal(t, []string{"true"}, w.FieldBuffers())
}

// 4. Param-mode enum ignores printable letters ---------------------------

func TestChoiceParamEnumIgnoresPrintable(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{{
			Label:  "Pick mood",
			Intent: "set_mood",
			Param: &app.ChoiceParam{
				Slot:   "mood",
				Type:   "enum",
				Values: []string{"calm", "tense", "elated"},
			},
		}},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	require.True(t, w.ParamMode())
	require.Equal(t, "calm", w.ParamBuf())

	// Printable letters must not mutate paramBuf for an enum param.
	for _, r := range []rune{'a', 'b', 'c', 't', 'e'} {
		require.Nil(t, w.SendRune(r))
	}
	require.Equal(t, "calm", w.ParamBuf(),
		"enum param buffer must be unchanged after printable runes")

	// Space still cycles (sanity).
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	require.Equal(t, "tense", w.ParamBuf())

	// Enter commits the current (cycled) value.
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, "tense", commit.Slots["mood"])
}

// 5. No letter-defocus footgun — multi mode -------------------------------

func TestChoiceMultiPrintableIsIgnored(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeMultiElement(), nil))

	commit := w.SendRune('h')
	require.Nil(t, commit, "printable letters do not dismiss the multi picker")
	require.True(t, w.IsActive())

	// Subsequent toggles still work — the widget didn't enter a wedged state.
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeySpace}))
	out := w.View(80)
	require.Contains(t, out, "[x]", "Space after a stray letter still toggles checkbox")
}

// 6. Tab is the off-ramp (ToChat signal) ---------------------------------

func TestChoiceMultiTabRequestsChat(t *testing.T) {
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeMultiElement(), nil))

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyTab})
	require.NotNil(t, commit)
	require.True(t, commit.Cancel, "Tab cancels the multi widget")
	require.True(t, commit.ToChat, "Tab signals an off-ramp to chat (not just a cancel)")
}

func TestChoiceFormTabDoesNotCommit(t *testing.T) {
	// Negative case for #6 — Tab in form mode CYCLES fields rather than
	// emitting a commit. The off-ramp gesture is only wired in single/multi.
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(makeFormElement(), nil))
	require.Equal(t, 0, w.FieldCursor())

	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyTab})
	require.Nil(t, commit, "Tab in form mode must NOT return a commit — it cycles fields")
	require.Equal(t, 1, w.FieldCursor(), "Tab in form mode moves to the next editable field")
	require.True(t, w.IsActive())
}

// 7. Readonly form fields ARE dispatched as slots ------------------------

func TestChoiceFormReadonlyFieldDispatchedInSlots(t *testing.T) {
	t.Run("readonly with expr-evaluated buffer is included", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "name", Type: "string", Required: true},
				{
					Name:     "computed",
					Type:     "string",
					Readonly: true,
					Expr:     "world.derived",
				},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, map[string]any{"derived": "auto-value"}))

		// Editable field is focused; readonly was skipped by firstEditable.
		require.Equal(t, 0, w.FieldCursor())
		// Type into the editable field.
		for _, r := range "alice" {
			require.Nil(t, w.SendRune(r))
		}
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
		require.NotNil(t, commit)
		require.Equal(t, "submit", commit.Intent)
		require.Equal(t, "alice", commit.Slots["name"])
		require.Equal(t, "auto-value", commit.Slots["computed"],
			"readonly field with a non-empty evaluated buffer must appear in the slot map")
	})

	t.Run("readonly with empty expr eval is skipped", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "name", Type: "string", Required: true},
				{
					Name:     "computed",
					Type:     "string",
					Readonly: true,
					Expr:     "world.derived",
				},
			},
		}
		w := tui.NewTestChoiceWidget()
		// world.derived is unset → defaultFormBuffer falls back to "".
		require.NoError(t, w.OpenChoice(el, map[string]any{}))

		for _, r := range "alice" {
			require.Nil(t, w.SendRune(r))
		}
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
		require.NotNil(t, commit)
		require.Equal(t, "alice", commit.Slots["name"])
		_, present := commit.Slots["computed"]
		require.False(t, present,
			"readonly field with an empty computed buffer must NOT appear in the slot map")
	})
}

// 8. All-readonly form renders the "computed" hint -----------------------

func TestChoiceFormAllReadonlyShowsHint(t *testing.T) {
	const hint = "(all fields are computed — press Enter to submit)"

	t.Run("all readonly → hint present", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "a", Type: "string", Readonly: true, Expr: "world.a"},
				{Name: "b", Type: "string", Readonly: true, Expr: "world.b"},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, map[string]any{"a": "x", "b": "y"}))
		out := w.View(80)
		require.Contains(t, out, hint,
			"all-readonly form must surface the hint so it's not mistaken for stuck UI")
	})

	t.Run("mixed editable + readonly → hint absent", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "a", Type: "string"},
				{Name: "b", Type: "string", Readonly: true, Expr: "world.b"},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, map[string]any{"b": "y"}))
		out := w.View(80)
		require.NotContains(t, out, hint,
			"a form with any editable field must not show the all-readonly hint")
	})
}

// 9. Live red on invalid type input — observable signal ------------------

func TestChoiceFormInvalidIntRendersRed(t *testing.T) {
	// choiceErrorStyle = Foreground(lipgloss.Color("9")) — that's the
	// standard "red" ANSI SGR. lipgloss renders 8-colour foregrounds as
	// `\x1b[91m` (bright) or `\x1b[31m` (regular). We assert by comparing
	// the rendered output of a focused int field with an invalid buffer
	// vs a focused int field with a valid buffer — only the invalid one
	// must contain the red foreground SGR.
	//
	// `go test` looks non-TTY, so lipgloss strips colours by default;
	// forceTrueColor scopes a TrueColor profile to this test so the SGR
	// codes are actually present in the output (mirrors the pattern in
	// view_chrome_test.go).
	forceTrueColor(t)

	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "count", Type: "int"},
		},
	}

	// Invalid buffer rendering.
	wInvalid := tui.NewTestChoiceWidget()
	require.NoError(t, wInvalid.OpenChoice(el, nil))
	for _, r := range "abc" {
		require.Nil(t, wInvalid.SendRune(r))
	}
	invalidOut := wInvalid.View(80)

	// Valid buffer rendering.
	wValid := tui.NewTestChoiceWidget()
	require.NoError(t, wValid.OpenChoice(el, nil))
	for _, r := range "42" {
		require.Nil(t, wValid.SendRune(r))
	}
	validOut := wValid.View(80)

	// The two renderings differ on the SGR used for the buffer column —
	// invalid uses the red foreground, valid uses underline only.
	require.NotEqual(t, invalidOut, validOut,
		"invalid int input must render differently from valid input")
	// Red SGRs: 91 (bright red) or 31 (regular red). lipgloss may emit
	// them combined with other attributes (e.g. `\x1b[4;91;4m`), so we
	// match the parameter substring anywhere inside an SGR sequence
	// rather than a fixed `\x1b[91m` form.
	hasRed := containsSGRParam(invalidOut, "91") || containsSGRParam(invalidOut, "31")
	require.True(t, hasRed,
		"invalid int buffer must render with a red foreground SGR; got: %q", invalidOut)
	// And the valid rendering must NOT contain those red codes (it might
	// contain other ANSI like underline; we only assert red absence).
	noRedOnValid := !containsSGRParam(validOut, "91") && !containsSGRParam(validOut, "31")
	require.True(t, noRedOnValid,
		"valid int buffer must NOT render with a red foreground SGR; got: %q", validOut)
}

// containsSGRParam reports whether s contains an ANSI SGR escape whose
// parameter list includes the literal param. lipgloss combines styles
// into one sequence like `\x1b[4;91;4m`, so a fixed `\x1b[91m` substring
// search would miss the red. We scan for ESC `[`, capture everything up
// to the terminating `m`, then look for ;param; / [param; / ;param$.
func containsSGRParam(s, param string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		end := i + 2
		for end < len(s) && s[end] != 'm' && s[end] != 0x1b {
			end++
		}
		if end >= len(s) || s[end] != 'm' {
			continue
		}
		params := s[i+2 : end]
		// Param boundaries are ; or string ends.
		bounded := ";" + params + ";"
		if strings.Contains(bounded, ";"+param+";") {
			return true
		}
	}
	return false
}

// 9b. Form-mode min/max bounds — block commit + render red --------------

// TestChoiceFormIntMinViolation pins the contract documented in
// docs/stories/choice-widget.md and docs/stories/story-style.md: an out-of-bounds
// int/float field must (a) block the form submit, and (b) render its
// buffer in the same red error style used for "invalid type" inputs.
// Mirrors the multi-mode min/max enforcement at coerceFormSlots
// (which uses the same red SGR as the live invalid-type check via
// coerceFieldValue).
func TestChoiceFormIntMinViolation(t *testing.T) {
	forceTrueColor(t)

	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "count", Type: "int", Min: 1, Max: 10},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	// Type "0" — below min:1.
	require.Nil(t, w.SendRune('0'))

	// (a) Enter must NOT commit; the widget stays open with an error.
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit, "out-of-bounds int must block submit")
	require.True(t, w.IsActive())
	require.Contains(t, w.ErrMsg(), "count", "error must name the offending field")
	require.Contains(t, w.ErrMsg(), "min", "error must mention the violated bound")

	// (b) View must render the buffer in the red error style — same
	// SGR (foreground 9 → 91/31) as the live invalid-type case.
	out := w.View(80)
	hasRed := containsSGRParam(out, "91") || containsSGRParam(out, "31")
	require.True(t, hasRed,
		"out-of-bounds int buffer must render with a red foreground SGR; got: %q", out)
}

func TestChoiceFormIntMaxViolation(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "count", Type: "int", Min: 1, Max: 10},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	// Type "99" — above max:10.
	for _, r := range "99" {
		require.Nil(t, w.SendRune(r))
	}
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, commit, "out-of-bounds int must block submit")
	require.Contains(t, w.ErrMsg(), "max")
}

func TestChoiceFormFloatBoundsEnforced(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			// Bounds arrive as float64 from the YAML decoder.
			{Name: "ratio", Type: "float", Min: 0.0, Max: 1.0},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	for _, r := range "1.5" {
		require.Nil(t, w.SendRune(r))
	}
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyEnter}),
		"float above max must block submit")
	require.Contains(t, w.ErrMsg(), "max")
}

func TestChoiceFormBoundsInRangeCommits(t *testing.T) {
	// Negative case — when the value is within bounds, the form
	// commits as before. Pins that the new bounds enforcement isn't
	// over-firing on valid input.
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "count", Type: "int", Min: 1, Max: 10},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	for _, r := range "5" {
		require.Nil(t, w.SendRune(r))
	}
	commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, commit)
	require.Equal(t, 5, commit.Slots["count"])
}

func TestChoiceFormBoundsAsStringsTemplated(t *testing.T) {
	// Bounds can arrive as strings (e.g. `min: "{{ world.lower }}"`
	// after the YAML decoder + pongo expansion). The widget normalises
	// them to numeric at Open; if they don't parse, the bound is
	// silently dropped (no enforcement) — matches the "unset bound"
	// branch in choiceBoundToFloat.
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "count", Type: "int", Min: "1", Max: "10"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	require.Nil(t, w.SendRune('0'))
	require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyEnter}),
		"string-typed bound must still enforce — '0' is below '1'")
	require.Contains(t, w.ErrMsg(), "min")
}

// 10. Placeholder renders as «...» plain text ---------------------------

func TestChoiceFormPlaceholderRendersAsBracketedPlainText(t *testing.T) {
	el := app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "form",
		ChoiceIntent: "submit",
		ChoiceFields: []app.ChoiceField{
			{Name: "name", Type: "string", Placeholder: "type a name"},
		},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))

	out := w.View(80)
	require.Contains(t, out, "«type a name»",
		"empty form field with a placeholder must render as «<placeholder>» literal")
	// And there must NOT be ANSI SGR wrapping the « or » directly.
	// We assert the bracketed literal appears with NO escape character
	// immediately before the « or immediately after the ».
	idx := strings.Index(out, "«type a name»")
	require.GreaterOrEqual(t, idx, 0)
	if idx > 0 {
		require.NotEqual(t, byte(0x1b), out[idx-1],
			"« must not be preceded by an ANSI escape — placeholder is plain text")
	}
	tail := idx + len("«type a name»")
	if tail < len(out) {
		require.NotEqual(t, byte('m'), out[tail],
			"» must not be followed by an SGR terminator")
	}
}

func TestChoiceParamPlaceholderRendersAsBracketedPlainText(t *testing.T) {
	el := app.ViewElement{
		Kind:       "choice",
		ChoiceMode: "single",
		ChoiceItems: []app.ChoiceItem{{
			Label:  "Pick theme",
			Intent: "generate_names",
			Param: &app.ChoiceParam{
				Slot:        "theme",
				Type:        "string",
				Placeholder: "norse mythology",
			},
		}},
	}
	w := tui.NewTestChoiceWidget()
	require.NoError(t, w.OpenChoice(el, nil))
	require.True(t, w.ParamMode())
	require.Equal(t, "", w.ParamBuf(),
		"non-enum param starts empty so the placeholder branch fires")

	out := w.View(80)
	require.Contains(t, out, "«norse mythology»",
		"empty param buffer with a placeholder must render as «<placeholder>» literal")
}

// 11. Up/Down uniformly cycles form fields (no numeric stepper trap) ----

func TestChoiceFormUpDownAlwaysCyclesFields(t *testing.T) {
	t.Run("int field: Up/Down cycles, does not step buffer", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "name", Type: "string"},
				{Name: "count", Type: "int", Default: 5},
				{Name: "tag", Type: "string"},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, nil))
		require.Equal(t, 0, w.FieldCursor())
		require.Equal(t, []string{"", "5", ""}, w.FieldBuffers())

		// Down → count (int).
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
		require.Equal(t, 1, w.FieldCursor())
		// Down again — must continue past the int field, not increment "5".
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
		require.Equal(t, 2, w.FieldCursor(),
			"Down on an int field cycles to the next field; it must not step the buffer")
		require.Equal(t, []string{"", "5", ""}, w.FieldBuffers(),
			"int field buffer must be unchanged after Down")

		// Up from tag → count. Up again must move to name, not decrement.
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyUp}))
		require.Equal(t, 1, w.FieldCursor())
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyUp}))
		require.Equal(t, 0, w.FieldCursor(),
			"Up on an int field cycles to the previous field; it must not step the buffer")
		require.Equal(t, []string{"", "5", ""}, w.FieldBuffers(),
			"int field buffer must be unchanged after Up")
	})

	t.Run("float field: Up/Down cycles, does not step buffer", func(t *testing.T) {
		el := app.ViewElement{
			Kind:         "choice",
			ChoiceMode:   "form",
			ChoiceIntent: "submit",
			ChoiceFields: []app.ChoiceField{
				{Name: "name", Type: "string"},
				{Name: "ratio", Type: "float", Default: "0.5"},
				{Name: "tag", Type: "string"},
			},
		}
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(el, nil))
		require.Equal(t, []string{"", "0.5", ""}, w.FieldBuffers())

		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
		require.Equal(t, 1, w.FieldCursor())
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyDown}))
		require.Equal(t, 2, w.FieldCursor())
		require.Equal(t, []string{"", "0.5", ""}, w.FieldBuffers(),
			"float field buffer must be unchanged after Down")

		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyUp}))
		require.Nil(t, w.SendKey(tea.KeyMsg{Type: tea.KeyUp}))
		require.Equal(t, 0, w.FieldCursor())
		require.Equal(t, []string{"", "0.5", ""}, w.FieldBuffers(),
			"float field buffer must be unchanged after Up")
	})
}

// 12. Tab signals chat off-ramp; Esc is a pure cancel --------------------

func TestChoiceCommitOffRampVsCancel(t *testing.T) {
	t.Run("Tab single → ToChat=true", func(t *testing.T) {
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(makeSingleElement(), nil))
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyTab})
		require.NotNil(t, commit)
		require.True(t, commit.Cancel)
		require.True(t, commit.ToChat,
			"Tab off-ramp signals via Cancel+ToChat so the caller can distinguish "+
				"it from Esc (which leaves ToChat false)")
	})

	t.Run("Tab multi → ToChat=true", func(t *testing.T) {
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(makeMultiElement(), nil))
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyTab})
		require.NotNil(t, commit)
		require.True(t, commit.Cancel)
		require.True(t, commit.ToChat)
	})

	t.Run("Esc single → ToChat=false", func(t *testing.T) {
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(makeSingleElement(), nil))
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEsc})
		require.NotNil(t, commit)
		require.True(t, commit.Cancel)
		require.False(t, commit.ToChat, "Esc is a pure cancel — not an off-ramp")
	})

	t.Run("Esc multi → ToChat=false", func(t *testing.T) {
		w := tui.NewTestChoiceWidget()
		require.NoError(t, w.OpenChoice(makeMultiElement(), nil))
		commit := w.SendKey(tea.KeyMsg{Type: tea.KeyEsc})
		require.NotNil(t, commit)
		require.True(t, commit.Cancel)
		require.False(t, commit.ToChat)
	})
}
