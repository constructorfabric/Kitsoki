package graph

import "testing"

func TestFeedbackNodeAfter_SetsKindAndFiledAgainstWhenSupplied(t *testing.T) {
	after := FeedbackNodeAfter(FeedbackNodeSpec{
		Type:         "portal-feedback",
		Fields:       []string{"summary", "report"},
		NodeID:       "feedback-abc123",
		Title:        "stage pill drifts",
		ReportRef:    "abc123",
		Kind:         "bug",
		TargetNodeID: "feature-portal-feedback",
	})

	if got := after["kind"]; got != "bug" {
		t.Errorf("kind = %v, want %q", got, "bug")
	}
	edges, ok := after["edges"].(map[string]any)
	if !ok {
		t.Fatalf("edges = %v (%T), want map[string]any", after["edges"], after["edges"])
	}
	filedAgainst, ok := edges["filed_against"].([]string)
	if !ok || len(filedAgainst) != 1 || filedAgainst[0] != "feature-portal-feedback" {
		t.Errorf("edges[filed_against] = %v, want [feature-portal-feedback]", edges["filed_against"])
	}
}

func TestFeedbackNodeAfter_OmitsKindAndEdgesWhenAbsent(t *testing.T) {
	after := FeedbackNodeAfter(FeedbackNodeSpec{
		Type:      "portal-feedback",
		Fields:    []string{"summary", "report"},
		NodeID:    "feedback-abc123",
		Title:     "stage pill drifts",
		ReportRef: "abc123",
	})

	if _, ok := after["kind"]; ok {
		t.Errorf("kind = %v, want absent", after["kind"])
	}
	if _, ok := after["edges"]; ok {
		t.Errorf("edges = %v, want absent (human fills in during review)", after["edges"])
	}
}
