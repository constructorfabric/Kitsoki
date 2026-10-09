// Package host — the generalized harness ladder for host.agent.* dispatch.
//
// A "ladder" is an ordered, cheap-first list of {backend, provider, model}
// slots (the MODEL axis — availability / cost) crossed with an ordered list of
// reasoning efforts (the EFFORT axis — capability). Two independent failure
// modes drive two independent responses:
//
//   - Infra failure (the provider/API is down, rate-limited, quota-exhausted,
//     5xx, timeout, model_not_found, connection-refused): rotate to the NEXT
//     PROVIDER/HARNESS for availability — a provider error is not solved by
//     another effort/model-strength rung on the same lane — and mark the
//     failing provider/harness in backoff so subsequent ladder walks (this
//     process or any other sharing the state file) skip it until a cooldown
//     passes. Reuses the on-disk provider-quota state file (quota_control.go)
//     rather than inventing a second backoff substrate.
//   - Capability failure (the agent ran but its schema/acceptance/gate check
//     failed — the CALLER detects and signals this, the ladder never guesses
//     it from a stack trace): escalate EFFORT-FIRST on the SAME model
//     (low→medium→high→xhigh→max, cheaper than a model jump) before moving to
//     a stronger model and sweeping effort again from low.
//
// So RunLadder walks the model×effort grid model-outer, effort-inner: for
// each model (skipped if its provider/harness lane is backed off), sweep every
// effort; a capability failure advances to the next effort (or, once efforts
// are exhausted, falls through to the next model); an infra failure abandons the
// remaining effort/model rungs for that provider/harness lane outright and moves
// on. A FailureFatal result (a config/argument error, e.g. a missing schema:
// arg) stops the walk immediately —
// no rung can fix a story authoring mistake.
//
// # Wrap point
//
// host.agent.decide and host.agent.task each split into a thin exported
// entrypoint plus an unexported "once" function carrying the original body.
// The entrypoint calls runAgentVerbWithLadder, which is a no-op passthrough
// when no LadderConfig is installed on ctx (WithHarnessLadder) — the default
// for every existing call site and test, so behavior is byte-identical until
// an operator opts in via `harness_ladder:` (see internal/webconfig). When a
// ladder IS installed, each attempt gets its own rung installed on ctx
// (WithLadderRung); agent_decide.go / agent_task.go apply it via
// applyLadderRung right after their existing applyProvider call, so a rung
// unconditionally overrides the resolved agent's model/effort/backend for
// that one attempt.
//
// # Instrumentation
//
// Every attempt (success or failure) is recorded in the returned LadderSummary
// with its resolved rung and failure classification. A rung's EscalatedBy
// field is computed BEFORE dispatch (not just on the eventual winner): ""
// for the very first attempt, "effort" when the immediately preceding attempt
// used the same model, "model" when it used a different one. This is exactly
// the effort-vs-model efficacy signal called out in the design: a downstream
// reader can correlate "escalated_by" against the preceding attempt's
// failure_kind to answer "how often does more effort fix it vs. how often do
// we need a stronger model". agent_decide.go / agent_task.go fold the active
// rung into each attempt's AgentReturned/AgentError trace Meta (ladder_rung /
// ladder_failure_kind) so the per-attempt detail survives into the trace
// without a new store.Event kind.
package host

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// FailureKind classifies why one host.agent.* call did not produce a usable
// result. See the Result.FailureKind doc for how handlers populate it.
type FailureKind string

const (
	// FailureNone means the call succeeded (or the field was left unset on a
	// handler that returned no error).
	FailureNone FailureKind = ""
	// FailureInfra means the provider/transport itself failed to answer
	// (rate limit, quota, 5xx, timeout, model_not_found, connection error).
	// The ladder rotates to the next provider/harness lane and backs the
	// current one off.
	FailureInfra FailureKind = "infra"
	// FailureCapability means the call ran to completion but its result
	// failed a schema/acceptance/gate check. The ladder escalates effort on
	// the same model before trying a stronger model.
	FailureCapability FailureKind = "capability"
	// FailureFatal means a config/argument error (e.g. a missing required
	// arg, an unknown agent name) that no rung on the ladder can fix. The
	// ladder stops immediately rather than repeating the identical mistake
	// across every rung.
	FailureFatal FailureKind = "fatal"
)

// LadderModel is one {backend, provider, model} slot on the ladder's model
// axis, ordered cheap→strong. Backend is a value accepted by
// ResolveAgentBackendName ("claude" | "codex" | "copilot"); empty defaults to
// claude. Provider, when set, names an entry resolved via
// ProvidersFromContext — the SAME `with: { provider: <name> }` mechanism
// host.agent.* already honors via applyProvider — so a rung's env overrides
// (API keys, base URLs) come from the story's `providers:` block (or, once an
// operator wires webconfig harness_profiles into that same map, from
// .kitsoki.yaml). Empty Provider means ambient env for Backend.
type LadderModel struct {
	Backend  string
	Provider string
	Model    string
}

