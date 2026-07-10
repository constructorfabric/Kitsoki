// Package host — the pre-dispatch budget / early-escalation gate
// (dispatch-context-floor proposal, task 1.4).
//
// Every host.agent.{decide,task} call already runs through the shared
// runAgentVerbWithLadder wrap point (ladder.go). This file adds one more
// step to that wrap, BEFORE any rung is dispatched: deterministically
// estimate how many tokens the call is about to send, compare that estimate
// against a per-verb (overridable per-agent) budget, and decide whether to
// proceed, escalate the ladder's starting rung, or refuse the call outright
// — all before a single claude subprocess is spawned, so an oversized call
// never gets to the point of being metered and billed.
//
// # Estimation
//
// estimateDispatchTokens is a deterministic, LLM-free heuristic: it
// marshals the call's args map to JSON (which — for every verb — already
// carries the bulk of what actually reaches the model: prompt/prompt_path
// text, task context maps, acceptance blocks, etc.) plus the resolved
// agent's SystemPrompt (the task-layer system-prompt body), and divides the
// total byte count by a fixed chars-per-token ratio. This is intentionally
// an approximation, not the exact count the claude CLI will report — see
// task 1.2's CacheUsage / Meta.usage for the after-the-fact real numbers.
// The point of this gate is a cheap, deterministic, pre-flight guess, not a
// precise budget.
//
// # Fail-closed config
//
// A per-agent TokenBudget override (Agent.TokenBudget) that is declared but
// invalid (WarnTokens <= 0, or RefuseTokens < WarnTokens) makes the gate
// refuse EVERY dispatch through that agent, regardless of the estimate —
// "missing/invalid budget config refuses rather than silently disabling."
// The built-in per-verb defaults (defaultVerbBudgets) are always valid, so
// the only way to hit this path is an author-declared override that doesn't
// pass validation (also caught earlier, at story load time — see
// internal/app/loader.go's agent token_budget: check — this is the runtime
// safety net, mirroring the Bash-profile double-check pattern already used
// for decide/ask).
//
// # Shipped defaults are generous
//
// defaultVerbBudgets ships thresholds far above any observed real call size
// (the token-bloat finding's worst single call was ~1.86M tokens across a
// whole 10-call pipeline run, not any one call) so that no existing story
// starts refusing or escalating calls on rollout — the gate is present and
// recording decisions from day one, but "effectively off" until an operator
// or story author tightens it deliberately.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kitsoki/internal/store"
)

// estimateCharsPerToken is the deterministic chars-per-token ratio used by
// estimateDispatchTokens. 4 is the commonly cited rule of thumb for English
// text / JSON-ish payloads; it need not be exact — the gate only needs a
// monotonic, reproducible proxy for "how big is this call", not a precise
// token count (the claude CLI's own usage report, surfaced by task 1.2,
// remains the source of truth for what a call actually cost).
const estimateCharsPerToken = 4

// BudgetThresholds is the pre-dispatch budget-gate configuration for one
// verb (or one agent's override of it): the estimated-token levels at which
// the gate escalates the ladder's starting rung (WarnTokens) or refuses the
// call outright (RefuseTokens). WarnTokens must be positive and RefuseTokens
// must be >= WarnTokens for the thresholds to be valid — see valid().
type BudgetThresholds struct {
	WarnTokens   int64
	RefuseTokens int64
}

// valid reports whether b is a well-formed threshold pair: a positive warn
// level at or below the refuse level. An agent-declared override that fails
// this check makes the gate refuse closed (see resolveBudgetThresholds)
// rather than silently falling back to the per-verb default — a
// story author who declared a budget gets exactly the enforcement they
// asked for, or a loud failure, never a silent no-op.
func (b BudgetThresholds) valid() bool {
	return b.WarnTokens > 0 && b.RefuseTokens >= b.WarnTokens
}

// defaultVerbBudgets are the shipped per-verb defaults: deliberately
// generous (see package doc) so no existing story starts refusing or
// escalating calls on rollout. Every entry here is valid() by construction;
// TestDefaultVerbBudgets_AreValid locks that invariant.
var defaultVerbBudgets = map[string]BudgetThresholds{
	"task":     {WarnTokens: 300_000, RefuseTokens: 1_000_000},
	"decide":   {WarnTokens: 300_000, RefuseTokens: 1_000_000},
	"ask":      {WarnTokens: 300_000, RefuseTokens: 1_000_000},
	"converse": {WarnTokens: 300_000, RefuseTokens: 1_000_000},
}

