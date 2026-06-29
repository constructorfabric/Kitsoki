package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/transport"
	"kitsoki/internal/world"
)

// dispatchHostCalls invokes each HostInvocation, applies bindings to world,
// and re-renders the view. Returns the new events, the updated world, the
// refreshed view (empty if no changes), an override state path (non-empty
// when an `on_error:` arc fires and the caller must redirect to the error
// state), and an error only when re-rendering fails.
//
// Individual handler failures without `on_error:` are folded into
// world.last_error and emitted as HostReturned events with error payloads —
// they do not stop dispatch of the remaining calls.  When an
// `on_error:` arc IS declared on the failing host call, dispatch of the
// remaining calls in the same on_enter block is aborted and the named error
// state is entered: its on_enter chain runs (including any host calls it
// emits), a TransitionApplied event is appended so replay restores the
// redirected state, and the view is rendered from the error state.
//
// When o.hosts is nil (deterministic flow tests), returns no events and the
// original world unchanged.
// writeModePosture resolves the write_mode posture for a dispatching state path
// and the active grant scope from the world (agent-write-mode-opt-in). It returns
// the room's write_mode (the leaf state's, falling back to its top-level room
// state — write_mode is a room-level field) and the active scope read from the
// engine-reserved write_mode_scope world key. Both empty when no read_only room
// is in effect, so the dispatch posture stays byte-for-byte today's. nil-safe.
func (o *Orchestrator) writeModePosture(state app.StatePath, w world.World) (writeMode, scope string) {
	if o == nil || o.def == nil {
		return "", ""
	}
	wm := ""
	if s := lookupStateByPath(o.def, state); s != nil && s.WriteMode != "" {
		wm = s.WriteMode
	} else {
		// write_mode is authored on the room (top-level) state; a dispatch from a
		// nested leaf inherits the room's posture.
		p := string(state)
		if idx := strings.Index(p, "#"); idx >= 0 {
			p = p[:idx]
		}
		if idx := strings.Index(p, "."); idx >= 0 {
			p = p[:idx]
		}
		if room := lookupStateByPath(o.def, app.StatePath(p)); room != nil {
			wm = room.WriteMode
		}
	}
	if wm != app.WriteModeReadOnly {
		return wm, "" // open / absent: no active scope to carry
	}
	if w.Vars != nil {
		if sc, ok := w.Vars[app.WriteModeScopeWorldKey].(string); ok {
			scope = sc
		}
	}
	return wm, scope
}

