// Package orchestrator — off-path runtime. See docs/stories/meta-mode.md
// for the read-only off-path / meta agent narrative.
//
// Off-path is the global escape hatch: a free-form chat with the agent
// that DOES NOT mutate world or state. It is intentionally orthogonal to
// the state machine — no Turn() is fired, no TransitionApplied event is
// emitted, the journey state is inviolate.
//
// The orchestrator owns the chat-thread resolution and the host.agent.converse
// invocation directly (no allow-list check on the app's `hosts:` block —
// off-path is engine-provided, not app-provided). Events OffPathQuestion
// and OffPathAnswer are appended for replay parity; OffPathEntered and
// OffPathExited bracket the session.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/intent"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// offPathRoom is the canonical room key used when resolving the per-session
// off-path chat thread. Combined with scope_key = session_id, this gives
// each session its own conversation that persists across off-path entries
// within the same session.
const offPathRoom = "off_path"

// offRampLockHeldKey marks a context that already holds the per-session lock,
// so the off-path append path (appendOffPathEventsCtx) skips re-locking. Set by
// maybeOffRamp, which always runs from inside a foreground turn that holds
// sessMu — without this flag the off-ramp's first append would self-deadlock on
// the non-reentrant per-session mutex.
type offRampLockHeldKeyType struct{}

var offRampLockHeldKey offRampLockHeldKeyType

// withOffRampLockHeld returns ctx marked as already holding the session lock.
func withOffRampLockHeld(ctx context.Context) context.Context {
	return context.WithValue(ctx, offRampLockHeldKey, true)
}

// offRampLockHeld reports whether ctx was marked by withOffRampLockHeld.
func offRampLockHeld(ctx context.Context) bool {
	v, _ := ctx.Value(offRampLockHeldKey).(bool)
	return v
}

// AskOffPath fires a single host.agent.converse turn for an off-path question
// against a per-session chat thread. It does NOT mutate world, advance the
// state machine, or emit StateExited/StateEntered events.
//
// On success, OffPathQuestion and OffPathAnswer events are appended to the
// session's event log (with turn = 0 to mark them as off-path/out-of-band).
// On failure, an OffPathQuestion event is still appended so the trace shows
// the user spoke; the error surfaces via the returned error.
//
// When no ChatStore is wired into the orchestrator, off-path runs in the
// legacy agent.talk path (no chat persistence) — the user still gets an
// answer, but there is no transcript history across turns.
func (o *Orchestrator) AskOffPath(ctx context.Context, sid app.SessionID, question string) (string, error) {
	// Default voice: the app's off_path: persona/agent (nil-safe). Zero
	// scope: the typed /freeform door keeps the session-wide off_path thread.
	return o.askOffPathVoiced(ctx, sid, question, o.offPathVoice(), offRampScope{})
}

// offRampScope carries the per-room conversation identity and the
// engine-composed room context for an off-ramp / conversation-lane converse
// call. The zero value preserves the typed-/freeform behavior: one
// session-wide chat thread under the offPathRoom key, no room context.
//
// A non-empty room keys the chat thread by that room instead, so each room
// holds exactly ONE persistent conversation per session (the chat is still
// session-scoped via the scope_key = session id passed at resolve time): the
// operator's follow-ups in a room resume the same warm Claude session, with
// the transcript in the ChatStore and nothing threaded through world.
type offRampScope struct {
	// room is the chat-thread room key; "" means the session-wide
	// offPathRoom thread (typed /freeform).
	room string
	// context is the engine-composed room context (composeRoomContext) fed
	// to the converse call as the room_context arg; "" adds nothing.
	context string
	// workingDir scopes the converse agent's read-only tool inspection to
	// the story's checkout; "" leaves the handler's default cwd behavior.
	workingDir string
}

// offRampScopeForState builds the per-room conversation scope for an
// off-ramp or conversation-lane entry resting in state: the chat thread is
// keyed by the room (prefixed so it can never collide with the reserved
// offPathRoom key) and the room context is composed from the app definition
// plus the live world.
func (o *Orchestrator) offRampScopeForState(state app.StatePath, st *app.State, w world.World, allowedNames []string) offRampScope {
	return offRampScope{
		room:       "room:" + string(state),
		context:    composeRoomContext(o.def, state, st, w, allowedNames),
		workingDir: worldWorkingDir(w),
	}
}

