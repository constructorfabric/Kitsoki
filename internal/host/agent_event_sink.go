// Package host — agent EventSink context plumbing and event-append helpers.
//
// This file provides the JSONL-sink side of agent tracing:
//
//  1. Context keys and helpers for the agent call context (session ID, turn,
//     state path) injected by the orchestrator into each agent handler call.
//  2. WithAgentEventSink / EventSinkFromAgentCtx — the EventSink used for
//     AgentCalled / AgentReturned / AgentError JSONL writes.
//  3. AgentCalledPayload, AgentReturnedPayload, AgentErrorPayload — the
//     wire types written to the JSONL trace for every agent turn.
//  4. appendAgentCalledEvent / appendAgentReturnedEvent / appendAgentErrorEvent —
//     the one-stop write helpers called by each agent verb after it completes.
//  5. marshalInput / marshalResponse — small marshal helpers used by the
//     agent verb handlers to serialize verb-specific descriptors.
//
// The legacy SQLite-backed journal (appendAgentCallJournal, AgentCallBody,
// WithAgentJournalWriter) was deleted in wave B-5.  The EventSink here is the
// only trace write path.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// ── Agent prompts directory context ──────────────────────────────────────────

// agentPromptsDir is the context key for the directory where large prompts
// are stored (e.g., {trace_dir}/agent-prompts/). If set, large prompts
// (>1KB) are written to separate files to keep JSONL lines under PIPE_BUF.
type agentPromptsDirKey struct{}

// WithAgentPromptsDir returns a child context carrying the directory where
// large prompts should be stored. Pass "" to disable separate prompt storage.
func WithAgentPromptsDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, agentPromptsDirKey{}, dir)
}

// AgentPromptsDirFromCtx returns the agent prompts directory from context,
// or "" if none was set.
func AgentPromptsDirFromCtx(ctx context.Context) string {
	dir, _ := ctx.Value(agentPromptsDirKey{}).(string)
	return dir
}

// ── Agent call context ───────────────────────────────────────────────────────

// AgentCallCtx carries session-level identifiers needed to populate JSONL
// events from within agent handlers (which don't have direct access to the
// orchestrator's session/turn state).
type AgentCallCtx struct {
	SessionID app.SessionID
	Turn      app.TurnNumber
	StatePath app.StatePath
	// WriteMode is the dispatching room's write_mode posture (app.WriteModeOpen /
	// app.WriteModeReadOnly / ""). Populated by the orchestrator from the active
	// state def; "" / open keeps today's dispatch posture verbatim. read_only
	// makes task dispatch boot the agent read-only and gate mutating steps.
	WriteMode string
	// WriteModeScope is the active write-mode grant breadth seeded from the
	// engine-reserved write_mode_scope world key (app.WriteModeScopeWorldKey):
	// "" | "turn" | "session". The gate honours a turn/session grant established
	// earlier in the same turn/session so it does not re-ask. Only meaningful when
	// WriteMode == read_only.
	WriteModeScope string
}

// agentCallCtxKey is the context key for an AgentCallCtx.
type agentCallCtxKey struct{}

// WithAgentCallCtx returns a child context carrying oc.
func WithAgentCallCtx(ctx context.Context, oc AgentCallCtx) context.Context {
	return context.WithValue(ctx, agentCallCtxKey{}, oc)
}

// AgentCallCtxFrom returns the AgentCallCtx previously injected with
// WithAgentCallCtx, or a zero value if none was injected.
func AgentCallCtxFrom(ctx context.Context) AgentCallCtx {
	oc, _ := ctx.Value(agentCallCtxKey{}).(AgentCallCtx)
	return oc
}

// ── Agent usage box ──────────────────────────────────────────────────────────

