// Package host — agent dispatcher. See docs/architecture/agent-plugin.md.
//
// AgentDispatch is the shared dispatcher that routes agent handler calls
// through the Agent plugin interface. It:
//
//  1. Resolves the agent plugin from the registry injected in context.
//  2. Calls agent.Ask(ctx, req).
//  3. Writes AgentCalled to the EventSink, using episode_id/match_idx from
//     resp.Meta when the transport is cassette-backed.
//  4. Validates SubEvents (namespace, call_id, size) before appending verbatim
//     between AgentCalled and AgentReturned.
//  5. Validates resp.Submission against req.SchemaJSON (kitsoki is validation authority).
//  6. Writes AgentReturned or AgentError.
//  7. Returns (submission, meta, error).
//
// SubEvents validation (B-4):
//   - Every sub-event Kind MUST start with the dispatching host's name + "."
//     (e.g., dispatching from "agent.autofix_fixer" requires Kind prefix
//     "agent.autofix_fixer."). Violation → AgentError{Kind: "sub_event_namespace_violation"}.
//   - Every sub-event CallID MUST match the parent AgentCalled.CallID.
//     Violation → AgentError{Kind: "sub_event_call_id_mismatch"}.
//   - Sub-events can be arbitrary size (PIPE_BUF limit was removed).
//   - Sub-event ts is re-stamped at append time using kitsoki's monotonic clock.
//     The plugin's claimed ts is ignored.
//   - On any validation failure: AgentCalled is already written; AgentError
//     replaces AgentReturned; no sub-events land. This is the atomicity boundary.
//
// AgentCalled is written after Ask returns so that the cassette transport's
// episode_id and match_idx (carried in resp.Meta) can be included on the event.
// For all other transports the ordering is semantically identical since AgentCalled
// is a no-op for replay and the event pair is what the runstatus SPA consumes.
//
// Backwards compat: when no agent registry is wired in context (all existing
// call sites before B-2), Dispatch returns errNoRegistry so the caller falls
// through to its existing direct handler logic.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/agent"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// agentRegistryKey is the context key for an agent.Registry injected by
// the orchestrator. The registry is optional — nil means "no registry wired,
// fall through to direct handler logic" (backwards compat).
type agentRegistryKey struct{}

// WithAgentRegistry returns a child context carrying reg. Agent handlers
// call AgentRegistryFromCtx to retrieve it.
func WithAgentRegistry(ctx context.Context, reg *agent.Registry) context.Context {
	if reg == nil {
		return ctx
	}
	return context.WithValue(ctx, agentRegistryKey{}, reg)
}

// AgentRegistryFromCtx returns the agent.Registry previously injected with
// WithAgentRegistry, or nil if none was injected.
func AgentRegistryFromCtx(ctx context.Context) *agent.Registry {
	reg, _ := ctx.Value(agentRegistryKey{}).(*agent.Registry)
	return reg
}

// errNoRegistry is returned by Dispatch when no registry is wired in context.
// Handlers use this to fall through to their existing direct logic.
var errNoRegistry = fmt.Errorf("agent: no registry in context")

// IsNoRegistryError returns true when err is the sentinel returned when no
// registry is wired. Used by handlers to decide whether to fall through.
func IsNoRegistryError(err error) bool {
	return err == errNoRegistry
}

// agentPluginNameKey is the context key for the agent plugin alias name
// injected by the orchestrator just before invoking an agent verb handler.
// When non-empty, the handler should route through host.Dispatch with this
// plugin name instead of its built-in subprocess logic.
type agentPluginNameKey struct{}

// WithAgentPluginName returns a child context carrying the agent plugin alias
// name for the current handler invocation. Injected by the orchestrator when
// the effect declares an explicit `agent:` field.
func WithAgentPluginName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, agentPluginNameKey{}, name)
}

// AgentPluginNameFromCtx returns the agent plugin alias name previously
// injected by WithAgentPluginName, or "" if none was injected.
func AgentPluginNameFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(agentPluginNameKey{}).(string)
	return s
}

// localLLMFallbackKey is the context key for the local-model validation-reject
// fallback flag (step 4). When present and true, a schema_invalid rejection of
// a local_llm backend's Submission triggers a single re-dispatch of the SAME
// call to agent.claude under the same call_id. Set only by TryDispatchVerb,
// and only when the resolved plugin is a builtin.local_llm transport — every
// other transport (external MCP plugins, the claude CLI itself) leaves it unset
// and stays on the hard-fail path so a genuine schema regression is not papered
// over.
type localLLMFallbackKey struct{}

