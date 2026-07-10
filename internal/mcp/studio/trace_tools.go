package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
)

// trace_tools.go — read a session trace OFF DISK, with no live handle.
//
// `session.trace` reads the in-memory history of an OPEN driving handle, and a
// read blocks while that handle is mid-turn. But the traces an agent most needs
// to inspect are produced OUTSIDE the studio's own handles: a `kitsoki web` live
// session journal under ~/.kitsoki/sessions, a dogfood/background-run trace, or a
// maker worktree's trace it must mine for the discarded `final_diff`. Today those
// force a shell-out (`cat`/`jq`/`grep` over the raw JSONL, or `kitsoki trace`).
//
//   - trace.read    — resolve a trace by path | session-id | app, parse the JSONL
//                     into store.Event records (a lock-free read, so it never
//                     collides with a live writer), filter by turn/kind, and
//                     return the events plus a kind/error summary.
//   - trace.to_flow — convert a recorded trace into a replayable flow fixture
//                     (+ host cassette) via testrunner.ConvertTraceToFlow. This
//                     is the no-LLM-testing loop closed inside MCP: dogfood live
//                     → trace.to_flow → story.test.
//
// Neither makes an interpretive (LLM) call.

// defaultTraceSummaryErrorKinds are the event kinds trace.read surfaces in its
// summary (and returns untruncated when errors_only is set): the swallowed
// failures an `on_error:` arc hides and a stalled turn leaves behind.
var traceErrorKinds = map[store.EventKind]bool{
	store.HarnessError: true,
	store.MachineError: true,
	store.AgentError:   true,
}

// registerTraceTools wires the trace.* tools onto the server. trace.read is a
// pure read (always available); trace.to_flow writes a fixture + cassette to
// disk, so a read-only server omits it.
func (srv *Server) registerTraceTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "trace.read",
		Description: "Read a session trace OFF DISK — the on-disk counterpart to session.trace (which needs an open, idle handle). Resolves the trace by {path} (an explicit *.jsonl), or the newest match for {session_id} (a basename substring) / {app} (a ~/.kitsoki/sessions/<app> subdir) / {ticket_id} (world ticket_id), under {root?} (default ~/.kitsoki/sessions). Parses the JSONL lock-free (never collides with a live writer). Filters: {since?, until?} (turn range), {kinds?} ([]event-kind), {limit?} (keep last N), {truncate_payload?} (cap each payload; default 500, 0 disables), {errors_only?} (only harness.error|machine.error|agent.call.error, untruncated). Returns {ok, source_path, events[], last_turn, summary:{by_kind, errors[]}}. Read-only.",
	}, srv.handleTraceRead)

	if !srv.readOnly {
		mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
			Name:        "trace.to_flow",
			Description: "Convert a recorded session trace into a deterministic, replayable flow fixture (+ host cassette) — wraps `kitsoki trace to-flow`. {trace (required, the *.jsonl to convert), app (required, the value written into the fixture's app: field, e.g. ../app.yaml), out (required, the flow fixture output path), recording? (cassette output path; default <out>.cassette.yaml), app_id?, initial_state?}. Returns {ok, flow_path, cassette_path?, num_turns, num_episodes}. This closes the no-LLM test loop in MCP: a live dogfood trace becomes a flow story.test can replay. No LLM.",
		}, srv.handleTraceToFlow)
	}
}

// ── trace.read ────────────────────────────────────────────────────────────────

// TraceReadArgs is the input to trace.read.
type TraceReadArgs struct {
	// Path is an explicit trace file. When set it wins over session_id/app.
	Path string `json:"path,omitempty"`
	// SessionID resolves the newest *.jsonl whose basename contains this substring.
	SessionID string `json:"session_id,omitempty"`
	// App restricts resolution to the <root>/<app> subdir (e.g. "kitsoki-dev").
	App string `json:"app,omitempty"`
	// TicketID restricts resolution to traces whose world ticket_id matches.
	TicketID string `json:"ticket_id,omitempty"`
	// Root overrides the search root (default ~/.kitsoki/sessions).
	Root string `json:"root,omitempty"`
	// Since/Until filter by turn number (inclusive).
	Since int64 `json:"since,omitempty"`
	Until int64 `json:"until,omitempty"`
	// Kinds filters to specific event kinds.
	Kinds []string `json:"kinds,omitempty"`
	// Limit keeps the last N matching events.
	Limit int `json:"limit,omitempty"`
	// TruncatePayload caps each event payload (default 500; pass 0 to disable).
	TruncatePayload *int `json:"truncate_payload,omitempty"`
	// ErrorsOnly returns only the error-kind events, untruncated — the swallowed
	// failures (harness/machine/agent errors) behind an on_error arc.
	ErrorsOnly bool `json:"errors_only,omitempty"`
}

