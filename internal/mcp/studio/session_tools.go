package studio

// session_tools.go — the session.* and render.* tools (slice 7): the payoff
// facade that lets an external agent DRIVE a live (replay-default) session and
// SEE the result.
//
//   - session.new / session.attach — open a driving handle (the slice-2 drive
//     setup behind sessionRuntime; replay/none by default → no LLM).
//   - session.drive   — free text → orch.Turn (the ONE interpretive seam).
//   - session.submit  — a chosen intent + slots → orch.SubmitDirect.
//   - session.continue— missing slots for a pending clarify → orch.ContinueTurn.
//   - session.drive_operation — drive an active operation handle.
//   - session.teleport— inbox notification -> orch.Teleport, marking it read.
//   - session.inspect — state / world / allowed_intents / last_view / jobs / inbox / last_turns.
//   - session.command — run a safe TUI slash command and return its frame.
//   - session.trace   — the session's JSONL trace events.
//   - render.tui      — the slice-1 Frame {text, ansi, metadata} at any width.
//   - render.tui_png  — the slice-3 ANSI→PNG rasteriser, as an MCP image block.
//   - render.web      — the slice-4 headless web→PNG, as an MCP image block.
//
// Every drive/submit/continue returns BOTH the structured TurnOutcome AND the
// Frame (the agent reasons on metadata and sees the screen in one call). The
// render.* tools are READ-ONLY re-renders — they never advance the machine
// (principle of least surprise). render.tui_png/render.web also accept an
// explicit spec ({story_path, state, world?}) so the agent can photograph a
// state it never drove to, headlessly, without touching any session.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/tui"
	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/tui/shot"
	"kitsoki/internal/webshot"
)

// defaultCols / defaultRows are the frame geometry render.* and the drive
// family fall back to when the caller omits cols/rows — the same 100×30
// `kitsoki drive` defaults so a studio frame matches a CLI frame byte-for-byte.
const (
	defaultCols            = 100
	defaultRows            = 30
	defaultDriveAsyncAfter = 25 * time.Second
)

func driveAsyncAfter(ms int) time.Duration {
	if ms < 0 {
		return 0
	}
	if ms == 0 {
		return defaultDriveAsyncAfter
	}
	return time.Duration(ms) * time.Millisecond
}

// registerStrictSessionDriverTools exposes the small direct-submit control loop
// needed to supervise a session from the strict operating-system profile. It
// intentionally excludes free-text session.drive and the broader legacy
// session/render toolbox: strict callers select explicit intents, poll bounded
// state, retain trace evidence, and answer an attached operator question.
func (srv *Server) registerStrictSessionDriverTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.new",
		Description: "Open a new driving session for a story. {story_path, harness?:replay|live, cassette?, host_cassette?, trace?, profile?}. Defaults to harness:replay (no LLM); a replay miss is a hard error, never a silent live call. cassette is a routing recording; host_cassette stubs host.* calls. profile selects a configured harness backend (synthetic, codex, …) for a live session. initial_world seeds story world vars for a headless parameterized drive. Returns {handle, state}.",
	}, srv.handleSessionNew)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.submit",
		Description: "Submit a chosen intent (a menu pick) directly, with no routing. {handle, intent, slots?, cols?, rows?, async_after_ms?}. Returns {outcome, frame}, {awaiting_operator}, or {running}; when running is returned, poll session.status until running disappears. The frame does NOT carry world — read it with session.world (one value or the key list) or session.inspect (full snapshot).",
	}, srv.handleSessionSubmit)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.answer",
		Description: "Answer a parked operator-ask (the suspend/resume fallback for clients without MCP elicitation). {handle, question_id, answers, async_after_ms?} where answers is keyed by each question's text → a chosen option label or custom answer (string), or labels ([]string). Resumes the turn; returns {outcome, frame}, {running}, or another awaiting_operator.",
	}, srv.handleSessionAnswer)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.status",
		Description: "Compact, overflow-proof snapshot of a driving handle: {handle} → {state, allowed_intents, running?, status?, last_error?, exit?}. Never embeds world or rendered views. While an async turn runs, running is present and state reports the LIVE in-flight state path (with running.last_event_at_unix_micro advancing as the turn works) — a stable state with running present is normal progress, NOT a stuck run; only treat it as stuck if last_event_at stops advancing for many minutes. status/last_error/exit are read from well-known world keys when present.",
	}, srv.handleSessionStatus)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.inspect",
		Description: "Read-only snapshot of a driving handle: {handle, omit_world?, max_value_len?} → {state, world, allowed_intents, last_view, async, jobs[], notifications[], pending_drives[], backgrounded_chats[], operator_questions[], mining_proposals[], last_turns[]}. Never advances the machine. omit_world:true drops the world map entirely; max_value_len:N truncates each world value to N chars (with '…' marker). For one value (not the whole map) prefer session.world.",
	}, srv.handleSessionInspect)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.world",
		Description: "Read world WITHOUT dumping the whole map — the targeted alternative to session.inspect for deep-import rooms where the flat world is hundreds of keys. {handle, key?, max_value_len?}: with key → {ok, key, value, found} (the raw typed value, untruncated unless max_value_len is set); without key → {ok, keys:[…]} (sorted key NAMES only, no values) so you can discover what to fetch cheaply. READ-ONLY: never advances the machine.",
	}, srv.handleSessionWorld)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.trace",
		Description: "Read the handle's JSONL trace events. {handle, since?, until?, limit?, truncate_payload?, kinds?} → {events[], last_turn}. since/until filter by turn number; limit keeps the last N; truncate_payload:N caps each event payload to N chars (defaults to 500 when unset; pass 0 to disable); kinds filters to specific event kinds. Read-only.",
	}, srv.handleSessionTrace)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.close",
		Description: "Close a driving session and release its trace-path exclusive lock so the same trace path can be reopened. {handle} → {ok, handle}. Without this, a stale live session squats its trace-path flock for the server-process lifetime and bricks any rerun on that path.",
	}, srv.handleSessionClose)
}

// registerSessionTools wires the complete legacy session.* and render.* toolbox
// onto the server. Called from NewServer after story.* tools so they share one
// registry.
func (srv *Server) registerSessionTools() {
	srv.registerStrictSessionDriverTools()

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.attach",
		Description: "Co-drive an existing keyed session via the external-attach bridge. {story_path, key, harness?, cassette?, host_cassette?, trace?, profile?}. Returns {handle, state}.",
	}, srv.handleSessionAttach)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.drive",
		Description: "Submit FREE TEXT to a driving handle; it is routed through the orchestrator turn loop (the one interpretive seam). {handle, input, cols?, rows?, async_after_ms?}. Returns {outcome, frame}, {awaiting_operator}, or {running}; when running is returned, poll session.status until running disappears. The frame does NOT carry world; read it on demand with session.world or session.inspect.",
	}, srv.handleSessionDrive)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.drive_operation",
		Description: "Drive the active autonomous/supervised operation handle without routing free text. {handle, cols?, rows?, async_after_ms?}. Returns {operation_drive, outcome, frame} when settled, or {running}; when running is returned, poll session.status until running disappears. Stops at terminal/waiting handles, manual-only policies, clarification/rejection, or when no safe driver intent exists.",
	}, srv.handleSessionDriveOperation)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.continue",
		Description: "Supply missing slots for a pending clarification. {handle, slots, cols?, rows?, async_after_ms?}. Returns {outcome, frame}, {awaiting_operator}, or {running}; when running is returned, poll session.status until running disappears. The frame does NOT carry world — read it with session.world (one value or the key list) or session.inspect (full snapshot).",
	}, srv.handleSessionContinue)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.teleport",
		Description: "Reacquire an inbox notification by teleporting the session to its saved target. {handle, notification_id, cols?, rows?}. Marks the notification read and returns {outcome, frame}.",
	}, srv.handleSessionTeleport)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "session.command",
		Description: "Run a TUI slash command against a driving handle and return the rendered frame. {handle, command, cols?, rows?}. Rejects slash commands that require async terminal side effects.",
	}, srv.handleSessionCommand)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "render.tui",
		Description: "Re-render a state as the slice-1 Frame {text, ansi, metadata} at any width. {handle | story_path+state+world?, cols?, rows?}. READ-ONLY: never advances a session.",
	}, srv.handleRenderTUI)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "render.tui_png",
		Description: "Rasterise the terminal Frame to a PNG (monospace + ANSI colour). {handle | story_path+state+world?, cols?, rows?, theme?}. Returns the Frame.text plus an MCP image block for vision-capable clients. READ-ONLY.",
	}, srv.handleRenderTUIPNG)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "render.web",
		Description: "Render the REAL kitsoki web view of a state to a PNG. {handle | story_path+state+world?, query?, assert_text?}. Returns text plus an MCP image block for vision-capable clients. READ-ONLY. Requires a browser-capable host (degrades to text otherwise).",
	}, srv.registerWebRenderer())
}

// ── wire shapes ───────────────────────────────────────────────────────────────

// SessionNewArgs is the input to session.new.
type SessionNewArgs struct {
	StoryPath string `json:"story_path"`
	// Harness is replay (default, no LLM) or live (opt-in). Empty → replay.
	Harness string `json:"harness,omitempty"`
	// Cassette is the replay recording for a replay-mode handle.
	Cassette string `json:"cassette,omitempty"`
	// HostCassette is a host_cassette file that stubs host.* calls while the
	// selected harness still owns free-text routing (or direct-submit replay).
	HostCassette string `json:"host_cassette,omitempty"`
	// Trace overrides the JSONL trace path (default: a fresh temp trace).
	Trace string `json:"trace,omitempty"`
	// Key requests a specific handle key (default: auto-assigned s<N>).
	Key string `json:"key,omitempty"`
	// Profile selects the operator-declared harness profile (synthetic, codex, …)
	// the live session routes agent dispatch through. Empty → the server's default
	// profile; ignored when no profiles are configured.
	Profile string `json:"profile,omitempty"`
	// InitialWorld seeds session world vars before the first on_enter — the studio
	// twin of a flow fixture's initial_world:. Parameterizes a story for a headless
	// drive (e.g. seeding a ticket into the bugfix pipeline). Omit for none.
	InitialWorld map[string]any `json:"initial_world,omitempty"`
}

// SessionAttachArgs is the input to session.attach.
type SessionAttachArgs struct {
	StoryPath    string `json:"story_path"`
	Key          string `json:"key"`
	Harness      string `json:"harness,omitempty"`
	Cassette     string `json:"cassette,omitempty"`
	HostCassette string `json:"host_cassette,omitempty"`
	Trace        string `json:"trace,omitempty"`
	// Profile selects the harness profile (see SessionNewArgs.Profile).
	Profile string `json:"profile,omitempty"`
}