// WithLocalLLMFallback returns a child context that marks the current agent
// call as eligible for the local-model → agent.claude validation-reject
// fallback. originalPlugin is the alias that was resolved to a local_llm
// backend; it is recorded in the substitution provenance on the closing event.
func WithLocalLLMFallback(ctx context.Context, originalPlugin string) context.Context {
	if originalPlugin == "" {
		return ctx
	}
	return context.WithValue(ctx, localLLMFallbackKey{}, originalPlugin)
}

// localLLMFallbackFrom returns the original plugin alias recorded by
// WithLocalLLMFallback, or "" when the fallback is not enabled for this call.
func localLLMFallbackFrom(ctx context.Context) string {
	s, _ := ctx.Value(localLLMFallbackKey{}).(string)
	return s
}

// AgentDispatchRequest carries everything the dispatcher needs to route one
// agent call through the plugin interface.
type AgentDispatchRequest struct {
	// Req is the fully constructed AskRequest — session, turn, state, verb,
	// prompt, schema, with-args, world, deadline, and call_id are all set.
	Req agent.AskRequest

	// PluginName is the agent alias to resolve (e.g. "agent.claude",
	// "agent.autofix_fixer"). Empty resolves to the default "agent.claude".
	PluginName string

	// Verb is the handler verb (ask / decide / extract / task / converse).
	// Copied to the event payload. Should equal Req.Verb.
	Verb string

	// Agent is the agent name resolved from the handler args. Written to
	// the event payload; opaque to the dispatcher.
	Agent string

	// Model is the model name from the resolved agent. Written to the event
	// payload; opaque to the dispatcher.
	Model string

	// PromptText is the rendered prompt (same as Req.PromptText). Split out
	// for event payload clarity.
	PromptText string

	// SystemPrompt is the effective system prompt. Written to AgentCalled.
	SystemPrompt string

	// InputDesc is verb-specific metadata written to the AgentCalled event
	// (e.g. {schema_path: "..."} for decide; {} for ask). Marshalled to JSON.
	InputDesc map[string]any
}

// AgentDispatchResult is returned by Dispatch on success.
type AgentDispatchResult struct {
	// Submission is the validated agent response. Bound to world by the handler.
	Submission json.RawMessage
	// Meta is opaque agent metadata (tokens, cost, model).
	Meta map[string]any
	// DurationMS is the round-trip duration in milliseconds.
	DurationMS int64
}

// subEventViolationKind is the AskError.Kind used when SubEvent validation fails.
const (
	subEventNamespaceViolation = "sub_event_namespace_violation"
	subEventCallIDMismatch     = "sub_event_call_id_mismatch"
)

