package proposal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

// ExecuteResult bundles the three things a caller needs after [Execute]: the
// mutated [Proposal] (also mutated in place — the field is for convenience),
// the raw [host.Result] for views that render handler data, and Err. Err is
// reserved for INFRA failures (the handler could not run); an expected
// host-domain failure is NOT an Err but shows up as Proposal in
// [StatusFailed] with a non-OK [Result], so callers can tell "the action
// failed" from "we could not attempt the action."
type ExecuteResult struct {
	// Proposal is the updated proposal after execution.
	Proposal *Proposal
	// HostResult is the raw result from the host handler.
	HostResult host.Result
	// Err is non-nil on infra failure (distinct from expected domain errors).
	Err error
}

// Execute runs a proposal's host invocation synchronously, recording the
// outcome on the proposal in place. It transitions to [StatusExecuting],
// resolves the kind's `with:` args (substituting {{p.X}} from the current
// draft), invokes the named handler, then records a [Result] and transitions
// to [StatusDone] or [StatusFailed]. The two failure shapes are kept distinct
// (see [ExecuteResult]): an infra error from the registry sets [StatusFailed]
// AND returns via Err; a host-domain error ([host.Result.Error] non-empty)
// sets [StatusFailed] but leaves Err nil. A kind with no execute block is an
// infra error. The proposal is mutated in place and must not be shared across
// goroutines during the call.
func Execute(ctx context.Context, p *Proposal, kind *app.ProposalKind, registry *host.Registry) ExecuteResult {
	if kind == nil || kind.Execute == nil {
		return ExecuteResult{
			Proposal: p,
			Err:      fmt.Errorf("proposal: kind %q has no execute block", p.Kind),
		}
	}

	p.Transition(StatusExecuting)
	startedAt := time.Now().UTC().Format(time.RFC3339)

	// Resolve the with: args, substituting {{p.X}} with $proposal.current.X.
	resolvedArgs := resolveProposalArgs(kind.Execute.With, p)

	hostResult, err := registry.Invoke(ctx, kind.Execute.Invoke, resolvedArgs)
	finishedAt := time.Now().UTC().Format(time.RFC3339)

	if err != nil {
		// Infra error.
		p.SetResult(Result{
			OK:         false,
			Error:      err.Error(),
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		})
		p.Transition(StatusFailed)
		return ExecuteResult{Proposal: p, Err: err}
	}

	if hostResult.Error != "" {
		// Domain / expected error.
		p.SetResult(Result{
			OK:         false,
			Error:      hostResult.Error,
			Data:       hostResult.Data,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
		})
		p.Transition(StatusFailed)
		return ExecuteResult{Proposal: p, HostResult: hostResult}
	}

	// Success.
	p.SetResult(Result{
		OK:         true,
		Data:       hostResult.Data,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	})
	p.Transition(StatusDone)
	return ExecuteResult{Proposal: p, HostResult: hostResult}
}

// resolveProposalArgs evaluates the kind's with: map, substituting {{p.X}}
// with the current proposal draft field X — the template shorthand the
// execute block uses to thread accepted draft values into host args (see the
// ProposalKind execute block in docs/embedded/app-schema.md).
func resolveProposalArgs(with map[string]any, p *Proposal) map[string]any {
	if len(with) == 0 {
		return nil
	}
	out := make(map[string]any, len(with))
	for k, v := range with {
		out[k] = resolveProposalValue(v, p)
	}
	return out
}

// resolveProposalValue substitutes {{p.X}} references in a single value.
func resolveProposalValue(v any, p *Proposal) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	if !strings.Contains(s, "{{") {
		return s
	}
	// Simple single-token substitution: {{p.fieldname}}.
	result := s
	for field, val := range p.Current {
		placeholder := "{{p." + field + "}}"
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", val))
	}
	return result
}
