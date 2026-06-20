package studio

// operator_prompter.go — the MCP client surface's host.OperatorPrompter (slice 8).
//
// When a studio-driven turn dispatches a kitsoki agent sub-agent that calls
// mcp__operator__ask (internal/host/operator_ask_bridge.go), the in-context
// OperatorPrompter is what surfaces the question and blocks for the answer. The
// TUI prompter pushes to bubbletea; the web prompter pushes to SSE; this — the
// THIRD implementation of the same DI seam — pushes to the driving MCP client.
//
// Two transports, both reusing every other piece of the operator-ask machinery
// (the per-call socket, attachOperatorAsk, the wire schema, the bounded wait,
// the trace events):
//
//   - PRIMARY — MCP elicitation. The server-initiated "ask the user for input"
//     protocol feature. Ask sends an elicitation request down the studio's own
//     ServerSession (the connection to the driving client) and blocks for the
//     client's response — mirroring exactly how the web/TUI prompters block.
//     session.drive stays one call: the elicitation is a nested server→client
//     request mid-turn while the sub-agent is parked on the socket.
//
//   - FALLBACK — suspend/resume via session.answer. For clients that do not
//     advertise the elicitation capability: Ask parks the question in a registry
//     and blocks; session.drive returns {awaiting_operator, question_id,
//     questions} instead of an outcome (the turn goroutine stays parked, the
//     sub-agent still blocked on the socket); the client calls session.answer,
//     which delivers the answer (unblocking Ask) and waits for the turn to
//     complete — returning the outcome or another awaiting_operator.
//
// The transport is an injectable seam (operatorAskTransport) so a no-LLM test
// injects a scripted-answer stub and drives a story's operator-ask branch
// deterministically — the exact pattern the existing operator-ask tests use.
//
// Bounded wait & graceful degrade: the prompter inherits operator-ask's timeout.
// The host bridge (handleConn) already wraps ctx with the operator-ask wait
// bound before calling Ask, so on timeout / client-cancel / ctx-cancel Ask
// returns an error, which the bridge surfaces to the sub-agent as a tool error
// ("operator did not answer; proceed without this input") — a non-responding
// client degrades to headless behaviour rather than hanging.

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
)

// operatorAskTransport is the injectable seam under the studio prompter: it
// takes a forwarded question batch and returns the operator's answers (or an
// error). The elicitation transport talks to the live MCP client; the suspend
// transport parks the turn for session.answer; a test injects a scripted stub.
//
// The contract matches host.OperatorPrompter.Ask exactly so the prompter is a
// thin adapter: honour ctx cancellation, return the AskUserQuestion-shaped
// answers map ({"<question>": "<label>" | ["<label>", …]}) on success, and a
// non-nil error when no usable answer was obtained.
type operatorAskTransport interface {
	ask(ctx context.Context, sessionID string, questions []host.OperatorQuestion) (map[string]any, error)
}

// studioOperatorPrompter is the host.OperatorPrompter for the MCP client surface.
// It delegates to a transport chosen per drive call (elicitation when the client
// advertises it, the suspend/resume fallback otherwise, a scripted stub in tests).
type studioOperatorPrompter struct {
	transport operatorAskTransport
}

var _ host.OperatorPrompter = (*studioOperatorPrompter)(nil)

// newStudioOperatorPrompter wraps a transport as a host.OperatorPrompter.
func newStudioOperatorPrompter(t operatorAskTransport) *studioOperatorPrompter {
	return &studioOperatorPrompter{transport: t}
}

// Ask implements host.OperatorPrompter. It forwards the question batch to the
// bound transport and blocks until it answers, ctx is cancelled, or the
// inherited operator-ask timeout fires (the host bridge already wraps ctx with
// the operator-ask wait bound before calling Ask, so a nil transport or a
// non-responding client degrades to a tool error rather than hanging).
func (p *studioOperatorPrompter) Ask(ctx context.Context, sessionID string, questions []host.OperatorQuestion) (map[string]any, error) {
	if p == nil || p.transport == nil {
		return nil, fmt.Errorf("studio operator prompter: no transport attached")
	}
	return p.transport.ask(ctx, sessionID, questions)
}