// LadderConfig is the ordered, cheap-first harness ladder. Models is the
// availability axis (outer loop); Efforts is the capability axis (inner
// loop, swept low→max on each model before moving to the next). MaxAttempts
// caps the total number of dispatches across the whole grid walk (0 = no
// cap beyond len(Models)*len(Efforts)). StatePath / Backoff let a config
// override the shared quota-state substrate and cooldown window; both default
// sanely when empty.
type LadderConfig struct {
	Models      []LadderModel
	Efforts     []string
	MaxAttempts int
	// StatePath overrides the on-disk backoff state file (defaults to the
	// same file quota_control.go uses for provider quota, "" ⇒ that default).
	StatePath string
	// Backoff is a Go duration string for how long an infra-failing
	// provider/harness lane is skipped after one failure ("" ⇒ defaultLadderBackoff).
	Backoff string
}

// defaultLadderBackoff is the cooldown applied to a model slot after an
// infra failure, when LadderConfig.Backoff is unset. Conservative relative to
// the 1-minute provider-quota window (quota_control.go's defaultQuotaWindow):
// an infra failure (429/5xx/timeout) usually needs longer than a token-bucket
// refill to clear.
const defaultLadderBackoff = 5 * time.Minute

// Enabled reports whether cfg describes at least one dispatchable model slot.
// A zero-value LadderConfig is disabled — the ladder wrapper then passes
// every call straight through (today's single-dispatch behavior).
func (c LadderConfig) Enabled() bool { return len(c.Models) > 0 }

func (c LadderConfig) statePath() string {
	if strings.TrimSpace(c.StatePath) != "" {
		return c.StatePath
	}
	return defaultQuotaStatePath
}

func (c LadderConfig) backoffDuration() time.Duration {
	if strings.TrimSpace(c.Backoff) != "" {
		if d, err := time.ParseDuration(c.Backoff); err == nil && d > 0 {
			return d
		}
	}
	return defaultLadderBackoff
}

func (c LadderConfig) efforts() []string {
	if len(c.Efforts) == 0 {
		return []string{""}
	}
	return c.Efforts
}

// DefaultLadderConfig is the shipped availability-first default:
// claude-native → codex-native → synthetic-claude → synthetic-codex, each
// swept low→max effort before the next model. synthetic-codex is intentionally
// last: keeping it in the ladder preserves the profile/catalog contract for
// operators, but normal automatic fallback should prefer synthetic through the
// claude backend before trying the codex retarget path. The
// Provider names match the harness_profiles convention used by
// .kitsoki.yaml / .kitsoki.local.yaml (see docs/guide/agents/harness-profiles.md)
// so a deployment that declares those profiles (folded into the providers map
// an operator exposes to host.agent.* — see docs/architecture/harness-ladder.md)
// gets matching env/credentials for free; a deployment that does not still
// gets correct backend+model+effort overrides per rung (env stays ambient for
// that rung, mirroring applyProvider's unknown-provider-name behavior).
func DefaultLadderConfig() LadderConfig {
	return LadderConfig{
		Models: []LadderModel{
			{Backend: "claude", Provider: "claude-native", Model: "opus"},
			{Backend: "codex", Provider: "codex-native", Model: "gpt-5.5"},
			{Backend: "claude", Provider: "synthetic-claude", Model: "hf:zai-org/GLM-5.2"},
			{Backend: "codex", Provider: "synthetic-codex", Model: "hf:zai-org/GLM-5.2"},
		},
		Efforts: []string{"low", "medium", "high", "xhigh", "max"},
	}
}

// LadderRung is one fully-resolved dispatch attempt: a model slot at a
// specific effort level, plus its position in the grid and how it relates to
// the attempt immediately before it.
type LadderRung struct {
	ModelIndex  int
	EffortIndex int
	Backend     string
	Provider    string
	Model       string
	Effort      string
	// EscalatedBy is computed BEFORE dispatch (not just for the eventual
	// winner): "" for the ladder's very first attempt, "effort" when the
	// immediately preceding attempt used the same model (this rung only
	// bumped effort), "model" when it used a different model. This is the
	// effort-vs-model efficacy signal: pair it with the preceding attempt's
	// FailureKind (in LadderSummary.Attempts) to see whether a bump in
	// effort or a jump in model actually resolved the failure.
	EscalatedBy string
}

// LadderOutcome records one attempt for LadderSummary.Attempts.
type LadderOutcome struct {
	Rung    LadderRung
	Failure FailureKind
	Error   string
}

// LadderSummary is the full attempt history from one RunLadder call, plus
// (on success) the winning rung and how it was reached.
type LadderSummary struct {
	Attempts []LadderOutcome
	// Winner is the rung that produced FailureNone, or nil when every rung
	// was exhausted / skipped.
	Winner *LadderRung
	// EscalatedBy mirrors Winner.EscalatedBy for convenience (empty when
	// Winner is nil).
	EscalatedBy string
}

// LastError returns the most recent attempt's error text, or "" when no
// attempt was made.
func (s LadderSummary) LastError() string {
	if len(s.Attempts) == 0 {
		return ""
	}
	return s.Attempts[len(s.Attempts)-1].Error
}

