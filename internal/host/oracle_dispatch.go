// Package host — oracle dispatcher (proposal §2 B-2 / B-4).
//
// OracleDispatch is the shared dispatcher that routes oracle handler calls
// through the Oracle plugin interface. It:
//
//  1. Resolves the oracle plugin from the registry injected in context.
//  2. Calls oracle.Ask(ctx, req).
//  3. Writes OracleCalled to the EventSink, using episode_id/match_idx from
//     resp.Meta when the transport is cassette-backed (§3.1 / B-4).
//  4. Validates SubEvents (namespace, call_id, size) before appending verbatim
//     between OracleCalled and OracleReturned (§2 resolution 2 / B-4).
//  5. Validates resp.Submission against req.SchemaJSON (kitsoki is validation authority).
//  6. Writes OracleReturned or OracleError.
//  7. Returns (submission, meta, error).
//
// SubEvents validation (B-4):
//   - Every sub-event Kind MUST start with the dispatching host's name + "."
//     (e.g., dispatching from "oracle.autofix_fixer" requires Kind prefix
//     "oracle.autofix_fixer."). Violation → OracleError{Kind: "sub_event_namespace_violation"}.
//   - Every sub-event CallID MUST match the parent OracleCalled.CallID.
//     Violation → OracleError{Kind: "sub_event_call_id_mismatch"}.
//   - Every sub-event is subject to the PIPE_BUF=4096 byte-per-line limit.
//     Oversize → OracleError{Kind: "sub_event_oversize"}.
//   - Sub-event ts is re-stamped at append time using kitsoki's monotonic clock.
//     The plugin's claimed ts is ignored.
//   - On any validation failure: OracleCalled is already written; OracleError
//     replaces OracleReturned; no sub-events land. This is the atomicity boundary.
//
// OracleCalled is written after Ask returns so that the cassette transport's
// episode_id and match_idx (carried in resp.Meta) can be included on the event.
// For all other transports the ordering is semantically identical since OracleCalled
// is a no-op for replay and the event pair is what the runstatus SPA consumes.
//
// Backwards compat: when no oracle registry is wired in context (all existing
// call sites before B-2), Dispatch returns errNoRegistry so the caller falls
// through to its existing direct handler logic.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/oracle"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// oracleRegistryKey is the context key for an oracle.Registry injected by
// the orchestrator. The registry is optional — nil means "no registry wired,
// fall through to direct handler logic" (backwards compat).
type oracleRegistryKey struct{}

// WithOracleRegistry returns a child context carrying reg. Oracle handlers
// call OracleRegistryFromCtx to retrieve it.
func WithOracleRegistry(ctx context.Context, reg *oracle.Registry) context.Context {
	if reg == nil {
		return ctx
	}
	return context.WithValue(ctx, oracleRegistryKey{}, reg)
}

// OracleRegistryFromCtx returns the oracle.Registry previously injected with
// WithOracleRegistry, or nil if none was injected.
func OracleRegistryFromCtx(ctx context.Context) *oracle.Registry {
	reg, _ := ctx.Value(oracleRegistryKey{}).(*oracle.Registry)
	return reg
}

// errNoRegistry is returned by Dispatch when no registry is wired in context.
// Handlers use this to fall through to their existing direct logic.
var errNoRegistry = fmt.Errorf("oracle: no registry in context")

// IsNoRegistryError returns true when err is the sentinel returned when no
// registry is wired. Used by handlers to decide whether to fall through.
func IsNoRegistryError(err error) bool {
	return err == errNoRegistry
}

// oraclePluginNameKey is the context key for the oracle plugin alias name
// injected by the orchestrator just before invoking an oracle verb handler.
// When non-empty, the handler should route through host.Dispatch with this
// plugin name instead of its built-in subprocess logic.
type oraclePluginNameKey struct{}

// WithOraclePluginName returns a child context carrying the oracle plugin alias
// name for the current handler invocation. Injected by the orchestrator when
// the effect declares an explicit `oracle:` field.
func WithOraclePluginName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, oraclePluginNameKey{}, name)
}

// OraclePluginNameFromCtx returns the oracle plugin alias name previously
// injected by WithOraclePluginName, or "" if none was injected.
func OraclePluginNameFromCtx(ctx context.Context) string {
	s, _ := ctx.Value(oraclePluginNameKey{}).(string)
	return s
}

// OracleDispatchRequest carries everything the dispatcher needs to route one
// oracle call through the plugin interface.
type OracleDispatchRequest struct {
	// Req is the fully constructed AskRequest — session, turn, state, verb,
	// prompt, schema, with-args, world, deadline, and call_id are all set.
	Req oracle.AskRequest

	// PluginName is the oracle alias to resolve (e.g. "oracle.claude",
	// "oracle.autofix_fixer"). Empty resolves to the default "oracle.claude".
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

	// SystemPrompt is the effective system prompt. Written to OracleCalled.
	SystemPrompt string

	// InputDesc is verb-specific metadata written to the OracleCalled event
	// (e.g. {schema_path: "..."} for decide; {} for ask). Marshalled to JSON.
	InputDesc map[string]any
}

