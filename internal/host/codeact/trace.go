package codeact

import (
	"context"
	"fmt"
)

// TraceStep is the stable, JSON-serializable projection of one loop
// iteration. It carries the same information as StepResult but flattened
// into plain fields so it can be journaled (e.g. into the event log) and
// round-tripped through JSON without needing StepResult's *ErrorEnvelope
// pointer or the Emission wrapper.
type TraceStep struct {
	// Step is the 0-based index of this iteration.
	Step int
	// Snippet is the Starlark snippet the agent emitted this step (empty on
	// a done() step).
	Snippet string
	// Done is true when this step was a done() call.
	Done bool
	// Payload is the candidate done() payload, set only when Done is true.
	Payload map[string]any
	// Observation is the snippet's returned dict on success (nil on error or
	// on a done() step).
	Observation map[string]any
	// Error is the human-readable error message from a failed snippet
	// execution or a rejected done() payload, empty on a successful step.
	Error string
}

// traceStepFromResult projects a StepResult (as journaled by Run) into its
// stable TraceStep form.
func traceStepFromResult(step int, sr StepResult) TraceStep {
	ts := TraceStep{
		Step:        step,
		Snippet:     sr.Emission.Snippet,
		Done:        sr.Emission.Done,
		Payload:     sr.Emission.Payload,
		Observation: sr.Observation,
	}
	if sr.Err != nil {
		ts.Error = sr.Err.Message
	}
	return ts
}

// CassetteStep is one recorded step in a Cassette: the exact Emission the
// agent produced, keyed by step index so ReplayAgent can serve it back
// verbatim without re-deriving anything from Observation/Error.
type CassetteStep struct {
	// Step is the 0-based index this recording corresponds to.
	Step int
	// Snippet is the snippet to replay verbatim for this step.
	Snippet string
	// Done mirrors TraceStep.Done: whether this step recorded a done() call.
	Done bool
	// Payload mirrors TraceStep.Payload: the done() payload to replay when
	// Done is true.
	Payload map[string]any
}

// Cassette is a minimal, JSON-serializable recording of a codeact
// trajectory, analogous to internal/testrunner's CassetteEpisode and
// internal/host/starlark's HTTPCassette: a flat, on-disk-friendly artifact
// that a ReplayAgent can drive Run from with zero live/scripted agent logic.
type Cassette struct {
	// Steps is the recorded trajectory, in order.
	Steps []CassetteStep
}

// NewCassetteFromTrace builds a Cassette from a recorded Trace, keeping only
// what a replay needs to reproduce the Emissions the agent produced (the
// Observation/Error fields are outcomes of replaying, not inputs to it, so
// they are intentionally dropped here).
func NewCassetteFromTrace(trace []TraceStep) Cassette {
	steps := make([]CassetteStep, 0, len(trace))
	for _, ts := range trace {
		steps = append(steps, CassetteStep{
			Step:    ts.Step,
			Snippet: ts.Snippet,
			Done:    ts.Done,
			Payload: ts.Payload,
		})
	}
	return Cassette{Steps: steps}
}

// ReplayAgent satisfies the Agent interface purely by replaying a Cassette:
// it never invents a snippet or payload. Asking it for a step beyond what
// the cassette recorded is an error, not a fallback to some default/live
// behavior — this is what makes replay "zero-LLM": the trajectory is fully
// determined by the cassette, and running out of cassette is a hard stop.
type ReplayAgent struct {
	Cassette Cassette
}

// Next returns the recorded Emission for step, or an error if the cassette
// has no recording for that step index.
func (r *ReplayAgent) Next(_ context.Context, step int, _ map[string]any, _ *ErrorEnvelope) (Emission, error) {
	if step < 0 || step >= len(r.Cassette.Steps) {
		return Emission{}, fmt.Errorf("codeact: replay: no cassette step recorded for step %d (cassette has %d steps)", step, len(r.Cassette.Steps))
	}
	cs := r.Cassette.Steps[step]
	return Emission{
		Snippet: cs.Snippet,
		Done:    cs.Done,
		Payload: cs.Payload,
	}, nil
}