// ExpandLadder returns the full model×effort grid in the order RunLadder
// walks it on an all-capability-failure run: model outer (cheap→strong),
// effort inner (low→max). An infra failure on a model skips its remaining
// effort rungs (RunLadder handles that; this is the reference ordering used
// by tests and any caller that wants to preview the walk without dispatching).
func ExpandLadder(cfg LadderConfig) []LadderRung {
	efforts := cfg.efforts()
	out := make([]LadderRung, 0, len(cfg.Models)*len(efforts))
	prevModelIdx := -1
	for mi, m := range cfg.Models {
		for ei, eff := range efforts {
			escalatedBy := ""
			switch {
			case prevModelIdx == -1:
				escalatedBy = ""
			case prevModelIdx == mi:
				escalatedBy = "effort"
			default:
				escalatedBy = "model"
			}
			out = append(out, LadderRung{
				ModelIndex: mi, EffortIndex: ei,
				Backend: m.Backend, Provider: m.Provider, Model: m.Model, Effort: eff,
				EscalatedBy: escalatedBy,
			})
			prevModelIdx = mi
		}
	}
	return out
}

// ── context plumbing ─────────────────────────────────────────────────────

// LadderSessionState carries the session-local ladder fallback pin. Once a
// ladder walk succeeds on a fallback rung, later host.agent.* calls in the same
// session start at that rung instead of probing earlier providers again. The
// orchestrator resets it when the operator changes provider/model/effort.
type LadderSessionState struct {
	mu     sync.RWMutex
	sticky *ladderStickyRung
}

type ladderStickyRung struct {
	modelsKey string
	rung      LadderRung
}

// NewLadderSessionState returns an empty session-local ladder state holder.
func NewLadderSessionState() *LadderSessionState {
	return &LadderSessionState{}
}

// Reset clears any session-pinned fallback rung.
func (s *LadderSessionState) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky = nil
}

func (s *LadderSessionState) stickyRung(cfg LadderConfig) (LadderRung, bool) {
	if s == nil {
		return LadderRung{}, false
	}
	modelsKey := ladderModelsKey(cfg)
	s.mu.RLock()
	sticky := s.sticky
	s.mu.RUnlock()
	if sticky == nil || sticky.modelsKey != modelsKey {
		return LadderRung{}, false
	}
	rung := sticky.rung
	if rung.ModelIndex < 0 || rung.ModelIndex >= len(cfg.Models) {
		return LadderRung{}, false
	}
	model := cfg.Models[rung.ModelIndex]
	if model.Backend != rung.Backend || model.Provider != rung.Provider || model.Model != rung.Model {
		return LadderRung{}, false
	}
	efforts := cfg.efforts()
	effortIndex := -1
	for i, effort := range efforts {
		if effort == rung.Effort {
			effortIndex = i
			break
		}
	}
	if effortIndex == -1 {
		return LadderRung{}, false
	}
	rung.EffortIndex = effortIndex
	rung.EscalatedBy = ""
	return rung, true
}

func (s *LadderSessionState) pinRung(cfg LadderConfig, rung LadderRung) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sticky = &ladderStickyRung{modelsKey: ladderModelsKey(cfg), rung: rung}
}

func ladderModelsKey(cfg LadderConfig) string {
	var b strings.Builder
	for _, model := range cfg.Models {
		b.WriteString(model.Backend)
		b.WriteByte('\x00')
		b.WriteString(model.Provider)
		b.WriteByte('\x00')
		b.WriteString(model.Model)
		b.WriteByte('\x00')
	}
	return b.String()
}

// ladderSessionCtxKey carries optional session-scoped sticky fallback state.
type ladderSessionCtxKey struct{}

// WithLadderSessionState installs session-local sticky fallback state.
func WithLadderSessionState(ctx context.Context, state *LadderSessionState) context.Context {
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, ladderSessionCtxKey{}, state)
}

// LadderSessionStateFromContext returns the state installed by
// WithLadderSessionState, when present.
func LadderSessionStateFromContext(ctx context.Context) (*LadderSessionState, bool) {
	state, ok := ctx.Value(ladderSessionCtxKey{}).(*LadderSessionState)
	return state, ok
}

// harnessLadderCtxKey carries the active LadderConfig, when one is installed.
type harnessLadderCtxKey struct{}

// WithHarnessLadder installs cfg as the harness ladder for every
// host.agent.decide / host.agent.task dispatch reached through the returned
// context. A disabled cfg (Enabled() == false) is a no-op, so passing a zero
// value is always safe — the common "no ladder configured" path leaves ctx
// untouched and every call site keeps today's single-dispatch behavior.
func WithHarnessLadder(ctx context.Context, cfg LadderConfig) context.Context {
	if !cfg.Enabled() {
		return ctx
	}
	return context.WithValue(ctx, harnessLadderCtxKey{}, cfg)
}