// agentUsageBox accumulates the token usage reported by the claude CLI
// transport (runClaudeStreamJSON, or the buffered json envelope path) during a
// single agent host call. The transport records each result event's usage
// here via recordAgentUsage; appendAgentReturnedEvent reads it via
// agentUsageMeta so the AgentReturned event carries per-invocation tokens
// without every verb handler threading a ClaudeRun through its (sometimes
// deep) call tree.
//
// The orchestrator installs one fresh box per host-call dispatch, but a
// single host-call dispatch can itself make MULTIPLE provider requests: a
// validator retry loop (decide/ask/task) issues several `claude --resume`
// calls under one call_id, and host.agent.codeact's step loop issues one
// claude call per step, all under one box. recordAgentUsage SUMS the known
// numeric usage buckets across every call into this box instead of
// overwriting, so the box's final usage/cost is the call's total spend, not
// just its last retry/step. Before this fix, last-write-wins silently
// discarded every retry/step but the final one — the root cause of the
// "$0.006 for a $0.034 call" false-cheap accounting in
// .context/2026-07-11-gx10-small-model-study-adversarial-review.md. The
// mutex guards the subprocess-reader goroutine vs. the handler; in practice
// the handler reads only after the claude call has returned.
type agentUsageBox struct {
	mu    sync.Mutex
	usage map[string]any
	cost  float64
}

// summableUsageKeys are the numeric usage fields recordAgentUsage sums across
// retries/resumes/steps within one box. Any other key in a later usage map
// (e.g. a string field a future transport might add) overwrites, matching the
// historical last-write-wins behavior for non-numeric fields — only the
// numeric spend buckets are load-bearing for cost/token accounting and must
// never be dropped.
var summableUsageKeys = []string{
	"input_tokens",
	"output_tokens",
	"cache_creation_input_tokens",
	"cache_read_input_tokens",
	"total_tokens",
}

// sumUsage merges next into prev, summing every key in summableUsageKeys
// (present in either map) and otherwise letting next's value win for any key
// not in that list. prev may be nil (first call in a box's lifetime).
func sumUsage(prev, next map[string]any) map[string]any {
	if prev == nil {
		out := make(map[string]any, len(next))
		for k, v := range next {
			out[k] = v
		}
		return out
	}
	out := make(map[string]any, len(prev)+len(next))
	for k, v := range prev {
		out[k] = v
	}
	summable := make(map[string]bool, len(summableUsageKeys))
	for _, k := range summableUsageKeys {
		summable[k] = true
	}
	for k, v := range next {
		if summable[k] {
			out[k] = usageInt64(out, k) + usageInt64(next, k)
			continue
		}
		out[k] = v
	}
	return out
}

type agentUsageBoxKey struct{}

// WithAgentUsageBox returns a child context carrying a fresh, empty usage box.
// Install one per agent host call so usage doesn't leak between calls.
func WithAgentUsageBox(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentUsageBoxKey{}, &agentUsageBox{})
}

func agentUsageBoxFrom(ctx context.Context) *agentUsageBox {
	b, _ := ctx.Value(agentUsageBoxKey{}).(*agentUsageBox)
	return b
}

// recordAgentUsage sums token usage + cost into the box in ctx, if one is
// installed. No-op when no box is present (e.g. direct unit-test calls) or when
// there's nothing to record, so the transport can call it unconditionally.
// Summing (not overwriting) is what makes the box correct across a call's
// internal retries/resumes/steps — see agentUsageBox's doc comment.
func recordAgentUsage(ctx context.Context, usage map[string]any, cost float64) {
	if usage == nil && cost == 0 {
		return
	}
	b := agentUsageBoxFrom(ctx)
	if b == nil {
		return
	}
	b.mu.Lock()
	if usage != nil {
		b.usage = sumUsage(b.usage, usage)
	}
	if cost != 0 {
		b.cost += cost
	}
	b.mu.Unlock()
}

// AgentCostFrom returns the total_cost_usd recorded into the per-call usage box
// in ctx, or 0 when no box is installed or nothing was recorded. The orchestrator
// reads this immediately after host.Invoke to fold the call's cost into the
// reserved world vars turn_cost_usd / session_cost_usd. Both the live claude
// transport (recordAgentUsage during streaming) and the cassette dispatch path
// (recordAgentUsage from resp.Meta) populate the box, so the value is the same
// in live and replay — keeping cost-budget guards deterministic in flow tests.
func AgentCostFrom(ctx context.Context) float64 {
	b := agentUsageBoxFrom(ctx)
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cost
}