// SessionOpenOK is the session.new / session.attach result.
type SessionOpenOK struct {
	OK     bool   `json:"ok"`     // always true on this branch
	Handle string `json:"handle"` // the new driving handle key
	State  string `json:"state"`  // the session's current state after open
	Mode   string `json:"mode"`   // the resolved harness mode (replay|live)
}

// SessionDriveArgs is the input to session.drive.
type SessionDriveArgs struct {
	Handle       string `json:"handle"`
	Input        string `json:"input"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	AsyncAfterMS int    `json:"async_after_ms,omitempty"`
}

// SessionSubmitArgs is the input to session.submit.
type SessionSubmitArgs struct {
	Handle       string         `json:"handle"`
	Intent       string         `json:"intent"`
	Slots        map[string]any `json:"slots,omitempty"`
	Cols         int            `json:"cols,omitempty"`
	Rows         int            `json:"rows,omitempty"`
	AsyncAfterMS int            `json:"async_after_ms,omitempty"`
}

// SessionDriveOperationArgs is the input to session.drive_operation.
type SessionDriveOperationArgs struct {
	Handle       string `json:"handle"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	AsyncAfterMS int    `json:"async_after_ms,omitempty"`
}

// SessionContinueArgs is the input to session.continue.
type SessionContinueArgs struct {
	Handle       string         `json:"handle"`
	Slots        map[string]any `json:"slots"`
	Cols         int            `json:"cols,omitempty"`
	Rows         int            `json:"rows,omitempty"`
	AsyncAfterMS int            `json:"async_after_ms,omitempty"`
}

// SessionCommandArgs is the input to session.command.
type SessionCommandArgs struct {
	Handle  string `json:"handle"`
	Command string `json:"command"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// TurnResult is the structured TurnOutcome projection returned alongside the
// Frame on every drive/submit/continue. It carries exactly the fields the
// proposal names — mode, the new state, the allowed-intent menu, the missing
// slots — so the agent can reason on metadata without re-parsing the screen.
type TurnResult struct {
	Mode           string         `json:"mode"`                      // transitioned|clarify|rejected|completed|offpath|cancelled
	State          string         `json:"state,omitempty"`           // the state after the turn (also in frame.metadata.state)
	AllowedIntents []string       `json:"allowed_intents,omitempty"` // the next menu
	SlotsNeeded    []SlotNeedItem `json:"slots_needed,omitempty"`    // missing required slots (ModeClarify)
	PendingIntent  string         `json:"pending_intent,omitempty"`  // intent awaiting slot completion
	ErrorCode      string         `json:"error_code,omitempty"`      // rejection code (ModeRejected)
	ErrorMessage   string         `json:"error_message,omitempty"`   // human-readable rejection
	GuardHint      string         `json:"guard_hint,omitempty"`      // author guard hint (ModeRejected)
	HarnessError   string         `json:"harness_error,omitempty"`   // dispatch-loop failure, if any
	TurnNumber     int64          `json:"turn_number,omitempty"`     // the turn that just completed
	// Error is set when the turn itself failed (e.g. a replay miss). It is NOT a
	// transport error — it rides back here so the agent sees the failure and the
	// frame together. Mode is "error" in that case.
	Error string `json:"error,omitempty"`
}

// SlotNeedItem is one missing slot in a clarify outcome.
type SlotNeedItem struct {
	Name        string   `json:"name"`
	Prompt      string   `json:"prompt,omitempty"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Values      []string `json:"values,omitempty"`
}

// FrameResult is the slice-1 Frame projected to the wire. It mirrors tui.Frame
// (the field names tui.Frame marshals to) so render.tui and the drive family
// return the identical screen the CLI does.
type FrameResult struct {
	Text     string        `json:"text"`
	Width    int           `json:"width"`
	Height   int           `json:"height"`
	Metadata FrameMetaItem `json:"metadata"`
}

// FrameMetaItem mirrors tui.FrameMeta on the wire.
type FrameMetaItem struct {
	State          string         `json:"state"`
	Mode           string         `json:"mode"`
	AllowedIntents []string       `json:"allowed_intents,omitempty"`
	WorldDigest    map[string]any `json:"world_digest,omitempty"`
}

// TurnResponse is the {outcome, frame} pair every drive/submit/continue returns.
// When a driven turn parks on an operator-ask under the session.answer fallback,
// AwaitingOperator is set INSTEAD of a settled outcome+frame: the client must
// call session.answer {handle, question_id, answers} to resume the turn.
type TurnResponse struct {
	OK      bool        `json:"ok"`
	Outcome TurnResult  `json:"outcome"`
	Frame   FrameResult `json:"frame"`
	// OperationDrive is present only for session.drive_operation and summarizes
	// the autonomous driver loop that produced Outcome/Frame.
	OperationDrive *OperationDriveResult `json:"operation_drive,omitempty"`
	// AwaitingOperator is non-nil when the turn paused on an operator-ask (the
	// fallback path). Outcome/Frame are zero in that case — the turn has not
	// settled yet.
	AwaitingOperator *AwaitingOperator `json:"awaiting_operator,omitempty"`
	// Running is non-nil when the turn is still executing in the background.
	// Poll session.status/session.inspect or the trace; the session model is
	// folded as soon as the background turn settles.
	Running *RunningDrive `json:"running,omitempty"`
}

// OperationDriveResult is the wire summary for one explicit operation drive.
type OperationDriveResult struct {
	Turns      int    `json:"turns"`
	StopReason string `json:"stop_reason,omitempty"`
	LastIntent string `json:"last_intent,omitempty"`
}

// AwaitingOperator is the suspend/resume status carried on a session.drive /
// session.answer result when the turn paused on an operator-ask. The client
// answers question_id (with answers keyed by each question's text) via
// session.answer to resume the parked turn.
type AwaitingOperator struct {
	QuestionID string                           `json:"question_id"`
	Questions  []kitsokimcp.OperatorAskQuestion `json:"questions"`
}

// RunningDrive tells MCP clients that session.drive accepted the turn but
// returned early before it settled, avoiding client-side tool-call timeouts on
// long live agent work.
type RunningDrive struct {
	Handle             string `json:"handle"`
	Input              string `json:"input,omitempty"`
	StartedAtUnixMicro int64  `json:"started_at_unix_micro,omitempty"`
	Poll               string `json:"poll"`
	// InFlightState is the live state path the running turn is currently
	// executing in (e.g. "bf.reproducing"), read from the most recent trace
	// event — NOT the resting state the turn was submitted from. Present
	// whenever the running turn has written at least one event (never-silent:
	// a poller must be able to see that work is happening and where).
	InFlightState string `json:"in_flight_state,omitempty"`
	// LastEventKind / LastEventAtUnixMicro identify the most recent trace
	// activity (e.g. "agent.stream"), so a poller can distinguish an actively
	// working turn from a genuinely wedged one by watching the timestamp move.
	LastEventKind        string `json:"last_event_kind,omitempty"`
	LastEventAtUnixMicro int64  `json:"last_event_at_unix_micro,omitempty"`
	// ActiveAgent is the agent currently dispatched inside the running turn
	// (empty between dispatches).
	ActiveAgent string `json:"active_agent,omitempty"`
}

// SessionAnswerArgs is the input to session.answer (the fallback resume).
type SessionAnswerArgs struct {
	Handle       string         `json:"handle"`
	QuestionID   string         `json:"question_id"`
	Answers      map[string]any `json:"answers"`
	Cols         int            `json:"cols,omitempty"`
	Rows         int            `json:"rows,omitempty"`
	AsyncAfterMS int            `json:"async_after_ms,omitempty"`
}

// SessionTeleportArgs is the input to session.teleport.
type SessionTeleportArgs struct {
	Handle         string `json:"handle"`
	NotificationID string `json:"notification_id"`
	Cols           int    `json:"cols,omitempty"`
	Rows           int    `json:"rows,omitempty"`
}

// ── session.new / attach ─────────────────────────────────────────────────────

// handleSessionNew opens a fresh driving handle for a story. It resolves the
// harness mode (replay default → no LLM), picks a trace path (a fresh temp file
// when unset), and wires the sessionRuntime through OpenDrivingSession.
func (srv *Server) handleSessionNew(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionNewArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.StoryPath == "" {
		return buildToolError(ErrBadRequest, "session.new: story_path is required"), nil, nil
	}
	tracePath, err := resolveTracePath(args.Trace, args.StoryPath, args.Key)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	// Publish KITSOKI_APP_DIR BEFORE the story loads so a relative
	// host.starlark.run script (or any other ${KITSOKI_APP_DIR}-relative
	// reference) resolves for this session — see app_dir.go.
	publishAppDir(args.StoryPath)
	sh, err := srv.sess.OpenDrivingSession(ctx, OpenDrivingSessionParams{
		Key:            args.Key,
		Mode:           HarnessMode(args.Harness),
		RecordingPath:  args.Cassette,
		HostCassette:   args.HostCassette,
		StoryPath:      args.StoryPath,
		TracePath:      tracePath,
		Profile:        args.Profile,
		InitialWorld:   args.InitialWorld,
		ImportResolver: srv.importResolver,
	})
	if err != nil {
		code, msg := AsToolError(err)
		return buildToolError(code, msg), nil, nil
	}
	return nil, SessionOpenOK{
		OK:     true,
		Handle: sh.Key,
		State:  string(sh.Runtime.orch.InitialState()),
		Mode:   string(sh.Mode),
	}, nil
}

// handleSessionAttach co-drives an existing keyed session. The studio holds its
// own in-process driving runtime per handle (epic decision: one server process
// per client, handles in-process); attach binds a NEW runtime to the story and
// records the external key on the trace so a separate process driving the same
// key shares the persisted journey under the writer lock (the
// hybrid-session-driving guarantee). The bind reuses OpenDrivingSession with the
// key as the handle key, so the agent addresses it the same way as a fresh one.
func (srv *Server) handleSessionAttach(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionAttachArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.StoryPath == "" {
		return buildToolError(ErrBadRequest, "session.attach: story_path is required"), nil, nil
	}
	if args.Key == "" {
		return buildToolError(ErrBadRequest, "session.attach: key is required"), nil, nil
	}
	tracePath, err := resolveTracePath(args.Trace, args.StoryPath, args.Key)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	// Publish KITSOKI_APP_DIR BEFORE the story loads — see handleSessionNew /
	// app_dir.go.
	publishAppDir(args.StoryPath)
	sh, err := srv.sess.OpenDrivingSession(ctx, OpenDrivingSessionParams{
		Key:            args.Key,
		Mode:           HarnessMode(args.Harness),
		RecordingPath:  args.Cassette,
		HostCassette:   args.HostCassette,
		StoryPath:      args.StoryPath,
		TracePath:      tracePath,
		Profile:        args.Profile,
		ImportResolver: srv.importResolver,
	})
	if err != nil {
		code, msg := AsToolError(err)
		return buildToolError(code, msg), nil, nil
	}
	return nil, SessionOpenOK{
		OK:     true,
		Handle: sh.Key,
		State:  string(sh.Runtime.orch.InitialState()),
		Mode:   string(sh.Mode),
	}, nil
}