// HarnessLadderFromContext returns the LadderConfig installed by
// WithHarnessLadder, and whether one was installed.
func HarnessLadderFromContext(ctx context.Context) (LadderConfig, bool) {
	cfg, ok := ctx.Value(harnessLadderCtxKey{}).(LadderConfig)
	return cfg, ok
}

// ladderRungCtxKey carries the rung for the CURRENT attempt inside a
// RunLadder walk.
type ladderRungCtxKey struct{}

// WithLadderRung installs r as the active ladder rung for one attempt.
// Exported for tests; production code only sees this via RunLadder.
func WithLadderRung(ctx context.Context, r LadderRung) context.Context {
	return context.WithValue(ctx, ladderRungCtxKey{}, r)
}

// LadderRungFromContext returns the rung installed by WithLadderRung, and
// whether one was installed (false on the non-ladder / single-dispatch path).
func LadderRungFromContext(ctx context.Context) (LadderRung, bool) {
	r, ok := ctx.Value(ladderRungCtxKey{}).(LadderRung)
	return r, ok
}

// applyLadderRung overrides agent for the active ladder rung installed on
// ctx (see WithLadderRung), if any. No-op when no rung is installed — the
// path taken by every call site with no harness_ladder: configured, so
// existing behavior is unaffected. When a rung IS installed it
// unconditionally sets agent.Model / agent.Effort to the rung's values (a
// ladder rung always wins over the story's declared agent defaults — that is
// the point of an operator-declared ladder) and installs the rung's backend
// plus, best-effort, its provider's env overrides. An unresolvable provider
// name (no providers map installed, or the name absent from it) leaves env
// ambient for that backend — mirroring applyProvider's documented
// unknown-provider-name behavior — rather than failing the attempt.
func applyLadderRung(ctx context.Context, agent Agent) (context.Context, Agent) {
	rung, ok := LadderRungFromContext(ctx)
	if !ok {
		return ctx, agent
	}
	if rung.Model != "" {
		agent.Model = rung.Model
	}
	if rung.Effort != "" {
		agent.Effort = rung.Effort
	}
	if rung.Backend != "" {
		ctx = WithAgentBackendNamed(ctx, rung.Backend)
	}
	if rung.Provider != "" {
		if providers := ProvidersFromContext(ctx); providers != nil {
			if prov, ok := providers[rung.Provider]; ok {
				ctx = WithAgentProviderEnv(ctx, prov.Env)
			}
		}
	}
	return ctx, agent
}

// ladderMetaFields returns the trace-Meta fragment ({"ladder_rung": {...},
// "ladder_failure_kind": "..."}) for the active ladder rung installed on ctx,
// or nil when no rung is installed (the non-ladder path — callers should
// leave Meta untouched in that case). failureKind is the outcome of THIS
// attempt (FailureNone on a returning success, so ladder_failure_kind is
// omitted), letting a trace reader correlate ladder_rung against
// ladder_failure_kind per attempt without cross-referencing a separate
// summary event.
func ladderMetaFields(ctx context.Context, failureKind FailureKind) map[string]any {
	rung, ok := LadderRungFromContext(ctx)
	if !ok {
		return nil
	}
	m := map[string]any{
		"ladder_rung": map[string]any{
			"model_index":  rung.ModelIndex,
			"effort_index": rung.EffortIndex,
			"backend":      rung.Backend,
			"provider":     rung.Provider,
			"model":        rung.Model,
			"effort":       rung.Effort,
			"escalated_by": rung.EscalatedBy,
		},
	}
	if failureKind != FailureNone {
		m["ladder_failure_kind"] = string(failureKind)
	}
	return m
}

// ── dispatch loop ────────────────────────────────────────────────────────

// LadderAttemptFunc runs one dispatch attempt. ctx already carries the
// attempt's rung (WithLadderRung); implementations apply it via
// applyLadderRung after their normal agent/provider resolution. args is the
// verb call's original args, unmodified across attempts — the rung rides ctx,
// never args, so retrying never mutates the caller's map.
type LadderAttemptFunc func(ctx context.Context, args map[string]any) (Result, error)