// AgentUsageFrom returns the token usage object recorded into the per-call
// usage box in ctx, or nil when no usage was recorded. The returned map is a
// shallow copy so callers can persist benchmark/report data without holding the
// box lock or mutating shared state.
func AgentUsageFrom(ctx context.Context) map[string]any {
	b := agentUsageBoxFrom(ctx)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage == nil {
		return nil
	}
	out := make(map[string]any, len(b.usage))
	for k, v := range b.usage {
		out[k] = v
	}
	return out
}

// agentUsageMeta builds the AgentReturned.Meta map from the usage box in ctx,
// or returns nil when no usage was recorded (so Meta stays omitempty). The
// shape is {"usage": {…claude usage object…}, "cache": {…CacheUsage…},
// "cost_usd": <float>}.
func agentUsageMeta(ctx context.Context) map[string]any {
	b := agentUsageBoxFrom(ctx)
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.usage == nil && b.cost == 0 {
		return nil
	}
	meta := map[string]any{}
	if b.usage != nil {
		meta["usage"] = b.usage
		if cache := cacheUsageFromMap(b.usage); cache != nil {
			meta["cache"] = cache
		}
	}
	if b.cost != 0 {
		meta["cost_usd"] = b.cost
	}
	return meta
}

// ── Cache-usage surfacing (dispatch-context-floor task 1.2) ─────────────────
//
// The claude CLI's stream-json `result` event already reports
// cache_read_input_tokens / cache_creation_input_tokens (parsed in
// agent_runner.go and stashed into the usage box above), so the raw numbers
// already reach agent.call.complete inside Meta.usage. What was missing —
// per the dispatch-context-floor spike (.context/2026-07-05-s3-spike-cache-fields.md)
// — was a named, typed surface mirroring internal/harness/live.go's
// UsageInfo (CacheReadTokens / CacheCreateTokens / CacheHit), the same shape
// LiveHarness already logs for routing calls, so a trace consumer doesn't
// have to know the claude-CLI-specific snake_case key names to compute
// cache-hit visibility for host.agent.* dispatch.

// CacheUsage is the named cache-usage surface for a single agent host-call,
// derived from the claude CLI's reported usage object. Hit is true only when
// this call actually read from the cache; a call that merely wrote a new
// cache-eligible prefix (CreationTokens > 0, ReadTokens == 0 — e.g. the very
// first call after a prompt-shape change) is a cache miss, not a hit.
type CacheUsage struct {
	ReadTokens     int64 `json:"read_tokens"`
	CreationTokens int64 `json:"creation_tokens"`
	Hit            bool  `json:"hit"`
}

// cacheUsageFromMap derives a CacheUsage from the raw usage map the claude CLI
// transport recorded (recordAgentUsage), or nil when the map carries neither
// cache key — e.g. a transport that reports no cache accounting at all (the
// copilot path, per mergeOutputTokens above) — so Meta.cache stays omitted
// rather than falsely reporting an all-zero cache result for a transport that
// never measured caching in the first place.
func cacheUsageFromMap(usage map[string]any) *CacheUsage {
	if usage == nil {
		return nil
	}
	_, hasRead := usage["cache_read_input_tokens"]
	_, hasCreate := usage["cache_creation_input_tokens"]
	if !hasRead && !hasCreate {
		return nil
	}
	// usageInt64 (quota_control.go) already extracts an integer usage field
	// from a raw usage map, tolerating both the float64 shape json.Unmarshal
	// produces and a plain int/int64 a test or in-process caller might
	// construct directly — reused here rather than duplicated.
	read := usageInt64(usage, "cache_read_input_tokens")
	create := usageInt64(usage, "cache_creation_input_tokens")
	return &CacheUsage{
		ReadTokens:     read,
		CreationTokens: create,
		Hit:            read > 0,
	}
}

// ── EventSink context plumbing ────────────────────────────────────────────────

// agentEventSinkKey is the context key for a store.EventSink injected into
// agent handler calls for the JSONL write.
type agentEventSinkKey struct{}

