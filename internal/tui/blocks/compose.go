package blocks

import "strings"

// RenderChatView composes the canonical chat-view layout from a fixture
// using r's width and theme. The output is a single string ready to
// print — it does NOT include trailing or leading blank lines beyond
// what blocks themselves emit, so it round-trips cleanly into
// golden-file comparisons.
//
// Block order:
//
//	header
//	────
//	system notice (welcome)
//	for each turn:
//	  user turn
//	  resolved routing
//	  agent turn
//	for each inbox:
//	  inbox notification
//	for each background complete:
//	  background-complete line
//	actions block
//	────
//	footer (1-2 lines)
//	prompt
//
// Block separation is one blank line, matching the proposal's sketches.
func (r *Renderer) RenderChatView(f ChatFixture) string {
	var parts []string
	parts = append(parts, r.Header(f.Location, f.Room))
	parts = append(parts, r.rule())
	if f.Welcome != "" {
		parts = append(parts, r.SystemNotice(f.Welcome))
	}
	for _, t := range f.Turns {
		parts = append(parts, r.UserTurn(t.UserInput))
		parts = append(parts, r.RoutingResolved(t.Resolved))
		if t.AgentBody != "" {
			parts = append(parts, r.AgentTurn(t.AgentBody))
		}
	}
	for _, n := range f.Inbox {
		parts = append(parts, r.Inbox(n))
	}
	for _, bc := range f.BackgroundCompletes {
		parts = append(parts, r.BackgroundComplete(bc.Room, bc.Summary))
	}
	if len(f.Actions) > 0 {
		parts = append(parts, r.Menu(f.Actions))
	}
	parts = append(parts, r.rule())
	parts = append(parts, r.Footer(f.FooterLine1, f.FooterLine2))
	parts = append(parts, r.Prompt(f.PromptMode))

	return strings.Join(parts, "\n\n")
}

// RenderWorldView composes the dedicated /world view: header, hierarchical
// body, footer hint.
func (r *Renderer) RenderWorldView(location string, nodes []WorldNode) string {
	parts := []string{
		r.Header("world", location),
		r.rule(),
		r.World(nodes),
		r.rule(),
		r.WorldFooterHint(),
	}
	return strings.Join(parts, "\n")
}

// RenderTraceView composes the /trace transcript block. This is a chat
// block today (not a dedicated view), but the preview prints it in
// isolation so authors can iterate on the trace-row format without
// stepping through a real session.
func (r *Renderer) RenderTraceView(events []TraceEvent) string {
	return r.RoutingTrace(events)
}

// RenderRoutingFrames produces one frame per phase the routing pipeline
// can be in for the same user input. Used by the preview's
// --block routing_status to demonstrate the live-update sequence as
// static stills so authors can see each intermediate state.
func (r *Renderer) RenderRoutingFrames(userInput string, phases []RoutingPhase, settled Resolved) string {
	var parts []string
	for _, p := range phases {
		parts = append(parts,
			r.UserTurn(userInput)+"\n"+r.RoutingStatus(p))
	}
	parts = append(parts,
		r.UserTurn(userInput)+"\n"+r.RoutingResolved(settled))
	return strings.Join(parts, "\n\n")
}

// rule renders a horizontal divider at r.Width. Used between header /
// body / footer in composed views.
func (r *Renderer) rule() string {
	w := r.Width
	if w < 1 {
		w = 1
	}
	return r.style(r.Theme.Border, nil, false, false).Render(strings.Repeat("─", w))
}