// worldWorkingDir resolves the story's checkout directory from world using
// the two established conventions, in precedence order: `working_dir`
// (git-ops) then `workdir` (dev-story / workbench:). Returns "" when
// neither is a non-empty string.
func worldWorkingDir(w world.World) string {
	for _, key := range []string{"working_dir", "workdir"} {
		if s, ok := w.Vars[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// offPathVoice is the offRampVoice for the app-level off_path: block, used as
// the default off-path/off-ramp voice. Nil-safe: an app with no off_path:
// block yields the zero voice (engine default persona).
func (o *Orchestrator) offPathVoice() offRampVoice {
	if o.def.OffPath == nil {
		return offRampVoice{}
	}
	return offRampVoice{persona: o.def.OffPath.Persona, agent: o.def.OffPath.Agent}
}

// offRampVoice is the persona/agent pair that styles an off-path or off-ramp
// converse call. It exists so the off-ramp can override the off-path voice
// per room (agent_off_ramp.agent > off_path.agent), with persona winning
// over agent within a single voice — mirroring AskOffPath's existing
// precedence (see the args-build block below).
type offRampVoice struct {
	persona string
	agent   string
}

func (o *Orchestrator) askOffPathVoiced(ctx context.Context, sid app.SessionID, question string, voice offRampVoice, scope offRampScope) (string, error) {
	if question == "" {
		return "", fmt.Errorf("orchestrator: AskOffPath: empty question")
	}

	o.logger.DebugContext(ctx, trace.EvOffPathAskStart,
		slog.String("session_id", string(sid)),
		slog.Int("question_bytes", len(question)),
	)

	// Resolve (or create) the chat thread. Scope by session_id so each
	// session has its own thread(s) that survive repeated entries within the
	// same session. A per-room scope keys the thread by the resting room —
	// one persistent conversation per room; the zero scope keeps the single
	// session-wide off_path thread the typed /freeform door has always used.
	chatRoom, chatTitle := offPathRoom, "off-path chat"
	if scope.room != "" {
		chatRoom, chatTitle = scope.room, scope.room+" conversation"
	}
	chatID := ""
	if o.chatStore != nil {
		chatRec, _, err := o.chatStore.Resolve(ctx, o.def.App.ID, chatRoom, string(sid), chatTitle)
		if err != nil {
			o.logger.WarnContext(ctx, trace.EvOffPathAskError,
				slog.String("session_id", string(sid)),
				slog.String("phase", "chat_resolve"),
				slog.String("err", err.Error()),
			)
		} else {
			chatID = chatRec.ID
			o.logger.DebugContext(ctx, trace.EvOffPathChatResolved,
				slog.String("session_id", string(sid)),
				slog.String("chat_id", chatID),
			)
		}
	}

	// Build args mirroring the dev-story agent.yaml host.agent.converse pattern.
	args := map[string]any{
		"question": question,
	}
	if chatID != "" {
		args["chat_id"] = chatID
	}
	// Engine-composed room context (room purpose, available commands,
	// relevant world) — appended to the converse system prompt by the
	// handler so the agent never has to tool-call for the basics.
	if scope.context != "" {
		args["room_context"] = scope.context
	}
	if scope.workingDir != "" {
		args["working_dir"] = scope.workingDir
	}
	// App-tunable persona for the off-path/off-ramp voice. Two equivalent
	// inputs, in priority order:
	//   1. voice.persona — inline back-compat shortcut. Wins when set.
	//   2. voice.agent   — names an entry in AppDef.Agents. The handler's
	//                      resolveAgent reads the agent from ctx and applies
	//                      SystemPrompt + Model.
	// Either path lands on the same claude flags
	// (--append-system-prompt [+ --model]); the agent: route keeps the
	// persona text declared in one place (the agents block) rather than
	// duplicated under off_path:. The voice is the app off_path: block for a
	// typed /freeform entry, or the room's agent_off_ramp: override for an
	// off-ramp entry (see maybeOffRamp).
	if voice.persona != "" {
		args["system_prompt"] = voice.persona
	} else if voice.agent != "" {
		args["agent"] = voice.agent
	}

	// Inject the ChatStore into ctx so the chat-aware handler path engages.
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	// Inject the agents map so handlers can resolve an `agent:` arg (used
	// by the off-path-via-agent path above when Model is set on the
	// resolved agent) without having to import the app package.
	ctx = host.WithAgents(ctx, agentsForContext(o.def))
	ctx = host.WithProviders(ctx, o.providersForDispatch())
	// Off-path agent calls honor the live harness selection too (see
	// host_dispatch.go).
	backendName, activeProfile := o.resolveSelection(o.agentBackendName)
	ctx = host.WithAgentBackendNamed(ctx, backendName)
	ctx = host.WithActiveProfile(ctx, activeProfile)
	ctx = host.WithHarnessLadder(ctx, o.harnessLadder)
	ctx = host.WithLadderSessionState(ctx, o.ladderSession)
	if o.agentLaunchPolicy.Enabled {
		ctx = host.WithAgentLaunchPolicy(ctx, o.agentLaunchPolicy)
	}
	ctx = host.WithPromptRenderer(ctx, o.promptRenderer)
	ctx = host.WithProjectContext(ctx, projectContextFor(o.def))
	// Inject the live IDE link (nil-safe) so the off-path agent subprocess
	// engages the same env-scrub gate as the main dispatch path.
	ctx = host.WithIDELink(ctx, o.currentIDELink())

	// Always log the question first — even if the call below fails, the
	// trace shows the user spoke off-path.
	qEvent := newOrchestratorEvent(store.OffPathQuestion, map[string]any{
		"question": question,
		"chat_id":  chatID,
	}, 0)

	// Resolve the converse handler through the host registry when one is wired,
	// so a deterministic stub (a --host-cassette dispatcher that Replace()d
	// host.agent.converse, or a flow host_handlers stub) intercepts the
	// off-path/off-ramp voice exactly like any other host.* call. Falling back
	// to the package handler keeps the no-registry path (bare tests) working.
	converse := host.AgentConverseHandler
	if o.hosts != nil {
		if h, ok := o.hosts.Get("host.agent.converse"); ok {
			converse = h
		}
	}
	res, err := converse(ctx, args)
	if err != nil {
		// Infrastructure failure (claude binary issues, etc.) — record the
		// question, surface the error to the caller.
		if appendErr := o.appendOffPathEventsCtx(ctx, sid, []store.Event{qEvent}); appendErr != nil {
			o.logger.WarnContext(ctx, trace.EvOffPathAskError,
				slog.String("session_id", string(sid)),
				slog.String("phase", "append_question_after_infra_err"),
				slog.String("err", appendErr.Error()),
			)
		}
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "agent_talk"),
			slog.String("err", err.Error()),
		)
		return "", fmt.Errorf("orchestrator: AskOffPath: %w", err)
	}
	if res.Error != "" {
		// Domain-level error (claude unavailable, lock busy, etc.) — record
		// and surface as a Go error so the TUI can render a soft message.
		if appendErr := o.appendOffPathEventsCtx(ctx, sid, []store.Event{qEvent}); appendErr != nil {
			o.logger.WarnContext(ctx, trace.EvOffPathAskError,
				slog.String("session_id", string(sid)),
				slog.String("phase", "append_question_after_domain_err"),
				slog.String("err", appendErr.Error()),
			)
		}
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "agent_talk_domain"),
			slog.String("err", res.Error),
		)
		return "", fmt.Errorf("orchestrator: AskOffPath: %s", res.Error)
	}

	answer, _ := res.Data["answer"].(string)
	aEvent := newOrchestratorEvent(store.OffPathAnswer, map[string]any{
		"answer":  answer,
		"chat_id": chatID,
	}, 0)
	if err := o.appendOffPathEventsCtx(ctx, sid, []store.Event{qEvent, aEvent}); err != nil {
		// The user has their answer; just warn on persistence failure.
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "append_events"),
			slog.String("err", err.Error()),
		)
	}
	o.logger.DebugContext(ctx, trace.EvOffPathAskDone,
		slog.String("session_id", string(sid)),
		slog.String("chat_id", chatID),
		slog.Int("answer_bytes", len(answer)),
	)
	return answer, nil
}