// WithAgentEventSink returns a child context carrying sink. Agent handlers
// call EventSinkFromAgentCtx to retrieve it. Nil is a safe no-op.
func WithAgentEventSink(ctx context.Context, sink store.EventSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, agentEventSinkKey{}, sink)
}

// EventSinkFromAgentCtx returns the store.EventSink previously injected with
// WithAgentEventSink, or nil if none was injected.
func EventSinkFromAgentCtx(ctx context.Context) store.EventSink {
	s, _ := ctx.Value(agentEventSinkKey{}).(store.EventSink)
	return s
}

// ── AgentCalled / AgentReturned / AgentError payload types ─────────────────

// AgentCalledPayload is the payload written to AgentCalled events.
// The verb identifies which agent verb dispatched the call (ask, decide,
// extract, task, converse). The call_id is a deterministic identifier that
// pairs this event with the matching AgentReturned or AgentError event.
// Replay treats AgentCalled as a no-op.
//
// NOTE: Large prompts are stored in separate files to keep the JSONL line
// under PIPE_BUF (4096 bytes). When PromptFile is set, the full prompt is
// in that external file. The prompt is available in:
// - The agent.AskRequest.PromptText (live mode)
// - The cassette via !include or separate prompt file (replay mode)
// This ensures deterministic replay while staying under atomic write limits.
// See agent_dispatch.go appendAgentCalledEventWithEpisode for details.
type AgentCalledPayload struct {
	Verb         string   `json:"verb"`
	Agent        string   `json:"agent,omitempty"`
	Model        string   `json:"model,omitempty"`
	Toolbox      string   `json:"toolbox,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	DeniedTools  []string `json:"denied_tools,omitempty"`
	Effect       string   `json:"effect,omitempty"`
	// Backend is the selected coding-agent backend for this call. It is stamped
	// centrally from the context so live, plugin, and cassette traces expose the
	// same provenance field.
	Backend string `json:"backend,omitempty"`
	// Profile is the active harness profile name in effect for this call, when a
	// session selected one (TUI /provider, web picker). It records which
	// operator-selected backend/endpoint answered — never the env secrets behind
	// it. Empty for the default no-profile path. Stamped centrally in
	// appendAgentCalledEvent / appendAgentCalledEventWithEpisode.
	Profile string `json:"profile,omitempty"`
	// Effort is the reasoning effort in effect for this call (the active profile's
	// effort or its operator override), when one was selected. Empty otherwise.
	Effort string `json:"effort,omitempty"`
	// RuntimeKind records the execution boundary for a codeact call. CLI-backed
	// CodeAct must be paired with agent.runtime receipts; direct_api has no
	// local subprocess and must not fabricate those receipts. Empty preserves
	// compatibility for the other host.agent verbs and legacy traces.
	RuntimeKind string `json:"runtime_kind,omitempty"`
	// Prompt is the inline rendered prompt, present when it is small enough to
	// embed (≤ the offload threshold). Larger prompts are written to a sidecar
	// file and referenced via PromptFile instead. Exactly one of Prompt /
	// PromptFile is set on every agent.call.start (see docs/tracing/trace-format.md),
	// so a consumer always has a prompt reference to resolve.
	Prompt     string          `json:"prompt,omitempty"`
	PromptFile string          `json:"prompt_file,omitempty"` // Path to external prompt file if large
	Input      json.RawMessage `json:"input,omitempty"`
	// PromptOverlay records the project prompt-overlay directory that was in
	// effect when this prompt was rendered, when one was. It is the provenance
	// of an extended prompt: the rendered bytes (Prompt / PromptFile) already
	// capture what the LLM saw, and PromptOverlay records that an overlay
	// contributed and which one. Empty for the common no-overlay case. See
	// docs/stories/prompts.md.
	PromptOverlay string `json:"prompt_overlay,omitempty"`
	// SpecOverridden / SpecDefaulted record which of the story base's spec_
	// specialization blocks the overlay overrode vs. left at their provisional
	// default on this render — the labeled datapoint behind "this provisional
	// default was never specialized here". Populated only when an overlay
	// contributed spec_ provenance.
	SpecOverridden []string `json:"spec_overridden,omitempty"`
	SpecDefaulted  []string `json:"spec_defaulted,omitempty"`
}

// AgentReturnedPayload is the payload written to AgentReturned events.
// Meta is opaque (tokens, cost, model — varies by agent transport).
// Replay treats AgentReturned as a no-op.
//
// NOTE: Large responses are stored in separate files to keep the JSONL line
// under PIPE_BUF (4096 bytes). When ResponseFile is set, the full response is
// in that external file. The response is available in:
// - The agent handler's response field (live mode)
// - The cassette's response field or separate response file (replay mode)
type AgentReturnedPayload struct {
	Verb         string          `json:"verb"`
	Agent        string          `json:"agent,omitempty"`
	Model        string          `json:"model,omitempty"`
	DurationMS   int64           `json:"duration_ms"`
	Response     json.RawMessage `json:"response,omitempty"`
	ResponseFile string          `json:"response_file,omitempty"` // Path to external response file if large
	Meta         map[string]any  `json:"meta,omitempty"`
	// Substitution records that the closing event belongs to a call whose
	// originally-resolved backend was substituted at runtime. Today the only
	// substitution is the local-model → agent.claude validation-reject
	// fallback (step 4): when a builtin.local_llm backend returns a
	// schema_invalid Submission, the SAME call is re-dispatched once to
	// agent.claude under the same call_id, and this field is set to
	// {reason, original_plugin, fallback_plugin} so the trace explains why a
	// local-model call's response actually came from claude. Omitted (nil) on
	// every normal call, so the field is backward compatible.
	Substitution map[string]any `json:"substitution,omitempty"`
	// TranscriptRef is the pointer-only reference to this call's agent-action
	// transcript sidecar (the claude stream-json / openai-chat events captured
	// for the "Agent actions" drawer). It carries NO inlined detail — just the
	// format, the relative sidecar path, the event count, and the schema version
	// (see host.TranscriptRef). Omitted (nil) when the call produced no
	// transcript, so a run with no transcripts renders exactly as before. The
	// detail lives in <trace_dir>/transcripts/<call_id>.jsonl (+ .timings),
	// fetched lazily by the web consumer; see docs/tracing/trace-format.md (Agent-action transcript sidecar).
	TranscriptRef *TranscriptRef `json:"transcript_ref,omitempty"`
}

// AgentErrorPayload is the payload written to AgentError events.
// Replay treats AgentError as a no-op.
type AgentErrorPayload struct {
	Verb       string `json:"verb"`
	Agent      string `json:"agent,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
	// Meta mirrors AgentReturnedPayload.Meta for a failed call — today used
	// only to carry the active harness-ladder rung (ladder_rung /
	// ladder_failure_kind, see ladderMetaFields in ladder.go) so a rung's
	// failing attempts are visible in the trace, not just its eventual
	// winner. Omitted (nil) on every call with no ladder installed.
	Meta map[string]any `json:"meta,omitempty"`
	// Substitution mirrors AgentReturnedPayload.Substitution: when the
	// local-model → agent.claude validation-reject fallback (step 4) was
	// attempted but the fallback ALSO failed, the closing AgentError carries
	// the same {reason, original_plugin, fallback_plugin} provenance so the
	// trace shows that a substitution was tried before the call failed.
	// Omitted (nil) on every normal error.
	Substitution map[string]any `json:"substitution,omitempty"`
	// TranscriptRef points at the per-call agent-action sidecar, same as on
	// AgentReturnedPayload. A failed call still produced agent actions (the
	// partial decide reject→nudge arc, a local-model transcript that was
	// schema-rejected before fallback), and they are exactly what an operator
	// reviewing a failure wants. Finalizing here also frees the writer's
	// in-memory buffer for the call. Omitted (nil) when no transcript was captured.
	TranscriptRef *TranscriptRef `json:"transcript_ref,omitempty"`
}