// ── drive / submit / continue ────────────────────────────────────────────────

func (srv *Server) handleSessionDrive(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionDriveArgs,
) (*mcpsdk.CallToolResult, any, error) {
	progress := newMCPProgress(req, "session.drive")
	progress.Start(ctx, args.Handle)
	if progress != nil {
		ctx = host.WithStreamSink(ctx, progress)
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)

	// Make the driving MCP client the OPERATOR: install a host.OperatorPrompter
	// so a dispatched sub-agent's mcp__operator__ask is auto-attached and
	// forwarded to this client (rather than the headless "proceed on your own"
	// path). Elicitation when the client advertises it (one blocking drive call);
	// otherwise the session.answer suspend/resume fallback.
	if ss := serverSessionOf(req); clientSupportsElicitation(ss) {
		prompter := newStudioOperatorPrompter(&elicitTransport{ss: ss})
		out, frame := rt.driveElicit(ctx, args.Input, cols, rows, prompter)
		progress.Done(ctx, args.Handle, turnOutcomeState(out))
		if rt.lastOperationDrive != nil {
			return nil, operationDriveResponse(turnResult{
				outcome:        out,
				frame:          frame,
				err:            rt.lastTurnErr,
				operationDrive: rt.lastOperationDrive,
			}), nil
		}
		return nil, turnResponse(out, frame, rt.lastTurnErr), nil
	}

	wait := driveAsyncAfter(args.AsyncAfterMS)
	res, pq, turnDone, running, err := rt.driveSuspendable(ctx, args.Input, cols, rows, wait)
	if err != nil {
		progress.Error(ctx, args.Handle, err)
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.drive: %v", err)), nil, nil
	}
	if running != nil {
		return nil, runningResponse(args.Handle, args.Input, running), nil
	}
	if !turnDone {
		progress.AwaitingOperator(ctx, args.Handle)
		return nil, awaitingResponse(pq), nil
	}
	progress.Done(ctx, args.Handle, turnOutcomeState(res.outcome))
	if res.operationDrive != nil {
		return nil, operationDriveResponse(res), nil
	}
	return nil, turnResponse(res.outcome, res.frame, res.err), nil
}

// serverSessionOf extracts the studio's ServerSession (the connection to the
// driving client) from a tool-call request, or nil for an in-process test
// without one. The elicit transport sends server→client elicitation over it.
func serverSessionOf(req *mcpsdk.CallToolRequest) *mcpsdk.ServerSession {
	if req == nil {
		return nil
	}
	return req.Session
}