// TraceReadOK is the trace.read result.
type TraceReadOK struct {
	OK         bool          `json:"ok"`
	SourcePath string        `json:"source_path"`
	Events     []store.Event `json:"events"`
	LastTurn   int64         `json:"last_turn"`
	Summary    TraceSummary  `json:"summary"`
}

// TraceSummary is the cheap structural digest of a trace: the per-kind counts
// over the whole file (not just the returned window) plus the error events,
// each as a compact {turn, kind, message}.
type TraceSummary struct {
	ByKind map[string]int     `json:"by_kind"`
	Errors []TraceErrorDigest `json:"errors,omitempty"`
}

// TraceErrorDigest is one error-kind event projected to {turn, kind, message}.
type TraceErrorDigest struct {
	Turn    int64  `json:"turn"`
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
}

// handleTraceRead resolves and reads an on-disk trace.
func (srv *Server) handleTraceRead(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args TraceReadArgs,
) (*mcpsdk.CallToolResult, any, error) {
	path, err := resolveTracePathArg(args.Root, args.Path, args.SessionID, args.App, args.TicketID)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.read: %v", err)), nil, nil
	}
	history, err := readTraceFile(path)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.read: %v", err)), nil, nil
	}

	// Summary is computed over the WHOLE file, before windowing, so it always
	// reports the true shape of the trace regardless of the returned slice.
	summary := summarizeTrace(history)

	truncate := 500
	if args.TruncatePayload != nil {
		truncate = *args.TruncatePayload
	}
	var kinds []string
	if args.ErrorsOnly {
		for k := range traceErrorKinds {
			kinds = append(kinds, string(k))
		}
		truncate = 0 // error payloads are the point — never clip them.
	} else {
		kinds = args.Kinds
	}

	filtered, lastTurn := projectTraceEvents(history, args.Since, args.Until, kinds, args.Limit, truncate)
	return nil, TraceReadOK{
		OK:         true,
		SourcePath: path,
		Events:     filtered,
		LastTurn:   lastTurn,
		Summary:    summary,
	}, nil
}

// ── trace.to_flow ─────────────────────────────────────────────────────────────

// TraceToFlowArgs is the input to trace.to_flow. It mirrors the `kitsoki trace
// to-flow` flags so the MCP path and the CLI produce byte-identical fixtures.
type TraceToFlowArgs struct {
	Trace        string `json:"trace"`
	App          string `json:"app"`
	Out          string `json:"out"`
	Recording    string `json:"recording,omitempty"`
	AppID        string `json:"app_id,omitempty"`
	InitialState string `json:"initial_state,omitempty"`
}

// TraceToFlowOK is the trace.to_flow result.
type TraceToFlowOK struct {
	OK           bool   `json:"ok"`
	FlowPath     string `json:"flow_path"`
	CassettePath string `json:"cassette_path,omitempty"`
	NumTurns     int    `json:"num_turns"`
	NumEpisodes  int    `json:"num_episodes"`
}

// handleTraceToFlow converts a trace to a flow fixture, writing the fixture (and
// its host cassette, when the trace has host calls) to disk. It mirrors the CLI
// path in cmd/kitsoki/trace.go: the cassette defaults to <out>.cassette.yaml and
// is referenced by basename when it sits beside the fixture.
func (srv *Server) handleTraceToFlow(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args TraceToFlowArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Trace == "" {
		return buildToolError(ErrBadRequest, "trace.to_flow: trace is required"), nil, nil
	}
	if args.App == "" {
		return buildToolError(ErrBadRequest, "trace.to_flow: app is required (the value for the fixture's app: field)"), nil, nil
	}
	if args.Out == "" {
		return buildToolError(ErrBadRequest, "trace.to_flow: out is required (the flow fixture output path)"), nil, nil
	}

	casPath := args.Recording
	if casPath == "" {
		casPath = strings.TrimSuffix(args.Out, ".yaml") + ".cassette.yaml"
	}
	casRef := casPath
	if filepath.Dir(casPath) == filepath.Dir(args.Out) {
		casRef = filepath.Base(casPath)
	}

	res, err := testrunner.ConvertTraceToFlow(args.Trace, testrunner.ConvertOptions{
		AppPath:      args.App,
		CassettePath: casRef,
		AppID:        args.AppID,
		InitialState: args.InitialState,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.to_flow: %v", err)), nil, nil
	}

	if err := os.MkdirAll(filepath.Dir(args.Out), 0o755); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.to_flow: mkdir for %s: %v", args.Out, err)), nil, nil
	}
	if err := os.WriteFile(args.Out, res.FlowYAML, 0o644); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("trace.to_flow: write flow %s: %v", args.Out, err)), nil, nil
	}
	out := TraceToFlowOK{OK: true, FlowPath: args.Out, NumTurns: res.NumTurns, NumEpisodes: res.NumEpisodes}
	if res.CassetteYAML != nil {
		if err := os.WriteFile(casPath, res.CassetteYAML, 0o644); err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("trace.to_flow: write cassette %s: %v", casPath, err)), nil, nil
		}
		out.CassettePath = casPath
	}
	return nil, out, nil
}