// Off-path entry reasons recorded on the OffPathEntered event's `reason`
// field (Task 1.5). The field is additive — older cassettes that predate it
// replay unchanged — and lets a trace distinguish a typed `/freeform` entry
// from an automatic no-match off-ramp.
const (
	// offPathReasonFreeform labels a typed-trigger entry (the user said the
	// off_path trigger string). This is the historical, implicit reason.
	offPathReasonFreeform = "freeform"
	// offPathReasonOffRamp labels an automatic off-ramp entry: a free-text
	// no-match in a room that declared agent_off_ramp.
	offPathReasonOffRamp = "off_ramp"
	// offPathReasonConversation labels a conversation-lane entry: the room
	// declared agent_off_ramp.capture_free_text and its synthesized
	// <room>_discuss intent was diverted to the off-ramp conversation before
	// the state machine ran (maybeConversationDivert). Additive, like the
	// other reasons — older cassettes replay unchanged.
	offPathReasonConversation = "conversation"
)

// MarkOffPathEntered appends an OffPathEntered event for a typed-trigger
// (/freeform) entry. Held outside the session lock because off-path events do
// not affect turn numbering — they are stamped with maxTurn+1 (a side-channel
// allocation that does not advance the foreground turn counter).
//
// The event carries reason: "freeform" (Task 1.5); the field is additive, so
// older cassettes lacking it replay unchanged. For the automatic no-match
// entry use markOffRampEntered, which stamps reason: "off_ramp" plus the
// triggering error code.
func (o *Orchestrator) MarkOffPathEntered(sid app.SessionID, fromState app.StatePath) error {
	o.logger.DebugContext(context.Background(), trace.EvOffPathEnter,
		slog.String("session_id", string(sid)),
		slog.String("from_state", string(fromState)),
		slog.String("reason", offPathReasonFreeform),
	)
	ev := newOrchestratorEvent(store.OffPathEntered, map[string]any{
		"from_state": string(fromState),
		"reason":     offPathReasonFreeform,
	}, 0)
	return o.appendOffPathEvents(sid, []store.Event{ev})
}

