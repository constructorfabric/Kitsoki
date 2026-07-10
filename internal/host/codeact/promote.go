package codeact

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ExtractTrajectory implements the "code as artifact" promotion ratchet
// (concept.md §4, docs/goals/codeact/decomposition.yaml s5-promotion-ratchet):
// given a recorded codeact trace and the trajectory's final done() payload, it
// finds the ONE snippet step whose Observation is literally that payload and
// freezes that step's Snippet verbatim as a standalone Starlark script, paired
// with a minimal sidecar declaring one `any`-typed required output per key in
// the payload.
//
// The match is required to be exact and unambiguous by construction: a
// trajectory whose done() payload was assembled out-of-band (composed from
// more than one step's Observation, or hand-authored by the model rather than
// literally returned by a snippet) is NOT a valid promotion target, so
// ExtractTrajectory refuses with an error rather than silently freezing a
// script that never actually produced that payload — freezing the wrong step
// would be worse than not promoting at all, since it would ship a "frozen"
// artifact that quietly diverges from the trajectory it claims to reproduce.
//
// Matching is done via a JSON-marshal comparison rather than reflect.DeepEqual
// because trace steps and the final payload both cross a JSON boundary
// (journaled trace, agent-emitted done() payload) where numeric types commonly
// drift between int/int64/float64 depending on decode path; comparing the
// canonical JSON encoding treats those as equal (as they are, semantically),
// while reflect.DeepEqual would spuriously reject a match that differs only in
// Go numeric type.
func ExtractTrajectory(trace []TraceStep, finalPayload map[string]any) (starSource, sidecarYAML []byte, err error) {
	wantJSON, err := json.Marshal(finalPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("codeact: promote: marshal final payload: %w", err)
	}

	for _, step := range trace {
		if step.Snippet == "" || step.Observation == nil {
			continue
		}
		gotJSON, err := json.Marshal(step.Observation)
		if err != nil {
			continue
		}
		if string(gotJSON) != string(wantJSON) {
			continue
		}
		sidecar, err := buildSidecarYAML(finalPayload)
		if err != nil {
			return nil, nil, fmt.Errorf("codeact: promote: build sidecar: %w", err)
		}
		return []byte(step.Snippet), sidecar, nil
	}

	return nil, nil, fmt.Errorf("codeact: promote: no snippet step's observation matches the final payload %s — the trajectory's result was not literally produced by any single step, so it is not a valid promotion target", wantJSON)
}

// buildSidecarYAML renders a minimal .star.yaml declaring one `any`-typed
// required output per key in payload, in sorted key order for a deterministic,
// diff-friendly result.
func buildSidecarYAML(payload map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := "outputs:\n"
	for _, k := range keys {
		out += fmt.Sprintf("  %s: { type: any, required: true }\n", k)
	}
	return []byte(out), nil
}