// TryDispatchVerb attempts to route an agent verb call through the plugin
// registry. It is the B-7 production wiring entry point called from each agent
// verb handler (ask, decide, extract, task, converse) after the prompt is
// rendered.
//
// Returns:
//
//	(result, true, nil)   — plugin handled the call; result is ready to return.
//	(result, true, err)   — plugin returned an error; caller should surface it.
//	(zero,  false, nil)   — no registry in context; caller falls through to its
//	                        existing subprocess / direct logic (backwards compat).
//
// The returned Result has the shape:
//
//	Data["submission"]  — parsed submission (any, may be nil when schema is nil)
//	Data["exit_code"]   — 0
//	Data["ok"]          — true
//	Data["meta"]        — opaque agent meta map
//
// This shape is intentionally compatible with existing bind: references that
// use "submitted" (a common alias). Callers MAY add extra keys before returning.
func TryDispatchVerb(ctx context.Context, verb, renderedPrompt, systemPrompt, agentName, model string, withArgs map[string]any, schemaJSON json.RawMessage) (Result, bool, error) {
	reg := AgentRegistryFromCtx(ctx)
	if reg == nil {
		return Result{}, false, nil
	}

	pluginName := AgentPluginNameFromCtx(ctx)

	// Backwards compat (existing stories don't change): the plugin
	// dispatch path is opt-in. Only route through it when the story explicitly
	// named an agent via the effect's `agent:` field (the orchestrator injects
	// the plugin name into context only in that case). With no explicit plugin,
	// fall through to the legacy in-process handler so existing rooms keep their
	// full result shape — notably the `stdout` bind key, which the dispatch
	// result does not expose — and the phase-A agent events the legacy path
	// already emits. The default agent.claude therefore stays on the
	// battle-tested claude_cli path; the Agent plugin contract is reserved for
	// declared/external agents.
	if pluginName == "" {
		return Result{}, false, nil
	}

	oc := AgentCallCtxFrom(ctx)

	// Step 4: when the resolved plugin is a local-model backend, mark this call
	// eligible for the validation-reject fallback to agent.claude. Dispatch
	// itself only knows the alias name (dr.PluginName), not its transport type,
	// so the eligibility decision is taken here where the registry is in hand.
	if reg.IsLocalLLM(pluginName) {
		ctx = WithLocalLLMFallback(ctx, pluginName)
	}

	dr := AgentDispatchRequest{
		Req: agent.AskRequest{
			SessionID:  oc.SessionID,
			TurnNumber: oc.Turn,
			StatePath:  oc.StatePath,
			Verb:       verb,
			PromptText: renderedPrompt,
			SchemaJSON: schemaJSON,
			WithArgs:   withArgs,
			World:      world.World{Vars: map[string]any{}}, // snapshot not available here; plugins use AskRequest.World for augmentation only
			Deadline:   time.Now().Add(10 * time.Minute),    // generous default; context cancel is the hard cap
		},
		PluginName:   pluginName,
		Verb:         verb,
		Agent:        agentName,
		Model:        model,
		PromptText:   renderedPrompt,
		SystemPrompt: systemPrompt,
		InputDesc:    map[string]any{},
	}
	if schemaJSON != nil {
		dr.InputDesc["schema_present"] = true
	}

	dispResult, dispErr := Dispatch(ctx, dr)
	if dispErr != nil {
		if IsNoRegistryError(dispErr) {
			return Result{}, false, nil
		}
		return Result{Error: dispErr.Error()}, true, dispErr
	}

	// Parse Submission into a map for easy binding.
	var parsed any
	if dispResult.Submission != nil {
		_ = json.Unmarshal(dispResult.Submission, &parsed)
	}

	// Inject world var binding key — callers use bind: {world_key: submission}.
	data := map[string]any{
		"submission": parsed,
		"submitted":  parsed, // alias for backward compat with existing bind: references
		"exit_code":  0,
		"ok":         true,
		"meta":       dispResult.Meta,
	}
	return Result{Data: data}, true, nil
}

