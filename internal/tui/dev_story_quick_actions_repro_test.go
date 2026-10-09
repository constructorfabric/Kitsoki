package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

// TestDevStoryLandingQuickActionsFitTerminalWidth reproduces bug 64: the
// dev-story landing room emits over-wide quick-action rows, so a normal terminal
// wraps hints at arbitrary columns (for example the "prd" row splits "author a
// PRD, then..." awkwardly on a 60-column TUI).
func TestDevStoryLandingQuickActionsFitTerminalWidth(t *testing.T) {
	const terminalWidth = 60

	root := newDevStoryLandingModel(t)
	model, _ := root.Update(tea.WindowSizeMsg{Width: terminalWidth, Height: 30})
	rm, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)

	frame := tuipkg.ComposeFrame(&rm, terminalWidth, 30)
	overWide := quickActionRowsWiderThan(frame.Text, terminalWidth)
	require.Empty(t, overWide,
		"dev-story landing quick-action rows should fit the terminal width so the terminal does not wrap hints awkwardly; frame:\n%s",
		frame.Text)
}

func TestDevStoryLandingQuickActionsUseBoundedPickerChrome(t *testing.T) {
	const (
		terminalWidth  = 100
		terminalHeight = 30
		maxChromeRows  = 16
	)

	root := newDevStoryLandingModel(t)
	model, _ := root.Update(tea.WindowSizeMsg{Width: terminalWidth, Height: terminalHeight})
	rm, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)

	require.Empty(t, tuipkg.LiveLineForTest(rm),
		"choice widgets must not occupy transcript.LiveLine; that slot is for short routing/progress rows")
	require.LessOrEqual(t, renderedLineCount(rm.View()), maxChromeRows,
		"quick-action picker chrome must stay bounded so normal-screen repaints do not stamp whole menus into scrollback")

	// Press well past the current quick-action count so the cursor clamps on
	// the last entry regardless of how many items landing.yaml declares —
	// pinning this to the list's exact length is what broke the last time a
	// quick action was added ahead of "look".
	for i := 0; i < 40; i++ {
		model, _ = tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyDown})
		rm, ok = tuipkg.ExtractRootModel(model)
		require.True(t, ok)
	}

	view := rm.View()
	require.Empty(t, tuipkg.LiveLineForTest(rm),
		"arrow-key updates must not reintroduce the picker into transcript.LiveLine")
	require.LessOrEqual(t, renderedLineCount(view), maxChromeRows,
		"bounded picker chrome must remain bounded after cursor movement")
	require.Contains(t, ansi.Strip(view), "look",
		"bounded picker window should follow the cursor toward later quick actions")
}

// TestDevStorySystemMenuFitsSmallNormalScreen reproduces the real xterm
// failure at the model boundary. dev-story currently receives eight builtin
// meta modes, so the Esc menu has thirteen logical entries after session/help/
// stories/world rows are added. The normal-screen renderer must keep that
// overlay inside the physical terminal or stale rows are pushed into native
// scrollback on the next resize/repaint.
func TestDevStorySystemMenuFitsSmallNormalScreen(t *testing.T) {
	const (
		terminalWidth  = 72
		terminalHeight = 16
	)

	// Match the source-checkout CLI posture: exporting KITSOKI_REPO enables
	// the four kitsoki.* modes in addition to the four story.* modes.
	t.Setenv("KITSOKI_REPO", "../..")
	rm := newDevStoryLandingModel(t)
	rm = tuipkg.ResizeRootModel(rm, terminalWidth, terminalHeight)
	// Enter through the production Esc path so the overlay pins the smallest
	// live-region budget it observes for the lifetime of this menu.
	tuipkg.SetModeForTest(&rm, tuipkg.ModeOnPath)
	model, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)

	first := rm.View()
	firstAnalyzer := tuipkg.NewRenderingAnalyzer(t, first)
	firstAnalyzer.AssertStructure("menu (", "[1] Exit", "↓")
	require.LessOrEqual(t, lipgloss.Height(first), terminalHeight,
		"the complete dev-story system-menu chrome must fit the terminal; view:\n%s", ansi.Strip(first))
	assertRenderedRowsFitWidth(t, first, terminalWidth)

	// Growing the terminal must not grow the live region: changing its physical
	// row count is enough to stamp the old top rows into normal-screen
	// scrollback on the following shrink.
	model, _ = tea.Model(rm).Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	wide, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(wide.View()),
		"the active menu must retain a stable live-region row count when the terminal grows")
	assertRenderedRowsFitWidth(t, wide.View(), 120)

	model, _ = tea.Model(wide).Update(tea.WindowSizeMsg{Width: terminalWidth, Height: terminalHeight})
	rm, ok = tuipkg.ExtractRootModel(model)
	require.True(t, ok)
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(rm.View()),
		"the active menu must retain a stable live-region row count after shrinking again")

	// Drive past the visible window to the thirteenth and final logical row.
	// Navigation stays over the full data set even though rendering is bounded.
	model = rm
	for i := 0; i < 20; i++ {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	rm, ok = tuipkg.ExtractRootModel(model)
	require.True(t, ok)

	last := rm.View()
	lastAnalyzer := tuipkg.NewRenderingAnalyzer(t, last)
	lastAnalyzer.AssertStructure("↑", "[13] World")
	require.LessOrEqual(t, lipgloss.Height(last), terminalHeight,
		"cursor-following system-menu chrome must remain inside the terminal; view:\n%s", ansi.Strip(last))
	require.Equal(t, lipgloss.Height(first), lipgloss.Height(last),
		"the live-region row count must stay stable while the menu cursor moves")
	assertRenderedRowsFitWidth(t, last, terminalWidth)
}

