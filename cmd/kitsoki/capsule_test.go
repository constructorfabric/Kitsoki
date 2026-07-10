package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapsuleListIncludesCoreAndRepoHistoryCapsules(t *testing.T) {
	out, err := execRoot(t, "capsule", "list")
	if err != nil {
		t.Fatalf("capsule list: %v\n%s", err, out)
	}
	for _, want := range []string{
		"clean-repo [core]",
		"repo-history/query-string/qs1 [repo-history]",
		"executor: internal/capsule",
		"executor: bugfix-bakeoff",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("capsule list missing %q:\n%s", want, out)
		}
	}
}

func TestCapsuleListRepoHistoryJSON(t *testing.T) {
	out, err := execRoot(t, "capsule", "list", "--kind", "repo-history", "--json")
	if err != nil {
		t.Fatalf("capsule list --json: %v\n%s", err, out)
	}
	var payload struct {
		OK       bool `json:"ok"`
		Count    int  `json:"count"`
		Capsules []struct {
			Ref      string `json:"ref"`
			Kind     string `json:"kind"`
			Executor string `json:"executor"`
		} `json:"capsules"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("capsule list JSON decode: %v\n%s", err, out)
	}
	if !payload.OK || payload.Count != len(payload.Capsules) || payload.Count == 0 {
		t.Fatalf("unexpected capsule list payload: %+v", payload)
	}
	for _, entry := range payload.Capsules {
		if entry.Ref == "repo-history/gears-rust/bug1" {
			if entry.Kind != "repo-history" || entry.Executor != "bugfix-bakeoff" {
				t.Fatalf("unexpected gears-rust entry: %+v", entry)
			}
			return
		}
	}
	t.Fatalf("missing repo-history/gears-rust/bug1 in %+v", payload.Capsules)
}

func TestCapsuleListRepoHistoryMarkdown(t *testing.T) {
	out, err := execRoot(t, "capsule", "list", "--kind", "repo-history", "--markdown")
	if err != nil {
		t.Fatalf("capsule list --markdown: %v\n%s", err, out)
	}
	for _, want := range []string{
		"# Repo History Capsules",
		"Capsules: **10**",
		"`repo-history/query-string/qs1`",
		"bugfix-bakeoff",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("capsule markdown missing %q:\n%s", want, out)
		}
	}
}