// RunLadder walks cfg's model×effort grid (model-outer, effort-inner),
// dispatching once via `once` per rung, until a rung succeeds, the ladder is
// exhausted, or a FailureFatal result stops the walk immediately. See the
// package doc for the infra-vs-capability routing rules.
//
// Returns the winning (or last-attempted) Result, a non-nil Go error only
// when the terminal attempt itself returned one (e.g. context cancellation —
// propagated immediately without trying further rungs, since a cancelled
// context will not answer any subsequent attempt either), and the full
// LadderSummary for instrumentation.
func RunLadder(ctx context.Context, cfg LadderConfig, args map[string]any, once LadderAttemptFunc) (Result, error, LadderSummary) {
	var summary LadderSummary
	var lastResult Result
	efforts := cfg.efforts()
	statePath := cfg.statePath()
	backoff := cfg.backoffDuration()
	prevModelIdx := -1
	attempts := 0
	startModelIdx := 0
	startEffortIdx := 0
	var stickyRung LadderRung
	hasSticky := false
	if sessionState, ok := LadderSessionStateFromContext(ctx); ok {
		if sticky, ok := sessionState.stickyRung(cfg); ok {
			stickyRung = sticky
			hasSticky = true
			startModelIdx = sticky.ModelIndex
			startEffortIdx = sticky.EffortIndex
		}
	}

outer:
	for mi := startModelIdx; mi < len(cfg.Models); mi++ {
		m := cfg.Models[mi]
		key := ladderBackoffKey(m)
		if ladderInBackoff(statePath, key) {
			continue
		}
		effortStart := 0
		if hasSticky && mi == startModelIdx {
			effortStart = startEffortIdx
		}
		for ei := effortStart; ei < len(efforts); ei++ {
			eff := efforts[ei]
			if cfg.MaxAttempts > 0 && attempts >= cfg.MaxAttempts {
				break outer
			}
			escalatedBy := ""
			switch {
			case prevModelIdx == -1:
				escalatedBy = ""
			case prevModelIdx == mi:
				escalatedBy = "effort"
			default:
				escalatedBy = "model"
			}
			rung := LadderRung{
				ModelIndex: mi, EffortIndex: ei,
				Backend: m.Backend, Provider: m.Provider, Model: m.Model, Effort: eff,
				EscalatedBy: escalatedBy,
			}
			attemptCtx := WithLadderRung(ctx, rung)
			emitLadderAttemptNotice(attemptCtx, rung)
			res, err := once(attemptCtx, args)
			attempts++
			prevModelIdx = mi
			lastResult = res

			kind, errText := classifyLadderOutcome(res, err)
			summary.Attempts = append(summary.Attempts, LadderOutcome{Rung: rung, Failure: kind, Error: errText})

			if kind == FailureNone {
				winner := rung
				summary.Winner = &winner
				summary.EscalatedBy = escalatedBy
				if sessionState, ok := LadderSessionStateFromContext(ctx); ok && shouldPinLadderWinner(summary, stickyRung, hasSticky) {
					sessionState.pinRung(cfg, rung)
					emitLadderSessionPinnedNotice(ctx, rung)
				}
				emitLadderSuccessNotice(ctx, rung)
				return res, nil, summary
			}
			if isContextDone(err) {
				// A cancelled/expired context will not answer any subsequent
				// attempt either — stop immediately rather than burn through
				// the rest of the grid. Result is intentionally empty here,
				// mirroring today's single-dispatch context-cancellation path.
				return Result{}, err, summary
			}
			if kind == FailureFatal {
				// A config/argument error: no rung can fix it. Return the
				// ORIGINAL error untouched (not wrapped in an "exhausted"
				// message) so the story sees the real problem immediately.
				emitLadderStopNotice(ctx, rung, kind, errText)
				return res, err, summary
			}
			if kind == FailureInfra {
				emitLadderFallbackNotice(ctx, rung, kind, errText)
				markLadderBackoff(statePath, key, backoff)
				break // abandon this lane's remaining efforts/models; next lane.
			}
			if kind == FailureCapability {
				emitLadderFallbackNotice(ctx, rung, kind, errText)
			}
			// FailureCapability: fall through to the next effort (or, once efforts
			// are exhausted, the outer loop naturally advances to the next model).
		}
	}

	// Every remaining exit is a graceful "exhausted" terminal (context
	// cancellation and FailureFatal already returned early above, inside the
	// loop, without reaching here).
	res := lastResult
	if len(summary.Attempts) == 0 {
		res.Error = "host: harness ladder exhausted — every model is in backoff; no rung attempted"
	} else if res.Error == "" {
		// once() returned a bare Go error with an empty Result — surface it
		// as a graceful Result.Error instead of propagating the raw error so
		// on_error: arcs keep routing deterministically (no-crash contract).
		res.Error = fmt.Sprintf("host: harness ladder exhausted after %d attempt(s) across %d model(s): %s",
			len(summary.Attempts), len(cfg.Models), summary.LastError())
	} else {
		res.Error = fmt.Sprintf("host: harness ladder exhausted after %d attempt(s) across %d model(s); last error: %s",
			len(summary.Attempts), len(cfg.Models), res.Error)
	}
	emitLadderExhaustedNotice(ctx, summary, res.Error)
	res.FailureKind = FailureNone // terminal: nothing further the ladder can do
	return res, nil, summary
}

func shouldPinLadderWinner(summary LadderSummary, sticky LadderRung, hasSticky bool) bool {
	if summary.Winner == nil {
		return false
	}
	winner := *summary.Winner
	if hasSticky && sameLadderRung(winner, sticky) {
		return false
	}
	if len(summary.Attempts) > 1 {
		return true
	}
	return winner.ModelIndex > 0 || winner.EffortIndex > 0
}

func sameLadderRung(a, b LadderRung) bool {
	return a.ModelIndex == b.ModelIndex &&
		a.Backend == b.Backend &&
		a.Provider == b.Provider &&
		a.Model == b.Model &&
		a.Effort == b.Effort
}

