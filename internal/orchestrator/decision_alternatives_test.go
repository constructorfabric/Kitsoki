package orchestrator_test

// Tests for Slice #4: Decision Alternatives — ranked runner-up scores in
// Verdict + gate_decided.
//
// Verifies:
//   - When the fake agent returns alternatives in the submitted verdict, the
//     gate_decided event carries them in the "alternatives" attr.
//   - Legacy responses without alternatives produce a gate_decided event with
//     no "alternatives" key.
//   - chosen_intent in gate_decided always reflects Verdict.Intent, never
//     influenced by the alternatives list.
//   - threshold is always recorded in gate_decided.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func TestDecisionAlternatives_WithAlternatives(t *testing.T) {
	// The fake agent returns a verdict with two runner-up alternatives.
	verdict := map[string]any{
		"intent":     "path_b",
		"confidence": 0.9,
		"reason":     "b is the clear winner",
		"alternatives": []any{
			map[string]any{"intent": "path_a", "score": 0.4, "reason": "a is possible but weaker"},
		},
	}
	orch := newDeciderOrchestrator(t, verdict,
		orchestrator.WithDecider(orchestrator.DeciderConfig{
			Agent:     "judge",
			Schema:    "schema.json",
			Threshold: 0.8,
		}),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)

	// Should have fired path_b and advanced to done_b.
	require.Equal(t, "done_b", string(out.NewState),
		"engine decider must fire the chosen intent")

	var gatePayload map[string]any
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			require.NoError(t, json.Unmarshal(ev.Payload, &gatePayload))
			break
		}
	}
	require.NotNil(t, gatePayload, "a GateDecided event must be emitted")

	// chosen_intent must be the verdict's Intent, not influenced by alternatives.
	require.Equal(t, "path_b", gatePayload["chosen_intent"],
		"chosen_intent must reflect Verdict.Intent")

	// alternatives must be present.
	alts, ok := gatePayload["alternatives"]
	require.True(t, ok, "gate_decided must carry alternatives when the verdict includes them")

	// alternatives is a slice (JSON array) with one entry.
	altsSlice, ok := alts.([]any)
	require.True(t, ok, "alternatives must be a JSON array")
	require.Len(t, altsSlice, 1, "exactly one runner-up expected")

	entry, ok := altsSlice[0].(map[string]any)
	require.True(t, ok, "each alternative must be a JSON object")
	require.Equal(t, "path_a", entry["intent"])
	require.InDelta(t, 0.4, entry["score"], 0.001)
	require.Equal(t, "a is possible but weaker", entry["reason"])

	// threshold must be recorded.
	require.InDelta(t, 0.8, gatePayload["threshold"], 0.001,
		"gate_decided must record the threshold")
}

func TestDecisionAlternatives_WithoutAlternatives(t *testing.T) {
	// Legacy verdict with no alternatives field.
	verdict := map[string]any{
		"intent":     "path_a",
		"confidence": 0.95,
		"reason":     "a is obvious",
	}
	orch := newDeciderOrchestrator(t, verdict,
		orchestrator.WithDecider(orchestrator.DeciderConfig{
			Agent:     "judge",
			Schema:    "schema.json",
			Threshold: 0.8,
		}),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, "done_a", string(out.NewState))

	var gatePayload map[string]any
	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			require.NoError(t, json.Unmarshal(ev.Payload, &gatePayload))
			break
		}
	}
	require.NotNil(t, gatePayload, "a GateDecided event must be emitted")

	// chosen_intent must match Verdict.Intent.
	require.Equal(t, "path_a", gatePayload["chosen_intent"])

	// alternatives must NOT be present for a legacy verdict.
	_, hasAlts := gatePayload["alternatives"]
	require.False(t, hasAlts, "gate_decided must not carry alternatives when absent from the verdict")

	// threshold still recorded.
	require.InDelta(t, 0.8, gatePayload["threshold"], 0.001)
}

func TestDecisionAlternatives_ChosenIntentNotInfluencedByAlternatives(t *testing.T) {
	// Alternatives list contains scores that are higher than the chosen
	// intent's confidence — chosen_intent must still be Verdict.Intent.
	verdict := map[string]any{
		"intent":     "path_b",
		"confidence": 0.85,
		"reason":     "b wins despite low confidence",
		"alternatives": []any{
			map[string]any{"intent": "path_a", "score": 0.99},
		},
	}
	orch := newDeciderOrchestrator(t, verdict,
		orchestrator.WithDecider(orchestrator.DeciderConfig{
			Agent:     "judge",
			Schema:    "schema.json",
			Threshold: 0.8,
		}),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.SubmitDirect(ctx, sid, "start", nil)
	require.NoError(t, err)

	// Engine must fire path_b (Verdict.Intent), not path_a (highest score in alternatives).
	require.Equal(t, "done_b", string(out.NewState),
		"engine must fire Verdict.Intent regardless of alternatives scores")

	for _, ev := range out.Events {
		if ev.Kind == store.GateDecided {
			var p map[string]any
			require.NoError(t, json.Unmarshal(ev.Payload, &p))
			require.Equal(t, "path_b", p["chosen_intent"],
				"chosen_intent must be Verdict.Intent, not influenced by alternatives")
		}
	}
}
