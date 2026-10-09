package testrunner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/testrunner"
)

func TestOperationDemoCorpus(t *testing.T) {
	const proofName = "no-LLM operation demo corpus"
	cases := operationDemoCorpusCases(t)

	t.Logf("%s: %d scenarios", proofName, len(cases))
	for _, tc := range cases {
		t.Logf("scenario=%s story=%s capsule=%s gates=%s", tc.name, tc.story, tc.capsule, tc.gates())
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("%s: %s", proofName, tc.reviewNote)
			t.Logf("story=%s source_flow=%s app=%s capsule=%s gates=%s", tc.story, tc.sourceFlow, tc.appPath, tc.capsule, tc.gates())

			workspace := capsuletest.Open(t, tc.capsule)
			capsuletest.Verify(t, workspace)

			flowPath := writeCapsuleBackedFlow(t, tc.sourceFlow, tc.appPath, workspace, tc.name, operationExpectations{
				policy:           tc.expectOperationPolicy,
				status:           tc.expectOperationStatus,
				terminalArtifact: tc.expectTerminalArtifact,
			}, flowOptions{
				selfProvisionWorkspace: tc.selfProvisionWorkspace,
			})
			report, err := testrunner.RunFlows(t.Context(), tc.appPath, flowPath, testrunner.FlowOptions{})
			require.NoError(t, err)
			requireFlowReportPassed(t, report)
			capsuletest.Verify(t, workspace)
		})
	}
}

type operationDemoCorpusCase struct {
	appPath                string
	capsule                string
	name                   string
	reviewNote             string
	sourceFlow             string
	story                  string
	expectOperationPolicy  string
	expectOperationStatus  string
	expectTerminalArtifact string
	selfProvisionWorkspace bool
}