// Dispatch routes an agent call through the plugin registry. Returns
// errNoRegistry when no registry is wired — callers should fall through to
// their existing direct handler logic in that case.
//
// On agent error, Dispatch writes an AgentError event and returns a non-nil
// error (an *agent.AskError or wrapped version).
// On schema validation failure, Dispatch writes AgentError and returns
// *agent.AskError{Kind: "schema_invalid"}.
// On SubEvents validation failure, Dispatch writes AgentError and returns
// *agent.AskError{Kind: "sub_event_namespace_violation" | "sub_event_call_id_mismatch"}.
func Dispatch(ctx context.Context, dr AgentDispatchRequest) (AgentDispatchResult, error) {
	reg := AgentRegistryFromCtx(ctx)
	if reg == nil {
		return AgentDispatchResult{}, errNoRegistry
	}

	plug, err := reg.Resolve(dr.PluginName)
	if err != nil {
		return AgentDispatchResult{}, fmt.Errorf("agent dispatch: %w", err)
	}

	callStart := time.Now()
	callID := dr.Req.CallID
	if callID == "" {
		callID = newUUID()
		dr.Req.CallID = callID
	}

	resp, askErr := plug.Ask(ctx, dr.Req)
	durationMS := time.Since(callStart).Milliseconds()

	// Extract cassette-transport metadata from resp.Meta (if present).
	// Cassette transports embed episode_id and match_idx in Meta so the
	// AgentCalled event carries them for post-resume SeedMatchCountsFromHistory.
	episodeID := episodeIDFromMeta(resp.Meta)
	matchIdx := matchIdxFromMeta(resp.Meta)

	// Record this call's cost into the per-call usage box so the orchestrator
	// can fold it into the reserved world vars turn_cost_usd / session_cost_usd
	// (see AgentCostFrom). Live calls already record cost via the claude CLI
	// transport during streaming; recording again here is a harmless last-write
	// of the same value. The point of doing it at this single chokepoint is the
	// CASSETTE path: replayed episodes carry cost only in resp.Meta, so without
	// this a cost-budget guard would see $0 in flow tests. recordAgentUsage is
	// a no-op when cost is 0, so non-agent dispatches are unaffected.
	if c, ok := resp.Meta["cost_usd"].(float64); ok && c != 0 {
		recordAgentUsage(ctx, nil, c)
	}

	// Write AgentCalled after Ask returns so cassette episode_id/match_idx
	// are available. For all transports this is functionally equivalent to
	// writing before: AgentCalled is a no-op for replay, and the event pair
	// is what the runstatus SPA consumes (ordered by ts, not by write sequence).
	// Store large prompts in separate files to stay under PIPE_BUF (4096 bytes).
	// The full prompt is available in:
	// - The agent.AskRequest.PromptText (live mode)
	// - Separate prompt file (referenced via prompt_file field)
	// - Cassette via !include (replay mode)
	promptFile, _ := storePromptIfLarge(ctx, callID, dr.PromptText)
	// Guarantee a prompt reference on every agent.call.start
	// (see docs/tracing/trace-format.md): when the prompt was small enough that it
	// was not offloaded to a sidecar file, embed it inline so a consumer always
	// has the prompt without a missing-file fallback. promptFile != "" means
	// the prompt is large and lives in that file; we don't duplicate it inline.
	inlinePrompt := ""
	if promptFile == "" {
		inlinePrompt = dr.PromptText
	}
	appendAgentCalledEventWithEpisode(ctx, callStart, callID, episodeID, matchIdx, AgentCalledPayload{
		Verb:       dr.Verb,
		Agent:      dr.Agent,
		Model:      dr.Model,
		Prompt:     inlinePrompt,
		PromptFile: promptFile,
		Input:      marshalInput(dr.InputDesc),
	})

	if askErr != nil {
		callEnd := time.Now()
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       dr.Verb,
			Agent:      dr.Agent,
			DurationMS: durationMS,
			Error:      askErr.Error(),
		})
		return AgentDispatchResult{}, askErr
	}

	// B-4: Validate SubEvents before any append. On violation: write AgentError
	// (not AgentReturned); no sub-events land. This is the atomicity boundary —
	// AgentCalled is already written; the call is abandoned cleanly.
	if len(resp.SubEvents) > 0 {
		if subErr := validateSubEvents(resp.SubEvents, dr.PluginName, callID); subErr != nil {
			callEnd := time.Now()
			appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
				Verb:       dr.Verb,
				Agent:      dr.Agent,
				DurationMS: durationMS,
				Error:      subErr.Error(),
			})
			return AgentDispatchResult{}, subErr
		}

		// Validation passed: append SubEvents with kitsoki-assigned ts.
		// Plugin-supplied ts is discarded; kitsoki's monotonic clock wins
		// (sub-events are ordered after the response).
		appendSubEventsValidated(ctx, resp.SubEvents, callID)
	}

	// Validate submission against schema (kitsoki is validation authority).
	if validErr := agent.ValidateSubmission(dr.Req.SchemaJSON, resp.Submission); validErr != nil {
		// Step 4: local-model validation-reject fallback. When the schema
		// rejection comes from a local_llm backend AND this call was marked
		// eligible (WithLocalLLMFallback, set at TryDispatchVerb resolution),
		// re-dispatch the SAME call exactly once to agent.claude under the
		// SAME call_id — best-effort small models fail soft, the deterministic
		// claude path is the guarantee. The current AgentError emit is SKIPPED
		// on the fallback branch so only ONE closing event is written per call.
		if origPlugin := localLLMFallbackFrom(ctx); origPlugin != "" && isSchemaInvalid(validErr) {
			// Preserve the rejected local-model transcript (the evidence of WHY it
			// was rejected) under this call_id before the claude fallback continues;
			// a single Finalize on the closing event flushes the whole local→claude
			// arc into one sidecar instead of discarding the local attempt.
			if resp.Transcript != nil {
				appendOutOfHostTranscript(ctx, callID, resp.Transcript.Format, resp.Transcript.Events, resp.Transcript.Timings)
			}
			return dispatchFallbackToClaude(ctx, reg, dr, callID, origPlugin)
		}
		callEnd := time.Now()
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       dr.Verb,
			Agent:      dr.Agent,
			DurationMS: durationMS,
			Error:      validErr.Error(),
		})
		return AgentDispatchResult{}, validErr
	}

	callEnd := time.Now()
	responseDesc := map[string]any{}
	if resp.Submission != nil {
		var parsed any
		if json.Unmarshal(resp.Submission, &parsed) == nil {
			responseDesc["submission"] = parsed
		}
	}
	if resp.Meta != nil {
		responseDesc["meta"] = resp.Meta
	}

	// Out-of-host backends carry their native execution detail up via
	// AskResponse.Transcript (the in-host claude path tees RawEvents directly
	// instead). Feed it into the per-call sidecar writer and Finalize under the
	// backend's own format, so both producers converge on one sidecar + one
	// transcript_ref. Nil/empty is a no-op.
	var transcriptRef *TranscriptRef
	if resp.Transcript != nil {
		transcriptRef = finalizeOutOfHostTranscript(ctx, callID, resp.Transcript.Format, resp.Transcript.Events, resp.Transcript.Timings)
	}

	appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
		Verb:          dr.Verb,
		Agent:         dr.Agent,
		Model:         dr.Model,
		DurationMS:    durationMS,
		Response:      marshalResponse(responseDesc),
		Meta:          resp.Meta,
		TranscriptRef: transcriptRef,
	})

	return AgentDispatchResult{
		Submission: resp.Submission,
		Meta:       resp.Meta,
		DurationMS: durationMS,
	}, nil
}

