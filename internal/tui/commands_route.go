package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// RouteCommand implements /route feedback and reroute affordances for the
// last resolved routed turn.
//
// Feedback mode:
//
//	/route up|down
//
// Reroute mode:
//
//	/route retry <intent|help|workbench|meta>
//	/route <intent|help|workbench|meta>
//
// Feedback journals the operator's verdict against the durable snapshot
// captured when the turn settled. Reroute rewinds the last contextual-routing
// decision by its decision ID and re-dispatches the original utterance under
// the selected class.
type RouteCommand struct{}

func (RouteCommand) Name() string { return "/route" }

func (RouteCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	verb := ""
	if len(args) > 0 {
		verb = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch verb {
	case "up", "good", "correct":
		return runRouteFeedback(m, orchestrator.RoutingFeedbackUp)
	case "down", "bad", "wrong":
		return runRouteFeedback(m, orchestrator.RoutingFeedbackDown)
	case "retry", "redo", "reroute":
		return runRouteReroute(m, args[1:])
	case "intent", "help", "room_request", "work", "workbench", "meta", "meta_edit":
		return runRouteReroute(m, args)
	default:
		return routeUsageBlock(m), m, nil
	}
}

func runRouteFeedback(m RootModel, verdict orchestrator.RoutingFeedbackVerdict) (string, RootModel, tea.Cmd) {
	if m.lastRoutedIntent == "" {
		return m.routeBlock("no routed turn yet this session to give feedback on"), m, nil
	}
	if m.orch == nil {
		return m.routeBlock("no active session"), m, nil
	}

	if err := m.orch.RecordRoutingFeedback(
		context.Background(),
		m.sid,
		m.lastRoutedState,
		m.lastRoutedIntent,
		m.lastInput,
		m.lastRoutedTier,
		verdict,
	); err != nil {
		return m.routeBlock(fmt.Sprintf("could not record routing feedback: %v", err)), m, nil
	}

	glyph := "👍"
	if verdict == orchestrator.RoutingFeedbackDown {
		glyph = "👎"
	}
	return m.routeBlock(fmt.Sprintf("%s recorded for %q → %s", glyph, m.lastInput, m.lastRoutedIntent)), m, nil
}

func runRouteReroute(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	if m.lastRoutedDecisionID == "" {
		return m.routeBlock("no routed turn yet this session to reroute"), m, nil
	}
	if m.orch == nil {
		return m.routeBlock("no active session"), m, nil
	}

	target, targetLabel, ok := parseRouteClass(args)
	if !ok {
		return routeUsageBlock(m), m, nil
	}

	// Use the slash-command input itself as the prompt/queue marker.
	workspacePath, _ := os.Getwd()
	next, cmd := startAsyncTurnDetailed(
		m,
		"/route retry "+targetLabel,
		asyncRerouteRoute(m.orch, m.sid, m.lastRoutedDecisionID, target, "operator reroute", workspacePath),
		pendingReroute,
	)
	return m.routeBlock(fmt.Sprintf("rerouting %q as %s", m.lastInput, targetLabel)), next, cmd
}

func asyncRerouteRoute(
	orch *orchestrator.Orchestrator,
	sid app.SessionID,
	decisionID string,
	newClass orchestrator.ContextRouteClass,
	reason string,
	workspacePath string,
) func(context.Context) (asyncTurnResult, error) {
	return func(ctx context.Context) (asyncTurnResult, error) {
		out, err := orch.RewindRoute(ctx, sid, decisionID, newClass, reason, workspacePath)
		return asyncTurnResult{outcome: out}, err
	}
}

func parseRouteClass(args []string) (orchestrator.ContextRouteClass, string, bool) {
	if len(args) == 0 {
		return "", "", false
	}
	value := strings.ToLower(strings.TrimSpace(args[0]))
	switch value {
	case "intent", "same", "self":
		return orchestrator.ClassIntent, "intent", true
	case "help":
		return orchestrator.ClassHelp, "help", true
	case "room_request", "work", "workbench":
		return orchestrator.ClassRoomRequest, "workbench", true
	case "meta", "meta_edit", "meta-agent", "meta_agent":
		return orchestrator.ClassMetaEdit, "meta", true
	default:
		return "", "", false
	}
}

func routeUsageBlock(m RootModel) string {
	return m.routeBlock("usage: /route up|down | /route retry <intent|help|workbench|meta>")
}

func (m RootModel) routeBlock(line string) string {
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput("route: " + line)
}
