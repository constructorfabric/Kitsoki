// Package codeact implements a bounded "code-act" agent loop: instead of
// choosing from a fixed tool menu, the agent emits a Starlark snippet (the
// same main(ctx) contract as host.starlark.run) each step, observes its
// result, and eventually emits done(payload) to finish. This is the s1
// executor core — it reuses internal/host/starlark's Run for evaluation, so
// each snippet sees the same capability-scoped ctx surface as host.starlark.run.
package codeact

import (
	"context"
	"fmt"

	kstarlark "kitsoki/internal/host/starlark"
)

// Terminated reason constants for Result.Terminated.
const (
	// TerminatedDone means the agent emitted done() with a payload that
	// passed Params.Schema.
	TerminatedDone = "done"
	// TerminatedBudgetExhausted means the step budget ran out before the
	// agent produced a schema-valid done().
	TerminatedBudgetExhausted = "budget_exhausted"
)

// ErrorEnvelope is the structured, agent-facing description of a failure from
// the previous step — either a Starlark runtime/domain error from executing
// the emitted snippet, or a schema-validation rejection of a done() payload.
// It is fed back into Agent.Next as errEnv on the following call so the agent
// can self-debug instead of aborting the loop.
type ErrorEnvelope struct {
	// Message is a human-readable description of what went wrong. Callers
	// (and the agent) can pattern-match against it, so it should name the
	// offending field/attribute where possible.
	Message string
	// Step is the index (0-based) of the step that produced this error.
	Step int
}

// Emission is what the Agent produces for a given step: either a Starlark
// snippet to execute, or a done() call carrying the final payload.
type Emission struct {
	// Snippet is a Starlark source defining def main(ctx): ... — evaluated
	// exactly like a host.starlark.run script. Ignored when Done is true.
	Snippet string
	// Done, when true, ends the loop (pending schema validation of Payload).
	Done bool
	// Payload is the candidate final result, validated by Params.Schema when
	// Done is true.
	Payload map[string]any
}

// Agent is the LLM stand-in interface. Next is called once per step with the
// step index, the previous step's observation (the snippet's returned dict,
// or nil on the first step / after an error), and the structured error
// envelope from the previous step (nil if the previous step succeeded).
type Agent interface {
	Next(ctx context.Context, step int, obs map[string]any, errEnv *ErrorEnvelope) (Emission, error)
}

// StepResult journals one iteration of the loop.
type StepResult struct {
	// Emission is what the agent produced this step.
	Emission Emission
	// Observation is the snippet's returned dict on success (nil on error or
	// when the step was a done() attempt).
	Observation map[string]any
	// Err is set when the snippet failed to execute, or the done() payload
	// failed schema validation.
	Err *ErrorEnvelope
}

// Result is the outcome of a full Run.
type Result struct {
	// Steps journals every iteration executed, in order. Its length is at
	// most Params.Budget, and exactly Params.Budget when the loop terminated
	// by budget exhaustion.
	Steps []StepResult
	// Terminated names why the loop stopped: TerminatedDone or
	// TerminatedBudgetExhausted.
	Terminated string
	// Payload is the schema-valid done() payload, set only when Terminated
	// == TerminatedDone.
	Payload map[string]any
	// Trace is the stable, serializable projection of Steps — one TraceStep
	// per executed iteration, in order — that tracing/journaling and
	// cassette-recording tooling is built against (see trace.go).
	Trace []TraceStep
}

// Params configures a Run.
type Params struct {
	// Budget is the maximum number of steps (agent turns) the loop will run
	// before giving up with TerminatedBudgetExhausted. Must be >= 1.
	Budget int
	// World is the read-only world snapshot exposed to every snippet via
	// ctx.world.get, exactly as in host.starlark.run.
	World map[string]any
	// Capabilities is the shared Starlark capability authority for every
	// snippet this run executes.
	Capabilities kstarlark.CapabilitySpec
	// Agent is the LLM stand-in driving the loop.
	Agent Agent
	// Schema validates a candidate done() payload. A non-nil error rejects
	// the payload and is fed back to the agent as the next step's
	// ErrorEnvelope rather than terminating the loop.
	Schema func(payload map[string]any) error
}

// scriptLabel is the fixed label used for every snippet's Starlark filename;
// snippets have no on-disk path, so a stable placeholder keeps error messages
// consistent without implying a real file exists.
const scriptLabel = "<codeact-snippet>"

// Run drives the bounded code-act loop: ask the agent for the next emission,
// execute a snippet (or validate a done() payload against Schema), journal
// the step, and feed the resulting observation or structured error back into
// the next agent call. It terminates when the agent produces a schema-valid
// done() or when Budget steps have been consumed, whichever comes first.
func Run(ctx context.Context, p Params) (*Result, error) {
	res := &Result{Steps: make([]StepResult, 0, p.Budget)}

	var obs map[string]any
	var errEnv *ErrorEnvelope

	for step := 0; step < p.Budget; step++ {
		emission, err := p.Agent.Next(ctx, step, obs, errEnv)
		if err != nil {
			return nil, fmt.Errorf("codeact: agent.Next at step %d: %w", step, err)
		}

		if emission.Done {
			if schemaErr := validateSchema(p.Schema, emission.Payload); schemaErr != nil {
				envelope := &ErrorEnvelope{
					Message: fmt.Sprintf("done() payload rejected: %v", schemaErr),
					Step:    step,
				}
				sr := StepResult{Emission: emission, Err: envelope}
				res.Steps = append(res.Steps, sr)
				res.Trace = append(res.Trace, traceStepFromResult(step, sr))
				obs = nil
				errEnv = envelope
				continue
			}

			sr := StepResult{Emission: emission}
			res.Steps = append(res.Steps, sr)
			res.Trace = append(res.Trace, traceStepFromResult(step, sr))
			res.Terminated = TerminatedDone
			res.Payload = emission.Payload
			return res, nil
		}

		out, runErr := runSnippet(ctx, emission.Snippet, p.World, p.Capabilities)
		if runErr != nil {
			envelope := &ErrorEnvelope{
				Message: runErr.Error(),
				Step:    step,
			}
			sr := StepResult{Emission: emission, Err: envelope}
			res.Steps = append(res.Steps, sr)
			res.Trace = append(res.Trace, traceStepFromResult(step, sr))
			obs = nil
			errEnv = envelope
			continue
		}

		sr := StepResult{Emission: emission, Observation: out}
		res.Steps = append(res.Steps, sr)
		res.Trace = append(res.Trace, traceStepFromResult(step, sr))
		obs = out
		errEnv = nil
	}

	res.Terminated = TerminatedBudgetExhausted
	return res, nil
}

// validateSchema runs Schema over payload, tolerating a nil Schema (treated
// as always-valid) so callers that don't need done() validation can omit it.
func validateSchema(schema func(map[string]any) error, payload map[string]any) error {
	if schema == nil {
		return nil
	}
	return schema(payload)
}

// runSnippet executes one agent-emitted Starlark snippet via the shared
// host.starlark.run evaluator, converting a *starlark.DomainError into the
// plain error runSnippet returns (Run wraps it into an ErrorEnvelope). A
// non-domain error is a genuine infra failure and is returned as-is.
func runSnippet(ctx context.Context, snippet string, world map[string]any, capabilities kstarlark.CapabilitySpec) (map[string]any, error) {
	result, err := kstarlark.Run(ctx, kstarlark.Params{
		Script:       scriptLabel,
		Source:       []byte(snippet),
		World:        world,
		Capabilities: capabilities,
	})
	if err != nil {
		if msg, ok := kstarlark.AsDomainError(err); ok {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return result.Outputs, nil
}
