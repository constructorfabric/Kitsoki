// Package mcp implements the MCP server exposing the `transition` tool to external
// MCP clients (Claude Desktop, Claude Code, etc.). It uses the official
// github.com/modelcontextprotocol/go-sdk v1.0.0. See docs/architecture/overview.md
// for where this server sits in the runtime.
//
// # Single `transition` tool
//
// kitsoki exposes one generic tool rather than per-intent tools. The intent and
// slots arrive as arguments; the server resolves the session, validates via the
// Machine, applies the transition, persists events, and returns TransitionOK or
// a structured error envelope (see the MCP validator in
// docs/guide/development/developer-guide.md).
//
// # Session identity
//
// For MVP, the caller supplies a `session_id` argument. A production-grade design
// would use MCP resource/session concepts; that is deferred.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// KitCaller is the narrow seam the generic `kit_call` tool needs from the
// installed-kit dispatcher (internal/kitendpoint.Dispatcher satisfies this).
// It is declared here, rather than this package importing kitendpoint
// directly, because internal/host already imports internal/mcp (for the
// agent-ask-with-mcp handler) and kitendpoint imports internal/host —
// importing kitendpoint here would be an import cycle. cmd/kitsoki (which can
// see every package) adapts a *kitendpoint.Dispatcher to this interface at
// wiring time; see internal/mcp's package doc.
type KitCaller interface {
	// Call invokes kit's iface.op with args and returns the same
	// (ok, data, errMsg) triple the JSON-RPC kit.<kit>.<iface>.<op> fallback
	// returns on its wire shape — err is reserved for resolution failures
	// (unknown kit/iface/op), errMsg for the handler's own reported Result.Error.
	Call(ctx context.Context, kit, iface, op string, args map[string]any) (ok bool, data map[string]any, errMsg string, err error)
}

// TransitionArgs is the typed input to the transition tool.
// The Go SDK auto-generates JSON Schema from struct tags.
// Note: the jsonschema tag here is just the description text (no "key=value" format).
type TransitionArgs struct {
	// Intent is the allowed intent name for the current state.
	Intent string `json:"intent" jsonschema:"allowed intent name for the current state"`
	// Slots holds intent-specific slot values; keys match the intent's declared slot names.
	Slots map[string]any `json:"slots,omitempty" jsonschema:"intent-specific slot values"`
	// Confidence is the LLM's self-reported extraction confidence (0–1, optional).
	Confidence float64 `json:"confidence,omitempty"`
	// SessionID identifies the kitsoki session to operate on.
	SessionID string `json:"session_id" jsonschema:"kitsoki session identifier"`
}

// TransitionOK is the success response of the transition tool.
// JSON tags are load-bearing: read by the LLM harness.
type TransitionOK struct {
	OK    bool     `json:"ok"`              // always true on this branch
	State string   `json:"state"`           // new current state path
	View  string   `json:"view"`            // rendered narrative
	Menu  []string `json:"menu"`            // currently-allowed intent names
	World any      `json:"world,omitempty"` // updated world snapshot (optional, for debugging)
}

// TransitionError is the structured error payload returned when a transition is
// rejected; see the MCP validator in docs/guide/development/developer-guide.md.
type TransitionError struct {
	OK    bool                    `json:"ok"` // always false on this branch
	Error *intent.ValidationError `json:"error"`
}

// ClarifyArgs is the typed input to the clarify tool.
type ClarifyArgs struct {
	// Question is the natural-language clarification to show the user.
	Question string `json:"question"`
	// Candidates lists intent candidates driving the disambiguation UI; see the
	// AMBIGUOUS_INTENT disambiguation card in docs/architecture/semantic-routing.md.
	Candidates []struct {
		Intent string `json:"intent"`
		Why    string `json:"why,omitempty"`
	} `json:"candidates,omitempty"`
}

// OffPathArgs is the typed input to the off_path tool.
type OffPathArgs struct {
	// Reason explains why the harness is triggering off-path mode.
	Reason string `json:"reason"`
}

