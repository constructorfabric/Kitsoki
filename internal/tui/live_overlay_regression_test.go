package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/host"
	"kitsoki/internal/metamode"
)

func TestStorySelectorLongListStaysInsideLiveChromeBudget(t *testing.T) {
	const width, height = 48, 14
	stories := make([]StoryOption, 30)
	for i := range stories {
		stories[i] = StoryOption{
			Path:  fmt.Sprintf("/very/long/story/path/%02d/app.yaml", i+1),
			AppID: fmt.Sprintf("story-%02d", i+1),
			Title: fmt.Sprintf("Story %02d with a deliberately long title", i+1),
		}
	}

	m := NewRootModel(testCloakOrchestrator(t), app.SessionID("story-window"), "", "",
		WithStorySelector(stories, nil, nil))
	m.width, m.height = width, height
	m = m.resize()
	m.mode = ModeStorySelector
	m.storySelector.Open("")

	first := m.View()
	assertLiveChromeFits(t, first, width, height)
	require.Contains(t, ansi.Strip(first), "[1] Story 01")
	require.Contains(t, ansi.Strip(first), "↓")

	for i := 0; i < len(stories)+5; i++ {
		model, _ := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyDown})
		m = model.(RootModel)
	}
	last := m.View()
	assertLiveChromeFits(t, last, width, height)
	require.Contains(t, ansi.Strip(last), "[30] Story 30")
	require.Contains(t, ansi.Strip(last), "↑")
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(last),
		"story-selector row count must remain stable as its window follows selection")
}

func TestSessionsPanelLongListStaysInsideLiveChromeBudget(t *testing.T) {
	const width, height = 52, 15
	listings := make([]metamode.ChatListing, 30)
	for i := range listings {
		listings[i] = metamode.ChatListing{
			ID:               fmt.Sprintf("chat-%02d-very-long-identifier", i+1),
			ModeName:         "story.improve",
			ScopeKey:         "foyer",
			Title:            fmt.Sprintf("Session %02d", i+1),
			UpdatedAt:        time.Date(2026, 7, 12, 12, i, 0, 0, time.UTC),
			FirstUserMessage: strings.Repeat(fmt.Sprintf("long preview %02d ", i+1), 5),
		}
	}

	m := NewRootModel(testCloakOrchestrator(t), app.SessionID("sessions-window"), "", "")
	m.width, m.height = width, height
	m = m.resize()
	m.mode = ModeMetaSessions
	m.sessionsPanel.Open(listings)

	first := m.View()
	assertLiveChromeFits(t, first, width, height)
	require.Contains(t, ansi.Strip(first), "meta sessions")
	require.Contains(t, ansi.Strip(first), "↓")

	for i := 0; i < len(listings)+5; i++ {
		model, _ := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyDown})
		m = model.(RootModel)
	}
	last := m.View()
	assertLiveChromeFits(t, last, width, height)
	require.Contains(t, ansi.Strip(last), "chat-30-")
	require.Contains(t, ansi.Strip(last), "↑")
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(last),
		"sessions-panel row count must remain stable as its window follows selection")
}

func TestChoiceOverlayUsesExactTinyTerminalBudget(t *testing.T) {
	const width, height = 42, 7
	m := NewRootModel(testCloakOrchestrator(t), app.SessionID("choice-budget"), "", "")
	m.width, m.height = width, height
	m = m.resize()
	m.mode = ModeChoosing
	err := m.choice.Open(app.ViewElement{
		Kind:         "choice",
		ChoiceMode:   "single",
		ChoicePrompt: "Pick one",
		ChoiceItems: []app.ChoiceItem{
			{Label: "one", Intent: "pick_one"},
			{Label: "two", Intent: "pick_two"},
			{Label: "three", Intent: "pick_three"},
			{Label: "four", Intent: "pick_four"},
			{Label: "five", Intent: "pick_five"},
		},
	}, expr.Env{}, nil)
	require.NoError(t, err)

	assertLiveChromeFits(t, m.View(), width, height)
}