// runAgentVerbWithLadder is the single wrap point shared by
// host.agent.decide and host.agent.task. No ladder installed on ctx ⇒ calls
// once exactly once and returns its result verbatim — byte-identical to
// calling once directly, so every existing call site / test that never
// installs a LadderConfig is unaffected. A ladder installed ⇒ drives RunLadder
// and logs a summary line for operators (per-attempt detail lives in each
// attempt's own AgentReturned/AgentError trace Meta via ladderMetaFields,
// applied by the verb handler itself).
func runAgentVerbWithLadder(ctx context.Context, args map[string]any, verb string, once LadderAttemptFunc) (Result, error) {
	if cfg, ok, err := ladderConfigFromArgs(args); err != nil {
		return Result{Error: fmt.Sprintf("host.agent.%s: harness_ladder: %v", verb, err), FailureKind: FailureFatal}, nil
	} else if ok {
		ctx = WithHarnessLadder(ctx, cfg)
	}

	// Pre-dispatch budget gate (dispatch-context-floor task 1.4): estimate
	// this call's token footprint and decide proceed/escalate/refuse BEFORE
	// any rung is dispatched. A refuse decision returns here — no claude
	// subprocess is ever spawned for this call. An escalate decision skips
	// the ladder's cheapest effort tier (see escalateLadderStart) so the
	// walk's first attempt already starts one step up; with no ladder
	// installed this is a no-op and the call proceeds exactly as before.
	if decision := checkDispatchBudget(ctx, args, verb); decision.Outcome == budgetRefuse {
		return decision.refuseResult(), nil
	} else if decision.Outcome == budgetEscalate {
		ctx = escalateLadderStart(ctx)
	}

	cfg, ok := HarnessLadderFromContext(ctx)
	if !ok || !cfg.Enabled() {
		return once(ctx, args)
	}
	res, err, summary := RunLadder(ctx, cfg, args, once)
	attrs := []any{
		"verb", verb,
		"attempts", len(summary.Attempts),
		"escalated_by", summary.EscalatedBy,
	}
	if summary.Winner != nil {
		attrs = append(attrs,
			"winner_backend", summary.Winner.Backend,
			"winner_model", summary.Winner.Model,
			"winner_effort", summary.Winner.Effort,
		)
	} else {
		attrs = append(attrs, "outcome", "exhausted")
	}
	slog.InfoContext(ctx, "agent.ladder.complete", attrs...)
	return res, err
}

func ladderConfigFromArgs(args map[string]any) (LadderConfig, bool, error) {
	if args == nil {
		return LadderConfig{}, false, nil
	}
	raw, ok := args["harness_ladder"]
	if !ok || raw == nil {
		return LadderConfig{}, false, nil
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) == "" {
		return LadderConfig{}, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return LadderConfig{}, true, fmt.Errorf("must be a mapping")
	}
	modelsRaw, ok := m["models"]
	if !ok {
		return LadderConfig{}, true, fmt.Errorf("models is required")
	}
	modelsList, ok := modelsRaw.([]any)
	if !ok || len(modelsList) == 0 {
		return LadderConfig{}, true, fmt.Errorf("models must be a non-empty list")
	}
	models := make([]LadderModel, 0, len(modelsList))
	for i, rawModel := range modelsList {
		mm, ok := rawModel.(map[string]any)
		if !ok {
			return LadderConfig{}, true, fmt.Errorf("models[%d] must be a mapping", i)
		}
		model := strings.TrimSpace(anyString(mm["model"]))
		if model == "" {
			return LadderConfig{}, true, fmt.Errorf("models[%d].model is required", i)
		}
		backend := strings.TrimSpace(anyString(mm["backend"]))
		if _, ok := ResolveAgentBackendName(backend); backend != "" && !ok {
			return LadderConfig{}, true, fmt.Errorf("models[%d].backend %q is invalid", i, backend)
		}
		models = append(models, LadderModel{
			Backend:  backend,
			Provider: strings.TrimSpace(anyString(mm["provider"])),
			Model:    model,
		})
	}
	efforts, err := ladderStringList(m["efforts"])
	if err != nil {
		return LadderConfig{}, true, err
	}
	for _, e := range efforts {
		if e != "low" && e != "medium" && e != "high" && e != "xhigh" && e != "max" {
			return LadderConfig{}, true, fmt.Errorf("efforts contains invalid value %q", e)
		}
	}
	maxAttempts, err := ladderInt(m["max_attempts"])
	if err != nil {
		return LadderConfig{}, true, err
	}
	if maxAttempts < 0 {
		return LadderConfig{}, true, fmt.Errorf("max_attempts must not be negative")
	}
	cfg := LadderConfig{
		Models:      models,
		Efforts:     efforts,
		MaxAttempts: maxAttempts,
		Backoff:     strings.TrimSpace(anyString(m["backoff"])),
		StatePath:   strings.TrimSpace(anyString(m["state_path"])),
	}
	return cfg, true, nil
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func ladderStringList(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("efforts must be a list")
	}
	out := make([]string, 0, len(raw))
	for i, item := range raw {
		s := strings.TrimSpace(anyString(item))
		if s == "" {
			return nil, fmt.Errorf("efforts[%d] must be a non-empty string", i)
		}
		out = append(out, s)
	}
	return out, nil
}