func TestDevStorySystemMenuOpenedWideKeepsSmallTerminalFootprint(t *testing.T) {
	const (
		wideWidth  = 120
		wideHeight = 30
		smallWidth = 72
		smallRows  = 10
	)

	t.Setenv("KITSOKI_REPO", "../..")
	rm := newDevStoryLandingModel(t)
	rm = tuipkg.ResizeRootModel(rm, wideWidth, wideHeight)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeOnPath)
	model, _ := tea.Model(rm).Update(tea.KeyMsg{Type: tea.KeyEsc})
	rm, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)

	wide := rm.View()
	require.LessOrEqual(t, lipgloss.Height(wide), smallRows,
		"a menu opened wide must start with the conservative small-terminal footprint")
	assertRenderedRowsFitWidth(t, wide, wideWidth)

	model, _ = tea.Model(rm).Update(tea.WindowSizeMsg{Width: smallWidth, Height: smallRows})
	small, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)
	require.Equal(t, lipgloss.Height(wide), lipgloss.Height(small.View()),
		"the first shrink must not reduce the live region and copy its old top rows into scrollback")
	assertRenderedRowsFitWidth(t, small.View(), smallWidth)

	model, _ = tea.Model(small).Update(tea.WindowSizeMsg{Width: wideWidth, Height: wideHeight})
	wideAgain, ok := tuipkg.ExtractRootModel(model)
	require.True(t, ok)
	require.Equal(t, lipgloss.Height(wide), lipgloss.Height(wideAgain.View()),
		"growing again must retain the same conservative overlay footprint")
}

func newDevStoryLandingModel(t *testing.T) tuipkg.RootModel {
	t.Helper()
	def, err := app.Load("../../stories/dev-story/app.yaml")
	require.NoError(t, err)
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, mach, s, nil)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	initialText, typed, env, rr, err := orch.InitialViewTyped(orch.InitialWorld())
	require.NoError(t, err)
	return tuipkg.NewRootModel(
		orch,
		sid,
		"../../stories/dev-story/app.yaml",
		initialText,
		tuipkg.WithInitialTypedView(typed, env, rr),
	)
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func quickActionRowsWiderThan(frame string, terminalWidth int) []string {
	quickActionLabels := []string{
		"tickets", "drive", "bugfix", "implement", "cypilot", "git",
		"✗ pr (no open PR yet)", "code review", "demo video", "prd",
		"idea", "model eval", "onboard", "inbox", "look",
	}
	var out []string
	found := 0
	for _, line := range strings.Split(frame, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		trimmed = strings.TrimPrefix(trimmed, "▸ ")
		for _, label := range quickActionLabels {
			if trimmed == label || strings.HasPrefix(trimmed, label+"  ") {
				found++
				if w := ansi.StringWidth(line); w > terminalWidth {
					out = append(out, line)
				}
				break
			}
		}
	}
	if found == 0 {
		out = append(out, "no quick action rows were rendered")
	}
	return out
}

func assertRenderedRowsFitWidth(t *testing.T, view string, width int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		require.LessOrEqualf(t, ansi.StringWidth(line), width,
			"rendered row %d exceeds terminal width %d: %q", i, width, ansi.Strip(line))
	}
}