func TestLiveOverlayDegradesWithoutMinimumHeightFloor(t *testing.T) {
	rows := []string{"[1] one", "[2] two", "[3] three"}

	one := ansi.Strip(renderLiveOverlay([]string{"menu"}, rows, 1, 20, 1))
	require.Equal(t, "[2] two", one,
		"a one-row budget must prefer the selected logical entry over decorative chrome")

	two := ansi.Strip(renderLiveOverlay([]string{"menu"}, rows, 1, 30, 2))
	require.Contains(t, two, "[2] two")
	require.Contains(t, two, "↑ 1 more")
	require.Contains(t, two, "↓ 1 more")
	require.Equal(t, 2, lipgloss.Height(two))
}

func TestLiveOverlayWindowNeverAddsIndicatorBeyondBudget(t *testing.T) {
	rows := make([]string, 40)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d", i+1)
	}
	window := windowLiveOverlayRows(rows, 3, 6, 40)
	require.Len(t, window, 6,
		"centering must reserve any newly-visible up indicator before sizing the body")
	require.Contains(t, ansi.Strip(strings.Join(window, "\n")), "row 4")
}

func TestSystemMenuOpenedWideReservesOperationSiblingBeforeShrink(t *testing.T) {
	const (
		wideWidth  = 120
		wideHeight = 30
		smallWidth = 72
	)
	ctx := context.Background()
	orch := testCloakOrchestrator(t)
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.PatchWorld(ctx, sid, map[string]any{
		app.OperationRunWorldKey: map[string]any{
			"operation_id": "wide-first-operation",
			"title":        "Wide-first operation",
			"status":       "running",
			"from":         "idle",
			"to":           "testing",
		},
	}))

	m := NewRootModel(orch, sid, "", "")
	m.width, m.height = wideWidth, wideHeight
	m = m.resize()
	model, _ := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = model.(RootModel)

	wide := m.View()
	require.Contains(t, ansi.Strip(wide), "operation: Wide-first operation")
	require.LessOrEqual(t, lipgloss.Height(wide), liveOverlayStableTerminalRows,
		"the operation sibling must reduce an overlay opened wide before its first shrink")

	model, _ = tea.Model(m).Update(tea.WindowSizeMsg{
		Width: smallWidth, Height: liveOverlayStableTerminalRows,
	})
	small := model.(RootModel)
	require.Equal(t, lipgloss.Height(wide), lipgloss.Height(small.View()),
		"operation-aware wide-open chrome must keep the same row count on first shrink")
	assertLiveChromeFits(t, small.View(), smallWidth, liveOverlayStableTerminalRows)
}

func TestOperatorQuestionLiveLineUsesSharedOverlayBudget(t *testing.T) {
	const width, height = 50, 12
	options := make([]host.OperatorOption, 30)
	for i := range options {
		options[i] = host.OperatorOption{
			Label:       fmt.Sprintf("Answer %02d", i+1),
			Description: strings.Repeat("long explanation ", 4),
		}
	}

	m := NewRootModel(testCloakOrchestrator(t), app.SessionID("operator-window"), "", "")
	m.width, m.height = width, height
	m = m.resize()
	m.mode = ModeOperatorQuestion
	require.NoError(t, m.operatorQuestion.Open([]host.OperatorQuestion{{
		Header:   "Choose",
		Question: "Which answer should the agent use?",
		Options:  options,
	}}, make(chan map[string]any, 1)))
	m.transcript.AppendLive(m.operatorQuestionLiveView())

	first := m.View()
	assertLiveChromeFits(t, first, width, height)
	require.Contains(t, ansi.Strip(first), "Answer 01")
	require.Contains(t, ansi.Strip(first), "↓")

	for i := 0; i < len(options)+5; i++ {
		model, _ := tea.Model(m).Update(tea.KeyMsg{Type: tea.KeyDown})
		m = model.(RootModel)
	}
	last := m.View()
	assertLiveChromeFits(t, last, width, height)
	require.Contains(t, ansi.Strip(last), "Answer 30")
	require.Contains(t, ansi.Strip(last), "↑")
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(last),
		"operator-question live-line rows must remain stable as selection moves")
}

func assertLiveChromeFits(t *testing.T, view string, width, height int) {
	t.Helper()
	analyzer := NewRenderingAnalyzer(t, view)
	require.LessOrEqual(t, analyzer.LineCount(), height,
		"live chrome exceeds terminal height %d:\n%s", height, ansi.Strip(view))
	for i, line := range analyzer.Lines() {
		require.LessOrEqualf(t, ansi.StringWidth(line), width,
			"live chrome row %d exceeds terminal width %d: %q", i, width, ansi.Strip(line))
	}
}