func ladderInt(v any) (int, error) {
	if v == nil {
		return 0, nil
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("max_attempts must be an integer")
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("max_attempts must be an integer")
	}
}

func emitLadderFallbackNotice(ctx context.Context, rung LadderRung, kind FailureKind, errText string) {
	text := fmt.Sprintf("Kitsoki harness: %s failed (%s); ", formatLadderRung(rung), kind)
	if kind == FailureInfra {
		text += "backing off this provider/harness and skipping its remaining effort/model ladder."
	} else {
		text += "trying the next effort/model rung."
	}
	if errText != "" {
		text += " Error: " + errText
	}
	slog.WarnContext(ctx, "agent.ladder.fallback",
		"backend", rung.Backend,
		"provider", rung.Provider,
		"model", rung.Model,
		"effort", rung.Effort,
		"failure_kind", string(kind),
		"error", errText,
	)
	appendAgentNoticeEvent(ctx, time.Now(), storeAgentNotice{
		Type:     "ladder_fallback",
		Subtype:  string(kind),
		Severity: "warn",
		Text:     text,
		Backend:  rung.Backend,
		Provider: rung.Provider,
		Model:    rung.Model,
		Effort:   rung.Effort,
		Error:    errText,
	})
}

func emitLadderSessionPinnedNotice(ctx context.Context, rung LadderRung) {
	text := "Kitsoki harness: session fallback is now " + formatLadderRung(rung) + ". " +
		"Kitsoki will keep using this fallback for this session until you change /provider, /model, or /effort."
	slog.WarnContext(ctx, "agent.ladder.session_pinned",
		"backend", rung.Backend,
		"provider", rung.Provider,
		"model", rung.Model,
		"effort", rung.Effort,
	)
	appendAgentNoticeEvent(ctx, time.Now(), storeAgentNotice{
		Type:     "ladder_session_pinned",
		Severity: "warn",
		Text:     text,
		Backend:  rung.Backend,
		Provider: rung.Provider,
		Model:    rung.Model,
		Effort:   rung.Effort,
	})
}

func emitLadderAttemptNotice(ctx context.Context, rung LadderRung) {
	text := "Kitsoki harness: trying " + formatLadderRung(rung) + "."
	slog.InfoContext(ctx, "agent.ladder.attempt",
		"backend", rung.Backend,
		"provider", rung.Provider,
		"model", rung.Model,
		"effort", rung.Effort,
		"escalated_by", rung.EscalatedBy,
	)
	appendAgentNoticeEvent(ctx, time.Now(), storeAgentNotice{
		Type:     "ladder_attempt",
		Text:     text,
		Backend:  rung.Backend,
		Provider: rung.Provider,
		Model:    rung.Model,
		Effort:   rung.Effort,
	})
}

func emitLadderSuccessNotice(ctx context.Context, rung LadderRung) {
	text := "Kitsoki harness: " + formatLadderRung(rung) + " succeeded."
	slog.InfoContext(ctx, "agent.ladder.success",
		"backend", rung.Backend,
		"provider", rung.Provider,
		"model", rung.Model,
		"effort", rung.Effort,
		"escalated_by", rung.EscalatedBy,
	)
	appendAgentNoticeEvent(ctx, time.Now(), storeAgentNotice{
		Type:     "ladder_success",
		Text:     text,
		Backend:  rung.Backend,
		Provider: rung.Provider,
		Model:    rung.Model,
		Effort:   rung.Effort,
	})
}

func emitLadderStopNotice(ctx context.Context, rung LadderRung, kind FailureKind, errText string) {
	text := fmt.Sprintf("Kitsoki harness: stopped after %s failed (%s).", formatLadderRung(rung), kind)
	if errText != "" {
		text += " Error: " + errText
	}
	slog.WarnContext(ctx, "agent.ladder.stop",
		"backend", rung.Backend,
		"provider", rung.Provider,
		"model", rung.Model,
		"effort", rung.Effort,
		"failure_kind", string(kind),
		"error", errText,
	)
	appendAgentNoticeEvent(ctx, time.Now(), storeAgentNotice{
		Type:     "ladder_stop",
		Subtype:  string(kind),
		Severity: "warn",
		Text:     text,
		Backend:  rung.Backend,
		Provider: rung.Provider,
		Model:    rung.Model,
		Effort:   rung.Effort,
		Error:    errText,
	})
}

func emitLadderExhaustedNotice(ctx context.Context, summary LadderSummary, errText string) {
	notice := storeAgentNotice{
		Type:     "ladder_exhausted",
		Severity: "warn",
		Text:     fmt.Sprintf("Kitsoki harness: ladder exhausted after %d attempt(s).", len(summary.Attempts)),
	}
	if len(summary.Attempts) > 0 {
		last := summary.Attempts[len(summary.Attempts)-1]
		notice.Subtype = string(last.Failure)
		notice.Backend = last.Rung.Backend
		notice.Provider = last.Rung.Provider
		notice.Model = last.Rung.Model
		notice.Effort = last.Rung.Effort
		notice.Error = errText
	}
	if errText != "" {
		notice.Text += " Last error: " + errText
	}
	slog.WarnContext(ctx, "agent.ladder.exhausted",
		"attempts", len(summary.Attempts),
		"error", errText,
	)
	appendAgentNoticeEvent(ctx, time.Now(), notice)
}