func operationDemoCorpusCases(t *testing.T) []operationDemoCorpusCase {
	t.Helper()
	return []operationDemoCorpusCase{
		{
			appPath:    repoStoriesBugfixAppPath(t),
			capsule:    "clean-repo",
			name:       "triage_codeact_completed",
			reviewNote: "bugfix triage can complete from the recorded codeact proof without a live agent",
			sourceFlow: repoPath(t, "../../stories/bugfix/flows/codeact_live_proof_triage.yaml"),
			story:      "bugfix",
		},
		{
			appPath:    repoStoriesBugfixAppPath(t),
			capsule:    "clean-repo",
			name:       "direct_ship_completed",
			reviewNote: "bugfix can ship a direct fix from the recorded autonomous path",
			sourceFlow: repoPath(t, "../../stories/bugfix/flows/bugfix_ships_direct.yaml"),
			story:      "bugfix",
		},
		{
			appPath:                repoStoriesBugfixAppPath(t),
			capsule:                "clean-repo",
			name:                   "quick_fix_completed",
			reviewNote:             "quick bugfix policy reaches the done artifact gate",
			sourceFlow:             repoPath(t, "../../stories/bugfix/flows/happy_quick_fix.yaml"),
			story:                  "bugfix",
			expectOperationPolicy:  "bugfix_quick",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "done_artifact",
		},
		{
			appPath:    repoStoriesBugfixAppPath(t),
			capsule:    "clean-repo",
			name:       "merged_red_waits_for_human",
			reviewNote: "bugfix stops for human review when merged code remains red",
			sourceFlow: repoPath(t, "../../stories/bugfix/flows/bugfix_needs_human_on_merged_red.yaml"),
			story:      "bugfix",
		},
		{
			appPath:                repoStoriesDevStoryAppPath(t),
			capsule:                "clean-repo",
			name:                   "dev_story_bugfix_to_pr_completed",
			reviewNote:             "dev-story bugfix import completes through the namespaced bugfix policy",
			sourceFlow:             repoPath(t, "../../stories/dev-story/flows/bugfix_to_pr.yaml"),
			story:                  "dev-story",
			expectOperationPolicy:  "bf__bugfix_full",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "bf__done_artifact",
		},
		{
			appPath:                repoStoriesDevStoryAppPath(t),
			capsule:                "dirty-index",
			name:                   "dev_story_self_provisioned_workdir_handoff_completed",
			reviewNote:             "dev-story can self-provision a dirty-index handoff workspace",
			sourceFlow:             repoPath(t, "../../stories/dev-story/flows/bugfix_to_pr_workdir_handoff.yaml"),
			story:                  "dev-story",
			expectOperationPolicy:  "bf__bugfix_full",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "bf__done_artifact",
			selfProvisionWorkspace: true,
		},
		{
			appPath:                repoStoriesDevStoryAppPath(t),
			capsule:                "clean-repo",
			name:                   "dev_story_fix_tests_completed",
			reviewNote:             "dev-story fix-tests import completes through its tests policy",
			sourceFlow:             repoPath(t, "../../stories/dev-story/flows/fix_tests_autonomous.yaml"),
			story:                  "dev-story",
			expectOperationPolicy:  "tests__fix_tests",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "tests__report_path",
		},
		{
			appPath:                repoKitsokiDevAppPath(t),
			capsule:                "clean-repo",
			name:                   "kitsoki_dev_fix_tests_completed",
			reviewNote:             "kitsoki-dev fix-tests import completes through the core tests policy",
			sourceFlow:             repoPath(t, "../../.kitsoki/stories/kitsoki-dev/flows/fix_tests_autonomous.yaml"),
			story:                  "kitsoki-dev",
			expectOperationPolicy:  "core__tests__fix_tests",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "core__tests__report_path",
		},
		{
			appPath:                repoStoriesDemoVideoLoopAppPath(t),
			capsule:                "clean-repo",
			name:                   "demo_video_loop_visual_qa_completed",
			reviewNote:             "demo-video-loop completes the visual QA operation and records its completion artifact",
			sourceFlow:             repoPath(t, "../../stories/demo-video-loop/flows/happy_path.yaml"),
			story:                  "demo-video-loop",
			expectOperationPolicy:  "demo_video_qa",
			expectOperationStatus:  "completed",
			expectTerminalArtifact: "qa_completion_artifact_handle",
		},
		{
			appPath:                repoStoriesShipItAppPath(t),
			capsule:                "clean-repo",
			name:                   "ship_it_existing_worktree_tail_completed",
			reviewNote:             "ship-it can tail an existing worktree through the ship operation",
			sourceFlow:             repoPath(t, "../../stories/ship-it/flows/integrate_existing_ships.yaml"),
			story:                  "ship-it",
			expectOperationPolicy:  "ship_it",
			expectOperationStatus:  "completed",
			selfProvisionWorkspace: true,
		},
		{
			appPath:               repoStoriesGitOpsAppPath(t),
			capsule:               "clean-repo",
			name:                  "gitops_sync_main_accept_completed",
			reviewNote:            "git-ops sync-main accept path completes through the sync operation",
			sourceFlow:            repoPath(t, "../../stories/git-ops/flows/sync_main_accept_commits_operation.yaml"),
			story:                 "git-ops",
			expectOperationPolicy: "gitops.sync_main",
			expectOperationStatus: "completed",
		},
	}
}

func (tc operationDemoCorpusCase) gates() string {
	var gates []string
	if tc.expectOperationPolicy != "" {
		gates = append(gates, "policy="+tc.expectOperationPolicy)
	}
	if tc.expectOperationStatus != "" {
		gates = append(gates, "status="+tc.expectOperationStatus)
	}
	if tc.expectTerminalArtifact != "" {
		gates = append(gates, "terminal_artifact="+tc.expectTerminalArtifact)
	}
	if tc.selfProvisionWorkspace {
		gates = append(gates, "self_provision_workspace")
	}
	if len(gates) == 0 {
		return "flow_passes"
	}
	return strings.Join(gates, ",")
}

func repoStoriesBugfixAppPath(t *testing.T) string {
	t.Helper()
	return repoPath(t, "../../stories/bugfix/app.yaml")
}

func repoStoriesDemoVideoLoopAppPath(t *testing.T) string {
	t.Helper()
	return repoPath(t, "../../stories/demo-video-loop/app.yaml")
}