func (o *Orchestrator) dispatchHostCalls(ctx context.Context, sid app.SessionID, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]store.Event, world.World, string, app.StatePath, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, w, "", "", nil
	}

	if o.transports != nil {
		ctx = transport.WithRegistry(ctx, o.transports)
	}
	if o.jobStore != nil {
		ctx = host.WithClarificationAnswerer(ctx, o.jobStore)
		// Wire host.inbox.add to JobStore.InsertNotification.  The
		// adapter is per-(orchestrator, session) and is rebuilt for
		// each dispatch — cheap (two pointer copies) and avoids
		// holding a stale session ID across long-lived orchestrator
		// references.  Without this seam every host.inbox.add call
		// returns persisted:false and the notification is dropped.
		// (P1-C from the dev-story-bugfix-unify Opus review.)
		ctx = host.WithInboxAdder(ctx, inbox.NewJobStoreAdder(o.jobStore, sid))
	}
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	// Inject the journal writer so host.artifacts_dir (and any producer that
	// media-emits a file) records a KindArtifactEmitted entry the
	// JournalArtifactResolver — and thus the /artifact/{id} route and the
	// /review video-feedback surface — can resolve. Without this the media-emit
	// branch copies the file but journals nothing, so the bound handle never
	// resolves. nil is safe (the handler skips journaling).
	if o.journalWriter != nil {
		ctx = host.WithArtifactJournalWriter(ctx, journal.SessionStamping(o.journalWriter, sid))
	}
	// Inject the frame resolver so an operator-facing agent call (converse/ask)
	// rejects an `input.visual` block whose frame_handle does not resolve to a
	// recorded artifact (docs/tracing/trace-format.md: no dangling frame
	// reference). Backed by the read side of the same artifact substrate
	// host.artifacts_dir records into. nil is safe (the check is skipped when no
	// reader is wired — flow fixtures with no substrate).
	if o.journalReader != nil {
		ctx = host.WithFrameResolver(ctx, host.FrameResolverFunc(func(handle string) bool {
			return artifactHandleResolves(o.journalReader, sid, handle)
		}))
	}
	// Inject the agents map so host.agent.* invocations can resolve
	// `with: { agent: <name> }` references to a host.Agent value. Built
	// once per dispatch (cheap — translation is tag-equivalent).
	ctx = host.WithAgents(ctx, agentsForContext(o.def))
	ctx = host.WithProviders(ctx, providersForContext(o.def))
	// Resolve the live harness selection once for this dispatch: the active
	// profile chooses the backend to fork and the env/model default (installed as
	// the operator-selected provider via WithActiveProfile). No profile selected
	// ⇒ the static backend and a no-op active profile (legacy path, byte-identical).
	backendName, activeProfile := o.resolveSelection(o.agentBackendName)
	ctx = host.WithAgentBackendNamed(ctx, backendName)
	ctx = host.WithActiveProfile(ctx, activeProfile)
	// Inject the prompt renderer so agent handlers resolve and render prompt
	// files through the story's overlay → story search path. nil is safe
	// (handlers use the legacy path).
	ctx = host.WithPromptRenderer(ctx, o.promptRenderer)
	// Inject the project's Layer-2 system-prompt grounding (app.context /
	// context_path) so every agent call composes kitsoki → project → task.
	ctx = host.WithProjectContext(ctx, projectContextFor(o.def))
	// Inject the live IDE link so host.ide.* handlers resolve the editor and the
	// agent env-scrub gate engages. nil is safe (not-connected result, no
	// scrub). The `world.ide.connected` gate is seeded once per turn in
	// loadJourney (the single seam every room runs through, including rooms with
	// no host calls), so it is NOT written here; re-seed against this dispatch's
	// world in case a redirect/post-bind recursion handed us a freshly-rebuilt
	// world without it.
	ctx = host.WithIDELink(ctx, o.currentIDELink())
	o.seedIDEConnected(w)
	// Re-seed world.session_id against this dispatch's world in case a
	// redirect/post-bind recursion handed us a freshly-rebuilt world without
	// it (mirrors seedIDEConnected just above). session_id drives a session-
	// distinct workdir; without it a redirect could re-derive a ticket-only
	// workdir and collide across concurrent sessions (bug9glm2).
	o.seedSessionID(w, sid)

	// Wave 3-agent: inject the EventSink so agent handlers can parallel-write
	// AgentCalled / AgentReturned / AgentError events to the JSONL alongside
	// the existing journal write.
	if o.eventSink != nil {
		ctx = host.WithAgentEventSink(ctx, o.eventSink)
		// Also inject the prompts directory so large prompts are stored separately
		// to stay under PIPE_BUF. Extract it from the JSONLSink path.
		if jl, ok := o.eventSink.(*store.JSONLSink); ok {
			traceDir := filepath.Dir(jl.Path)
			promptsDir := filepath.Join(traceDir, "agent-prompts")
			ctx = host.WithAgentPromptsDir(ctx, promptsDir)
			// Install the per-call agent-action-transcript writer alongside the
			// prompts dir, so the claude tee / out-of-host backends persist their
			// native execution detail to <trace_dir>/transcripts/<call_id>.jsonl
			// (created lazily on first write). The same seam serves web + flow +
			// replay so the web RPC can later read these sidecars. See
			// docs/tracing/trace-format.md (Agent-action transcript sidecar).
			transcriptsDir := filepath.Join(traceDir, "transcripts")
			ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(transcriptsDir))
		} else if td, ok := o.eventSink.(interface{ TranscriptsDir() string }); ok {
			// Web/live posture: the sink is a *runstatus/server.LiveSession that
			// wraps the JSONLSink, so the direct type assertion above misses. Install
			// the transcript writer via the dir it exposes (the RPC reads these
			// sidecars). Prompt-offload is intentionally left unchanged here (prompts
			// stay inline in the web trace) to avoid altering the existing web trace
			// shape. Discovered through an anonymous interface to avoid an import
			// cycle (orchestrator must not import runstatus/server).
			if dir := td.TranscriptsDir(); dir != "" {
				ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(dir))
			}
		}
	}
	// B-2: inject the agent plugin registry so handlers can route through
	// Agent.Ask. When nil, handlers fall through to direct claude-CLI logic.
	if o.agentRegistry != nil {
		ctx = host.WithAgentRegistry(ctx, o.agentRegistry)
	}
	// AgentCallCtx carries session/turn/state so agent.call.start /
	// agent.call.complete events are stamped with the FOREGROUND turn of the
	// transition that entered `state` and with `state` itself as the
	// destination phase (see docs/tracing/trace-format.md). The Turn is supplied
	// by the turn entry point via WithAgentCallCtx on the ctx (Turn /
	// RunIntent / SubmitDirect) and is inherited here — including across the
	// post-bind emit recursion (settlePostBindEmits), which is how the bugfix
	// story advances phase-to-phase. We always rewrite StatePath to `state`
	// (the destination phase the on_enter chain is running in) so a stale
	// entry-state path can never leak onto a phase's agent events; the Turn
	// is preserved from any existing ctx (zero only when no entry point set it,
	// e.g. RunInitialOnEnter, which stamps turn=0 deliberately).
	existing := host.AgentCallCtxFrom(ctx)
	// Write-mode posture (agent-write-mode-opt-in): carry the dispatching room's
	// write_mode and the active grant scope (the engine-reserved write_mode_scope
	// world key) so host.agent.task can boot the agent read-only and gate
	// mutating steps. Absent / open leaves both empty → today's dispatch posture.
	writeMode, writeModeScope := o.writeModePosture(state, w)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID:      sid,
		Turn:           existing.Turn,
		StatePath:      state,
		WriteMode:      writeMode,
		WriteModeScope: writeModeScope,
	})

	var events []store.Event
	applied := false
	var redirect app.StatePath

	// dispatchBinds accumulates the world keys bound by host results as the
	// loop progresses, so a later invoke's `with:` re-render sees an earlier
	// invoke's bind. It is layered on top of each invoke's machine-time
	// WorldSnapshot — NOT the live post-chain world — so a `set:` positioned
	// AFTER an invoke in the same chain cannot clobber that invoke's args.
	// See machine.HostInvocation.WorldSnapshot.
	dispatchBinds := map[string]any{}

	// batchCost sums the agent cost (total_cost_usd) of every call in this
	// batch; folded into the reserved cost world vars after the loop.
	var batchCost float64

	for _, hc := range calls {
		// Background invocations go to the scheduler; foreground go to the host registry.
		if hc.Background && o.scheduler != nil {
			bgEvents, bgWorld, bgErr := o.dispatchBackground(ctx, sid, state, hc, w)
			if bgErr != nil {
				o.logger.ErrorContext(ctx, trace.EvJobError,
					slog.String("session_id", string(sid)),
					slog.String("namespace", hc.Namespace),
					slog.String("phase", "dispatch_background"),
					slog.String("err", bgErr.Error()),
				)
				w.Vars["last_error"] = bgErr.Error()
				events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
					"set": map[string]any{"last_error": bgErr.Error()},
				}, 0))
			} else {
				w = bgWorld
			}
			events = append(events, bgEvents...)
			applied = true
			continue
		}

		// Re-render RawWith against the current world so downstream
		// effects in the same `on_enter:` block see prior binds.  Falls
		// back to hc.Args if RawWith isn't set (older HostInvocation
		// instances or test stubs).  See the corresponding machine-side
		// note on HostInvocation.RawWith. The world is the invoke's
		// position-snapshot + accumulated binds, NOT the live post-chain
		// world, so a later `set:` cannot clobber these args — see
		// dispatchRerenderWorld.
		invokeArgs, fellBack := rerenderHostArgs(hc, dispatchRerenderWorld(hc, dispatchBinds, w))
		invokeArgs, err := o.prepareHostInvokeArgs(sid, state, hc, invokeArgs)
		if err != nil {
			return events, w, "", "", err
		}

		// HostDispatched records the *actual* args the handler is about
		// to receive (post-rerender), so the event trace is honest even
		// when rerenderHostArgs had to fall back for one or more leaves.
		// Unlike HostInvoked (which snapshots pre-bind args at machine
		// time), this fires immediately before the handler is invoked.
		// Replay treats it as a no-op (see store/replay.go).
		//
		// Stamp it with the foreground turn (existing.Turn, inherited from the
		// turn entry point via AgentCallCtx) so the live JSONL write buckets it
		// under the turn it actually belongs to, not turn 0.
		hostDispatchedEv := newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, existing.Turn)
		// Flush HostDispatched to the JSONL sink LIVE, before the (possibly
		// long-blocking) Invoke below — otherwise the whole turn's event batch
		// is committed only at turn-end, so a slow or wedged host.run leaves the
		// trace and the web SSE stream empty and the UI frozen with nothing to
		// show for it (the silent-freeze half of the triage-hang bug). Mirrors
		// the agent handlers' live AgentCalled write (see WithAgentEventSink
		// above).
		//
		// The event ALSO stays in the returned `events` batch unconditionally:
		// that batch is what expect_host_calls assertions read (tr.Events =
		// machResult.Events) and what the SQLite write consumes — dropping it
		// from the batch silently broke every `expect_host_calls` assertion and
		// left HostDispatched out of SQLite. To avoid a DUPLICATE JSONL line, we
		// mark the event SinkFlushed so appendEventsAndJournal's turn-end sink
		// write skips re-appending it (it still goes to SQLite). When there is no
		// eventSink (pure-SQLite / test scaffolds) the live flush is a no-op and
		// the batch is the only writer.
		if o.eventSink != nil {
			hostDispatchedEv.SinkFlushed = true
			if err := o.eventSink.Append(hostDispatchedEv); err != nil {
				o.logger.WarnContext(ctx, "host.dispatched.flush_error",
					slog.String("session_id", string(sid)),
					slog.String("namespace", hc.Namespace),
					slog.String("phase", "host_dispatched_live_flush"),
					slog.String("err", err.Error()),
				)
			}
		}
		events = append(events, hostDispatchedEv)

		// B-7: inject the agent plugin alias into the context so the handler
		// can route through host.Dispatch with the correct plugin. When
		// AgentPlugin is empty the handler falls back to "agent.claude" (the
		// default). This is the production wiring that makes explicit `agent:`
		// effect fields take effect at runtime.
		// Install a fresh per-call usage box so the claude CLI transport can
		// record token usage that appendAgentReturnedEvent surfaces on the
		// AgentReturned event's Meta. One box per host call keeps usage from
		// leaking between calls in the same on_enter block.
		invokeCtx := host.WithAgentUsageBox(ctx)
		if hc.AgentPlugin != "" {
			invokeCtx = host.WithAgentPluginName(invokeCtx, hc.AgentPlugin)
		}
		// Expose the world as it stands at this call (after earlier binds in the
		// same on_enter block) so host.starlark.run scripts can read ctx.world.
		invokeCtx = host.WithWorldSnapshot(invokeCtx, w.Vars)

		res, err := o.hosts.Invoke(invokeCtx, hc.Namespace, invokeArgs)
		batchCost += host.AgentCostFrom(invokeCtx)
		// Operator cancellation (runstatus.session.cancel) propagates a context
		// cancel down to the agent subprocess via exec.CommandContext, which
		// surfaces here as a cancelled ctx + an Invoke error. Abort the WHOLE
		// on_enter dispatch WITHOUT baking the cancellation into last_error /
		// host_error or routing through on_error: a cancelled turn must leave the
		// session at its pre-turn state, never persist "context canceled" into the
		// view (the same poisoning the turn-stream WithoutCancel guards against for
		// client disconnects). Returning the error makes Turn drop every event
		// accumulated so far and persist nothing. Checked before the err/res.Error
		// branches so cancellation never masquerades as a domain failure.
		if ctx.Err() != nil {
			return events, w, "", "", ctx.Err()
		}
		if err != nil {
			// Infrastructure failure (e.g. handler not registered): record and move on.
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"last_error": err.Error()},
			}, 0))
			// Structured global host_error so the redirect target room can render
			// a rich error (namespace + message). Reserved global, never folded.
			herr := map[string]any{
				"namespace": hc.Namespace,
				"message":   err.Error(),
			}
			w.Vars["host_error"] = herr
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"host_error": herr},
			}, 0))
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			// Honour on_error even on infrastructure failure: the
			// app-author's intent is "if this host call doesn't succeed,
			// route here", and "never registered" is a stronger failure
			// than a non-zero exit.  Stop processing further calls.
			if hc.OnError != "" {
				o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
					slog.String("session_id", string(sid)),
					slog.String("namespace", hc.Namespace),
					slog.String("from", string(state)),
					slog.String("to", hc.OnError),
					slog.String("error", err.Error()),
					slog.String("phase", "infra"),
				)
				redirect = app.StatePath(hc.OnError)
				break
			}
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"last_error": res.Error},
			}, 0))
			// Structured global host_error mirrors last_error but carries the
			// namespace and (when the host result returned a Data payload) the
			// raw data plus the conventional stderr/exit_code host.run carries.
			// Reserved global key, never namespaced by import folding.
			herr := map[string]any{
				"namespace": hc.Namespace,
				"message":   res.Error,
			}
			if res.Data != nil {
				herr["data"] = res.Data
				if v, ok := res.Data["stderr"]; ok {
					herr["stderr"] = v
				}
				if v, ok := res.Data["exit_code"]; ok {
					herr["exit_code"] = v
				}
			}
			w.Vars["host_error"] = herr
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"host_error": herr},
			}, 0))
		}

		// Emit one EffectApplied event per binding so replay reconstructs
		// the final world deterministically from the event log.
		//
		// `dkey` is either a dot-separated path (e.g. `submitted.names[0]`)
		// resolved against res.Data, or — when it contains `{{` — an
		// expr-lang template rendered against an env that exposes res.Data
		// as the `result` root plus the current (post-prior-binds) world.
		// The template form lets authors derive values at bind time without
		// a follow-up subprocess (e.g.
		// `party_names: "{{ join(result.submitted.names, ',') }}"`).
		bindEnv, hasBindEnv := hc.Env.(expr.Env)
		// Iterate bind keys in sorted order, NOT raw Go map order: map
		// iteration is randomized, so two live runs with identical results
		// would otherwise emit these EffectApplied events in different
		// orders (non-deterministic trace) and a template `bind:` that reads
		// a sibling key bound in the same block would render differently per
		// run. Sorting makes both emission order and intra-block reads
		// deterministic. Replay is unaffected (it consumes the recorded log).
		for _, wkey := range slices.Sorted(maps.Keys(hc.Bind)) {
			dkey := hc.Bind[wkey]
			var (
				val any
				ok  bool
			)
			if containsTemplate(dkey) {
				if !hasBindEnv {
					continue
				}
				bindEnv.World = w.Vars
				bindEnv.Result = res.Data
				rendered, err := expr.RenderValue(dkey, bindEnv)
				if err != nil {
					o.logger.WarnContext(ctx, trace.EvHostBindError,
						slog.String("session_id", string(sid)),
						slog.String("namespace", hc.Namespace),
						slog.String("bind_key", wkey),
						slog.String("template", dkey),
						slog.String("err", err.Error()),
					)
					continue
				}
				val = rendered
				ok = true
			} else {
				if res.Data == nil {
					continue
				}
				val, ok = lookupBindPath(res.Data, dkey)
				if !ok {
					continue
				}
			}
			w.Vars[wkey] = val
			dispatchBinds[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))

		// If the call failed and the author declared an `on_error:` arc,
		// abort dispatch of the remaining calls in this on_enter block
		// and route to the error state.  This is what makes pass/fail
		// host scripts (the bugfix room's verifier, deploy, etc.)
		// actually block the pipeline instead of silently advancing.
		if res.Error != "" && hc.OnError != "" {
			o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
				slog.String("session_id", string(sid)),
				slog.String("namespace", hc.Namespace),
				slog.String("from", string(state)),
				slog.String("to", hc.OnError),
				slog.String("error", res.Error),
				slog.String("phase", "domain"),
			)
			redirect = app.StatePath(hc.OnError)
			break
		}
	}

	// Fold this batch's agent spend into the reserved cost world vars before the
	// redirect/render paths so they (and any error room) see the current totals.
	if costEvents := foldAgentCost(&w, batchCost); len(costEvents) > 0 {
		events = append(events, costEvents...)
		applied = true
	}

	if redirect != "" {
		// Run the error state's on_enter chain and recursively dispatch
		// any host calls it emits.  Append a TransitionApplied event so
		// replay correctly lands the journey in the error state after a
		// process restart.  resolvedRedirect captures the emit_intent-
		// resolved leaf when the error state's on_enter chain emitted
		// onward (P1-D); when no emit fired it equals `redirect`.
		errEvents, errWorld, errView, resolvedRedirect, redirErr := o.enterRedirectState(ctx, sid, state, redirect, w)
		if redirErr != nil {
			// Even on cap-fire / infra error, enterRedirectState may have
			// produced events (notably the HarnessError carrying
			// reason=on_error.depth_cap_exceeded). Append them so the
			// failure surfaces in the persisted journal — without this
			// the SubmitDirect/Turn caller still gets a clean turn-end
			// event sequence but the operator-visible diagnostic
			// vanishes.
			events = append(events, errEvents...)
			return events, errWorld, "", "", redirErr
		}
		events = append(events, errEvents...)
		w = errWorld
		applied = true
		if errView == "" {
			// Fallback: render the resolved error state's view against the
			// post-on_enter world so callers always have a refreshed
			// view to show the user.
			v, rErr := o.machine.RenderState(resolvedRedirect, w)
			if rErr != nil {
				return events, w, "", "", fmt.Errorf("orchestrator: render redirect state %q: %w", resolvedRedirect, rErr)
			}
			errView = v
		}
		return events, w, errView, resolvedRedirect, nil
	}

	if !applied {
		return events, w, "", "", nil
	}

	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return events, w, "", "", fmt.Errorf("orchestrator: re-render after host dispatch: %w", err)
	}
	// A nil-error empty view is the silent dead-end mode the user
	// witnessed on 2026-05-20: the post-bind RenderState returned
	// ("", nil) and result.View ended up empty (the initial machine.Turn
	// render skipped because the on_enter chain had host calls that
	// would bind). Surface the unusual case to the trace so a future
	// occurrence is diagnosable from the trace alone.
	if view == "" {
		o.logger.Warn("orchestrator.post_bind_render_empty",
			slog.String("state", string(state)),
			slog.Int("world_keys", len(w.Vars)),
		)
	}
	return events, w, view, "", nil
}

