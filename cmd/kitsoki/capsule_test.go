package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		"executor: internal/capsule/pinned-git",
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
			Ref         string `json:"ref"`
			Kind        string `json:"kind"`
			Executor    string `json:"executor"`
			Environment string `json:"environment"`
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
			if entry.Kind != "repo-history" || entry.Executor != repoHistoryCapsuleMaterializer || entry.Environment != "repo-history-gears-rust" {
				t.Fatalf("unexpected gears-rust entry: %+v", entry)
			}
			return
		}
	}
	t.Fatalf("missing repo-history/gears-rust/bug1 in %+v", payload.Capsules)
}

func TestCapsuleListCoreJSONUsesManagedDefinitions(t *testing.T) {
	out, err := execRoot(t, "capsule", "list", "--kind", "core", "--json")
	if err != nil {
		t.Fatalf("capsule list --kind core --json: %v\n%s", err, out)
	}
	var payload struct {
		OK       bool   `json:"ok"`
		Kind     string `json:"kind"`
		Capsules []struct {
			Ref      string `json:"ref"`
			Kind     string `json:"kind"`
			Path     string `json:"path"`
			Executor string `json:"executor"`
			Scenario string `json:"scenario"`
		} `json:"capsules"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("capsule list core JSON decode: %v\n%s", err, out)
	}
	if !payload.OK || payload.Kind != "core" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	for _, entry := range payload.Capsules {
		if entry.Ref == "clean-repo" {
			if entry.Kind != "core" || entry.Executor != "internal/capsule" || entry.Path != "capsules/clean-repo/capsule.yaml" || entry.Scenario != "git-flow" {
				t.Fatalf("unexpected clean-repo entry: %+v", entry)
			}
			return
		}
	}
	t.Fatalf("missing clean-repo in %+v", payload.Capsules)
}

func TestCapsuleListRepoHistoryMarkdown(t *testing.T) {
	jsonOut, err := execRoot(t, "capsule", "list", "--kind", "repo-history", "--json")
	if err != nil {
		t.Fatalf("capsule list --json: %v\n%s", err, jsonOut)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("capsule list JSON decode: %v\n%s", err, jsonOut)
	}

	out, err := execRoot(t, "capsule", "list", "--kind", "repo-history", "--markdown")
	if err != nil {
		t.Fatalf("capsule list --markdown: %v\n%s", err, out)
	}
	// The repo-history corpus grows over time (bugfix-bakeoff adds fixtures),
	// so the count is cross-checked against --json rather than hardcoded.
	for _, want := range []string{
		"# Repo History Capsules",
		fmt.Sprintf("Capsules: **%d**", payload.Count),
		"`repo-history/query-string/qs1`",
		"internal/capsule/pinned-git",
		"repo-history-query-string",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("capsule markdown missing %q:\n%s", want, out)
		}
	}
}

func TestCapsuleOpenCoreUsesManagedPathWithLegacyManifest(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "opened")
	absDest, err := filepath.Abs(dest)
	if err != nil {
		t.Fatal(err)
	}
	if realDest, err := filepath.EvalSymlinks(filepath.Dir(absDest)); err == nil {
		absDest = filepath.Join(realDest, filepath.Base(absDest))
	}
	out, err := execRoot(t, "capsule", "open", "clean-repo", "--dest", dest, "--json")
	if err != nil {
		t.Fatalf("capsule open: %v\n%s", err, out)
	}
	var manifest struct {
		CapsuleName string `json:"capsule_name"`
		Workspace   string `json:"workspace"`
		TreeDigest  string `json:"tree_digest"`
		Source      struct {
			Synthetic bool   `json:"synthetic"`
			Head      string `json:"head"`
		} `json:"source"`
	}
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		t.Fatalf("manifest decode: %v\n%s", err, out)
	}
	if manifest.CapsuleName != "clean-repo" || manifest.Workspace != absDest || manifest.TreeDigest == "" || !manifest.Source.Synthetic || manifest.Source.Head == "" {
		t.Fatalf("unexpected manifest %#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(dest, "capsule-manifest.json")); err != nil {
		t.Fatalf("missing manifest: %v", err)
	}
	verify, err := execRoot(t, "capsule", "verify", dest)
	if err != nil {
		t.Fatalf("capsule verify: %v\n%s", err, verify)
	}
	if !strings.Contains(verify, "capsule clean-repo verification: ok") {
		t.Fatalf("unexpected verify output:\n%s", verify)
	}
	closed, err := execRoot(t, "capsule", "close", dest)
	if err != nil {
		t.Fatalf("capsule close: %v\n%s", err, closed)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists or stat failed: %v", err)
	}
}

func TestCapsuleCloseRefusesNonCapsule(t *testing.T) {
	dir := t.TempDir()
	out, err := execRoot(t, "capsule", "close", dir)
	if err == nil {
		t.Fatalf("capsule close accepted non-capsule:\n%s", out)
	}
	if !strings.Contains(err.Error(), "missing .kitsoki-capsule") {
		t.Fatalf("unexpected close error: %v\n%s", err, out)
	}
}

func TestCapsuleVerifyCoreUsesManagedOpenWithLegacyResult(t *testing.T) {
	out, err := execRoot(t, "capsule", "verify", "clean-repo", "--json")
	if err != nil {
		t.Fatalf("capsule verify clean-repo --json: %v\n%s", err, out)
	}
	var result struct {
		OK                 bool   `json:"ok"`
		CapsuleName        string `json:"capsule_name"`
		Workspace          string `json:"workspace"`
		ExpectedTreeDigest string `json:"expected_tree_digest"`
		ActualTreeDigest   string `json:"actual_tree_digest"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("verify JSON decode: %v\n%s", err, out)
	}
	if !result.OK || result.CapsuleName != "clean-repo" || result.Workspace == "" || result.ActualTreeDigest == "" {
		t.Fatalf("unexpected verify result: %+v", result)
	}
}