// Server is the concrete MCP server wrapping the Go SDK.
// Construct with NewServer; serve with Serve or Run.
type Server struct {
	mcpSrv *mcpsdk.Server
	m      machine.Machine
	s      store.Store
	appDef *app.AppDef
	// kits is the installed-kit dispatcher backing the generic `kit_call`
	// tool (S3b). Nil (the default) means no kits are installed; kit_call
	// then returns a structured error rather than being omitted, since the
	// Go SDK tool list is static for the lifetime of the process (see
	// NewServer's doc comment on "no dynamic tool lists").
	kits KitCaller
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithKits attaches the installed-kit endpoint dispatcher (S3b — see
// internal/kitendpoint.Dispatcher, adapted to KitCaller by the caller),
// enabling the generic `kit_call` tool to actually resolve and invoke kit
// interface operations. Without it, kit_call reports a structured "no kits
// installed" error for every call.
func WithKits(kc KitCaller) Option {
	return func(s *Server) { s.kits = kc }
}

// NewServer constructs an MCP Server with the given Machine, Store, and AppDef.
// The server registers the `transition` tool and the generic `kit_call` tool
// (S3b — mirrors the JSON-RPC kit.<kit>.<iface>.<op> fallback so headless
// agents get the same kit extension surface; there is deliberately no
// per-kit/per-op tool registered here, only this one generic carrier) and is
// ready to call Serve or Run.
func NewServer(m machine.Machine, s store.Store, appDef *app.AppDef, opts ...Option) *Server {
	srv := &Server{m: m, s: s, appDef: appDef}
	for _, opt := range opts {
		opt(srv)
	}
	srv.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki",
		Version: "0.0.1",
	}, nil)

	// Register the single `transition` tool using the generic AddTool (typed args).
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "transition",
		Description: "Map the user utterance to one allowed intent with filled slots. Returns ok+next-state on success or a structured error envelope listing missing slots and suggestions.",
	}, srv.handleTransition)

	// Register the single generic `kit_call` tool (S3b/D2.3). One tool
	// handles every installed kit/interface/operation — no dynamic tool
	// lists, matching how `transition` already covers every intent.
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit_call",
		Description: "Invoke an installed kit's endpoint: {kit, iface, op, args}. Resolves the kit's declared host_interfaces and invokes the bound handler (Go host verb or starlark), transparently — the same mechanism the kit.<kit>.<iface>.<op> JSON-RPC fallback uses. Returns {ok, data?} or {ok:false, error}.",
	}, srv.handleKitCall)

	return srv
}

// Run starts the MCP server on the StdioTransport and blocks until the context
// is done or the peer disconnects. This is the main entry point for `kitsoki serve`.
func (srv *Server) Run(ctx context.Context) error {
	return srv.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Serve starts the MCP server on a custom r/w pair (for testing).
func (srv *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	return fmt.Errorf("mcp: Serve(r, w) not directly supported; use Run or Connect")
}

// Connect exposes the underlying mcpsdk.Server so callers can use
// mcpsdk.NewInMemoryTransports for in-process testing.
func (srv *Server) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return srv.mcpSrv.Connect(ctx, t, opts)
}

// handleTransition is the tool handler for the `transition` tool.
func (srv *Server) handleTransition(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args TransitionArgs,
) (*mcpsdk.CallToolResult, any, error) {
	// 1. Validate session ID.
	if args.SessionID == "" {
		ve := &intent.ValidationError{
			Code:    intent.ErrUnknownIntent,
			Message: "session_id is required",
		}
		return buildErrorResult(ve), nil, nil
	}

	sessionID := app.SessionID(args.SessionID)

	// 2. Load session history to reconstruct current state, world, and turn number.
	js, err := srv.resolveJourney(sessionID)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: resolve session %q: %w", sessionID, err)
	}

	// 3. Build the IntentCall.
	call := intent.IntentCall{
		Intent:     args.Intent,
		Slots:      world.Slots(args.Slots),
		Confidence: args.Confidence,
	}

	// 4. Validate via Machine (checks allowed intents + slot schema).
	vr := srv.m.Validate(js.State, js.World, call)
	if !vr.OK {
		return buildErrorResult(vr.Err), nil, nil
	}

	// 5. Apply transition.
	result, err := srv.m.Turn(ctx, js.State, js.World, vr.Accepted)
	if err != nil {
		return nil, nil, fmt.Errorf("mcp: machine.Turn: %w", err)
	}

	// 6. If the transition produced a validation error (guard failed, etc.), return it.
	if result.ValidationError != nil {
		return buildErrorResult(result.ValidationError), nil, nil
	}

	// 7. Assign turn numbers to events (next turn = last known turn + 1).
	nextTurn := js.Turn + 1
	for i := range result.Events {
		result.Events[i].Turn = nextTurn
	}

	// 8. Persist events via StoreSinkAdapter (wave-2a seam).
	sink := store.NewStoreSinkAdapter(srv.s, sessionID)
	if err := sink.AppendBatch(result.Events); err != nil {
		return nil, nil, fmt.Errorf("mcp: append events: %w", err)
	}

	// 9. Return success.
	ok := TransitionOK{
		OK:    true,
		State: string(result.NewState),
		View:  result.View,
		Menu:  result.Menu,
		World: result.World.Vars,
	}
	return nil, ok, nil
}