// defaultBudgetFallback applies to any verb not enumerated in
// defaultVerbBudgets (forward-compatible with a future host.agent.* verb
// that starts routing through runAgentVerbWithLadder without an explicit
// entry here).
var defaultBudgetFallback = BudgetThresholds{WarnTokens: 300_000, RefuseTokens: 1_000_000}

// resolveBudgetThresholds resolves the effective BudgetThresholds for one
// call: the agent's declared override when present (fail-closed if
// invalid), else the verb's default, else the generic fallback.
func resolveBudgetThresholds(verb string, agent Agent) (BudgetThresholds, error) {
	if agent.TokenBudget != nil {
		b := *agent.TokenBudget
		if !b.valid() {
			return BudgetThresholds{}, fmt.Errorf(
				"agent token_budget must set warn_tokens > 0 and refuse_tokens >= warn_tokens (got warn_tokens=%d refuse_tokens=%d)",
				b.WarnTokens, b.RefuseTokens)
		}
		return b, nil
	}
	if b, ok := defaultVerbBudgets[verb]; ok {
		return b, nil
	}
	return defaultBudgetFallback, nil
}

// estimateDispatchTokens deterministically approximates how many tokens this
// call is about to send: the JSON-marshaled size of the call's args (which
// carries the prompt/prompt_path/context content for every verb — decide's
// `prompt`, task's `context` map, etc. — without needing verb-specific
// field knowledge) plus the resolved agent's SystemPrompt (the task-layer
// system-prompt body composed into every dispatch). A marshal failure (args
// containing something unencodable — practically never for a host call's
// args map) degrades to just the system-prompt length rather than erroring
// the gate itself.
func estimateDispatchTokens(args map[string]any, agent Agent) int64 {
	var chars int64
	if b, err := json.Marshal(args); err == nil {
		chars += int64(len(b))
	}
	chars += int64(len(agent.SystemPrompt))
	return chars / estimateCharsPerToken
}

// budgetOutcome is the decision recorded by the budget gate.
type budgetOutcome string

const (
	budgetProceed  budgetOutcome = "proceed"
	budgetEscalate budgetOutcome = "escalate"
	budgetRefuse   budgetOutcome = "refuse"
)

// budgetDecision is the full outcome of one checkDispatchBudget call.
type budgetDecision struct {
	Verb            string
	EstimatedTokens int64
	Budget          BudgetThresholds
	Outcome         budgetOutcome
	Reason          string
	// Rung, when non-nil, previews the ladder rung the walk will actually
	// start from after an escalate decision (nil when no ladder is
	// installed, or the decision was proceed/refuse).
	Rung *LadderRung
}

// refuseResult builds the terminal Result for a refuse decision — the call
// never reaches a claude subprocess. FailureFatal: an oversized call (or a
// broken budget config) is not something any ladder rung can fix by
// retrying with a different model/effort, exactly like a missing required
// arg (see FailureFatal's doc).
func (d budgetDecision) refuseResult() Result {
	return Result{
		Error: fmt.Sprintf("host.agent.%s: dispatch budget gate refused before dispatch — %s (estimated_tokens=%d warn_tokens=%d refuse_tokens=%d)",
			d.Verb, d.Reason, d.EstimatedTokens, d.Budget.WarnTokens, d.Budget.RefuseTokens),
		FailureKind: FailureFatal,
	}
}

// checkDispatchBudget resolves the effective budget, estimates this call's
// token footprint, decides proceed/escalate/refuse, records the decision as
// an agent.dispatch.budget_checked trace event, and returns the decision for
// runAgentVerbWithLadder to act on. ctx is expected to already carry any
// ladder installed for this call (runAgentVerbWithLadder calls this AFTER
// merging harness_ladder: args onto ctx) so an escalate decision can preview
// the rung the walk would start from.
func checkDispatchBudget(ctx context.Context, args map[string]any, verb string) budgetDecision {
	agent, _ := resolveAgent(ctx, args)
	budget, cfgErr := resolveBudgetThresholds(verb, agent)
	estimated := estimateDispatchTokens(args, agent)

	dec := budgetDecision{Verb: verb, EstimatedTokens: estimated, Budget: budget}

	if cfgErr != nil {
		dec.Outcome = budgetRefuse
		dec.Reason = cfgErr.Error()
		appendAgentBudgetCheckedEvent(ctx, time.Now(), dec)
		return dec
	}

	switch {
	case estimated > budget.RefuseTokens:
		dec.Outcome = budgetRefuse
		dec.Reason = fmt.Sprintf("estimated %d tokens exceeds refuse_tokens threshold %d", estimated, budget.RefuseTokens)
	case estimated > budget.WarnTokens:
		dec.Outcome = budgetEscalate
		dec.Reason = fmt.Sprintf("estimated %d tokens exceeds warn_tokens threshold %d", estimated, budget.WarnTokens)
		if cfg, ok := HarnessLadderFromContext(ctx); ok && cfg.Enabled() {
			if preview := previewEscalatedRung(cfg); preview != nil {
				dec.Rung = preview
			}
		}
	default:
		dec.Outcome = budgetProceed
	}

	appendAgentBudgetCheckedEvent(ctx, time.Now(), dec)
	return dec
}

