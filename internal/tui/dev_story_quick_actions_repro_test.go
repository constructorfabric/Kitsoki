package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

	for i := 0; i < 18; i++ {
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