// KitCallArgs is the typed input to the generic `kit_call` tool (S3b).
type KitCallArgs struct {
	// Kit is the installed kit's short name (e.g. "object-graph").
	Kit string `json:"kit" jsonschema:"installed kit short name"`
	// Iface is the host_interface name one of the kit's provided stories declares.
	Iface string `json:"iface" jsonschema:"kit-declared host_interface name"`
	// Op is the operation name declared on that interface.
	Op string `json:"op" jsonschema:"operation name declared on the interface"`
	// Args holds the operation's invocation arguments.
	Args map[string]any `json:"args,omitempty" jsonschema:"operation invocation arguments"`
}

// KitCallResult is the response shape for `kit_call`, mirroring the JSON-RPC
// kit.<kit>.<iface>.<op> fallback's wire shape (internal/runstatus/server's
// dispatchKit) so the two carriers read identically to a caller.
type KitCallResult struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data,omitempty"`
	Error string         `json:"error,omitempty"`
}

// handleKitCall is the tool handler for the generic `kit_call` tool. It
// delegates entirely to the shared kitendpoint.Dispatcher — the same
// resolution + invocation path the JSON-RPC fallback uses — so a kit
// interface/operation is invoked and journaled identically regardless of
// which carrier (web JSON-RPC or MCP) an agent used to reach it.
func (srv *Server) handleKitCall(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitCallArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if srv.kits == nil {
		return buildKitCallError("kit_call: no kits installed on this server"), nil, nil
	}
	if args.Kit == "" || args.Iface == "" || args.Op == "" {
		return buildKitCallError("kit_call: kit, iface, and op are all required"), nil, nil
	}

	ok, data, errMsg, err := srv.kits.Call(ctx, args.Kit, args.Iface, args.Op, args.Args)
	if err != nil {
		return buildKitCallError(err.Error()), nil, nil
	}
	if !ok {
		return buildKitCallError(errMsg), nil, nil
	}
	return nil, KitCallResult{OK: true, Data: data}, nil
}

// buildKitCallError wraps a message into an IsError CallToolResult, mirroring
// buildErrorResult's shape for the transition tool.
func buildKitCallError(msg string) *mcpsdk.CallToolResult {
	payload := KitCallResult{OK: false, Error: msg}
	b, _ := json.Marshal(payload)
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}
}

// resolveJourney replays the event history for a session and returns the
// current JourneyState (state path, world snapshot, and last turn number).
func (srv *Server) resolveJourney(sessionID app.SessionID) (*store.JourneyState, error) {
	history, err := srv.s.LoadHistory(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	// Determine the initial state from the app definition.
	initialState := app.StatePath("")
	if root, ok := srv.appDef.Root.(string); ok {
		initialState = app.StatePath(root)
	}

	// Initialise world from schema defaults.
	initialWorld := machine.WorldFromSchema(srv.appDef.World)

	// Check if there is a snapshot to use as the base.
	snap, hasSnap, err := srv.s.LatestSnapshot(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load snapshot: %w", err)
	}
	if hasSnap {
		initialState = snap.StatePath
		var vars map[string]any
		if err := json.Unmarshal(snap.WorldJSON, &vars); err == nil {
			initialWorld = world.World{Vars: vars}
		}
	}

	js, err := store.BuildJourney(srv.appDef, initialState, initialWorld, history)
	if err != nil {
		return nil, fmt.Errorf("build journey: %w", err)
	}

	// If no transitions have been applied yet, use the initial state.
	if js.State == "" {
		js.State = initialState
	}

	return js, nil
}

// buildErrorResult wraps a ValidationError into a CallToolResult with IsError=true.
// The structured JSON payload is placed in Content so the LLM can self-correct.
func buildErrorResult(ve *intent.ValidationError) *mcpsdk.CallToolResult {
	payload := TransitionError{OK: false, Error: ve}
	b, _ := json.Marshal(payload)
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}
}