func repoStoriesShipItAppPath(t *testing.T) string {
	t.Helper()
	return repoPath(t, "../../stories/ship-it/app.yaml")
}

func repoStoriesGitOpsAppPath(t *testing.T) string {
	t.Helper()
	return repoPath(t, "../../stories/git-ops/app.yaml")
}

func repoKitsokiDevAppPath(t *testing.T) string {
	t.Helper()
	return repoPath(t, "../../.kitsoki/stories/kitsoki-dev/app.yaml")
}

func repoPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	require.NoError(t, err)
	return abs
}

type operationExpectations struct {
	policy           string
	status           string
	terminalArtifact string
}

type flowOptions struct {
	selfProvisionWorkspace bool
}

func writeCapsuleBackedFlow(t *testing.T, sourceFlow, appPath, workspace, name string, expectations operationExpectations, opts flowOptions) string {
	t.Helper()
	raw, err := os.ReadFile(sourceFlow)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	initialWorld := stringAnyMap(t, doc, "initial_world")
	slug := strings.ReplaceAll(name, "_", "-")
	workspaceID := "capsule-" + slug
	replacements := map[string]string{}
	for _, key := range []string{"workdir", "worktree_path", "working_dir", "main_worktree_path"} {
		if old, ok := initialWorld[key].(string); ok && old != "" {
			replacements[old] = workspace
		}
	}
	if old, ok := initialWorld["workspace_id"].(string); ok && old != "" {
		replacements[old] = workspaceID
	}
	if old, ok := initialWorld["feature_branch"].(string); ok && old != "" {
		replacements[old] = workspaceID
	}
	replaceStringValues(doc, replacements)

	doc["app"] = appPath
	if cassette, ok := doc["host_cassette"].(string); ok && cassette != "" && !filepath.IsAbs(cassette) {
		doc["host_cassette"] = filepath.Join(filepath.Dir(sourceFlow), cassette)
	}
	if expectations.policy != "" {
		doc["expect_operation_policy"] = expectations.policy
	}
	if expectations.status != "" {
		doc["expect_operation_status"] = expectations.status
	}
	if expectations.terminalArtifact != "" {
		doc["expect_terminal_artifact"] = expectations.terminalArtifact
	}
	initialWorld = stringAnyMap(t, doc, "initial_world")
	if !opts.selfProvisionWorkspace {
		initialWorld["workdir"] = workspace
		initialWorld["worktree_path"] = workspace
		initialWorld["working_dir"] = workspace
		initialWorld["main_worktree_path"] = workspace
		initialWorld["workspace_id"] = workspaceID
		initialWorld["feature_branch"] = workspaceID
		// capsuletest.Open has already prepared an isolated checkout. Imported
		// bugfix runs should trust that absolute path instead of re-deriving a
		// repo-relative .worktrees checkout.
		initialWorld["workspace_prepared"] = true
	}

	generated, err := yaml.Marshal(doc)
	require.NoError(t, err)
	flowPath := filepath.Join(t.TempDir(), slug+".yaml")
	require.NoError(t, os.WriteFile(flowPath, generated, 0o644))
	return flowPath
}

func stringAnyMap(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := doc[key]
	if !ok || value == nil {
		out := map[string]any{}
		doc[key] = out
		return out
	}
	out, ok := value.(map[string]any)
	require.Truef(t, ok, "%s must be a map, got %T", key, value)
	return out
}

func replaceStringValues(value any, replacements map[string]string) any {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			typed[k] = replaceStringValues(v, replacements)
		}
		return typed
	case []any:
		for i, v := range typed {
			typed[i] = replaceStringValues(v, replacements)
		}
		return typed
	case string:
		if replacement, ok := replacements[typed]; ok {
			return replacement
		}
		return typed
	default:
		return typed
	}
}

func requireFlowReportPassed(t *testing.T, report *testrunner.FlowReport) {
	t.Helper()
	require.NotNil(t, report)
	for _, r := range report.Results {
		if r.Passed {
			continue
		}
		for _, turn := range r.Turns {
			for _, failure := range turn.Failures {
				t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, failure)
			}
		}
	}
	require.Equal(t, 0, report.Failed)
}