// isSchemaInvalid reports whether err is an *agent.AskError with
// Kind=="schema_invalid". Only schema rejections are eligible for the
// local-model fallback; transport/deadline errors are not (a local model that
// is down should not silently fan out to claude).
func isSchemaInvalid(err error) bool {
	ae, ok := err.(*agent.AskError)
	return ok && ae.Kind == "schema_invalid"
}

// dispatchFallbackToClaude re-dispatches a schema_invalid local-model call to
// agent.claude exactly once, reusing the same call_id so the whole exchange
// stays a single agent.call.* pair (AgentCalled was already written before
// Ask). On fallback success it writes the single closing AgentReturned with a
// substitution provenance map + Meta["fallback_of"]; on fallback failure it
// writes the single closing AgentError carrying the same substitution map and
// returns the fallback's error. There is no same-backend retry and no second
// hop — claude either satisfies the schema or the call fails hard.
func dispatchFallbackToClaude(ctx context.Context, reg *agent.Registry, dr AgentDispatchRequest, callID, origPlugin string) (AgentDispatchResult, error) {
	substitution := map[string]any{
		"reason":          "schema_invalid",
		"original_plugin": origPlugin,
		"fallback_plugin": agent.DefaultAgentName,
	}

	plug2, err := reg.Resolve(agent.DefaultAgentName)
	if err != nil {
		callEnd := time.Now()
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:         dr.Verb,
			Agent:        dr.Agent,
			Error:        fmt.Errorf("local_llm fallback: %w", err).Error(),
			Substitution: substitution,
		})
		return AgentDispatchResult{}, err
	}

	// Re-dispatch the SAME request under the default agent, keeping the same
	// call_id (one agent.call.* pair across both Ask attempts).
	dr2 := dr
	dr2.PluginName = agent.DefaultAgentName
	dr2.Req.CallID = callID

	fbStart := time.Now()
	resp2, askErr2 := plug2.Ask(ctx, dr2.Req)
	fbDuration := time.Since(fbStart).Milliseconds()

	if askErr2 != nil {
		callEnd := time.Now()
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:         dr.Verb,
			Agent:        dr.Agent,
			DurationMS:   fbDuration,
			Error:        askErr2.Error(),
			Substitution: substitution,
		})
		return AgentDispatchResult{}, askErr2
	}

	if validErr := agent.ValidateSubmission(dr2.Req.SchemaJSON, resp2.Submission); validErr != nil {
		callEnd := time.Now()
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:         dr.Verb,
			Agent:        dr.Agent,
			DurationMS:   fbDuration,
			Error:        validErr.Error(),
			Substitution: substitution,
		})
		return AgentDispatchResult{}, validErr
	}

	// Fallback succeeded: tag the meta so consumers can see the substituted
	// backend, then write the single closing AgentReturned.
	meta := resp2.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	meta["fallback_of"] = origPlugin

	// Append the fallback backend's transcript (if it carried one) under the same
	// call_id; combined with the preserved local-model transcript, the closing
	// AgentReturned's Finalize flushes the full arc into one sidecar.
	if resp2.Transcript != nil {
		appendOutOfHostTranscript(ctx, callID, resp2.Transcript.Format, resp2.Transcript.Events, resp2.Transcript.Timings)
	}

	callEnd := time.Now()
	responseDesc := map[string]any{}
	if resp2.Submission != nil {
		var parsed any
		if json.Unmarshal(resp2.Submission, &parsed) == nil {
			responseDesc["submission"] = parsed
		}
	}
	responseDesc["meta"] = meta

	appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
		Verb:         dr.Verb,
		Agent:        dr.Agent,
		Model:        dr.Model,
		DurationMS:   fbDuration,
		Response:     marshalResponse(responseDesc),
		Meta:         meta,
		Substitution: substitution,
	})

	return AgentDispatchResult{
		Submission: resp2.Submission,
		Meta:       meta,
		DurationMS: fbDuration,
	}, nil
}

