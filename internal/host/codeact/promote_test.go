package codeact

import (
	"context"
	"testing"

	kstarlark "kitsoki/internal/host/starlark"
)

// TestExtractTrajectory_PromotesMatchingStep pins the "code as artifact"
// promotion ratchet (concept.md §4, docs/goals/codeact/decomposition.yaml
// s5-promotion-ratchet): given a recorded trace whose final done() payload is
// literally the Observation of one of its own snippet steps, extraction
// freezes that step's snippet verbatim and declares a matching sidecar.
func TestExtractTrajectory_PromotesMatchingStep(t *testing.T) {
	snippet := "def main(ctx):\n    return {\"total\": 7}\n"
	trace := []TraceStep{
		{Step: 0, Snippet: snippet, Observation: map[string]any{"total": int64(7)}},
		{Step: 1, Done: true, Payload: map[string]any{"total": int64(7)}},
	}

	starSource, sidecarYAML, err := ExtractTrajectory(trace, map[string]any{"total": int64(7)})
	if err != nil {
		t.Fatalf("ExtractTrajectory: %v", err)
	}
	if string(starSource) != snippet {
		t.Fatalf("expected the frozen source to be step 0's snippet verbatim, got %q", starSource)
	}

	sidecar, err := kstarlark.ParseSidecar(sidecarYAML)
	if err != nil {
		t.Fatalf("generated sidecar does not parse: %v\n%s", err, sidecarYAML)
	}
	if _, ok := sidecar.Outputs["total"]; !ok {
		t.Fatalf("expected sidecar to declare a %q output, got %+v", "total", sidecar.Outputs)
	}
}

// TestExtractTrajectory_ReproducesDeterministically proves the frozen script,
// run through the SAME host.starlark.run the codeact executor already uses
// (no forking), reproduces the trajectory's payload byte-for-byte with zero
// agent dispatch — the whole point of the ratchet.
func TestExtractTrajectory_ReproducesDeterministically(t *testing.T) {
	snippet := "def main(ctx):\n    return {\"total\": 7}\n"
	trace := []TraceStep{
		{Step: 0, Snippet: snippet, Observation: map[string]any{"total": int64(7)}},
		{Step: 1, Done: true, Payload: map[string]any{"total": int64(7)}},
	}
	starSource, sidecarYAML, err := ExtractTrajectory(trace, map[string]any{"total": int64(7)})
	if err != nil {
		t.Fatalf("ExtractTrajectory: %v", err)
	}
	sidecar, err := kstarlark.ParseSidecar(sidecarYAML)
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}

	res, err := kstarlark.Run(context.Background(), kstarlark.Params{
		Script:  "promoted.star",
		Source:  starSource,
		Sidecar: sidecar,
	})
	if err != nil {
		t.Fatalf("Run(extracted script): %v", err)
	}
	got, ok := res.Outputs["total"].(int64)
	if !ok || got != 7 {
		t.Fatalf("expected the promoted script to deterministically reproduce total=7, got %#v", res.Outputs["total"])
	}
}

// TestExtractTrajectory_NoMatchingStep_Errors: a trajectory whose done()
// payload was NOT literally the output of any one snippet step (the model
// composed it out-of-band) is not a valid promotion target — the extractor
// must refuse rather than silently freeze a script that never actually
// produced that payload.
func TestExtractTrajectory_NoMatchingStep_Errors(t *testing.T) {
	trace := []TraceStep{
		{Step: 0, Snippet: "def main(ctx):\n    return {\"total\": 3}\n", Observation: map[string]any{"total": int64(3)}},
		{Step: 1, Done: true, Payload: map[string]any{"total": int64(7)}},
	}
	_, _, err := ExtractTrajectory(trace, map[string]any{"total": int64(7)})
	if err == nil {
		t.Fatalf("expected an error when no snippet step's observation matches the final payload, got nil")
	}
}
