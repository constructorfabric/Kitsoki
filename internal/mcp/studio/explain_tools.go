package studio

import (
	"context"
	"fmt"
	"sort"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/store"
)

const (
	defaultExplainLimit = 80
	maxExplainLimit     = 200
	defaultStallAfterMS = 30_000
)

// NextCallHint makes an explanation actionable without making an unsupported
// causal claim. Arguments are deliberately small enough for a client to replay.
type NextCallHint struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Reason    string         `json:"reason"`
}

// ExplainArgs bounds an explanation to the most recent events in a requested
// turn window. ExpectedRoute is optional: without it, route evidence is only
// reported when the trace itself records an expected route.
type ExplainArgs struct {
	Since             int64  `json:"since,omitempty"`
	Until             int64  `json:"until,omitempty"`
	Limit             int    `json:"limit,omitempty"`
	ExpectedRoute     string `json:"expected_route,omitempty"`
	StallAfterMS      int64  `json:"stall_after_ms,omitempty"`
	ObservedAtUnixMic int64  `json:"observed_at_unix_micros,omitempty"`
}

// SessionExplainArgs selects one open session for explanation.
type SessionExplainArgs struct {
	Handle string `json:"handle"`
	ExplainArgs
}

// TraceExplainArgs selects an on-disk trace using the same resolution contract
// as trace.read. It intentionally leaves trace.read untouched.
type TraceExplainArgs struct {
	Path      string `json:"path,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	App       string `json:"app,omitempty"`
	TicketID  string `json:"ticket_id,omitempty"`
	Root      string `json:"root,omitempty"`
	ExplainArgs
}

// ExplainEvidence identifies the precise observed event used for a finding.
type ExplainEvidence struct {
	Turn      int64  `json:"turn"`
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	StatePath string `json:"state_path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// ExplainFinding is an additive classification. It always describes only what
// the supplied bounded event window establishes.
type ExplainFinding struct {
	Class    string            `json:"class"`
	Summary  string            `json:"summary"`
	Evidence []ExplainEvidence `json:"evidence"`
	NextCall NextCallHint      `json:"next_call"`
}

// ExplainResult is shared by session.explain and trace.explain.
type ExplainResult struct {
	OK             bool             `json:"ok"`
	SourcePath     string           `json:"source_path,omitempty"`
	ObservedEvents int              `json:"observed_events"`
	LastTurn       int64            `json:"last_turn"`
	Bounded        bool             `json:"bounded"`
	Findings       []ExplainFinding `json:"findings,omitempty"`
	NextCall       NextCallHint     `json:"next_call"`
}

func (srv *Server) handleSessionExplain(_ context.Context, _ *mcpsdk.CallToolRequest, args SessionExplainArgs) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	return nil, explainHistory(rt.history(), args.ExplainArgs, "", time.Now()), nil
}

func (srv *Server) handleTraceExplain(_ context.Context, _ *mcpsdk.CallToolRequest, args TraceExplainArgs) (*mcpsdk.CallToolResult, any, error) {
	path, err := resolveTracePathArg(args.Root, args.Path, args.SessionID, args.App, args.TicketID)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.explain: %v", err)), nil, nil
	}
	history, err := readTraceFile(path)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.explain: %v", err)), nil, nil
	}
	return nil, explainHistory(history, args.ExplainArgs, path, time.Now()), nil
}

func explainHistory(history store.History, args ExplainArgs, sourcePath string, now time.Time) ExplainResult {
	limit := args.Limit
	if limit <= 0 {
		limit = defaultExplainLimit
	}
	if limit > maxExplainLimit {
		limit = maxExplainLimit
	}
	window, lastTurn := projectTraceEvents(history, args.Since, args.Until, nil, limit, 0)
	result := ExplainResult{
		OK:             true,
		SourcePath:     sourcePath,
		ObservedEvents: len(window),
		LastTurn:       lastTurn,
		Bounded:        len(window) < len(history),
		NextCall:       NextCallHint{Tool: "trace.read", Arguments: map[string]any{"path": sourcePath, "errors_only": true}, Reason: "Read the raw events before deciding on a repair."},
	}
	if len(window) == 0 {
		return result
	}

	if finding, ok := explainRouteSurprise(window, args.ExpectedRoute); ok {
		result.Findings = append(result.Findings, finding)
	}
	if finding, ok := explainRecordedErrorTransition(window); ok {
		result.Findings = append(result.Findings, finding)
	}
	if finding, ok := explainStalledTurn(window, args, now); ok {
		result.Findings = append(result.Findings, finding)
	}
	return result
}

