package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// RouteCommand implements `/route up|down` — the TUI's thumbs-up/down
// affordance on the most recently RESOLVED routed turn (WS-C C4: the
// routing-dissatisfaction substrate; see docs/testing/routing-tuning.md and
// .context/dev-workflows-surface-matrix-plan.md WS-C). It journals the
// operator's verdict via Orchestrator.RecordRoutingFeedback against the
// snapshot m.snapshotRoutedTurn captured when that turn settled
// (m.lastRoutedIntent / m.lastRoutedTier / m.lastRoutedState) — no world or
// machine state is touched, so this is safe to type at any point without
// disturbing the session.
//
// Aliases: `/route 👍`/`/route 👎` and the bare `/route+`/`/route-` are NOT
// supported — one canonical spelling keeps mined phrases unambiguous.
type RouteCommand struct{}

func (RouteCommand) Name() string { return "/route" }

func (RouteCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	verb := ""
	if len(args) > 0 {
		verb = strings.ToLower(strings.TrimSpace(args[0]))
	}

	var verdict orchestrator.RoutingFeedbackVerdict
	switch verb {
	case "up", "good", "correct":
		verdict = orchestrator.RoutingFeedbackUp
	case "down", "bad", "wrong":
		verdict = orchestrator.RoutingFeedbackDown
	default:
		return m.routeBlock("usage: /route up|down (records a verdict on the last routed turn)"), m, nil
	}

	if m.lastRoutedIntent == "" {
		return m.routeBlock("no routed turn yet this session to give feedback on"), m, nil
	}
	if m.orch == nil {
		return m.routeBlock("no active session"), m, nil
	}

	if err := m.orch.RecordRoutingFeedback(context.Background(), m.sid, m.lastRoutedState, m.lastRoutedIntent, m.lastInput, m.lastRoutedTier, verdict); err != nil {
		return m.routeBlock(fmt.Sprintf("could not record routing feedback: %v", err)), m, nil
	}

	glyph := "👍"
	if verdict == orchestrator.RoutingFeedbackDown {
		glyph = "👎"
	}
	return m.routeBlock(fmt.Sprintf("%s recorded for %q → %s", glyph, m.lastInput, m.lastRoutedIntent)), m, nil
}

func (m RootModel) routeBlock(line string) string {
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput("route: " + line)
}
