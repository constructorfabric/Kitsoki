// Package orchestrator — off-path runtime (§7.7, §11).
//
// Off-path is the global escape hatch: a free-form chat with the oracle
// that DOES NOT mutate world or state. It is intentionally orthogonal to
// the state machine — no Turn() is fired, no TransitionApplied event is
// emitted, the journey state is inviolate.
//
// The orchestrator owns the chat-thread resolution and the host.oracle.converse
// invocation directly (no allow-list check on the app's `hosts:` block —
// off-path is engine-provided, not app-provided). Events OffPathQuestion
// and OffPathAnswer are appended for replay parity; OffPathEntered and
// OffPathExited bracket the session.

package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// offPathRoom is the canonical room key used when resolving the per-session
// off-path chat thread. Combined with scope_key = session_id, this gives
// each session its own conversation that persists across off-path entries
// within the same session.
const offPathRoom = "off_path"

// AskOffPath fires a single host.oracle.converse turn for an off-path question
// against a per-session chat thread. It does NOT mutate world, advance the
// state machine, or emit StateExited/StateEntered events.
//
// On success, OffPathQuestion and OffPathAnswer events are appended to the
// session's event log (with turn = 0 to mark them as off-path/out-of-band).
// On failure, an OffPathQuestion event is still appended so the trace shows
// the user spoke; the error surfaces via the returned error.
//
// When no ChatStore is wired into the orchestrator, off-path runs in the
// legacy oracle.talk path (no chat persistence) — the user still gets an
// answer, but there is no transcript history across turns.
func (o *Orchestrator) AskOffPath(ctx context.Context, sid app.SessionID, question string) (string, error) {
	if question == "" {
		return "", fmt.Errorf("orchestrator: AskOffPath: empty question")
	}

	o.logger.DebugContext(ctx, trace.EvOffPathAskStart,
		slog.String("session_id", string(sid)),
		slog.Int("question_bytes", len(question)),
	)

	// Resolve (or create) the off-path chat thread for this session.
	// Scope by session_id so each session has its own thread that survives
	// repeated /freeform → /onpath cycles within the same session.
	chatID := ""
	if o.chatStore != nil {
		chatRec, _, err := o.chatStore.Resolve(ctx, o.def.App.ID, offPathRoom, string(sid), "off-path chat")
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

	// Build args mirroring the dev-story oracle.yaml host.oracle.converse pattern.
	args := map[string]any{
		"question": question,
	}
	if chatID != "" {
		args["chat_id"] = chatID
	}
	// App-tunable persona for the off-path voice. Two equivalent inputs,
	// in priority order:
	//   1. OffPathDef.Persona   — inline back-compat shortcut. Wins when set.
	//   2. OffPathDef.Agent     — names an entry in AppDef.Agents. The
	//                             handler's resolveAgent reads the agent
	//                             from ctx and applies SystemPrompt + Model.
	// Either path lands on the same claude flags
	// (--append-system-prompt [+ --model]); the agent: route keeps the
	// persona text declared in one place (the agents block) rather than
	// duplicated under off_path:.
	if o.def.OffPath != nil {
		if o.def.OffPath.Persona != "" {
			args["system_prompt"] = o.def.OffPath.Persona
		} else if o.def.OffPath.Agent != "" {
			args["agent"] = o.def.OffPath.Agent
		}
	}

	// Inject the ChatStore into ctx so the chat-aware handler path engages.
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	// Inject the agents map so handlers can resolve an `agent:` arg (used
	// by the off-path-via-agent path above when Model is set on the
	// resolved agent) without having to import the app package.
	ctx = host.WithAgents(ctx, agentsForContext(o.def))

	// Always log the question first — even if the call below fails, the
	// trace shows the user spoke off-path.
	qEvent := newOrchestratorEvent(store.OffPathQuestion, map[string]any{
		"question": question,
		"chat_id":  chatID,
	}, 0)

	res, err := host.OracleConverseHandler(ctx, args)
	if err != nil {
		// Infrastructure failure (claude binary issues, etc.) — record the
		// question, surface the error to the caller.
		if appendErr := o.appendOffPathEvents(sid, []store.Event{qEvent}); appendErr != nil {
			o.logger.WarnContext(ctx, trace.EvOffPathAskError,
				slog.String("session_id", string(sid)),
				slog.String("phase", "append_question_after_infra_err"),
				slog.String("err", appendErr.Error()),
			)
		}
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "oracle_talk"),
			slog.String("err", err.Error()),
		)
		return "", fmt.Errorf("orchestrator: AskOffPath: %w", err)
	}
	if res.Error != "" {
		// Domain-level error (claude unavailable, lock busy, etc.) — record
		// and surface as a Go error so the TUI can render a soft message.
		if appendErr := o.appendOffPathEvents(sid, []store.Event{qEvent}); appendErr != nil {
			o.logger.WarnContext(ctx, trace.EvOffPathAskError,
				slog.String("session_id", string(sid)),
				slog.String("phase", "append_question_after_domain_err"),
				slog.String("err", appendErr.Error()),
			)
		}
		o.logger.WarnContext(ctx, trace.EvOffPathAskError,
			slog.String("session_id", string(sid)),
			slog.String("phase", "oracle_talk_domain"),
			slog.String("err", res.Error),
		)
		return "", fmt.Errorf("orchestrator: AskOffPath: %s", res.Error)
	}

	answer, _ := res.Data["answer"].(string)
	aEvent := newOrchestratorEvent(store.OffPathAnswer, map[string]any{
		"answer":  answer,
		"chat_id": chatID,
	}, 0)
	if err := o.appendOffPathEvents(sid, []store.Event{qEvent, aEvent}); err != nil {
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

// MarkOffPathEntered appends an OffPathEntered event to the session's event
// log. Held outside the session lock because off-path events do not affect
// turn numbering — they are stamped with maxTurn+1 (a side-channel allocation
// that does not advance the foreground turn counter).
func (o *Orchestrator) MarkOffPathEntered(sid app.SessionID, fromState app.StatePath) error {
	o.logger.DebugContext(context.Background(), trace.EvOffPathEnter,
		slog.String("session_id", string(sid)),
		slog.String("from_state", string(fromState)),
	)
	ev := newOrchestratorEvent(store.OffPathEntered, map[string]any{
		"from_state": string(fromState),
	}, 0)
	return o.appendOffPathEvents(sid, []store.Event{ev})
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
// counter — see isOffPathEvent in internal/store/replay.go.
func (o *Orchestrator) appendOffPathEvents(sid app.SessionID, events []store.Event) error {
	if o.store == nil || len(events) == 0 {
		return nil
	}
	mu := o.sessionLock(sid)
	mu.Lock()
	defer mu.Unlock()

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
	return o.store.AppendEventsAndJournal(sid, events, opJEntries)
}