// ── JSONL append helpers ───────────────────────────────────────────────────────

// appendAgentCalledEvent appends an AgentCalled event to the EventSink in
// ctx (if any). callID and ts are the deterministic call identifier and the
// dispatch timestamp respectively. oc carries the session/turn/state.
//
// promptText is the rendered prompt for this call. It is always recorded as a
// reference on the event (see docs/tracing/trace-format.md): small prompts are
// embedded inline in payload.Prompt; large prompts are written to a sidecar
// file and referenced via payload.PromptFile. Callers should leave both
// Prompt and PromptFile unset on the payload they pass — this helper fills the
// appropriate one. Pass "" for promptText to record neither (e.g. verbs with
// no single prompt string).
func appendAgentCalledEvent(ctx context.Context, ts time.Time, callID string, promptText string, payload AgentCalledPayload) {
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

	// Guarantee a prompt reference: offload large prompts to a sidecar file,
	// otherwise embed inline so a consumer never faces a missing reference.
	if promptText != "" {
		if promptFile, _ := storePromptIfLarge(ctx, callID, promptText); promptFile != "" {
			payload.PromptFile = promptFile
		} else {
			payload.Prompt = promptText
		}
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return // best-effort; marshal failure is not a reason to abort the call
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentCalled,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// appendAgentReturnedEvent appends an AgentReturned event to the EventSink
// in ctx (if any). ts is the response timestamp (real, not fudged).
// If the response is large, it is stored in a separate file and the
// payload.ResponseFile is set to reference it.
func appendAgentReturnedEvent(ctx context.Context, ts time.Time, callID string, payload AgentReturnedPayload) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)

	// Default Meta from the per-call usage box (token usage + cost captured by
	// the claude CLI transport). A handler that already set Meta explicitly
	// (e.g. the plugin dispatch path, which carries the plugin's own meta) wins.
	if payload.Meta == nil {
		payload.Meta = agentUsageMeta(ctx)
	}

	// Finalize the agent-action transcript sidecar (if a writer + events were
	// accumulated for this call) and attach the pointer-only ref before the
	// agent.call.complete event is emitted. The in-host claude path teed its
	// RawEvents into the writer during the call (agent_runner.go); the dispatch
	// path for out-of-host backends fed AskResponse.Transcript in first. Finalize
	// returns nil (and the field stays omitted) when nothing was appended, so a
	// call with no transcript renders exactly as before. A handler that already
	// set TranscriptRef explicitly wins (none does today).
	if payload.TranscriptRef == nil {
		payload.TranscriptRef = finalizeTranscript(ctx, callID)
	}

	// Store large responses in separate files to keep JSONL lines under PIPE_BUF.
	if len(payload.Response) > 0 {
		if responseFile, err := storeResponseIfLarge(ctx, callID, payload.Response); err == nil && responseFile != "" {
			payload.ResponseFile = responseFile
			payload.Response = nil // Clear the inline response since it's in a separate file
		}
		// On error, proceed with the original (possibly large) payload anyway
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentReturned,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// appendAgentErrorEvent appends an AgentError event to the EventSink in
// ctx (if any). ts is the error timestamp.
func appendAgentErrorEvent(ctx context.Context, ts time.Time, callID string, payload AgentErrorPayload) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		// Still finalize so the writer's per-call buffer is freed even when no
		// sink is installed (the ref is just discarded).
		_ = finalizeTranscript(ctx, callID)
		return
	}
	oc := AgentCallCtxFrom(ctx)

	// Flush the agent-action transcript accumulated for this call (the partial
	// arc up to the failure) and attach the pointer, mirroring
	// appendAgentReturnedEvent. Nil when nothing was captured. This also frees
	// the writer's in-memory buffer for callID.
	if payload.TranscriptRef == nil {
		payload.TranscriptRef = finalizeTranscript(ctx, callID)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	ev := store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentError,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
		CallID:    callID,
	}
	_ = sink.Append(ev)
}