// redirectDepthKey is the context key holding the current
// on_error-redirect recursion depth for the active turn-side host
// dispatch. enterRedirectState increments it on each recursion and
// surfaces a HarnessError when it exceeds the cap; values at the
// top-level dispatch entry are zero.
type redirectDepthKey struct{}

func withRedirectDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, redirectDepthKey{}, d)
}

func redirectDepthFromCtx(ctx context.Context) int {
	if v, ok := ctx.Value(redirectDepthKey{}).(int); ok {
		return v
	}
	return 0
}

func (o *Orchestrator) prepareHostInvokeArgs(sid app.SessionID, state app.StatePath, hc machine.HostInvocation, args map[string]any) (map[string]any, error) {
	if hc.Namespace != "host.chat.drive" {
		return args, nil
	}
	out := make(map[string]any, len(args)+3)
	for k, v := range args {
		out[k] = v
	}
	out["__origin_session_id"] = string(sid)
	out["__origin_state"] = string(state)
	if len(hc.OnComplete) > 0 {
		b, err := json.Marshal(hc.OnComplete)
		if err != nil {
			return nil, fmt.Errorf("host.chat.drive: marshal on_complete: %w", err)
		}
		out["__on_complete"] = string(b)
	}
	return out, nil
}

// enterRedirectState runs the on_enter chain for the named error state and
// recursively dispatches any host calls it emits.  Used by dispatchHostCalls
// to land the session in the on_error: target after a host failure.
//
// Emits a TransitionApplied event (from prior → target) so the replayer
// updates the journey state, plus StateExited/StateEntered events to mirror
// the regular machine.Turn transition shape.  Returns the accumulated
// events, the post-on_enter world, the rendered view (empty if rendering
// is left to the caller), the resolved leaf state (which may differ from
// `target` when the error state's on_enter chain emit_intented onward, or
// when a nested on_error redirected to a deeper error state), and a
// non-nil error only on infrastructure failure.
//
// Calls RunEffectsAndState (not RunEffects) so emit_intent dispatched
// inside the error state's on_enter steers the resolved leaf — without
// this the session would land at `target` even when an emit_intent has
// already routed it onward.  (P1-D from the dev-story-bugfix-unify Opus
// review.)
func (o *Orchestrator) enterRedirectState(ctx context.Context, sid app.SessionID, prior, target app.StatePath, w world.World) ([]store.Event, world.World, string, app.StatePath, error) {
	// Bound recursion depth. Each on_error redirect that runs an
	// on_enter chain whose host calls fail with another on_error: arc
	// recurses through dispatchHostCalls → enterRedirectState. Without
	// a cap, a host that fails idempotently (e.g.
	// `git worktree add` against an existing dir) loops forever:
	// idle.on_enter creates a workspace that fails → on_error: idle →
	// idle.on_enter runs again → repeat. Cap at 4 redirects per
	// turn-side dispatch. On overflow we surface a HarnessError and
	// stay at the deepest resolved state rather than infinite-looping
	// or popping back to the original on_error target.
	const maxRedirectDepth = 4
	depth := redirectDepthFromCtx(ctx)
	if depth > maxRedirectDepth {
		o.logger.WarnContext(ctx, "orchestrator.on_error.depth_cap_exceeded",
			slog.String("session_id", string(sid)),
			slog.String("prior", string(prior)),
			slog.String("target", string(target)),
			slog.Int("depth", depth),
			slog.Int("cap", maxRedirectDepth),
		)
		ev := newOrchestratorEvent(store.HarnessError, map[string]any{
			"reason":  "on_error.depth_cap_exceeded",
			"prior":   string(prior),
			"target":  string(target),
			"depth":   depth,
			"cap":     maxRedirectDepth,
			"message": "on_error redirect chain exceeded depth cap; staying at the originating state. A host call's on_error: arc is looping (likely the same host failing repeatedly).",
		}, 0)
		return []store.Event{ev}, w, "", prior, fmt.Errorf("orchestrator: on_error redirect from %q to %q exceeded depth cap %d — host call's on_error chain is looping", prior, target, maxRedirectDepth)
	}
	ctx = withRedirectDepth(ctx, depth+1)

	// Resolve `../`-relative on_error targets against the prior state
	// path. The import rewriter (internal/app/imports.go) rewrites a
	// bare-name on_error target like `ticket_search` declared inside an
	// imported sub-story to `../ticket_search` so it resolves to a
	// sibling of the import wrapper. lookupStateByPath only understands
	// flat dotted paths, so without resolving the `..` first the
	// redirect would always fail with "on_error target state not found"
	// for any import-folded room with an `on_error:` arc — which is
	// every dev-story room with an on_enter invoke.
	target = app.StatePath(resolveOnCompleteTarget(string(prior), string(target)))

	// Validate target exists; if not, surface as an infrastructure error.
	tgtState := lookupStateByPath(o.def, target)
	if tgtState == nil {
		return nil, w, "", target, fmt.Errorf("orchestrator: on_error target state %q not found", target)
	}

	// Self-redirect: the on_error arc points back at the current room.
	// Re-firing on_enter would re-invoke the host call that just failed,
	// land here again, and loop. Treat this as "stay in place, surface
	// the failure via last_error" and return without re-running on_enter.
	// Authors writing `on_error: <self>` mean "don't bail out" — not
	// "re-enter and try again forever".
	if target == prior {
		o.logger.DebugContext(ctx, "orchestrator.on_error.self_redirect_skipped",
			slog.String("session_id", string(sid)),
			slog.String("state", string(target)),
		)
		return nil, w, "", target, nil
	}

	var events []store.Event

	// TransitionApplied is the event the replayer uses to update
	// js.State, so it must be emitted for the redirect to survive a
	// process restart.
	events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
		"from":   string(prior),
		"to":     string(target),
		"intent": "on_error",
	}, 0))

	// Mirror the StateExited/StateEntered shape that machine.Turn emits
	// for a regular transition.  Single-level paths only — compound
	// state hierarchies are handled as a flat exit/enter pair, which
	// matches the on_error: arc's flat-target contract.
	events = append(events, newOrchestratorEvent(store.StateExited, map[string]any{
		"state": string(prior),
	}, 0))
	events = append(events, newOrchestratorEvent(store.StateEntered, map[string]any{
		"state": string(target),
	}, 0))

	resolved := target

	// Run the error state's on_enter via the machine.  This collects
	// any nested host calls so we can recurse below.  RunEffectsAndState
	// also returns the leaf state path the chain settled at — if the
	// error state's on_enter contains an emit_intent that fires at
	// machine time, the resolved leaf differs from target and the
	// orchestrator must surface it as the post-redirect state.
	if len(tgtState.OnEnter) > 0 {
		emitState, newWorld, hostCalls, _, effEvents, runErr := o.machine.RunEffectsAndState(ctx, target, w, tgtState.OnEnter)
		if runErr != nil {
			return events, w, "", target, fmt.Errorf("orchestrator: run on_enter for redirect %q: %w", target, runErr)
		}
		w = newWorld
		events = append(events, effEvents...)
		if emitState != "" && emitState != target {
			resolved = emitState
		}

		// Recursively dispatch.  A nested on_error redirect supersedes
		// this one — the caller will see the deepest target.
		if len(hostCalls) > 0 {
			nestedEvents, nestedWorld, nestedView, nestedRedirect, nestedErr := o.dispatchHostCalls(ctx, sid, hostCalls, w, resolved)
			if nestedErr != nil {
				// Propagate nested events even on error so the
				// HarnessError emitted by a deeper cap-fire reaches
				// the persisted journal. Mirrors the outer
				// dispatchHostCalls branch.
				events = append(events, nestedEvents...)
				return events, nestedWorld, "", resolved, nestedErr
			}
			events = append(events, nestedEvents...)
			w = nestedWorld
			if nestedRedirect != "" {
				// A deeper on_error fired; emit one more
				// TransitionApplied so replay lands at the
				// deepest target, but otherwise let the
				// nested events already capture the chain.
				events = append(events, newOrchestratorEvent(store.TransitionApplied, map[string]any{
					"from":   string(resolved),
					"to":     string(nestedRedirect),
					"intent": "on_error",
				}, 0))
				return events, w, nestedView, nestedRedirect, nil
			}
			return events, w, nestedView, resolved, nil
		}
	}

	return events, w, "", resolved, nil
}