// validateSubEvents checks all sub-events against the B-4 constraints:
//   - namespace: every Kind must start with pluginName+"."
//   - call_id: every sub-event CallID must match parentCallID
//
// Returns the first violation as an *agent.AskError. The full AskResponse is
// rejected on first violation (atomicity: no partial sub-event append).
func validateSubEvents(subEvents []store.Event, pluginName, parentCallID string) *agent.AskError {
	requiredPrefix := pluginName + "."
	for i, se := range subEvents {
		// Namespace check: Kind must start with pluginName+".".
		if !strings.HasPrefix(string(se.Kind), requiredPrefix) {
			return &agent.AskError{
				Kind:   subEventNamespaceViolation,
				Detail: fmt.Sprintf("sub_event[%d]: Kind %q does not start with required namespace prefix %q", i, se.Kind, requiredPrefix),
			}
		}
		// call_id check: must match the parent AgentCalled's call_id.
		if se.CallID != parentCallID {
			return &agent.AskError{
				Kind:   subEventCallIDMismatch,
				Detail: fmt.Sprintf("sub_event[%d]: CallID %q does not match parent call_id %q", i, se.CallID, parentCallID),
			}
		}
	}
	return nil
}

// appendSubEventsValidated appends sub-events that have already passed
// validateSubEvents. Kitsoki re-stamps each sub-event's ts using time.Now()
// so the plugin's claimed ts is ignored (sub-events are ordered after
// the response" — kitsoki's monotonic clock is the source of truth for all ts
// fields in the JSONL trace).
//
// The CallID is set to parentCallID (already validated to match), and the
// event is appended verbatim otherwise.
//
// NOTE: plugin-supplied ts values are discarded here. Kitsoki re-stamps ts at
// append time so that:
//  1. The trace has a monotonically increasing ts sequence.
//  2. Plugins cannot forge timestamps for forensic events.
//  3. All sub-events have ts in [AgentCalled.ts, AgentReturned.ts).
func appendSubEventsValidated(ctx context.Context, subEvents []store.Event, parentCallID string) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	for _, se := range subEvents {
		// Re-stamp ts with kitsoki's clock. Plugin ts is ignored.
		se.Ts = time.Now()
		// Ensure CallID matches (already validated, but set explicitly for clarity).
		se.CallID = parentCallID
		_ = sink.Append(se)
	}
}

// appendAgentCalledEventWithEpisode is like appendAgentCalledEvent but also
// sets EpisodeID and MatchIdx on the event. Used by Dispatch when the cassette
// transport returns episode_id and match_idx in AskResponse.Meta.
//
// When episodeID is "" (non-cassette transports), the EpisodeID/MatchIdx fields
// are zero-valued and omitted from the marshalled JSON (omitempty).
func appendAgentCalledEventWithEpisode(ctx context.Context, ts time.Time, callID, episodeID string, matchIdx int, payload AgentCalledPayload) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	if ap, ok := ActiveProfileFromContext(ctx); ok {
		if payload.Profile == "" {
			payload.Profile = ap.Name
		}
		if payload.Effort == "" {
			payload.Effort = ap.Provider.Effort
		}
	}
	if payload.Backend == "" {
		payload.Backend = AgentBackendFromContext(ctx).Name()
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentCalled,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
		EpisodeID: episodeID,
		MatchIdx:  matchIdx,
	}
	_ = sink.Append(ev)
}

// episodeIDFromMeta extracts the episode_id field from AskResponse.Meta.
// Returns "" when not present (non-cassette transports).
func episodeIDFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	s, _ := meta["episode_id"].(string)
	return s
}

// matchIdxFromMeta extracts the match_idx field from AskResponse.Meta.
// Returns 0 when not present (non-cassette transports).
func matchIdxFromMeta(meta map[string]any) int {
	if meta == nil {
		return 0
	}
	switch v := meta["match_idx"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