// ── elicitation transport (primary) ───────────────────────────────────────────

// elicitTransport forwards a forwarded question batch to the driving client as
// an MCP elicitation request and maps the accepted form data back to the
// AskUserQuestion answer shape. It is selected only when the client advertises
// the elicitation capability (clientSupportsElicitation).
type elicitTransport struct {
	ss *mcpsdk.ServerSession
}

// ask sends one elicitation request carrying every question as a flat top-level
// schema property (MCP elicitation forbids nesting), each a string with an enum
// of the option labels. The client's accepted Content (keyed by property) is
// mapped back to answers keyed by the question text — the AskUserQuestion shape.
//
// A decline/cancel, or any transport error, is returned as an error so the host
// bridge surfaces it to the sub-agent as "proceed without this input".
func (t *elicitTransport) ask(ctx context.Context, _ string, questions []host.OperatorQuestion) (map[string]any, error) {
	if t.ss == nil {
		return nil, fmt.Errorf("elicit transport: no server session")
	}
	props := make(map[string]any, len(questions))
	required := make([]string, 0, len(questions))
	keyToQuestion := make(map[string]host.OperatorQuestion, len(questions))
	for i, q := range questions {
		key := fmt.Sprintf("q%d", i)
		keyToQuestion[key] = q
		required = append(required, key)
		labels := make([]string, 0, len(q.Options))
		for _, o := range q.Options {
			labels = append(labels, o.Label)
		}
		prop := map[string]any{
			"type":        "string",
			"title":       q.Header,
			"description": q.Question,
		}
		if len(labels) > 0 {
			prop["enum"] = labels
		}
		props[key] = prop
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}

	res, err := t.ss.Elicit(ctx, &mcpsdk.ElicitParams{
		Message:         elicitMessage(questions),
		RequestedSchema: schema,
	})
	if err != nil {
		return nil, fmt.Errorf("elicit: %w", err)
	}
	if res.Action != "accept" {
		return nil, fmt.Errorf("operator did not answer (elicitation %s)", res.Action)
	}

	answers := make(map[string]any, len(questions))
	for key, q := range keyToQuestion {
		v, ok := res.Content[key]
		if !ok {
			continue
		}
		answers[q.Question] = coerceElicitedAnswer(v, q.MultiSelect)
	}
	if len(answers) == 0 {
		return nil, fmt.Errorf("elicit: client accepted but returned no answers")
	}
	return answers, nil
}

// elicitMessage is the human-facing prompt the client renders above the form.
func elicitMessage(questions []host.OperatorQuestion) string {
	if len(questions) == 1 {
		return questions[0].Question
	}
	return fmt.Sprintf("The agent has %d questions for the operator.", len(questions))
}

// coerceElicitedAnswer normalises one elicited value to the AskUserQuestion
// answer shape: a single label (string) or, for a multiSelect question, a
// []string. Elicitation's flat schema returns a string per property, so a
// multiSelect answer arrives comma- or already-list-shaped; both are handled.
func coerceElicitedAnswer(v any, multi bool) any {
	switch tv := v.(type) {
	case []any:
		labels := make([]string, 0, len(tv))
		for _, e := range tv {
			if s, ok := e.(string); ok {
				labels = append(labels, s)
			}
		}
		if multi {
			return labels
		}
		if len(labels) > 0 {
			return labels[0]
		}
		return ""
	case string:
		if multi {
			return []string{tv}
		}
		return tv
	default:
		return v
	}
}

// clientSupportsElicitation reports whether the driving client advertised the
// elicitation capability at initialize. Only then is the primary transport
// chosen; otherwise the suspend/resume fallback is used so coherence never
// depends on client capability (proposal Q1: the fallback is the floor).
func clientSupportsElicitation(ss *mcpsdk.ServerSession) bool {
	if ss == nil {
		return false
	}
	params := ss.InitializeParams()
	if params == nil || params.Capabilities == nil {
		return false
	}
	return params.Capabilities.Elicitation != nil
}
