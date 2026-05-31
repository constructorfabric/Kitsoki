package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
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
	// Inject the agents map so host.oracle.* invocations can resolve
	// `with: { agent: <name> }` references to a host.Agent value. Built
	// once per dispatch (cheap — translation is tag-equivalent).
	ctx = host.WithAgents(ctx, agentsForContext(o.def))

	// Wave 3-oracle: inject the EventSink so oracle handlers can parallel-write
	// OracleCalled / OracleReturned / OracleError events to the JSONL alongside
	// the existing journal write.
	if o.eventSink != nil {
		ctx = host.WithOracleEventSink(ctx, o.eventSink)
		// Also inject the prompts directory so large prompts are stored separately
		// to stay under PIPE_BUF. Extract it from the JSONLSink path.
		if jl, ok := o.eventSink.(*store.JSONLSink); ok {
			promptsDir := filepath.Join(filepath.Dir(jl.Path), "oracle-prompts")
			ctx = host.WithOraclePromptsDir(ctx, promptsDir)
		}
	}
	// B-2: inject the oracle plugin registry so handlers can route through
	// Oracle.Ask. When nil, handlers fall through to direct claude-CLI logic.
	if o.oracleRegistry != nil {
		ctx = host.WithOracleRegistry(ctx, o.oracleRegistry)
	}
	// OracleCallCtx carries session/turn/state for journal Entry metadata.
	// Turn is not directly available here (it lives in the Turn() local), so
	// we inject a best-effort value of 0 when not provided; callers that
	// inject the turn via WithOracleCallCtx before dispatchHostCalls override
	// this default.
	if existing := host.OracleCallCtxFrom(ctx); existing.SessionID == "" {
		ctx = host.WithOracleCallCtx(ctx, host.OracleCallCtx{
			SessionID: sid,
			StatePath: state,
		})
	}

	var events []store.Event
	applied := false
	var redirect app.StatePath

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
		// note on HostInvocation.RawWith.
		invokeArgs, fellBack := rerenderHostArgs(hc, w)

		// HostDispatched records the *actual* args the handler is about
		// to receive (post-rerender), so the event trace is honest even
		// when rerenderHostArgs had to fall back for one or more leaves.
		// Unlike HostInvoked (which snapshots pre-bind args at machine
		// time), this fires immediately before the handler is invoked.
		// Replay treats it as a no-op (see store/replay.go).
		events = append(events, newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, 0))

		// B-7: inject the oracle plugin alias into the context so the handler
		// can route through host.Dispatch with the correct plugin. When
		// OraclePlugin is empty the handler falls back to "oracle.claude" (the
		// default). This is the production wiring that makes explicit `oracle:`
		// effect fields take effect at runtime.
		invokeCtx := ctx
		if hc.OraclePlugin != "" {
			invokeCtx = host.WithOraclePluginName(ctx, hc.OraclePlugin)
		}

		res, err := o.hosts.Invoke(invokeCtx, hc.Namespace, invokeArgs)
		if err != nil {
			// Infrastructure failure (e.g. handler not registered): record and move on.
			w.Vars["last_error"] = err.Error()
			events = append(events, newOrchestratorEvent(store.EffectApplied, map[string]any{
				"set": map[string]any{"last_error": err.Error()},
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
		for wkey, dkey := range hc.Bind {
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
	ctx = host.WithAgents(ctx, agentsForContext(o.def))

	summaries := make([]HostCallSummary, 0, len(calls))
	var events []store.Event
	applied := false
	var redirect app.StatePath

	for _, hc := range calls {
		// Re-render templates against the current world so chained
		// `on_enter:` host calls compose — see rerenderHostArgs above.
		invokeArgs, fellBack := rerenderHostArgs(hc, w)
		summary := HostCallSummary{Namespace: hc.Namespace, Args: invokeArgs}
		events = append(events, newOrchestratorEvent(store.HostDispatched, map[string]any{
			"namespace":          hc.Namespace,
			"args":               invokeArgs,
			"rerender_fell_back": fellBack,
			"background":         hc.Background,
		}, 0))
		// B-7: inject oracle plugin alias for summary dispatch path.
		invokeCtx2 := ctx
		if hc.OraclePlugin != "" {
			invokeCtx2 = host.WithOraclePluginName(ctx, hc.OraclePlugin)
		}
		res, err := o.hosts.Invoke(invokeCtx2, hc.Namespace, invokeArgs)
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

		for wkey, dkey := range hc.Bind {
			if res.Data == nil {
				continue
			}
			val, ok := lookupBindPath(res.Data, dkey)
			if !ok {
				continue
			}
			w.Vars[wkey] = val
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