// handleSessionAnswer is the fallback resume: it delivers the operator's answer
// to a parked operator-ask and blocks until the turn completes or parks on the
// next question, returning {outcome, frame} or another awaiting_operator.
func (srv *Server) handleSessionAnswer(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionAnswerArgs,
) (*mcpsdk.CallToolResult, any, error) {
	progress := newMCPProgress(req, "session.answer")
	progress.Start(ctx, args.Handle)
	if progress != nil {
		ctx = host.WithStreamSink(ctx, progress)
	}
	if args.QuestionID == "" {
		return buildToolError(ErrBadRequest, "session.answer: question_id is required"), nil, nil
	}
	if len(args.Answers) == 0 {
		return buildToolError(ErrBadRequest, "session.answer: answers is required"), nil, nil
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	wait := driveAsyncAfter(args.AsyncAfterMS)
	res, pq, turnDone, running, ok, err := rt.resumeSuspendable(ctx, args.QuestionID, args.Answers, wait)
	if err != nil {
		progress.Error(ctx, args.Handle, err)
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.answer: %v", err)), nil, nil
	}
	if !ok {
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.answer: no turn awaiting question_id %q on this handle", args.QuestionID)), nil, nil
	}
	if running != nil {
		return nil, runningResponse(args.Handle, "answer:"+args.QuestionID, running), nil
	}
	if !turnDone {
		progress.AwaitingOperator(ctx, args.Handle)
		return nil, awaitingResponse(pq), nil
	}
	progress.Done(ctx, args.Handle, turnOutcomeState(res.outcome))
	if res.operationDrive != nil {
		return nil, operationDriveResponse(res), nil
	}
	return nil, turnResponse(res.outcome, res.frame, res.err), nil
}

// awaitingResponse projects a parked question into the awaiting_operator wire
// shape (the turn has not settled; the client must session.answer to resume).
func awaitingResponse(pq *pendingQuestion) TurnResponse {
	ao := awaitingOperator(pq)
	return TurnResponse{OK: true, AwaitingOperator: &ao}
}

func runningResponse(handle, input string, r *runningDrive) TurnResponse {
	return TurnResponse{
		OK:      true,
		Running: runningDriveWire(handle, input, r),
	}
}

func runningDriveWire(handle, fallbackInput string, r *runningDrive) *RunningDrive {
	if r == nil {
		return nil
	}
	input := r.input
	if input == "" {
		input = fallbackInput
	}
	return &RunningDrive{
		Handle:             handle,
		Input:              input,
		StartedAtUnixMicro: r.startedAt.UnixMicro(),
		Poll:               "session.status",
	}
}

func (srv *Server) handleSessionSubmit(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionSubmitArgs,
) (*mcpsdk.CallToolResult, any, error) {
	progress := newMCPProgress(req, "session.submit")
	progress.Start(ctx, args.Handle)
	if progress != nil {
		ctx = host.WithStreamSink(ctx, progress)
	}
	if args.Intent == "" {
		return buildToolError(ErrBadRequest, "session.submit: intent is required"), nil, nil
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	wait := driveAsyncAfter(args.AsyncAfterMS)
	res, pq, turnDone, running, err := rt.submitSuspendable(ctx, args.Intent, args.Slots, cols, rows, wait)
	if err != nil {
		progress.Error(ctx, args.Handle, err)
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.submit: %v", err)), nil, nil
	}
	if running != nil {
		return nil, runningResponse(args.Handle, "intent:"+args.Intent, running), nil
	}
	if !turnDone {
		progress.AwaitingOperator(ctx, args.Handle)
		return nil, awaitingResponse(pq), nil
	}
	progress.Done(ctx, args.Handle, turnOutcomeState(res.outcome))
	if res.operationDrive != nil {
		return nil, operationDriveResponse(res), nil
	}
	return nil, turnResponse(res.outcome, res.frame, res.err), nil
}

func (srv *Server) handleSessionDriveOperation(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionDriveOperationArgs,
) (*mcpsdk.CallToolResult, any, error) {
	progress := newMCPProgress(req, "session.drive_operation")
	progress.Start(ctx, args.Handle)
	if progress != nil {
		ctx = host.WithStreamSink(ctx, progress)
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	wait := driveAsyncAfter(args.AsyncAfterMS)
	res, pq, turnDone, running, err := rt.driveOperationSuspendable(ctx, cols, rows, wait)
	if err != nil {
		progress.Error(ctx, args.Handle, err)
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.drive_operation: %v", err)), nil, nil
	}
	if running != nil {
		return nil, runningResponse(args.Handle, "operation", running), nil
	}
	if !turnDone {
		progress.AwaitingOperator(ctx, args.Handle)
		return nil, awaitingResponse(pq), nil
	}
	progress.Done(ctx, args.Handle, turnOutcomeState(res.outcome))
	return nil, operationDriveResponse(res), nil
}

func (srv *Server) handleSessionContinue(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionContinueArgs,
) (*mcpsdk.CallToolResult, any, error) {
	progress := newMCPProgress(req, "session.continue")
	progress.Start(ctx, args.Handle)
	if progress != nil {
		ctx = host.WithStreamSink(ctx, progress)
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	wait := driveAsyncAfter(args.AsyncAfterMS)
	res, pq, turnDone, running, err := rt.continueSuspendable(ctx, args.Slots, cols, rows, wait)
	if err != nil {
		progress.Error(ctx, args.Handle, err)
		return buildToolError(ErrBadRequest, fmt.Sprintf("session.continue: %v", err)), nil, nil
	}
	if running != nil {
		return nil, runningResponse(args.Handle, "continue", running), nil
	}
	if !turnDone {
		progress.AwaitingOperator(ctx, args.Handle)
		return nil, awaitingResponse(pq), nil
	}
	progress.Done(ctx, args.Handle, turnOutcomeState(res.outcome))
	if res.operationDrive != nil {
		return nil, operationDriveResponse(res), nil
	}
	return nil, turnResponse(res.outcome, res.frame, res.err), nil
}

func (srv *Server) handleSessionTeleport(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionTeleportArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.NotificationID == "" {
		return buildToolError(ErrBadRequest, "session.teleport: notification_id is required"), nil, nil
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	out, frame := rt.teleport(ctx, args.NotificationID, cols, rows)
	return nil, turnResponse(out, frame, rt.lastTurnErr), nil
}

// ── inspect / trace ──────────────────────────────────────────────────────────

// SessionStatusArgs is the input to session.status.
type SessionStatusArgs struct {
	Handle string `json:"handle"`
}

// SessionStatusResult is the compact session.status snapshot. It never embeds
// the full world — only the well-known keys (status, last_error, exit) when
// present — so it stays small regardless of how large the world grows.
type SessionStatusResult struct {
	OK             bool     `json:"ok"`
	State          string   `json:"state"`
	AllowedIntents []string `json:"allowed_intents"`
	// Running is present when a previous session.drive accepted a turn and
	// returned early after its bounded wait while the turn continues in the
	// background.
	Running *RunningDrive `json:"running,omitempty"`
	// Status is world["status"] when present (a story-level status string).
	Status string `json:"status,omitempty"`
	// LastError is world["last_error"] when present (the last agent/host error).
	LastError string `json:"last_error,omitempty"`
	// Exit is world["exit"] when present (a story-level exit/completion marker).
	Exit any `json:"exit,omitempty"`
}

// SessionInspectArgs is the input to session.inspect.
type SessionInspectArgs struct {
	Handle      string `json:"handle"`
	LastTurns   int    `json:"last_turns,omitempty"`
	OmitWorld   bool   `json:"omit_world,omitempty"`
	MaxValueLen int    `json:"max_value_len,omitempty"`
}

// InspectResult is the session.inspect snapshot. State/world/allowed_intents
// come from the same orchestrator reads buildInspectOutput uses (LoadJourney +
// AllowedIntents), so it matches the CLI inspect for that state.
type InspectResult struct {
	OK                bool                   `json:"ok"`
	State             string                 `json:"state"`
	World             map[string]any         `json:"world,omitempty"`
	AllowedIntents    []string               `json:"allowed_intents"`
	LastView          string                 `json:"last_view"`
	Async             *AsyncInspectSummary   `json:"async,omitempty"`
	Running           *RunningDrive          `json:"running,omitempty"`
	Jobs              []JobInspectItem       `json:"jobs,omitempty"`
	Notifications     []InboxInspectItem     `json:"notifications,omitempty"`
	PendingDrives     []PendingDriveItem     `json:"pending_drives,omitempty"`
	BackgroundedChats []BackgroundedChatItem `json:"backgrounded_chats,omitempty"`
	OperatorQuestions []OperatorQuestionItem `json:"operator_questions,omitempty"`
	MiningProposals   []MiningProposalItem   `json:"mining_proposals,omitempty"`
	LastTurns         []TurnSummaryItem      `json:"last_turns"`
}

// AsyncInspectSummary gives clients a cheap priority signal before they inspect
// the detailed job/notification rows.
type AsyncInspectSummary struct {
	JobsTotal                   int `json:"jobs_total"`
	JobsRunning                 int `json:"jobs_running"`
	JobsAwaitingInput           int `json:"jobs_awaiting_input"`
	JobsTerminal                int `json:"jobs_terminal"`
	NotificationsTotal          int `json:"notifications_total"`
	NotificationsUnread         int `json:"notifications_unread"`
	NotificationsActionRequired int `json:"notifications_action_required"`
	PendingDrives               int `json:"pending_drives"`
	DispatchingDrives           int `json:"dispatching_drives"`
	FailedDrives                int `json:"failed_drives"`
	BackgroundedChats           int `json:"backgrounded_chats"`
	OperatorQuestions           int `json:"operator_questions"`
	MiningProposals             int `json:"mining_proposals"`
	RunningDrive                int `json:"running_drive"`
}

// JobInspectItem is a compact, structured projection of one background job.
// It lets MCP clients see running, awaiting-input, and terminal work without
// scraping the TUI frame or decoding trace internals.
type JobInspectItem struct {
	ID                  string                    `json:"id"`
	Kind                string                    `json:"kind"`
	Status              jobs.JobStatus            `json:"status"`
	OriginState         string                    `json:"origin_state"`
	OriginProposalID    string                    `json:"origin_proposal_id,omitempty"`
	Error               string                    `json:"error,omitempty"`
	RetryCount          int                       `json:"retry_count,omitempty"`
	ClarificationSchema *jobs.ClarificationSchema `json:"clarification_schema,omitempty"`
	CreatedAtUnixMilli  int64                     `json:"created_at_unix_milli"`
	UpdatedAtUnixMilli  int64                     `json:"updated_at_unix_milli"`
	StartedAtUnixMilli  int64                     `json:"started_at_unix_milli,omitempty"`
	FinishedAtUnixMilli int64                     `json:"finished_at_unix_milli,omitempty"`
}

// InboxInspectItem is a compact projection of one non-dismissed inbox
// notification for the session, newest first.
type InboxInspectItem struct {
	ID                 string                    `json:"id"`
	Severity           jobs.NotificationSeverity `json:"severity"`
	Title              string                    `json:"title"`
	Body               string                    `json:"body,omitempty"`
	CreatedAtUnixMilli int64                     `json:"created_at_unix_milli"`
	ReadAtUnixMilli    int64                     `json:"read_at_unix_milli,omitempty"`
	TeleportState      string                    `json:"teleport_state,omitempty"`
	TeleportSlots      map[string]any            `json:"teleport_slots,omitempty"`
	TeleportProposalID string                    `json:"teleport_proposal_id,omitempty"`
	TeleportJobID      string                    `json:"teleport_job_id,omitempty"`
	OriginKind         string                    `json:"origin_kind"`
	OriginRef          string                    `json:"origin_ref"`
	OriginURL          string                    `json:"origin_url,omitempty"`
}

// PendingDriveItem is one pending or dispatching chat-input-queue row owned by
// this session. These are resumable async chat turns.
type PendingDriveItem struct {
	DriveID               string               `json:"drive_id"`
	ChatID                string               `json:"chat_id"`
	Transport             chats.DriveTransport `json:"transport"`
	Status                chats.DriveStatus    `json:"status"`
	Actor                 string               `json:"actor,omitempty"`
	Thread                string               `json:"thread,omitempty"`
	CorrelationID         string               `json:"correlation_id,omitempty"`
	Payload               string               `json:"payload,omitempty"`
	ErrorMessage          string               `json:"error_message,omitempty"`
	OriginState           string               `json:"origin_state,omitempty"`
	ReceivedAtUnixMicro   int64                `json:"received_at_unix_micro"`
	DispatchedAtUnixMicro int64                `json:"dispatched_at_unix_micro,omitempty"`
	CompletedAtUnixMicro  int64                `json:"completed_at_unix_micro,omitempty"`
}

// BackgroundedChatItem is one tmux-hosted chat that remains alive in
// pty_background mode and belongs to this session.
type BackgroundedChatItem struct {
	ChatID              string `json:"chat_id"`
	TmuxSession         string `json:"tmux_session"`
	TmuxHost            string `json:"tmux_host"`
	PermissionMode      string `json:"permission_mode,omitempty"`
	WorkspacePath       string `json:"workspace_path,omitempty"`
	UpdatedAtUnixMicro  int64  `json:"updated_at_unix_micro"`
	LastIdleAtUnixMicro int64  `json:"last_idle_at_unix_micro,omitempty"`
}

// OperatorQuestionItem is one parked operator-ask fallback question batch for
// this session. MCP clients can answer it with Reacquire.Tool and Args.
type OperatorQuestionItem struct {
	QuestionID         string                           `json:"question_id"`
	Questions          []kitsokimcp.OperatorAskQuestion `json:"questions"`
	CreatedAtUnixMicro int64                            `json:"created_at_unix_micro,omitempty"`
	Reacquire          WorkReacquire                    `json:"reacquire"`
}

// MiningProposalItem is one trace-backed proposal awaiting review. It is folded
// from mining.proposal_raised minus any later mining.proposal_decided event.
type MiningProposalItem struct {
	RecipeID          string        `json:"recipe_id"`
	Kind              string        `json:"kind,omitempty"`
	Target            string        `json:"target,omitempty"`
	Priority          float64       `json:"priority,omitempty"`
	Rung              int           `json:"rung,omitempty"`
	DraftPath         string        `json:"draft_path,omitempty"`
	RaisedTurn        int64         `json:"raised_turn,omitempty"`
	RaisedAtUnixMicro int64         `json:"raised_at_unix_micro,omitempty"`
	Reacquire         WorkReacquire `json:"reacquire"`
}

// TurnSummaryItem collapses one turn's events into a one-line record (the same
// shape `kitsoki inspect` emits).
type TurnSummaryItem struct {
	Turn      int64    `json:"turn"`
	Input     string   `json:"input,omitempty"`
	Intent    string   `json:"intent,omitempty"`
	FromState string   `json:"from_state,omitempty"`
	ToState   string   `json:"to_state,omitempty"`
	Outcome   string   `json:"outcome,omitempty"`
	ErrorCode string   `json:"error_code,omitempty"`
	HostCalls []string `json:"host_calls,omitempty"`
}

// handleSessionStatus returns the compact status snapshot — never the full world.
func (srv *Server) handleSessionStatus(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionStatusArgs,
) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	result, err := rt.status(ctx, args.Handle)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, result, nil
}

func (srv *Server) handleSessionInspect(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionInspectArgs,
) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	lastTurns := args.LastTurns
	if lastTurns <= 0 {
		lastTurns = 5
	}
	out, err := rt.inspect(ctx, lastTurns, args.Handle)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	// Apply optional projections.
	if args.OmitWorld {
		out.World = nil
	} else if args.MaxValueLen > 0 {
		out.World = truncateWorldValues(out.World, args.MaxValueLen)
	}
	return nil, out, nil
}

// SessionWorldArgs is the input to session.world.
type SessionWorldArgs struct {
	Handle string `json:"handle"`
	// Key selects a single world value. Empty → list the key names instead.
	Key string `json:"key,omitempty"`
	// MaxValueLen truncates the returned value to N chars (with '…'); 0 = full.
	// Ignored when Key is empty (the key list carries no values).
	MaxValueLen int `json:"max_value_len,omitempty"`
}

// SessionWorldValue is the session.world result when a key is requested: the
// single value, plus whether the key existed (Found=false ⇒ Value is null).
type SessionWorldValue struct {
	OK    bool   `json:"ok"`
	Key   string `json:"key"`
	Value any    `json:"value"`
	Found bool   `json:"found"`
}

// SessionWorldKeys is the session.world result when no key is requested: the
// sorted key names only (no values), so a caller can discover the world surface
// cheaply before fetching specific values.
type SessionWorldKeys struct {
	OK   bool     `json:"ok"`
	Keys []string `json:"keys"`
}

// handleSessionWorld reads world without dumping the whole map. With a key it
// returns that single value; without one it returns the sorted key names. This
// is the targeted read that lets the advancing turns (and frames) carry no world
// at all — the caller pulls only what it needs.
func (srv *Server) handleSessionWorld(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionWorldArgs,
) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	world, err := rt.worldVars()
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if args.Key == "" {
		keys := make([]string, 0, len(world))
		for k := range world {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, SessionWorldKeys{OK: true, Keys: keys}, nil
	}
	value, found := world[args.Key]
	if found && args.MaxValueLen > 0 {
		// Reuse the inspect truncation so a single huge value (a rendered diff)
		// can still be bounded on request.
		value = truncateWorldValues(map[string]any{args.Key: value}, args.MaxValueLen)[args.Key]
	}
	return nil, SessionWorldValue{OK: true, Key: args.Key, Value: value, Found: found}, nil
}

func (srv *Server) handleSessionCommand(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionCommandArgs,
) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	frame, err := rt.slash(args.Command, cols, rows)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, RenderTUIResult{OK: true, Frame: frameResult(frame)}, nil
}

// SessionTraceArgs is the input to session.trace.
type SessionTraceArgs struct {
	Handle string `json:"handle"`
	// Since / Until filter events by turn number (inclusive). Zero means unbounded.
	Since int64 `json:"since,omitempty"`
	Until int64 `json:"until,omitempty"`
	// Limit keeps only the last N matching events (0 = all).
	Limit int `json:"limit,omitempty"`
	// TruncatePayload caps each event's raw JSON payload to N bytes (appending
	// "…" when truncated). Unset defaults to 500 (raw payloads are token-heavy
	// and rarely needed in full); pass an explicit 0 to opt out of truncation.
	TruncatePayload *int `json:"truncate_payload,omitempty"`
	// Kinds filters to events whose Kind is in this list. Empty means all kinds.
	Kinds []string `json:"kinds,omitempty"`
}

// TraceResult is the session.trace result: the (filtered) JSONL events and the
// highest turn number seen, so the agent can page forward.
type TraceResult struct {
	OK       bool          `json:"ok"`
	Events   []store.Event `json:"events"`
	LastTurn int64         `json:"last_turn"`
}

func (srv *Server) handleSessionTrace(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionTraceArgs,
) (*mcpsdk.CallToolResult, any, error) {
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return rerr, nil, nil
	}
	history := rt.history()
	// Build a set of allowed kinds for fast lookup (nil = all).
	var kindSet map[string]bool
	if len(args.Kinds) > 0 {
		kindSet = make(map[string]bool, len(args.Kinds))
		for _, k := range args.Kinds {
			kindSet[k] = true
		}
	}
	// Default truncation to 500 bytes when unset; an explicit 0 opts out.
	truncatePayload := 500
	if args.TruncatePayload != nil {
		truncatePayload = *args.TruncatePayload
	}
	var filtered []store.Event
	var lastTurn int64
	for _, ev := range history {
		t := int64(ev.Turn)
		if t > lastTurn {
			lastTurn = t
		}
		if args.Since > 0 && t < args.Since {
			continue
		}
		if args.Until > 0 && t > args.Until {
			continue
		}
		if kindSet != nil && !kindSet[string(ev.Kind)] {
			continue
		}
		if truncatePayload > 0 && len(ev.Payload) > truncatePayload {
			// Encode the truncated portion as a valid JSON string (not a raw JSON
			// fragment) so the result is always valid JSON when marshalled.
			truncated := string(ev.Payload[:truncatePayload]) + "…"
			encoded, merr := json.Marshal(truncated)
			if merr == nil {
				trunc := ev
				trunc.Payload = encoded
				ev = trunc
			}
		}
		filtered = append(filtered, ev)
	}
	if args.Limit > 0 && len(filtered) > args.Limit {
		filtered = filtered[len(filtered)-args.Limit:]
	}
	return nil, TraceResult{OK: true, Events: filtered, LastTurn: lastTurn}, nil
}

// ── session.close ─────────────────────────────────────────────────────────────

// SessionCloseArgs is the input to session.close.
type SessionCloseArgs struct {
	Handle string `json:"handle"`
}

// SessionCloseOK is the session.close result.
type SessionCloseOK struct {
	OK     bool   `json:"ok"`
	Handle string `json:"handle"`
}

// handleSessionClose closes a driving session, tearing down its runtime and
// releasing the exclusive trace-path flock so the same trace path can be
// reopened. Without this seam the lock is held for the whole server-process
// lifetime and any reopen of the path fails the non-blocking flock.
func (srv *Server) handleSessionClose(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args SessionCloseArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Handle == "" {
		return buildToolError(ErrBadRequest, "session.close: handle is required"), nil, nil
	}
	if err := srv.sess.CloseSession(args.Handle); err != nil {
		code, msg := AsToolError(err)
		return buildToolError(code, msg), nil, nil
	}
	return nil, SessionCloseOK{OK: true, Handle: args.Handle}, nil
}

// ── render.tui ───────────────────────────────────────────────────────────────

// RenderArgs is the shared input to the render.* tools: EITHER a handle (re-render
// the session's current state, read-only) OR a spec (story_path + state + world,
// rendered headlessly without any session).
type RenderArgs struct {
	Handle     string            `json:"handle,omitempty"`
	StoryPath  string            `json:"story_path,omitempty"`
	State      string            `json:"state,omitempty"`
	World      map[string]any    `json:"world,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	AssertText []string          `json:"assert_text,omitempty"`
	Cols       int               `json:"cols,omitempty"`
	Rows       int               `json:"rows,omitempty"`
	Theme      string            `json:"theme,omitempty"`
}

// RenderTUIResult is the render.tui result: just the Frame (read-only re-render).
type RenderTUIResult struct {
	OK    bool        `json:"ok"`
	Frame FrameResult `json:"frame"`
}

func (srv *Server) handleRenderTUI(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args RenderArgs,
) (*mcpsdk.CallToolResult, any, error) {
	cols, rows := geometry(args.Cols, args.Rows)
	frame, rerr := srv.composeRenderFrame(ctx, args, cols, rows)
	if rerr != nil {
		return rerr, nil, nil
	}
	return nil, RenderTUIResult{OK: true, Frame: frameResult(frame)}, nil
}

// ── render.tui_png ───────────────────────────────────────────────────────────

func (srv *Server) handleRenderTUIPNG(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args RenderArgs,
) (*mcpsdk.CallToolResult, any, error) {
	cols, rows := geometry(args.Cols, args.Rows)
	frame, rerr := srv.composeRenderFrame(ctx, args, cols, rows)
	if rerr != nil {
		return rerr, nil, nil
	}
	var buf bytes.Buffer
	theme := blocks.ThemeByName(args.Theme)
	if err := shot.RenderPNG(&buf, frame.ANSI, shot.Options{Theme: theme, Cols: cols, Rows: rows}); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("render.tui_png: rasterise: %v", err)), nil, nil
	}
	return imageResult(req, frame.Text, buf.Bytes(), "image/png"), nil, nil
}

// ── render.web ───────────────────────────────────────────────────────────────

// registerWebRenderer returns the render.web handler bound to the server's web
// shot seam. It is a method-returning-closure so the BrowserInvoker/ServerProvider
// seam (srv.webShot) is injectable for tests without a Chromium or a kitsoki web.
func (srv *Server) registerWebRenderer() func(context.Context, *mcpsdk.CallToolRequest, RenderArgs) (*mcpsdk.CallToolResult, any, error) {
	return srv.handleRenderWeb
}

func (srv *Server) handleRenderWeb(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args RenderArgs,
) (*mcpsdk.CallToolResult, any, error) {
	shotFn := srv.webShot
	if shotFn == nil {
		// No browser-capable seam wired: degrade to text (epic open Q1) rather
		// than fail — a text-only host still learns this needs a browser.
		return imageResult(req,
			"render.web: web rendering needs a browser-capable host (none attached)", nil, ""), nil, nil
	}
	spec, rerr := srv.webSpec(args)
	if rerr != nil {
		return rerr, nil, nil
	}
	png, err := shotFn(ctx, spec)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("render.web: %v", err)), nil, nil
	}
	// Always include a textual frame; the image rides alongside for a
	// vision-capable client.
	text := fmt.Sprintf("render.web: %s @ %s (%d bytes)", spec.story(), spec.stateLabel(), len(png))
	return imageResult(req, text, png, "image/png"), nil, nil
}

// ── frame composition (handle | spec) ─────────────────────────────────────────

// composeRenderFrame builds the slice-1 Frame for a render.* call. For a handle
// it re-renders the session's CURRENT state read-only (no machine advance); for
// a spec ({story_path, state, world?}) it builds a throwaway runtime, teleports
// to the state with the world seeded, and composes — without touching any open
// session.
func (srv *Server) composeRenderFrame(ctx context.Context, args RenderArgs, cols, rows int) (tui.Frame, *mcpsdk.CallToolResult) {
	switch {
	case args.Handle != "":
		rt, rerr := srv.resolveRuntime(args.Handle)
		if rerr != nil {
			return tui.Frame{}, rerr
		}
		return rt.frame(cols, rows), nil
	case args.StoryPath != "":
		frame, err := srv.specFrame(ctx, args.StoryPath, args.State, args.World, cols, rows)
		if err != nil {
			code, msg := AsToolError(err)
			return tui.Frame{}, buildToolError(code, msg)
		}
		return frame, nil
	default:
		return tui.Frame{}, buildToolError(ErrBadRequest, "render: provide a handle or a {story_path, state} spec")
	}
}

// specFrame renders a headless Frame for (storyPath, state, world) WITHOUT a live
// session: it builds an ephemeral runtime (fresh temp trace, replay-free — no
// harness is needed because a spec render never routes free text), teleports to
// the target state with world seeded as slots, folds the outcome into the
// composer model, and composes the Frame. The ephemeral runtime is torn down
// before returning, so a spec render leaves nothing behind and touches no open
// handle.
func (srv *Server) specFrame(ctx context.Context, storyPath, state string, world map[string]any, cols, rows int) (tui.Frame, error) {
	tracePath, err := ephemeralTracePath()
	if err != nil {
		return tui.Frame{}, err
	}
	// No harness, no profiles, no seed: a spec render is a pure re-render and
	// never calls orch.Turn or dispatches an agent.
	rt, err := newSessionRuntime(ctx, storyPath, tracePath, nil, nil, "", nil, "", false, srv.importResolver, nil, nil, host.AgentLaunchPolicy{})
	if err != nil {
		return tui.Frame{}, err
	}
	defer rt.Close()

	// When a target state is named, teleport there with the world merged in;
	// otherwise photograph the initial state the runtime already settled on.
	if state != "" {
		out, terr := rt.orch.Teleport(ctx, rt.sid, inbox.TeleportTarget{State: app.StatePath(state), Slots: world})
		if terr != nil {
			return tui.Frame{}, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("render spec: teleport to %q: %v", state, terr)}
		}
		rt.model = rt.model.ApplyTurnOutcome(out, "", nil)
	} else if len(world) > 0 {
		if perr := rt.orch.PatchWorld(ctx, rt.sid, world); perr != nil {
			return tui.Frame{}, &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("render spec: patch world: %v", perr)}
		}
		// Re-render the initial state against the patched world.
		out, verr := rt.orch.CurrentView(ctx, rt.sid)
		if verr == nil {
			rt.model = rt.model.ApplyTurnOutcome(out, "", nil)
		}
	}
	return rt.frame(cols, rows), nil
}

// ── runtime resolution & helpers ─────────────────────────────────────────────

// resolveRuntime resolves a handle to its live driving runtime, returning a
// structured tool error for an unknown handle or one with no runtime attached
// (a metadata-only handle never opened for driving).
func (srv *Server) resolveRuntime(handle string) (*sessionRuntime, *mcpsdk.CallToolResult) {
	if handle == "" {
		return nil, buildToolError(ErrBadRequest, "handle is required")
	}
	sh, err := srv.sess.ResolveSession(handle)
	if err != nil {
		code, msg := AsToolError(err)
		return nil, buildToolError(code, msg)
	}
	if sh.Runtime == nil {
		return nil, buildToolError(ErrBadRequest, fmt.Sprintf("handle %q has no driving runtime (open it with session.new)", handle))
	}
	return sh.Runtime, nil
}

// geometry resolves the effective (cols, rows), falling back to the drive
// defaults when the caller omits them.
func geometry(cols, rows int) (int, int) {
	if cols < 1 {
		cols = defaultCols
	}
	if rows < 1 {
		rows = defaultRows
	}
	return cols, rows
}

func turnOutcomeState(out *orchestrator.TurnOutcome) string {
	if out == nil {
		return ""
	}
	return string(out.NewState)
}

// turnResponse projects a TurnOutcome + Frame into the {outcome, frame} wire
// shape. turnErr carries an orchestrator-side failure (e.g. a replay miss) so it
// surfaces as outcome.error / mode="error" alongside the frame, never as a
// transport error.
func turnResponse(out *orchestrator.TurnOutcome, frame tui.Frame, turnErr error) TurnResponse {
	tr := TurnResult{}
	if turnErr != nil {
		tr.Mode = "error"
		tr.Error = turnErr.Error()
	}
	if out != nil {
		tr.Mode = out.Mode.String()
		tr.State = string(out.NewState)
		tr.AllowedIntents = visibleAllowedIntentStrings(string(out.NewState), out.AllowedIntents)
		tr.PendingIntent = out.PendingIntent
		tr.ErrorCode = string(out.ErrorCode)
		tr.ErrorMessage = out.ErrorMessage
		tr.GuardHint = out.GuardHint
		tr.HarnessError = out.HarnessError
		tr.TurnNumber = int64(out.TurnNumber)
		for _, sn := range out.SlotsNeeded {
			tr.SlotsNeeded = append(tr.SlotsNeeded, SlotNeedItem{
				Name:        sn.Name,
				Prompt:      sn.Prompt,
				Description: sn.Description,
				Type:        sn.Type,
				Values:      sn.Values,
			})
		}
		if turnErr != nil {
			// Keep the structured outcome but flag the failure too.
			tr.Error = turnErr.Error()
		}
	}
	return TurnResponse{OK: turnErr == nil, Outcome: tr, Frame: frameResult(frame)}
}

func operationDriveResponse(res turnResult) TurnResponse {
	out := turnResponse(res.outcome, res.frame, res.err)
	if res.operationDrive != nil {
		out.OperationDrive = &OperationDriveResult{
			Turns:      res.operationDrive.Turns,
			StopReason: res.operationDrive.StopReason,
			LastIntent: res.operationDrive.LastIntent,
		}
	}
	return out
}

// frameResult projects a tui.Frame onto the wire shape. The frame NEVER carries
// the world_digest: in deep-import rooms the flat digest is hundreds of
// alias-prefixed keys and tens of thousands of chars, enough to blow the MCP
// tool-result cap and spill the state-transition result to a file (the exact
// dogfood failure). World is read on demand instead — `session.world {handle}`
// lists the keys and `session.world {handle, key}` returns one value — so a
// turn/render result stays small and the caller fetches only what it needs.
// session.inspect remains the full-snapshot read (with its omit_world /
// max_value_len controls) for when the whole map is genuinely wanted.
func frameResult(f tui.Frame) FrameResult {
	return FrameResult{
		Text:   f.Text,
		Width:  f.Width,
		Height: f.Height,
		Metadata: FrameMetaItem{
			State:          f.Metadata.State,
			Mode:           f.Metadata.Mode,
			AllowedIntents: f.Metadata.AllowedIntents,
			// WorldDigest deliberately omitted — see the doc comment.
		},
	}
}

// resolveTracePath returns the trace path for a driving handle: the caller's
// override when set, else a fresh temp .jsonl file. Each session gets its own
// durable trace (the same JSONL `kitsoki turn --trace` writes).
// resolveTracePath picks the JSONL trace path for a driving session. An
// explicit override always wins. Otherwise the trace lands under the same
// discoverable ~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl location the web
// transport uses (store.DefaultTracePath), tagged with the "mcp" transport
// label so `kitsoki trace --app <app> --latest` and cost-mining tools can find
// and distinguish it — see
// issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md.
// Before this fix the default was an anonymous $TMPDIR/kitsoki-studio-*.jsonl
// file: real durable events, but unreachable by app/id and eventually
// temp-reaped, silently losing cost/token/replay evidence for every MCP-driven
// live session that didn't pass an explicit trace: arg.
func resolveTracePath(override, storyPath, key string) (string, error) {
	if override != "" {
		return override, nil
	}
	thread := key
	if thread == "" {
		thread = uuid.NewString()
	}
	path := store.DefaultTracePath(appSlugFromStoryPath(storyPath), "mcp", thread)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("create trace dir: %v", err)}
	}
	return path, nil
}

// ephemeralTracePath returns a throwaway temp-file trace path for callers that
// explicitly don't want a discoverable, durable trace — e.g. specFrame's
// torn-down-before-returning ephemeral runtime. This is the pre-fix behavior
// of resolveTracePath's default branch, kept for the cases where it was
// actually correct (not a driving session an operator might want to inspect
// or replay later).
func ephemeralTracePath() (string, error) {
	f, err := os.CreateTemp("", "kitsoki-studio-*.jsonl")
	if err != nil {
		return "", &openError{Code: ErrBadRequest, Msg: fmt.Sprintf("create trace file: %v", err)}
	}
	name := f.Name()
	_ = f.Close()
	return name, nil
}

// appSlugFromStoryPath derives an app identifier from a story's app.yaml path
// for trace-path purposes, without requiring the app to be loaded first (trace
// path is resolved before OpenDrivingSession loads the story). By convention a
// story's directory name IS its app id (stories/bugfix/app.yaml -> "bugfix"),
// matching store.DefaultTracePath's appSlug for the web transport
// (cmd/kitsoki/registry.go, which uses the loaded def.App.ID — same value for
// every story in this repo). Falls back to "app" for an unparseable path
// rather than failing the whole session.new call over a cosmetic detail.
func appSlugFromStoryPath(storyPath string) string {
	dir := filepath.Base(filepath.Dir(storyPath))
	if dir == "" || dir == "." || dir == string(filepath.Separator) {
		return "app"
	}
	return dir
}

// ── inspect on the runtime ────────────────────────────────────────────────────

// status builds the compact session.status snapshot. It reads state and
// allowed intents from the orchestrator (same sources as inspect), then reads
// only the well-known keys status/last_error/exit from the world — never the
// full world map — so the result stays small regardless of world size.
func (rt *sessionRuntime) status(ctx context.Context, handle string) (SessionStatusResult, error) {
	if running, snap := rt.runningDriveSnapshot(handle); running != nil {
		state := snap.state
		if running.InFlightState != "" {
			// Never-silent: while the turn runs, report where it actually is
			// (e.g. "bf.reproducing"), not the resting state it left. The
			// resting-state report misled pollers into treating healthy live
			// runs as stuck.
			state = running.InFlightState
		}
		result := SessionStatusResult{
			OK:             true,
			State:          state,
			AllowedIntents: visibleAllowedIntentStrings(state, snap.allowedIntents),
			Running:        running,
		}
		addStatusWorldKeys(&result, snap.world)
		return result, nil
	}

	j, err := rt.orch.LoadJourney(rt.sid)
	if err != nil {
		return SessionStatusResult{}, fmt.Errorf("session.status: load journey: %w", err)
	}
	allowed := rt.orch.AllowedIntents(j.State, j.World)
	allowedNames := visibleAllowedIntentNames(string(j.State), allowed)
	result := SessionStatusResult{
		OK:             true,
		State:          string(j.State),
		AllowedIntents: allowedNames,
		Running:        rt.runningDrive(handle),
	}
	addStatusWorldKeys(&result, j.World.Vars)
	return result, nil
}

func addStatusWorldKeys(result *SessionStatusResult, vars map[string]any) {
	// Read only the well-known world keys.
	if v, ok := vars["status"]; ok {
		if s, isStr := v.(string); isStr {
			result.Status = s
		}
	}
	if v, ok := vars["last_error"]; ok {
		if s, isStr := v.(string); isStr {
			result.LastError = s
		}
	}
	if v, ok := vars["exit"]; ok {
		result.Exit = v
	}
}

// truncateWorldValues returns a copy of the world map where each string value
// longer than maxLen is trimmed to maxLen characters with an ellipsis appended.
// Non-string values are marshalled to JSON first, then trimmed the same way.
func truncateWorldValues(world map[string]any, maxLen int) map[string]any {
	if world == nil {
		return nil
	}
	out := make(map[string]any, len(world))
	for k, v := range world {
		var s string
		switch tv := v.(type) {
		case string:
			s = tv
		default:
			b, err := json.Marshal(v)
			if err != nil {
				out[k] = v
				continue
			}
			s = string(b)
		}
		if len([]rune(s)) > maxLen {
			runes := []rune(s)[:maxLen]
			out[k] = string(runes) + "…"
		} else {
			out[k] = s
		}
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// inspect builds the session.inspect snapshot from the live orchestrator —
// state/world from the journey, the allowed-intent menu from the machine, the
// current rendered view, background jobs, inbox notifications, and a tail of
// turn summaries from the trace. Read-only.
func (rt *sessionRuntime) inspect(ctx context.Context, lastTurns int, handle string) (InspectResult, error) {
	if running, snap := rt.runningDriveSnapshot(handle); running != nil {
		jobs, notifications, unreadNotifications, pendingDrives, backgroundedChats, asyncErr := rt.inspectAsync(ctx)
		if asyncErr != nil {
			return InspectResult{}, asyncErr
		}
		operatorQuestions := rt.pendingOperatorQuestions()
		state := snap.state
		if running.InFlightState != "" {
			state = running.InFlightState // never-silent: live in-flight state, not the resting state
		}
		return InspectResult{
			OK:                true,
			State:             state,
			World:             snap.world,
			AllowedIntents:    visibleAllowedIntentStrings(state, snap.allowedIntents),
			LastView:          snap.lastView,
			Async:             asyncSummaryOrNil(summarizeAsync(jobs, notifications, unreadNotifications, pendingDrives, backgroundedChats, operatorQuestions, nil, running)),
			Running:           running,
			Jobs:              jobs,
			Notifications:     notifications,
			PendingDrives:     pendingDrives,
			BackgroundedChats: backgroundedChats,
			OperatorQuestions: inspectOperatorQuestions(handle, operatorQuestions),
			MiningProposals:   nil,
			LastTurns:         nil,
		}, nil
	}

	j, err := rt.orch.LoadJourney(rt.sid)
	if err != nil {
		return InspectResult{}, fmt.Errorf("session.inspect: load journey: %w", err)
	}
	allowed := rt.orch.AllowedIntents(j.State, j.World)
	allowedNames := visibleAllowedIntentNames(string(j.State), allowed)
	view, verr := rt.orch.RenderState(j.State, j.World)
	if verr != nil {
		view = fmt.Sprintf("<render error: %v>", verr)
	}
	jobs, notifications, unreadNotifications, pendingDrives, backgroundedChats, asyncErr := rt.inspectAsync(ctx)
	if asyncErr != nil {
		return InspectResult{}, asyncErr
	}
	operatorQuestions := rt.pendingOperatorQuestions()
	running := rt.runningDrive(handle)
	miningProposals := pendingMiningProposals(handle, rt.history())
	return InspectResult{
		OK:                true,
		State:             string(j.State),
		World:             j.World.Vars,
		AllowedIntents:    allowedNames,
		LastView:          view,
		Async:             asyncSummaryOrNil(summarizeAsync(jobs, notifications, unreadNotifications, pendingDrives, backgroundedChats, operatorQuestions, miningProposals, running)),
		Running:           running,
		Jobs:              jobs,
		Notifications:     notifications,
		PendingDrives:     pendingDrives,
		BackgroundedChats: backgroundedChats,
		OperatorQuestions: inspectOperatorQuestions(handle, operatorQuestions),
		MiningProposals:   miningProposals,
		LastTurns:         summariseTrace(rt.history(), lastTurns),
	}, nil
}

func (rt *sessionRuntime) inspectAsync(ctx context.Context) ([]JobInspectItem, []InboxInspectItem, map[jobs.NotificationSeverity]int, []PendingDriveItem, []BackgroundedChatItem, error) {
	var (
		jobItems          []JobInspectItem
		notificationItems []InboxInspectItem
		unreadCounts      map[jobs.NotificationSeverity]int
		pendingDriveItems []PendingDriveItem
		backgroundedChats []BackgroundedChatItem
	)
	if rt.jobStore == nil {
		return nil, nil, nil, nil, nil, nil
	}
	jobRows, err := rt.jobStore.ListBySession(ctx, rt.sid)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session.inspect: list jobs: %w", err)
	}
	notifRows, err := rt.jobStore.ListNotifications(ctx, rt.sid, 0)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session.inspect: list notifications: %w", err)
	}
	unreadCounts, err = rt.jobStore.UnreadCount(ctx, rt.sid)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("session.inspect: count unread notifications: %w", err)
	}
	jobItems = inspectJobs(jobRows)
	notificationItems = inspectNotifications(notifRows)
	if rt.chatStore != nil {
		driveRows, err := rt.chatStore.ListDrivesBySession(ctx, string(rt.sid),
			activeDriveStatuses())
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("session.inspect: list pending drives: %w", err)
		}
		pendingDriveItems = inspectPendingDrives(driveRows)

		ptyRows, err := rt.chatStore.ListPTYForHost(ctx)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("session.inspect: list backgrounded chats: %w", err)
		}
		backgroundedChats = rt.inspectBackgroundedChats(ctx, ptyRows)
	}
	return jobItems, notificationItems, unreadCounts, pendingDriveItems, backgroundedChats, nil
}

func inspectJobs(in []jobs.Job) []JobInspectItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]JobInspectItem, 0, len(in))
	for _, j := range in {
		item := JobInspectItem{
			ID:                  j.ID,
			Kind:                j.Kind,
			Status:              j.Status,
			OriginState:         string(j.OriginState),
			OriginProposalID:    j.OriginProposalID,
			Error:               j.Error,
			RetryCount:          j.RetryCount,
			ClarificationSchema: clarificationSchema(j.ClarificationSchema),
			CreatedAtUnixMilli:  j.CreatedAt.UnixMilli(),
			UpdatedAtUnixMilli:  j.UpdatedAt.UnixMilli(),
		}
		if j.StartedAt != nil {
			item.StartedAtUnixMilli = j.StartedAt.UnixMilli()
		}
		if j.FinishedAt != nil {
			item.FinishedAtUnixMilli = j.FinishedAt.UnixMilli()
		}
		out = append(out, item)
	}
	return out
}

func clarificationSchema(raw any) *jobs.ClarificationSchema {
	switch v := raw.(type) {
	case nil:
		return nil
	case jobs.ClarificationSchema:
		return &v
	case *jobs.ClarificationSchema:
		return v
	case map[string]any:
		schema := jobs.ClarificationSchema{Fields: map[string]string{}}
		if prompt, ok := v["prompt"].(string); ok {
			schema.Prompt = prompt
		}
		if fields, ok := v["fields"].(map[string]any); ok {
			for name, typ := range fields {
				if text, ok := typ.(string); ok {
					schema.Fields[name] = text
				}
			}
		}
		return &schema
	default:
		return nil
	}
}

func inspectNotifications(in []jobs.Notification) []InboxInspectItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]InboxInspectItem, 0, len(in))
	for _, n := range in {
		item := InboxInspectItem{
			ID:                 n.ID,
			Severity:           n.Severity,
			Title:              n.Title,
			Body:               n.Body,
			CreatedAtUnixMilli: n.CreatedAt.UnixMilli(),
			TeleportState:      n.TeleportState,
			TeleportSlots:      n.TeleportSlots,
			TeleportProposalID: n.TeleportProposalID,
			TeleportJobID:      n.TeleportJobID,
			OriginKind:         n.OriginKind,
			OriginRef:          n.OriginRef,
			OriginURL:          n.OriginURL,
		}
		if n.ReadAt != nil {
			item.ReadAtUnixMilli = n.ReadAt.UnixMilli()
		}
		out = append(out, item)
	}
	return out
}

func inspectPendingDrives(in []chats.Drive) []PendingDriveItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]PendingDriveItem, 0, len(in))
	for _, d := range in {
		item := PendingDriveItem{
			DriveID:             d.DriveID,
			ChatID:              d.ChatID,
			Transport:           d.Transport,
			Status:              d.Status,
			Actor:               d.Actor,
			Thread:              d.Thread,
			CorrelationID:       d.CorrelationID,
			Payload:             d.Payload,
			ErrorMessage:        d.ErrorMessage,
			OriginState:         d.OriginState,
			ReceivedAtUnixMicro: d.ReceivedAt.UnixMicro(),
		}
		if d.DispatchedAt != nil {
			item.DispatchedAtUnixMicro = d.DispatchedAt.UnixMicro()
		}
		if d.CompletedAt != nil {
			item.CompletedAtUnixMicro = d.CompletedAt.UnixMicro()
		}
		out = append(out, item)
	}
	return out
}

func (rt *sessionRuntime) inspectBackgroundedChats(ctx context.Context, in []chats.PtySession) []BackgroundedChatItem {
	if len(in) == 0 || rt.chatStore == nil {
		return nil
	}
	out := make([]BackgroundedChatItem, 0, len(in))
	for _, p := range in {
		if p.Mode != chats.PtyModeBackground {
			continue
		}
		ch, err := rt.chatStore.Get(ctx, p.ChatID)
		if err != nil || ch == nil || ch.SessionID != string(rt.sid) {
			continue
		}
		item := BackgroundedChatItem{
			ChatID:             p.ChatID,
			TmuxSession:        p.TmuxSession,
			TmuxHost:           p.TmuxHost,
			PermissionMode:     p.PermissionMode,
			WorkspacePath:      p.WorkspacePath,
			UpdatedAtUnixMicro: p.UpdatedAt.UnixMicro(),
		}
		if p.LastIdleAt != nil {
			item.LastIdleAtUnixMicro = p.LastIdleAt.UnixMicro()
		}
		out = append(out, item)
	}
	return out
}

func summarizeAsync(jobRows []JobInspectItem, notifications []InboxInspectItem, unreadNotifications map[jobs.NotificationSeverity]int, pendingDrives []PendingDriveItem, backgroundedChats []BackgroundedChatItem, operatorQuestions []pendingQuestion, miningProposals []MiningProposalItem, running *RunningDrive) AsyncInspectSummary {
	out := AsyncInspectSummary{
		JobsTotal:          len(jobRows),
		NotificationsTotal: len(notifications),
		BackgroundedChats:  len(backgroundedChats),
		OperatorQuestions:  len(operatorQuestions),
		MiningProposals:    len(miningProposals),
	}
	if running != nil {
		out.RunningDrive = 1
	}
	for _, j := range jobRows {
		switch j.Status {
		case jobs.JobRunning:
			out.JobsRunning++
		case jobs.JobAwaitingInput:
			out.JobsAwaitingInput++
		case jobs.JobDone, jobs.JobFailed, jobs.JobCancelled:
			out.JobsTerminal++
		}
	}
	for _, count := range unreadNotifications {
		out.NotificationsUnread += count
	}
	out.NotificationsActionRequired = unreadNotifications[jobs.SeverityActionRequired]
	for _, d := range pendingDrives {
		switch d.Status {
		case chats.DriveStatusPending:
			out.PendingDrives++
		case chats.DriveStatusDispatching:
			out.DispatchingDrives++
		case chats.DriveStatusFailed:
			out.FailedDrives++
		}
	}
	return out
}

// asyncSummaryOrNil returns a pointer to the summary, or nil when every count is
// zero, so session.inspect omits the async block entirely when there's no work.
func asyncSummaryOrNil(s AsyncInspectSummary) *AsyncInspectSummary {
	if s == (AsyncInspectSummary{}) {
		return nil
	}
	return &s
}

func pendingMiningProposals(handle string, history store.History) []MiningProposalItem {
	if len(history) == 0 {
		return nil
	}
	byRecipe := make(map[string]MiningProposalItem)
	var order []string
	for _, ev := range history {
		switch ev.Kind {
		case store.MiningProposalRaised:
			var payload store.MiningProposalRaisedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			if _, exists := byRecipe[payload.RecipeID]; !exists {
				order = append(order, payload.RecipeID)
			}
			byRecipe[payload.RecipeID] = MiningProposalItem{
				RecipeID:          payload.RecipeID,
				Kind:              payload.Kind,
				Target:            payload.Target,
				Priority:          payload.Priority,
				Rung:              payload.Rung,
				DraftPath:         payload.DraftPath,
				RaisedTurn:        int64(ev.Turn),
				RaisedAtUnixMicro: ev.Ts.UnixMicro(),
				Reacquire: WorkReacquire{
					Tool: "session.inspect",
					Args: map[string]any{"handle": handle, "last_turns": 10},
				},
			}
		case store.MiningProposalDecided:
			var payload store.MiningProposalDecidedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			delete(byRecipe, payload.RecipeID)
		}
	}
	out := make([]MiningProposalItem, 0, len(byRecipe))
	for _, recipeID := range order {
		if item, ok := byRecipe[recipeID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func (rt *sessionRuntime) pendingOperatorQuestions() []pendingQuestion {
	if rt == nil {
		return nil
	}
	rt.mu.Lock()
	broker := rt.inFlight
	rt.mu.Unlock()
	if broker == nil {
		return nil
	}
	return broker.snapshotPending()
}

func (rt *sessionRuntime) runningDrive(handle string) *RunningDrive {
	running, _ := rt.runningDriveSnapshot(handle)
	return running
}

func (rt *sessionRuntime) runningDriveSnapshot(handle string) (*RunningDrive, driveSnapshot) {
	if rt == nil {
		return nil, driveSnapshot{}
	}
	rt.mu.Lock()
	broker := rt.inFlight
	rt.mu.Unlock()
	if broker == nil {
		return nil, driveSnapshot{}
	}
	running := runningDriveWire(handle, "", broker.snapshotRunning())
	if running == nil {
		return nil, driveSnapshot{}
	}
	snap, ok := broker.snapshotState()
	if !ok {
		return nil, driveSnapshot{}
	}
	rt.decorateRunning(running)
	return running, snap
}

// decorateRunning fills the live in-flight fields on a RunningDrive from the
// session's activity tap (never-silent: pollers must see what the running
// turn is doing, not just the resting state it was submitted from).
func (rt *sessionRuntime) decorateRunning(running *RunningDrive) {
	if rt == nil || running == nil {
		return
	}
	act, ok := rt.tap.snapshot()
	if !ok {
		return
	}
	running.InFlightState = act.statePath
	running.LastEventKind = act.kind
	if !act.ts.IsZero() {
		running.LastEventAtUnixMicro = act.ts.UnixMicro()
	}
	running.ActiveAgent = act.agent
}

func inspectOperatorQuestions(handle string, questions []pendingQuestion) []OperatorQuestionItem {
	out := make([]OperatorQuestionItem, 0, len(questions))
	for _, q := range questions {
		out = append(out, OperatorQuestionItem{
			QuestionID:         q.id,
			Questions:          hostToWireOperatorQuestions(q.questions),
			CreatedAtUnixMicro: q.createdAt.UnixMicro(),
			Reacquire: WorkReacquire{
				Tool: "session.answer",
				Args: map[string]any{
					"handle":      handle,
					"question_id": q.id,
				},
			},
		})
	}
	return out
}

func activeDriveStatuses() []chats.DriveStatus {
	return []chats.DriveStatus{chats.DriveStatusPending, chats.DriveStatusDispatching, chats.DriveStatusFailed}
}

// summariseTrace folds a JSONL history into one summary per turn, returning the
// last n. It is the studio twin of cmd/kitsoki summariseTurns — the same event
// kinds projected to the same fields — so session.inspect's last_turns matches
// the CLI inspect.
func summariseTrace(history store.History, n int) []TurnSummaryItem {
	if n <= 0 {
		return nil
	}
	byTurn := map[int64]*TurnSummaryItem{}
	var order []int64
	for _, ev := range history {
		t := int64(ev.Turn)
		if t == 0 {
			continue
		}
		ts, ok := byTurn[t]
		if !ok {
			ts = &TurnSummaryItem{Turn: t}
			byTurn[t] = ts
			order = append(order, t)
		}
		var p map[string]any
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &p)
		}
		switch ev.Kind {
		case store.TurnStarted:
			if v, ok := p["input"].(string); ok {
				ts.Input = v
			}
		case store.UserInputReceived:
			if v, ok := p["input"].(string); ok && ts.Input == "" {
				ts.Input = v
			}
		case store.TransitionApplied:
			if v, ok := p["intent"].(string); ok && ts.Intent == "" {
				ts.Intent = v
			}
			if v, ok := p["from"].(string); ok {
				ts.FromState = v
			}
			if v, ok := p["to"].(string); ok {
				ts.ToState = v
			}
		case store.TurnEnded:
			if v, ok := p["outcome"].(string); ok {
				ts.Outcome = v
			}
			if v, ok := p["code"].(string); ok {
				ts.ErrorCode = v
			}
		case store.HostInvoked:
			if v, ok := p["namespace"].(string); ok {
				ts.HostCalls = append(ts.HostCalls, v)
			}
		}
	}
	if len(order) > n {
		order = order[len(order)-n:]
	}
	out := make([]TurnSummaryItem, len(order))
	for i, t := range order {
		out[i] = *byTurn[t]
	}
	return out
}

// ── web shot seam ────────────────────────────────────────────────────────────

// WebShotFunc is the compatibility form of the injectable render.web seam: given
// a webshot.Spec it returns a PNG. New visual callers use WebShotResultFunc so
// the same browser pass can also return a compact semantic observation.
type WebShotFunc func(ctx context.Context, spec WebRenderSpec) ([]byte, error)

// WebShotResult is the screenshot plus optional page-side semantic observation.
type WebShotResult struct {
	PNG          []byte
	SemanticJSON []byte
	RRWebJSON    []byte
}

// WebShotResultFunc is the richer render.web seam used by visual.snapshot.
type WebShotResultFunc func(ctx context.Context, spec WebRenderSpec) (WebShotResult, error)

// WebActFunc performs a browser action against a web render target and returns
// the post-action screenshot/semantic observation.
type WebActFunc func(ctx context.Context, spec WebRenderSpec, action WebActionSpec) (WebShotResult, error)

// WebActionSpec is the studio-level browser action used by visual.act.
type WebActionSpec struct {
	Kind         string
	ActionHandle string
	Point        *VisualPoint
	Button       string
	Modifiers    []string
}

// WebRenderSpec is the studio's render.web target: a story + state + world OR a
// live handle's session. It is mapped to a webshot.Spec by the production seam;
// kept as a studio type so the studio package does not force every render.web
// caller through webshot's exact Spec shape.
type WebRenderSpec struct {
	StoryPath  string
	State      string
	World      map[string]any
	SessionID  string
	Query      map[string]string
	AssertText []string
}

func (s WebRenderSpec) story() string {
	if s.StoryPath != "" {
		return s.StoryPath
	}
	return "session:" + s.SessionID
}

func (s WebRenderSpec) stateLabel() string {
	if s.State != "" {
		return s.State
	}
	return "current"
}

// ToWebshotSpec maps the studio render spec onto the webshot package's Spec, the
// adapter the production WebShotFunc uses. Exposed so cmd/kitsoki can wire the
// real webshot.Shot without re-deriving the mapping.
func (s WebRenderSpec) ToWebshotSpec() webshot.Spec {
	if s.SessionID != "" {
		return webshot.Spec{SessionID: s.SessionID, Query: s.Query, AssertText: s.AssertText}
	}
	return webshot.Spec{StoryPath: s.StoryPath, State: s.State, World: s.World, Query: s.Query, AssertText: s.AssertText}
}

// webSpec resolves a render.web RenderArgs to a WebRenderSpec: a handle maps to
// its session id (live form), a spec maps to story+state+world.
func (srv *Server) webSpec(args RenderArgs) (WebRenderSpec, *mcpsdk.CallToolResult) {
	switch {
	case args.Handle != "":
		sh, err := srv.sess.ResolveSession(args.Handle)
		if err != nil {
			code, msg := AsToolError(err)
			return WebRenderSpec{}, buildToolError(code, msg)
		}
		if sh.Runtime == nil {
			return WebRenderSpec{}, buildToolError(ErrBadRequest, "handle has no driving runtime")
		}
		return WebRenderSpec{StoryPath: sh.StoryPath, SessionID: string(sh.SID), Query: args.Query, AssertText: args.AssertText}, nil
	case args.StoryPath != "":
		return WebRenderSpec{StoryPath: args.StoryPath, State: args.State, World: args.World, Query: args.Query, AssertText: args.AssertText}, nil
	default:
		return WebRenderSpec{}, buildToolError(ErrBadRequest, "render.web: provide a handle or a {story_path, state} spec")
	}
}

// ── MCP image content gating ──────────────────────────────────────────────────

// imageResult builds a CallToolResult that ALWAYS carries the textual frame and,
// when the client advertises image support and png is non-empty, an
// mcpsdk.ImageContent block. A text-only client still gets something (the text);
// a vision-capable one also sees the screen (epic open Q1 / slice invariant).
func imageResult(req *mcpsdk.CallToolRequest, text string, png []byte, mime string) *mcpsdk.CallToolResult {
	content := []mcpsdk.Content{&mcpsdk.TextContent{Text: text}}
	if len(png) > 0 && clientSupportsImages(req) {
		content = append(content, &mcpsdk.ImageContent{Data: png, MIMEType: mime})
	}
	return &mcpsdk.CallToolResult{Content: content}
}

// clientSupportsImages reports whether the connected client accepts MCP image
// content blocks. MCP has no closed "image" capability, so the lean is: image
// content is part of the base tool-result protocol every spec-compliant client
// can receive, UNLESS the client opts OUT via the experimental capability
// {"images": false} (the documented escape hatch for a strictly text-only host).
// A nil request/session (in-process tests that don't initialize) is treated as
// image-capable so the image path is exercised by default.
func clientSupportsImages(req *mcpsdk.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return true
	}
	params := req.Session.InitializeParams()
	if params == nil || params.Capabilities == nil {
		return true
	}
	if v, ok := params.Capabilities.Experimental["images"]; ok {
		if b, isBool := v.(bool); isBool {
			return b
		}
	}
	return true
}