// markOffRampEntered appends an OffPathEntered event for an automatic off-ramp
// entry — a free-text no-match in a room that declared agent_off_ramp. The
// event mirrors MarkOffPathEntered but labels reason: "off_ramp" and records
// the triggering no-match error code (UNKNOWN_INTENT / INTENT_UNKNOWN) plus the
// router confidence, so a trace can tell why the turn went free-form. All three
// fields are additive on the existing event (Task 1.5) — replay-safe.
func (o *Orchestrator) markOffRampEntered(ctx context.Context, sid app.SessionID, fromState app.StatePath, errorCode string, confidence float64) error {
	return o.markOffRampEnteredReason(ctx, sid, fromState, offPathReasonOffRamp, errorCode, confidence)
}

// markOffRampEnteredReason is markOffRampEntered parameterized on the entry
// reason so the conversation lane (reason: "conversation") shares the event
// shape without duplicating the append.
func (o *Orchestrator) markOffRampEnteredReason(ctx context.Context, sid app.SessionID, fromState app.StatePath, reason, errorCode string, confidence float64) error {
	o.logger.DebugContext(ctx, trace.EvOffPathEnter,
		slog.String("session_id", string(sid)),
		slog.String("from_state", string(fromState)),
		slog.String("reason", reason),
		slog.String("error_code", errorCode),
		slog.Float64("confidence", confidence),
	)
	ev := newOrchestratorEvent(store.OffPathEntered, map[string]any{
		"from_state": string(fromState),
		"reason":     reason,
		"error_code": errorCode,
		"confidence": confidence,
	}, 0)
	return o.appendOffPathEventsCtx(ctx, sid, []store.Event{ev})
}

// codeLLMClarification is the synthetic rejection code the orchestrator stamps
// when the harness returns a *harness.ClarifyResponse — the LLM/router answered
// but could not map the free text to any allowed intent. It is the dominant
// free-text no-match entry point into the off-ramp. It is NOT a machine.Turn
// ve.Code (machine.Turn never emits it); it originates in orchestrator.go's
// clarify branch, which now consults maybeOffRamp before returning ModeRejected.
const codeLLMClarification intent.ErrorCode = "LLM_CLARIFICATION"

// isNoMatchCode reports whether code is a genuine "the user said something we
// could not map to a declared intent" signal — the only class the agent
// off-ramp intercepts. There are three such entry points:
//
//   - LLM_CLARIFICATION — free-text path. The harness returned a ClarifyResponse
//     (the LLM couldn't classify the utterance); orchestrator.go's clarify branch
//     routes the ORIGINAL free text here. This is the dominant case and the whole
//     reason the off-ramp exists.
//   - UNKNOWN_INTENT — MCP/explicit-intent path. A caller submitted an intent
//     name the app declares nowhere (RunIntent / SubmitDirect).
//   - INTENT_UNKNOWN — semantic-tie / router-verdict path. The router reports it
//     could not map the utterance to any allowed intent.
//
// Every OTHER rejection code (GUARD_FAILED, MISSING_SLOTS,
// INTENT_NOT_ALLOWED_IN_STATE, INVALID_SLOT_VALUE, AMBIGUOUS_INTENT) is a
// meaningful, author-surfaced signal — the user DID name a real action, it just
// can't run right now — and must NOT off-ramp. The helper is deliberately inert
// for them (the scope guard, see docs/stories/meta-mode.md).
func isNoMatchCode(code intent.ErrorCode) bool {
	return code == intent.ErrUnknownIntent ||
		code == intent.ErrIntentUnknown ||
		code == codeLLMClarification
}