// appendAgentStreamEvent appends a compact in-flight provider event to the
// trace so the web timeline moves while the agent is still running. Full raw
// provider events remain in the transcript sidecar; this event is intentionally
// small and replay-no-op.
func appendAgentStreamEvent(ctx context.Context, ts time.Time, callID string, ev StreamEvent) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil || callID == "" || ev.IsResult {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	payload := map[string]any{
		"type":    ev.Type,
		"subtype": ev.Subtype,
	}
	if ev.Severity != "" {
		payload["severity"] = ev.Severity
	}
	if ev.Tool != "" {
		payload["tool"] = ev.Tool
	}
	if ev.Preview != "" {
		payload["preview"] = ev.Preview
	}
	if ev.Text != "" {
		payload["text"] = truncateTraceText(ev.Text, 700)
	}
	if ev.Thinking != "" {
		payload["thinking"] = truncateTraceText(ev.Thinking, 700)
	}
	if len(ev.Tools) > 0 {
		tools := make([]map[string]string, 0, len(ev.Tools))
		for _, tool := range ev.Tools {
			tools = append(tools, map[string]string{
				"name":    tool.Name,
				"preview": tool.Preview,
			})
		}
		payload["tools"] = tools
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentStreamEvent,
		StatePath: oc.StatePath,
		CallID:    callID,
		Payload:   raw,
	})
	if ev.ActionState == "" || ev.Tool == "" {
		return
	}
	action, err := json.Marshal(map[string]any{
		"schema": "agent-action/v1", "method": ev.Tool, "action_id": ev.ActionID,
		"parent_call_id": callID, "outcome_class": ev.ActionState,
		"duration_available": false, "result_summary_available": false,
		"arguments_summary": ev.Preview,
	})
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{Turn: oc.Turn, Ts: ts, Kind: store.EventKind("agent.action"), StatePath: oc.StatePath, CallID: callID, Payload: action})
}