// previewEscalatedRung reports the first rung escalateLadderStart's mutated
// config would dispatch, for the trace event — without mutating cfg itself
// (that mutation is applied separately by escalateLadderStart, only once the
// caller has decided to actually escalate).
func previewEscalatedRung(cfg LadderConfig) *LadderRung {
	escalated := escalatedLadderConfig(cfg)
	rungs := ExpandLadder(escalated)
	if len(rungs) == 0 {
		return nil
	}
	r := rungs[0]
	return &r
}

// escalatedLadderConfig returns cfg with its cheapest effort tier dropped —
// the concrete meaning of "escalate rung" for a call the budget gate flagged
// as large-but-not-refused: skip straight past the lowest effort so the
// ladder's first attempt is already one step up, rather than burning an
// attempt on a rung unlikely to handle the oversized context well. A
// single-effort (or empty) ladder has nothing to skip and is returned
// unchanged.
func escalatedLadderConfig(cfg LadderConfig) LadderConfig {
	efforts := cfg.efforts()
	if len(efforts) <= 1 {
		return cfg
	}
	cfg.Efforts = append([]string(nil), efforts[1:]...)
	return cfg
}

// escalateLadderStart applies escalatedLadderConfig to the ladder installed
// on ctx, if any, and reinstalls it. No-op (returns ctx unchanged) when no
// ladder is installed or it has nothing to skip — a call flagged "escalate"
// with no configured ladder simply proceeds at its normal (single) rung,
// consistent with the generous-defaults invariant: nothing here changes
// dispatch behavior for a story that never opted into harness_ladder:.
func escalateLadderStart(ctx context.Context) context.Context {
	cfg, ok := HarnessLadderFromContext(ctx)
	if !ok || !cfg.Enabled() {
		return ctx
	}
	return WithHarnessLadder(ctx, escalatedLadderConfig(cfg))
}

// storeAgentBudgetChecked is the JSON payload written to the
// agent.dispatch.budget_checked trace event.
type storeAgentBudgetChecked struct {
	Verb            string             `json:"verb"`
	EstimatedTokens int64              `json:"estimated_tokens"`
	WarnTokens      int64              `json:"budget_warn_tokens"`
	RefuseTokens    int64              `json:"budget_refuse_tokens"`
	Decision        string             `json:"decision"`
	Reason          string             `json:"reason,omitempty"`
	Rung            *budgetRungSummary `json:"rung,omitempty"`
}

// budgetRungSummary is the compact rung preview folded into
// storeAgentBudgetChecked.Rung on an escalate decision.
type budgetRungSummary struct {
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
}

// appendAgentBudgetCheckedEvent records dec as an agent.dispatch.budget_checked
// trace event. No-op when no EventSink is installed on ctx (e.g. a direct
// unit-test call with a bare context), mirroring appendAgentNoticeEvent.
func appendAgentBudgetCheckedEvent(ctx context.Context, ts time.Time, dec budgetDecision) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	payload := storeAgentBudgetChecked{
		Verb:            dec.Verb,
		EstimatedTokens: dec.EstimatedTokens,
		WarnTokens:      dec.Budget.WarnTokens,
		RefuseTokens:    dec.Budget.RefuseTokens,
		Decision:        string(dec.Outcome),
		Reason:          dec.Reason,
	}
	if dec.Rung != nil {
		payload.Rung = &budgetRungSummary{
			Backend: dec.Rung.Backend,
			Model:   dec.Rung.Model,
			Effort:  dec.Rung.Effort,
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        ts,
		Kind:      store.AgentDispatchBudgetChecked,
		StatePath: oc.StatePath,
		CallID:    CallIDFrom(ctx),
		Payload:   raw,
	})
}