func explainRouteSurprise(history []store.Event, callerExpected string) (ExplainFinding, bool) {
	for _, ev := range history {
		if ev.Kind != store.UserInputReceived {
			continue
		}
		observed := payloadFirstString(ev.Payload, "route", "intent", "chosen_intent")
		expected := callerExpected
		if expected == "" {
			expected = payloadFirstString(ev.Payload, "expected_route", "expected_intent", "requested_route")
		}
		if observed == "" || expected == "" || observed == expected {
			continue
		}
		return ExplainFinding{
			Class: "route_surprise", Summary: "The recorded route differs from an explicit expected route; the trace does not establish why it differed.",
			Evidence: []ExplainEvidence{{Turn: int64(ev.Turn), Seq: ev.Seq, Kind: string(ev.Kind), StatePath: string(ev.StatePath), Detail: "expected=" + expected + ", observed=" + observed}},
			NextCall: NextCallHint{Tool: "session.inspect", Reason: "Inspect allowed intents and the current route evidence before changing routing."},
		}, true
	}
	return ExplainFinding{}, false
}

func explainRecordedErrorTransition(history []store.Event) (ExplainFinding, bool) {
	for i, ev := range history {
		if !traceErrorKinds[ev.Kind] {
			continue
		}
		for _, later := range history[i+1:] {
			if later.Kind != store.TransitionApplied {
				continue
			}
			if later.Turn < ev.Turn {
				continue
			}
			return ExplainFinding{
				Class: "recorded_error_then_transition", Summary: "An error was recorded before a later transition. This is consistent with an error path, but the trace alone does not prove that the error was intentionally swallowed.",
				Evidence: []ExplainEvidence{eventEvidence(ev, payloadField(ev.Payload, "error")), eventEvidence(later, payloadField(later.Payload, "to"))},
				NextCall: NextCallHint{Tool: "trace.read", Arguments: map[string]any{"since": int64(ev.Turn), "kinds": []string{string(ev.Kind), string(store.TransitionApplied)}}, Reason: "Read the raw error and transition payloads together, including any on_error routing evidence."},
			}, true
		}
	}
	return ExplainFinding{}, false
}

func explainStalledTurn(history []store.Event, args ExplainArgs, now time.Time) (ExplainFinding, bool) {
	starts := make(map[int64]store.Event)
	ended := make(map[int64]bool)
	for _, ev := range history {
		turn := int64(ev.Turn)
		if ev.Kind == store.TurnStarted {
			starts[turn] = ev
		}
		if ev.Kind == store.TurnEnded {
			ended[turn] = true
		}
	}
	turns := make([]int64, 0, len(starts))
	for turn := range starts {
		if !ended[turn] {
			turns = append(turns, turn)
		}
	}
	if len(turns) == 0 {
		return ExplainFinding{}, false
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i] > turns[j] })
	start := starts[turns[0]]
	cutoff := args.StallAfterMS
	if cutoff <= 0 {
		cutoff = defaultStallAfterMS
	}
	observedAt := now
	if args.ObservedAtUnixMic != 0 {
		observedAt = time.UnixMicro(args.ObservedAtUnixMic)
	}
	if start.Ts.IsZero() || observedAt.Sub(start.Ts) < time.Duration(cutoff)*time.Millisecond {
		return ExplainFinding{}, false
	}
	return ExplainFinding{
		Class: "stalled_or_unfinished_turn", Summary: "The bounded trace contains a started turn with no recorded turn.end by the supplied observation time. It may still be running if newer events are outside this window.",
		Evidence: []ExplainEvidence{eventEvidence(start, "no turn.end observed in the bounded window")},
		NextCall: NextCallHint{Tool: "session.status", Reason: "Poll the live handle when available; otherwise use trace.read with a larger limit to check for later events."},
	}, true
}

func eventEvidence(ev store.Event, detail string) ExplainEvidence {
	return ExplainEvidence{Turn: int64(ev.Turn), Seq: ev.Seq, Kind: string(ev.Kind), StatePath: string(ev.StatePath), Detail: detail}
}

func payloadFirstString(payload []byte, fields ...string) string {
	for _, field := range fields {
		if value := payloadField(payload, field); value != "" {
			return value
		}
	}
	return ""
}