// ── shared helpers ────────────────────────────────────────────────────────────

// projectTraceEvents applies the turn-window / kind / limit / payload-truncation
// filters shared by session.trace and trace.read. It returns the filtered slice
// and the highest turn number seen across the WHOLE history (not just the
// window), so a caller always learns how far the trace runs.
func projectTraceEvents(history store.History, since, until int64, kinds []string, limit, truncatePayload int) ([]store.Event, int64) {
	var kindSet map[string]bool
	if len(kinds) > 0 {
		kindSet = make(map[string]bool, len(kinds))
		for _, k := range kinds {
			kindSet[k] = true
		}
	}
	var filtered []store.Event
	var lastTurn int64
	for _, ev := range history {
		t := int64(ev.Turn)
		if t > lastTurn {
			lastTurn = t
		}
		if since > 0 && t < since {
			continue
		}
		if until > 0 && t > until {
			continue
		}
		if kindSet != nil && !kindSet[string(ev.Kind)] {
			continue
		}
		if truncatePayload > 0 && len(ev.Payload) > truncatePayload {
			truncated := string(ev.Payload[:truncatePayload]) + "…"
			if encoded, merr := json.Marshal(truncated); merr == nil {
				trunc := ev
				trunc.Payload = encoded
				ev = trunc
			}
		}
		filtered = append(filtered, ev)
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, lastTurn
}

// summarizeTrace builds the cheap structural digest: per-kind counts plus a
// compact projection of every error-kind event (its "error" payload field).
func summarizeTrace(history store.History) TraceSummary {
	sum := TraceSummary{ByKind: make(map[string]int)}
	for _, ev := range history {
		sum.ByKind[string(ev.Kind)]++
		if traceErrorKinds[ev.Kind] {
			sum.Errors = append(sum.Errors, TraceErrorDigest{
				Turn:    int64(ev.Turn),
				Kind:    string(ev.Kind),
				Message: payloadField(ev.Payload, "error"),
			})
		}
	}
	return sum
}

// payloadField pulls a single string field out of a raw JSON payload, returning
// "" when the payload is not an object or the field is absent/non-string.
func payloadField(payload json.RawMessage, field string) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	s, _ := m[field].(string)
	return s
}

// readTraceFile reads a JSONL trace into a store.History WITHOUT taking the
// JSONLSink's exclusive flock — the read must never block (or be blocked by) a
// live session writing the same path. Lines that don't parse as an Event are
// skipped (a partially-flushed tail line is normal for a live trace).
func readTraceFile(path string) (store.History, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	var history store.History
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev store.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerate a torn final line / non-event line
		}
		history = append(history, ev)
	}
	return history, nil
}

// resolveTracePathArg resolves a trace file from an explicit path, or the newest
// *.jsonl whose basename contains sessionID, under root (default
// ~/.kitsoki/sessions) optionally narrowed to the <root>/<app> subdir and/or
// traces whose world ticket_id matches ticketID. This mirrors resolveTraceArg in
// cmd/kitsoki/trace.go.
func resolveTracePathArg(root, path, sessionID, app, ticketID string) (string, error) {
	if path != "" {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path, nil
		}
		return "", fmt.Errorf("path %q is not a readable file", path)
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".kitsoki", "sessions")
	}
	searchDir := root
	if app != "" {
		searchDir = filepath.Join(root, app)
	}
	type cand struct {
		path string
		mod  int64
	}
	var cands []cand
	_ = filepath.WalkDir(searchDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if sessionID != "" && !strings.Contains(filepath.Base(p), sessionID) {
			return nil
		}
		if ticketID != "" {
			ok, e := tracePathMatchesTicket(p, ticketID)
			if e != nil || !ok {
				return nil
			}
		}
		if info, e := d.Info(); e == nil {
			cands = append(cands, cand{p, info.ModTime().UnixNano()})
		}
		return nil
	})
	if len(cands) == 0 {
		hint := "no trace found under " + searchDir
		if sessionID != "" {
			hint += fmt.Sprintf(" matching %q", sessionID)
		}
		if ticketID != "" {
			hint += fmt.Sprintf(" with ticket_id %q", ticketID)
		}
		return "", fmt.Errorf("%s (pass an explicit path, or run a session first)", hint)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod > cands[j].mod })
	return cands[0].path, nil
}

func tracePathMatchesTicket(path, ticketID string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	want := strings.TrimSpace(ticketID)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev store.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Kind != store.EffectApplied {
			continue
		}
		var p struct {
			Set map[string]any `json:"set"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil {
			continue
		}
		for k, v := range p.Set {
			if (k == "ticket_id" || strings.HasSuffix(k, "__ticket_id")) && strings.TrimSpace(fmt.Sprint(v)) == want {
				return true, nil
			}
		}
	}
	return false, nil
}