// OracleDispatchResult is returned by Dispatch on success.
type OracleDispatchResult struct {
	// Submission is the validated oracle response. Bound to world by the handler.
	Submission json.RawMessage
	// Meta is opaque oracle metadata (tokens, cost, model).
	Meta map[string]any
	// DurationMS is the round-trip duration in milliseconds.
	DurationMS int64
}

// subEventViolationKind is the AskError.Kind used when SubEvent validation fails.
const (
	subEventNamespaceViolation = "sub_event_namespace_violation"
	subEventCallIDMismatch     = "sub_event_call_id_mismatch"
	subEventOversize           = "sub_event_oversize"
)

// pipeBufLimit mirrors store.pipeBuf (4096). Duplicated here to avoid importing
// an internal store constant; the store package enforces this on Append anyway,
// but we pre-validate to produce a clearer error message and prevent partial writes.
const pipeBufLimit = 4096

// TryDispatchVerb attempts to route an oracle verb call through the plugin
// registry. It is the B-7 production wiring entry point called from each oracle
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
//	Data["meta"]        — opaque oracle meta map
//
// This shape is intentionally compatible with existing bind: references that
// use "submitted" (a common alias). Callers MAY add extra keys before returning.
func TryDispatchVerb(ctx context.Context, verb, renderedPrompt, systemPrompt, agentName, model string, withArgs map[string]any, schemaJSON json.RawMessage) (Result, bool, error) {
	reg := OracleRegistryFromCtx(ctx)
	if reg == nil {
		return Result{}, false, nil
	}

	pluginName := OraclePluginNameFromCtx(ctx)

	// Backwards compat (proposal §2 "Existing stories don't change"): the plugin
	// dispatch path is opt-in. Only route through it when the story explicitly
	// named an oracle via the effect's `oracle:` field (the orchestrator injects
	// the plugin name into context only in that case). With no explicit plugin,
	// fall through to the legacy in-process handler so existing rooms keep their
	// full result shape — notably the `stdout` bind key, which the dispatch
	// result does not expose — and the phase-A oracle events the legacy path
	// already emits. The default oracle.claude therefore stays on the
	// battle-tested claude_cli path; the Oracle plugin contract is reserved for
	// declared/external oracles.
	if pluginName == "" {
		return Result{}, false, nil
	}

	oc := OracleCallCtxFrom(ctx)

	dr := OracleDispatchRequest{
		Req: oracle.AskRequest{
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

// Dispatch routes an oracle call through the plugin registry. Returns
// errNoRegistry when no registry is wired — callers should fall through to
// their existing direct handler logic in that case.
//
// On oracle error, Dispatch writes an OracleError event and returns a non-nil
// error (an *oracle.AskError or wrapped version).
// On schema validation failure, Dispatch writes OracleError and returns
// *oracle.AskError{Kind: "schema_invalid"}.
// On SubEvents validation failure, Dispatch writes OracleError and returns
// *oracle.AskError{Kind: "sub_event_namespace_violation" | "sub_event_call_id_mismatch" | "sub_event_oversize"}.
func Dispatch(ctx context.Context, dr OracleDispatchRequest) (OracleDispatchResult, error) {
	reg := OracleRegistryFromCtx(ctx)
	if reg == nil {
		return OracleDispatchResult{}, errNoRegistry
	}

	plug, err := reg.Resolve(dr.PluginName)
	if err != nil {
		return OracleDispatchResult{}, fmt.Errorf("oracle dispatch: %w", err)
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
	// OracleCalled event carries them for post-resume SeedMatchCountsFromHistory.
	episodeID := episodeIDFromMeta(resp.Meta)
	matchIdx := matchIdxFromMeta(resp.Meta)

	// Write OracleCalled after Ask returns so cassette episode_id/match_idx
	// are available. For all transports this is functionally equivalent to
	// writing before: OracleCalled is a no-op for replay, and the event pair
	// is what the runstatus SPA consumes (ordered by ts, not by write sequence).
	appendOracleCalledEventWithEpisode(ctx, callStart, callID, episodeID, matchIdx, OracleCalledPayload{
		Verb:         dr.Verb,
		Agent:        dr.Agent,
		Model:        dr.Model,
		Prompt:       dr.PromptText,
		SystemPrompt: dr.SystemPrompt,
		Input:        marshalInput(dr.InputDesc),
	})

	if askErr != nil {
		callEnd := time.Now()
		appendOracleErrorEvent(ctx, callEnd, callID, OracleErrorPayload{
			Verb:       dr.Verb,
			Agent:      dr.Agent,
			DurationMS: durationMS,
			Error:      askErr.Error(),
		})
		return OracleDispatchResult{}, askErr
	}

	// B-4: Validate SubEvents before any append. On violation: write OracleError
	// (not OracleReturned); no sub-events land. This is the atomicity boundary —
	// OracleCalled is already written; the call is abandoned cleanly.
	if len(resp.SubEvents) > 0 {
		if subErr := validateSubEvents(resp.SubEvents, dr.PluginName, callID); subErr != nil {
			callEnd := time.Now()
			appendOracleErrorEvent(ctx, callEnd, callID, OracleErrorPayload{
				Verb:       dr.Verb,
				Agent:      dr.Agent,
				DurationMS: durationMS,
				Error:      subErr.Error(),
			})
			return OracleDispatchResult{}, subErr
		}

		// Validation passed: append SubEvents with kitsoki-assigned ts.
		// Plugin-supplied ts is discarded; kitsoki's monotonic clock wins
		// (proposal §2 testing "Sub-events ordered after the response").
		appendSubEventsValidated(ctx, resp.SubEvents, callID)
	}

	// Validate submission against schema (kitsoki is validation authority).
	if validErr := oracle.ValidateSubmission(dr.Req.SchemaJSON, resp.Submission); validErr != nil {
		callEnd := time.Now()
		appendOracleErrorEvent(ctx, callEnd, callID, OracleErrorPayload{
			Verb:       dr.Verb,
			Agent:      dr.Agent,
			DurationMS: durationMS,
			Error:      validErr.Error(),
		})
		return OracleDispatchResult{}, validErr
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

	appendOracleReturnedEvent(ctx, callEnd, callID, OracleReturnedPayload{
		Verb:       dr.Verb,
		Agent:      dr.Agent,
		Model:      dr.Model,
		DurationMS: durationMS,
		Response:   marshalResponse(responseDesc),
		Meta:       resp.Meta,
	})

	return OracleDispatchResult{
		Submission: resp.Submission,
		Meta:       resp.Meta,
		DurationMS: durationMS,
	}, nil
}

// validateSubEvents checks all sub-events against the B-4 constraints:
//   - namespace: every Kind must start with pluginName+"."
//   - call_id: every sub-event CallID must match parentCallID
//   - size: marshalled event must not exceed pipeBufLimit bytes
//
// Returns the first violation as an *oracle.AskError. The full AskResponse is
// rejected on first violation (atomicity: no partial sub-event append).
func validateSubEvents(subEvents []store.Event, pluginName, parentCallID string) *oracle.AskError {
	requiredPrefix := pluginName + "."
	for i, se := range subEvents {
		// Namespace check: Kind must start with pluginName+".".
		if !strings.HasPrefix(string(se.Kind), requiredPrefix) {
			return &oracle.AskError{
				Kind:   subEventNamespaceViolation,
				Detail: fmt.Sprintf("sub_event[%d]: Kind %q does not start with required namespace prefix %q", i, se.Kind, requiredPrefix),
			}
		}
		// call_id check: must match the parent OracleCalled's call_id.
		if se.CallID != parentCallID {
			return &oracle.AskError{
				Kind:   subEventCallIDMismatch,
				Detail: fmt.Sprintf("sub_event[%d]: CallID %q does not match parent call_id %q", i, se.CallID, parentCallID),
			}
		}
		// Size check: marshalled line must not exceed PIPE_BUF.
		b, err := json.Marshal(se)
		if err == nil && len(b) > pipeBufLimit {
			return &oracle.AskError{
				Kind:   subEventOversize,
				Detail: fmt.Sprintf("sub_event[%d]: marshalled size %d exceeds PIPE_BUF limit %d", i, len(b), pipeBufLimit),
			}
		}
	}
	return nil
}

// appendSubEventsValidated appends sub-events that have already passed
// validateSubEvents. Kitsoki re-stamps each sub-event's ts using time.Now()
// so the plugin's claimed ts is ignored (proposal §2 "Sub-events ordered after
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
//  3. All sub-events have ts in [OracleCalled.ts, OracleReturned.ts).
func appendSubEventsValidated(ctx context.Context, subEvents []store.Event, parentCallID string) {
	sink := EventSinkFromOracleCtx(ctx)
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

// appendOracleCalledEventWithEpisode is like appendOracleCalledEvent but also
// sets EpisodeID and MatchIdx on the event. Used by Dispatch when the cassette
// transport returns episode_id and match_idx in AskResponse.Meta.
//
// When episodeID is "" (non-cassette transports), the EpisodeID/MatchIdx fields
// are zero-valued and omitted from the marshalled JSON (omitempty).
func appendOracleCalledEventWithEpisode(ctx context.Context, ts time.Time, callID, episodeID string, matchIdx int, payload OracleCalledPayload) {
	sink := EventSinkFromOracleCtx(ctx)
	if sink == nil {
		return
	}
	oc := OracleCallCtxFrom(ctx)

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.OracleCalled,
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
