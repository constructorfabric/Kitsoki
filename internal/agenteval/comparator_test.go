package agenteval

import "testing"

func TestCompareEnum(t *testing.T) {
	got := Compare(
		ComparatorSpec{Kind: "enum", Field: "intent"},
		map[string]any{"intent": "accept", "reason": "ok"},
		map[string]any{"intent": "accept", "reason": "different"},
	)
	if !got.Pass {
		t.Fatalf("Compare enum failed: %s", got.Reason)
	}
}

func TestCompareFieldSubset(t *testing.T) {
	got := Compare(
		ComparatorSpec{Kind: "field_subset"},
		map[string]any{"outer": map[string]any{"intent": "refine"}},
		map[string]any{"outer": map[string]any{"intent": "refine", "reason": "missing sha"}},
	)
	if !got.Pass {
		t.Fatalf("Compare field_subset failed: %s", got.Reason)
	}
}