func formatLadderRung(rung LadderRung) string {
	provider := strings.TrimSpace(rung.Provider)
	if provider == "" {
		provider = "ambient"
	}
	model := strings.TrimSpace(rung.Model)
	if model == "" {
		model = "default model"
	}
	backend := strings.TrimSpace(rung.Backend)
	if backend == "" {
		backend = "claude"
	}
	label := provider + " / " + model + " via " + backend
	if strings.TrimSpace(rung.Effort) != "" {
		label += " (effort " + rung.Effort + ")"
	}
	return label
}

// ── failure classification ──────────────────────────────────────────────

// classifyLadderOutcome maps one attempt's (Result, error) to a FailureKind
// and the associated error text. A non-nil Go error is always infra (the
// Result-vs-error split documented on host.Result: "infra failures are
// returned as Go errors instead"). Otherwise an empty Result.Error is
// success; a non-empty one prefers the caller's explicit
// Result.FailureKind (host.agent.decide / host.agent.task populate this at
// their infra- and capability-exhaustion return sites) and falls back to a
// best-effort text heuristic (looksInfraError) only when the caller left it
// unclassified.
func classifyLadderOutcome(res Result, err error) (FailureKind, string) {
	if err != nil {
		return FailureInfra, err.Error()
	}
	if res.Error == "" {
		return FailureNone, ""
	}
	switch res.FailureKind {
	case FailureInfra, FailureCapability, FailureFatal:
		return res.FailureKind, res.Error
	}
	if looksInfraError(res.Error) {
		return FailureInfra, res.Error
	}
	return FailureCapability, res.Error
}

// isContextDone reports whether err is (or wraps) context.Canceled /
// context.DeadlineExceeded — a signal to stop the ladder walk immediately
// rather than burn through every remaining rung against a dead context.
func isContextDone(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}

// looksInfraError is the best-effort fallback text heuristic used only when a
// handler left Result.FailureKind unclassified. Extends quota_control.go's
// looksRateLimited (429/quota/rate-limit) with the rest of the enumerated
// infra modes: timeouts, model_not_found, connection errors, and 5xx-shaped
// server failures.
func looksInfraError(s string) bool {
	if looksRateLimited(s) {
		return true
	}
	ls := strings.ToLower(s)
	if strings.Contains(ls, "without successful submit") && strings.Contains(ls, "0 attempt(s)") {
		return true
	}
	for _, sig := range []string{
		"timeout", "timed out", "deadline exceeded",
		"model_not_found", "model not found",
		"connection refused", "connection reset", "no such host",
		"broken pipe", "eof",
		"service unavailable", "internal server error",
		"bad gateway", "gateway timeout",
		" 500", " 502", " 503", " 504",
		"claude exec failed", // agent_task.go's cr.Infra wrapper message
		"binary not found", "cli not found", "command not found",
	} {
		if strings.Contains(ls, sig) {
			return true
		}
	}
	return false
}

// ── backoff bookkeeping (shared substrate with quota_control.go) ─────────

// ladderBackoffKey builds the persistent backoff key for a provider/harness
// lane. A provider/transport failure (429, quota, broken CLI, 5xx, timeout)
// is not a signal to try a stronger model on the same lane, so the key
// deliberately excludes Model. Distinct namespace ("ladder|") from
// providerQuotaKey's ActiveProfile-keyed entries so the two features never
// collide in the shared state file.
func ladderBackoffKey(m LadderModel) string {
	return "ladder|" + m.Backend + "|" + m.Provider
}

// ladderInBackoff reports whether the named ladder provider/harness lane is
// currently inside an infra-failure backoff window, per the shared on-disk
// provider-quota state file (quota_control.go). A read failure (e.g. the
// state file doesn't exist yet) is treated as "not in backoff" — the lane is
// attempted and, if it fails again, marked.
func ladderInBackoff(statePath, key string) bool {
	st, err := readQuotaState(statePath)
	if err != nil {
		return false
	}
	p := st.Profiles[key]
	if p == nil {
		return false
	}
	return time.Now().Before(p.BackoffUntil)
}

// markLadderBackoff records an infra failure against the named provider/harness
// lane, backing it off for dur. Reuses quotaLimiter.withState so the write is
// file-locked against concurrent writers (other sessions/processes sharing the
// same state file) exactly like the provider-quota path.
func markLadderBackoff(statePath, key string, dur time.Duration) {
	lim := &quotaLimiter{statePath: statePath}
	now := time.Now()
	_ = lim.withState(func(st *quotaStateFile) error {
		p := st.profile(key)
		p.LastRateLimitedAt = now
		p.BackoffUntil = now.Add(dur)
		return nil
	})
}