type storeAgentNotice struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype,omitempty"`
	Severity        string          `json:"severity,omitempty"`
	Text            string          `json:"text"`
	Backend         string          `json:"backend,omitempty"`
	Provider        string          `json:"provider,omitempty"`
	Model           string          `json:"model,omitempty"`
	Effort          string          `json:"effort,omitempty"`
	Error           string          `json:"error,omitempty"`
	Bin             string          `json:"bin,omitempty"`
	WorkingDir      string          `json:"working_dir,omitempty"`
	Args            []string        `json:"args,omitempty"`
	PID             int             `json:"pid,omitempty"`
	DurationMS      int64           `json:"duration_ms,omitempty"`
	ExitCode        int             `json:"exit_code,omitempty"`
	RawEventCount   int             `json:"raw_event_count,omitempty"`
	ProviderEnvKeys []string        `json:"provider_env_keys,omitempty"`
	EnvPresent      map[string]bool `json:"env_present,omitempty"`
	UID             int             `json:"uid,omitempty"`
	IsRoot          bool            `json:"is_root,omitempty"`
	IsSandbox       bool            `json:"is_sandbox,omitempty"`
}

// appendAgentNoticeEvent records an operator-facing agent breadcrumb that is
// not tied to a provider stream event. It is deliberately stored as
// agent.stream so existing timeline surfaces show the notice without a new
// trace vocabulary.
func appendAgentNoticeEvent(ctx context.Context, ts time.Time, notice storeAgentNotice) {
	if sink := StreamSinkFrom(ctx); sink != nil && notice.Text != "" {
		sink.OnStreamEvent(ctx, StreamEvent{
			Type:     notice.Type,
			Subtype:  notice.Subtype,
			Severity: notice.Severity,
			Preview:  onelinePreview(notice.Text, 120),
			Text:     notice.Text,
			Backend:  notice.Backend,
			Provider: notice.Provider,
			Model:    notice.Model,
			Effort:   notice.Effort,
			Error:    notice.Error,
		})
	}
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil || notice.Text == "" {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	raw, err := json.Marshal(notice)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentStreamEvent,
		StatePath: oc.StatePath,
		CallID:    CallIDFrom(ctx),
		Payload:   raw,
	})
}

func truncateTraceText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// ── Legacy cassette record-mode types (B-5: dead code, kept for compile compat) ─

