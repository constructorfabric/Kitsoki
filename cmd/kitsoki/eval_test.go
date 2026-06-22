package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEvalRunOfflineMergeJudge(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"eval", "run", "../../stories/pr-refinement/evals/merge_judge.yaml"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "OK offline eval contract: merge_judge") {
		t.Fatalf("missing offline confirmation in output:\n%s", out.String())
	}
}

func TestEvalListIncludesMergeJudge(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"eval", "list", "../../stories/pr-refinement"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "merge_judge") {
		t.Fatalf("missing merge_judge in output:\n%s", out.String())
	}
}