// maybeOffRamp is the single interception point for the agent off-ramp,
// consulted immediately before each shared ModeRejected return that carries
// ve.Code (the main-turn LLM path, the RunIntent path, and the ContinueTurn
// path). Routing all three rejection sites through one helper keeps them from
// drifting (Task 1.3).
//
// It fires only when BOTH hold:
//   - code is a genuine no-match (isNoMatchCode), AND
//   - the resting state declared agent_off_ramp (State.AgentOffRamp != nil,
//     after the loader's normalize pass).
//
// On a hit it records an OffPathEntered event labeled reason: "off_ramp" with
// the triggering code (markOffRampEntered), hands the user's ORIGINAL free
// text to an agent converse turn via askOffPathVoiced — honoring the room's
// agent_off_ramp voice over the app off_path: voice — and returns a
// ModeOffPath outcome WITHOUT advancing the state machine (no Turn(), no
// TransitionApplied) and WITHOUT mutating world. The resting state and its
// menu are returned unchanged so the same options are there next turn.
//
// On a miss it returns (nil, false) and the caller proceeds with the ordinary
// ModeRejected return. A converse failure also returns (nil, false): the
// off-ramp could not answer, so the caller falls back to the normal rejection
// rather than swallowing the turn — the user still sees a response.
//
// allowedNames is the resting state's menu (already computed at the call site)
// and turnNum is the foreground turn the rejection belonged to; both are
// echoed onto the outcome so the TUI/web can keep rendering the room.
func (o *Orchestrator) maybeOffRamp(
	ctx context.Context,
	sid app.SessionID,
	state app.StatePath,
	w world.World,
	input string,
	code intent.ErrorCode,
	confidence float64,
	allowedNames []string,
	turnNum app.TurnNumber,
) (*TurnOutcome, bool) {
	if !isNoMatchCode(code) {
		return nil, false
	}
	st := lookupStateByPath(o.def, state)
	if st == nil || st.AgentOffRamp == nil {
		return nil, false
	}
	// The off-ramp needs the original free text to converse over. A no-match
	// with empty input (e.g. a slot-continuation path that carries no fresh
	// utterance) has nothing to ask; fall back to the normal rejection.
	if input == "" {
		return nil, false
	}

	// maybeOffRamp runs from inside a foreground turn that already holds the
	// per-session lock (Turn/RunIntentWithInput/ContinueTurn). Mark ctx so the
	// off-path append path skips re-locking the same non-reentrant mutex.
	ctx = withOffRampLockHeld(ctx)

	// Record the entry first so the trace shows WHY the turn went free-form,
	// even if the converse call below fails.
	if err := o.markOffRampEntered(ctx, sid, state, string(code), confidence); err != nil {
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "off_ramp_mark_entered"),
			slog.String("err", err.Error()),
		)
	}

	answer, err := o.askOffPathVoiced(ctx, sid, input, o.offRampVoice(st), o.offRampScopeForState(state, st, w, allowedNames))
	if err != nil {
		// The off-ramp could not produce an answer. Fall back to the ordinary
		// rejection so the user is not left with a silent turn.
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "off_ramp_converse"),
			slog.String("err", err.Error()),
		)
		return nil, false
	}

	return &TurnOutcome{
		Mode:           ModeOffPath,
		View:           answer,
		NewState:       state,
		AllowedIntents: allowedNames,
		TurnNumber:     turnNum,
	}, true
}

// offRampVoice resolves the converse voice for a room's off-ramp. The room's
// agent_off_ramp: agent/persona takes precedence over the app off_path:
// voice (mirroring the agent/persona precedence noted in meta-mode.md); an
// empty field on the off-ramp falls back to the off_path: voice.
func (o *Orchestrator) offRampVoice(st *app.State) offRampVoice {
	base := o.offPathVoice()
	if st == nil || st.AgentOffRamp == nil {
		return base
	}
	v := base
	if st.AgentOffRamp.Persona != "" {
		// An explicit off-ramp persona overrides both the off_path persona and
		// agent (persona wins within a voice).
		v.persona = st.AgentOffRamp.Persona
		v.agent = ""
	} else if st.AgentOffRamp.Agent != "" {
		v.agent = st.AgentOffRamp.Agent
		v.persona = ""
	}
	return v
}

