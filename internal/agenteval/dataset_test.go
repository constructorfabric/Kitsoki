package agenteval

import (
	"path/filepath"
	"testing"
)

func TestValidateDatasetResolvesMergeJudge(t *testing.T) {
	path := filepath.Join("..", "..", "stories", "pr-refinement", "evals", "merge_judge.yaml")
	result, err := ValidateDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() {
		t.Fatalf("ValidateDataset errors: %v", result.Errors)
	}
	if result.Call.Call != "merge_judge" {
		t.Fatalf("call = %q, want merge_judge", result.Call.Call)
	}
	if result.Call.Handler != "host.agent.decide" {
		t.Fatalf("handler = %q, want host.agent.decide", result.Call.Handler)
	}
	if result.Call.SelectionRef == "" {
		t.Fatal("selection evidence ref was not captured")
	}
}

func TestReportStatusStale(t *testing.T) {
	call := CallSite{DatasetHash: "sha256:new"}
	report := &Report{
		DatasetHash: "sha256:old",
		Candidates:  []CandidateResult{{Pass: true}},
	}
	if got := ReportStatus(report, call); got != "stale" {
		t.Fatalf("ReportStatus = %q, want stale", got)
	}
}