// dispatchHostCallsDetailed is the same dispatch loop as dispatchHostCalls
// but additionally returns one HostCallSummary per invocation so callers
// (currently OneShot) can surface args/data/error to the user. The events
// returned here are identical to what dispatchHostCalls would have produced.
//
// Honours `on_error:` arcs the same way dispatchHostCalls does — when a
// host call with `on_error:` declared returns Result.Error != "", dispatch
// of the remaining calls in the batch is aborted and the named error
// state is entered (its on_enter chain runs and any nested host calls are
// dispatched).  The returned `redirect` is non-empty in that case so the
// caller can override `result.NewState`.
func (o *Orchestrator) dispatchHostCallsDetailed(ctx context.Context, calls []machine.HostInvocation, w world.World, state app.StatePath) ([]HostCallSummary, []store.Event, world.World, string, app.StatePath, error) {
	if o.hosts == nil || len(calls) == 0 {
		return nil, nil, w, "", "", nil
	}

	if o.transports != nil {
		ctx = transport.WithRegistry(ctx, o.transports)
	}
	if o.chatStore != nil {
		ctx = host.WithChatStore(ctx, o.chatStore)
	}
	// Mirror the primary dispatch path: a media-emit during this OneShot dispatch
	// must journal its artifact too (see the dispatchHostCalls wiring above). The
	// session id rides on the ctx's AgentCallCtx here (no sid param on this path).
	if o.journalWriter != nil {
		if oc := host.AgentCallCtxFrom(ctx); oc.SessionID != "" {
			ctx = host.WithArtifactJournalWriter(ctx, journal.SessionStamping(o.journalWriter, oc.SessionID))
		}
	}
	ctx = host.WithAgents(ctx, agentsForContext(o.def))
	ctx = host.WithProviders(ctx, providersForContext(o.def))
	// Resolve the live harness selection once for this dispatch: the active
	// profile chooses the backend to fork and the env/model default (installed as
	// the operator-selected provider via WithActiveProfile). No profile selected
	// ⇒ the static backend and a no-op active profile (legacy path, byte-identical).
	backendName, activeProfile := o.resolveSelection(o.agentBackendName)
	ctx = host.WithAgentBackendNamed(ctx, backendName)
	ctx = host.WithActiveProfile(ctx, activeProfile)
	// Inject the prompt renderer so agent handlers resolve and render prompt
	// files through the story's overlay → story search path. nil is safe
	// (handlers use the legacy path).
	ctx = host.WithPromptRenderer(ctx, o.promptRenderer)
	// Inject the project's Layer-2 system-prompt grounding (app.context /
	// context_path) so every agent call composes kitsoki → project → task.
	ctx = host.WithProjectContext(ctx, projectContextFor(o.def))
	// Inject the live IDE link (nil-safe). The `world.ide.connected` gate is
	// seeded per turn in loadJourney; re-seed against this dispatch's world (see
	// the note in dispatchHostCalls).
	ctx = host.WithIDELink(ctx, o.currentIDELink())
	o.seedIDEConnected(w)

	summaries := make([]HostCallSummary, 0, len(calls))
	var events []store.Event
	applied := false
	var redirect app.StatePath

	// dispatchBinds accumulates the world keys bound by host results as the
	// loop progresses, so a later invoke's `with:` re-render sees an earlier
	// invoke's bind. It is layered on top of each invoke's machine-time
	// WorldSnapshot — NOT the live post-chain world — so a `set:` positioned
	// AFTER an invoke in the same chain cannot clobber that invoke's args.
	// See machine.HostInvocation.WorldSnapshot.
	dispatchBinds := map[string]any{}

	// batchCost sums the agent cost (total_cost_usd) of every call in this
	// batch; folded into the reserved cost world vars after the loop.
	var batchCost float64

	for _, hc := range calls {
		// Re-render templates against the invoke's position-snapshot +
		// accumulated binds (NOT the live post-chain world) so chained
		// `on_enter:` host calls compose without a later `set:` clobbering
		// earlier args — see dispatchRerenderWorld / rerenderHostArgs above.
		invokeArgs, fellBack := rerenderHostArgs(hc, dispatchRerenderWorld(hc, dispatchBinds, w))
		summary := HostCallSummary{Namespace: hc.Namespace, Args: invokeArgs}
		events = append(events, newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, 0))
		// B-7: inject agent plugin alias for summary dispatch path.
		invokeCtx2 := host.WithWorldSnapshot(ctx, w.Vars)
		invokeCtx2 = host.WithAgentUsageBox(invokeCtx2)
		if hc.AgentPlugin != "" {
			invokeCtx2 = host.WithAgentPluginName(invokeCtx2, hc.AgentPlugin)
		}
		res, err := o.hosts.Invoke(invokeCtx2, hc.Namespace, invokeArgs)
		batchCost += host.AgentCostFrom(invokeCtx2)
		if err != nil {
			summary.Error = err.Error()
			summaries = append(summaries, summary)
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"last_error": err.Error()},
			}, 0))
			events = append(events, newOrchestratorEvent(store.HostReturned, map[string]any{
				"namespace": hc.Namespace,
				"error":     err.Error(),
			}, 0))
			applied = true
			if hc.OnError != "" {
				o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
					slog.String("namespace", hc.Namespace),
					slog.String("from", string(state)),
					slog.String("to", hc.OnError),
					slog.String("error", err.Error()),
					slog.String("phase", "infra"),
				)
				redirect = app.StatePath(hc.OnError)
				break
			}
			continue
		}
		if res.Error != "" {
			w.Vars["last_error"] = res.Error
			summary.Error = res.Error
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"last_error": res.Error},
			}, 0))
		}
		if res.Data != nil {
			summary.Data = res.Data
		}
		summaries = append(summaries, summary)

		// Sorted, not raw map order — see the determinism note on the bind
		// loop in dispatchHostCalls above.
		for _, wkey := range slices.Sorted(maps.Keys(hc.Bind)) {
			dkey := hc.Bind[wkey]
			if res.Data == nil {
				continue
			}
			val, ok := lookupBindPath(res.Data, dkey)
			if !ok {
				continue
			}
			w.Vars[wkey] = val
			dispatchBinds[wkey] = val
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{wkey: val},
			}, 0))
			applied = true
		}

		payload := map[string]any{"namespace": hc.Namespace}
		if res.Error != "" {
			payload["error"] = res.Error
		}
		if res.Data != nil {
			payload["data"] = res.Data
		}
		events = append(events, newOrchestratorEvent(store.HostReturned, payload, 0))

		if res.Error != "" && hc.OnError != "" {
			o.logger.DebugContext(ctx, trace.EvHostOnErrorRedirect,
				slog.String("namespace", hc.Namespace),
				slog.String("from", string(state)),
				slog.String("to", hc.OnError),
				slog.String("error", res.Error),
				slog.String("phase", "domain"),
			)
			redirect = app.StatePath(hc.OnError)
			break
		}
	}

	// Fold this batch's agent spend into the reserved cost world vars before the
	// redirect/render paths so they (and any error room) see the current totals.
	if costEvents := foldAgentCost(&w, batchCost); len(costEvents) > 0 {
		events = append(events, costEvents...)
		applied = true
	}

	if redirect != "" {
		errEvents, errWorld, errView, resolvedRedirect, redirErr := o.enterRedirectState(ctx, "", state, redirect, w)
		if redirErr != nil {
			return summaries, events, w, "", "", redirErr
		}
		events = append(events, errEvents...)
		w = errWorld
		applied = true
		if errView == "" {
			v, rErr := o.machine.RenderState(resolvedRedirect, w)
			if rErr != nil {
				return summaries, events, w, "", "", fmt.Errorf("render redirect state %q: %w", resolvedRedirect, rErr)
			}
			errView = v
		}
		return summaries, events, w, errView, resolvedRedirect, nil
	}

	if !applied {
		return summaries, events, w, "", "", nil
	}
	view, err := o.machine.RenderState(state, w)
	if err != nil {
		return summaries, events, w, "", "", fmt.Errorf("re-render after host dispatch: %w", err)
	}
	return summaries, events, w, view, "", nil
}

