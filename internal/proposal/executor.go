// Package proposal — synchronous execute executor.
package proposal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

// ExecuteResult is returned by Execute after a synchronous host invocation.
type ExecuteResult struct {
	// Proposal is the updated proposal after execution.
	Proposal *Proposal
	// HostResult is the raw result from the host handler.
	HostResult host.Result
	// Err is non-nil on infra failure (distinct from expected domain errors).
	Err error
}

// Execute runs the host handler for a proposal synchronously.
// It updates the proposal's status and result in place and returns the updated proposal.
// On host domain error (Result.Error != ""), the proposal transitions to StatusFailed.
// On infra error (err != nil), the proposal transitions to StatusFailed and returns the error.
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

// resolveProposalArgs evaluates the with: map, substituting {{p.X}} with
// the current proposal draft field X. This is the template shorthand from §3.1.
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