// MarkOffPathExited appends an OffPathExited event to the session's event log.
func (o *Orchestrator) MarkOffPathExited(sid app.SessionID, toState app.StatePath) error {
	o.logger.DebugContext(context.Background(), trace.EvOffPathExit,
		slog.String("session_id", string(sid)),
		slog.String("to_state", string(toState)),
	)
	ev := newOrchestratorEvent(store.OffPathExited, map[string]any{
		"to_state": string(toState),
	}, 0)
	return o.appendOffPathEvents(sid, []store.Event{ev})
}

// appendOffPathEvents is a thin wrapper around store.AppendEvents that
// no-ops when no store is wired (test scaffolds) and serialises under the
// per-session lock so off-path appends don't race with the foreground turn
// path or the background-job listener.
//
// Off-path events are stamped with a fresh turn number = max(existing turn
// in session) + 1 so two AskOffPath calls don't collide on the
// (session_id, turn, seq) primary key. The replay layer (store.BuildJourney)
// explicitly ignores off-path event turn numbers when computing js.Turn,
// so this side-channel allocation doesn't corrupt the foreground turn
// counter — see the off-path handling in store.BuildJourney.
func (o *Orchestrator) appendOffPathEvents(sid app.SessionID, events []store.Event) error {
	return o.appendOffPathEventsCtx(context.Background(), sid, events)
}

// AppendMiningEvent records one mining proposal event (MiningProposalRaised /
// MiningProposalDecided) as a side-channel beside the foreground turn — the
// same off-path append path GateDecided's siblings use, so the verdict lands in
// the trace with a fresh turn number and the replay layer folds it as a no-op.
// It is the exported seam the mining loop's EventSink adapter delegates to so
// the mining package never imports the orchestrator. payload is the marshaled
// typed payload (see store.MiningProposal*Payload).
func (o *Orchestrator) AppendMiningEvent(sid app.SessionID, kind store.EventKind, payload json.RawMessage) error {
	return o.appendOffPathEvents(sid, []store.Event{{Kind: kind, Payload: payload}})
}

// appendOffPathEventsCtx is appendOffPathEvents with the active context so the
// off-ramp can signal that the per-session lock is ALREADY held by the
// in-flight foreground turn (Turn/RunIntentWithInput/ContinueTurn all hold
// sessMu when they reach maybeOffRamp). Re-acquiring the same non-reentrant
// mutex from inside the turn would self-deadlock, so when offRampLockHeld(ctx)
// is set we skip the lock and rely on the caller's. The side-channels that run
// OUTSIDE a turn (/freeform, AskOffPath) call appendOffPathEvents and still
// take the lock themselves.
func (o *Orchestrator) appendOffPathEventsCtx(ctx context.Context, sid app.SessionID, events []store.Event) error {
	if o.store == nil || len(events) == 0 {
		return nil
	}
	if !offRampLockHeld(ctx) {
		mu := o.sessionLock(sid)
		mu.Lock()
		defer mu.Unlock()
	}

	// Allocate a fresh turn number for this batch of off-path events.
	// We use LoadHistory so the calculation is store-implementation-agnostic;
	// the lock above keeps it consistent against concurrent foreground turns.
	hist, err := o.store.LoadHistory(sid)
	if err != nil {
		return err
	}
	var maxTurn app.TurnNumber
	for _, ev := range hist {
		if ev.Turn > maxTurn {
			maxTurn = ev.Turn
		}
	}
	offTurn := maxTurn + 1
	for i := range events {
		events[i].Turn = offTurn
		// Record the foreground turn that was active when this off-path batch
		// was appended so the trace UI can sub-group these events under their
		// parent rather than rendering them as a top-level sibling turn.
		events[i].ParentTurn = maxTurn
	}
	// Sites 10–13: dual-write journal entries for off-path events.
	// Off-path events never mutate world or state; use empty worlds for the diff.
	emptyWorld := world.World{Vars: map[string]any{}}
	opJEntries := journalEntriesForEvents(sid, offTurn, time.Now(), events,
		emptyWorld, emptyWorld, "", "", "")
	return o.appendEventsAndJournal(sid, events, opJEntries)
}