// AgentCallBody was the body written to KindAgentCall journal entries by the
// now-deleted appendAgentCallJournal helper. It is retained here only because
// internal/testrunner still references it for cassette record-mode scaffolding.
// With the SQLite journal write path deleted in B-5, journalLookup will always
// return (nil, false); the cassette agent block is never populated via this
// path. Phase C+ will remove the cassette record mode entirely.
type AgentCallBody struct {
	// Identity
	CallID string `json:"call_id"`
	Verb   string `json:"verb"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`

	// TaskTraceID (task verb only).
	TaskTraceID string `json:"task_trace_id,omitempty"`

	// Timing and token usage.
	DurationMS     int64   `json:"duration_ms"`
	PromptTokens   int     `json:"prompt_tokens,omitempty"`
	ResponseTokens int     `json:"response_tokens,omitempty"`
	CostUSD        float64 `json:"cost_usd,omitempty"`

	// Full prompt content.
	SystemPrompt string `json:"system_prompt,omitempty"`
	Prompt       string `json:"prompt,omitempty"`

	// Verb-specific input and response descriptors.
	Input    json.RawMessage `json:"input,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`

	// Error string when the call failed.
	Error string `json:"error,omitempty"`
}

// marshalInput marshals the verb-specific input descriptor to JSON, returning
// nil on error.
func marshalInput(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// marshalResponse marshals the verb-specific response descriptor to JSON,
// returning nil on error.
func marshalResponse(v any) json.RawMessage {
	return marshalInput(v) // same logic
}

// StorePromptIfLargeForTest is exported for cassette tests.
// See storePromptIfLarge for details.
func StorePromptIfLargeForTest(ctx context.Context, callID string, prompt string) (string, error) {
	return storePromptIfLarge(ctx, callID, prompt)
}

// storePromptIfLarge writes the prompt to a separate file if it's large (>1KB),
// returning the file reference for the PromptFile field, or "" if the prompt
// was small or storage is not configured. Large prompts are stored in
// {promptsDir}/{callID}.txt to keep JSONL lines under PIPE_BUF.
//
// Returns: (promptFileRef, error). On error, returns ("", err) and the event
// should still be written (prompt unavailable in UI, but execution continues).
func storePromptIfLarge(ctx context.Context, callID string, prompt string) (string, error) {
	const largeThreshold = 1024 // 1KB

	if len(prompt) <= largeThreshold {
		return "", nil // Small enough to include inline in future; not implemented yet
	}

	promptsDir := AgentPromptsDirFromCtx(ctx)
	if promptsDir == "" {
		return "", nil // Storage not configured; skip
	}

	// Ensure prompts directory exists.
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return "", fmt.Errorf("storePromptIfLarge: mkdir %q: %w", promptsDir, err)
	}

	// Write prompt to {promptsDir}/{callID}.txt.
	promptPath := filepath.Join(promptsDir, callID+".txt")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return "", fmt.Errorf("storePromptIfLarge: write %q: %w", promptPath, err)
	}

	// Return relative path for portability (relative to trace dir).
	return filepath.Join("agent-prompts", callID+".txt"), nil
}

// storeResponseIfLarge writes the response to a separate file if it's large (>1KB),
// returning the file reference for the ResponseFile field, or "" if the response
// was small or storage is not configured. Large responses are stored in
// {promptsDir}/{callID}-response.json to keep JSONL lines under PIPE_BUF.
//
// Returns: (responseFileRef, error). On error, returns ("", err) and the event
// should still be written (response unavailable in UI, but execution continues).
func storeResponseIfLarge(ctx context.Context, callID string, response json.RawMessage) (string, error) {
	const largeThreshold = 1024 // 1KB

	if len(response) <= largeThreshold {
		return "", nil // Small enough to include inline
	}

	promptsDir := AgentPromptsDirFromCtx(ctx)
	if promptsDir == "" {
		return "", nil // Storage not configured; skip
	}

	// Ensure prompts directory exists (reuse for both prompts and responses).
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return "", fmt.Errorf("storeResponseIfLarge: mkdir %q: %w", promptsDir, err)
	}

	// Write response to {promptsDir}/{callID}-response.json.
	responsePath := filepath.Join(promptsDir, callID+"-response.json")
	if err := os.WriteFile(responsePath, []byte(response), 0o644); err != nil {
		return "", fmt.Errorf("storeResponseIfLarge: write %q: %w", responsePath, err)
	}

	// Return relative path for portability (relative to trace dir).
	return filepath.Join("agent-prompts", callID+"-response.json"), nil
}