// foldAgentCost folds the agent spend accumulated across one host-dispatch
// batch (batchCost, the sum of host.AgentCostFrom over the batch's calls) into
// the reserved, engine-managed world vars, returning the EffectApplied events to
// journal so replay reconstructs the same totals from the event log:
//
//   - turn_cost_usd    — cost of the most recent host-dispatch batch (reset to 0
//     on a batch with no agent spend, e.g. host.run-only).
//   - session_cost_usd — cumulative agent spend across the whole session.
//
// Stories guard on these directly (e.g. `when: "world.session_cost_usd >=
// world.cost_budget"`); WorldFromSchema seeds both to 0 so a guard that runs
// before any agent call still reads a number. No event is emitted when nothing
// changed, keeping the journal free of no-op writes.
func foldAgentCost(w *world.World, batchCost float64) []store.Event {
	var events []store.Event
	if batchCost != worldFloat(w.Vars["turn_cost_usd"]) {
		w.Vars["turn_cost_usd"] = batchCost
		events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
			"set": map[string]any{"turn_cost_usd": batchCost},
		}, 0))
	}
	if batchCost > 0 {
		session := worldFloat(w.Vars["session_cost_usd"]) + batchCost
		w.Vars["session_cost_usd"] = session
		events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
			"set": map[string]any{"session_cost_usd": session},
		}, 0))
	}
	return events
}

// worldFloat coerces a world value to float64 — float64 when set by foldAgentCost
// or rehydrated from journal JSON, int when a story declares an integer default,
// 0 when missing or non-numeric.
func worldFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

// artifactHandleResolves reports whether handle names a recorded artifact in
// session sid's typed journal. It scans for a journal.KindArtifactEmitted entry
// whose ID equals handle — the same record host.artifacts_dir writes when a
// frame still is grabbed, and the same one the runstatus JournalArtifactResolver
// serves the /artifact/{id} route from. Kept predicate-only (no path/MIME) so
// the host package's dangling-frame check stays decoupled from artifact serving.
func artifactHandleResolves(r journal.Reader, sid app.SessionID, handle string) bool {
	if r == nil || handle == "" {
		return false
	}
	seq, errFn := r.ReplayTyped(sid)
	for entry := range seq {
		if entry.Kind != journal.KindArtifactEmitted {
			continue
		}
		var ev journal.ArtifactEvent
		if err := json.Unmarshal(entry.Body, &ev); err != nil {
			continue
		}
		if ev.ID == handle {
			_ = errFn()
			return true
		}
	}
	_ = errFn()
	return false
}
