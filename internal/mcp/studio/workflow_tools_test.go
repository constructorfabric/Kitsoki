package studio_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/dynamicworkflow"
	studio "kitsoki/internal/mcp/studio"
	"kitsoki/internal/testrunner"
)

func TestWorkflowCreateValidateExport(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "workflow.create", map[string]any{
		"goal": "implement dynamic workflows",
		"slug": "mcp-dwf-test",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "workflow.create errored: %s", contentText(res))
	var receipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &receipt))
	require.True(t, receipt.Validation.OK)
	require.NotEmpty(t, receipt.EventsPath)

	status, err := callTool(ctx, cs, "workflow.status", map[string]any{
		"workflow_id": receipt.WorkflowID,
	})
	require.NoError(t, err)
	require.False(t, status.IsError, "workflow.status errored: %s", contentText(status))
	var statusReceipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(status)), &statusReceipt))
	require.Equal(t, receipt.WorkflowID, statusReceipt.WorkflowID)
	require.FileExists(t, receipt.EventsPath)

	launch, err := callTool(ctx, cs, "workflow.launch", map[string]any{
		"workflow_id": receipt.WorkflowID,
	})
	require.NoError(t, err)
	require.False(t, launch.IsError, "workflow.launch errored: %s", contentText(launch))
	var launched dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(launch)), &launched))
	require.Equal(t, receipt.WorkflowID, launched.WorkflowID)
	require.NotEmpty(t, launched.TracePath, "launch should persist a trace path")
	require.NotEmpty(t, launched.StatePath, "launch should persist the runtime state path")
	require.NotEmpty(t, launched.SessionID, "launch should open a studio session")
	require.NotEmpty(t, launched.SessionHandle, "launch should return a studio handle")
	require.FileExists(t, launched.EventsPath)
	events, err := os.ReadFile(launched.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.launched")

	start, err := callTool(ctx, cs, "session.submit", map[string]any{
		"handle": launched.SessionHandle,
		"intent": "start",
	})
	require.NoError(t, err)
	require.False(t, start.IsError, "session.submit start errored: %s", contentText(start))
	require.NotContains(t, contentText(start), "must be repo-relative or under /tmp")
	require.FileExists(t, launched.StatePath)
	require.NotContains(t, contentText(start), `"state":"needs_human"`)

	exportDir := filepath.Join(t.TempDir(), "exported", "mcp-dwf-test")
	export, err := callTool(ctx, cs, "workflow.export", map[string]any{
		"workflow_id": receipt.WorkflowID,
		"target":      exportDir,
	})
	require.NoError(t, err)
	require.False(t, export.IsError, "workflow.export errored: %s", contentText(export))
	var exported dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(export)), &exported))
	require.Equal(t, exportDir, exported.ExportPath)
	require.FileExists(t, filepath.Join(exportDir, "app", "app.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "manifest.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "export-report.json"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
	require.FileExists(t, exported.EventsPath)
}

var _ = studio.Version

func TestWorkflowCreateResearchGoalWritesResearchManifest(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "workflow.create", map[string]any{
		"goal": "research the different testing approaches in the repo with a dynamic workflow; inspect Go tests, TypeScript/JavaScript tests, story flow fixtures, Playwright/e2e tests, coverage gates, cassettes, and no-LLM policies",
		"slug": "mcp-research-test",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "workflow.create errored: %s", contentText(res))
	var receipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &receipt))
	require.True(t, receipt.Validation.OK)

	manifestBytes, err := os.ReadFile(receipt.ManifestPath)
	require.NoError(t, err)
	manifest := string(manifestBytes)
	for _, want := range []string{
		"id: research-scope",
		"id: go-testing-research",
		"id: js-e2e-testing-research",
		"id: story-flow-research",
		"id: research-synthesis",
	} {
		require.Contains(t, manifest, want)
	}
	require.NotContains(t, manifest, "id: go-coverage")
	require.NotContains(t, manifest, "Add focused deterministic Go tests")
	require.NotContains(t, manifest, "implementation_story:")
	require.NotContains(t, manifest, "implementation_prompt:")
	require.True(t, strings.Contains(manifest, ".context/"), "research prompts should name .context outputs")
}

func TestWorkflowCreateAcceptsStructuredItems(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "workflow.create", map[string]any{
		"goal": "fix dynamic workflow audit problems",
		"slug": "mcp-structured-items",
		"items": []map[string]any{
			{
				"id":          "tui-proof",
				"title":       "TUI operation handle proof",
				"owner_scope": "internal/tui",
				"gate":        "go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1",
			},
			{
				"id":          "corpus-report",
				"title":       "Demo corpus runner/report clarity",
				"owner_scope": "internal/testrunner/operation_demo_corpus_test.go",
				"gate":        "go test ./internal/testrunner -run TestOperationDemoCorpus -count=1",
			},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "workflow.create errored: %s", contentText(res))
	var receipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &receipt))
	require.True(t, receipt.Validation.OK)
	require.Equal(t, "codex-native", receipt.ModelPolicy.Profile)
	require.Equal(t, "gpt-5.5", receipt.ModelPolicy.Model)

	manifestBytes, err := os.ReadFile(receipt.ManifestPath)
	require.NoError(t, err)
	manifest := string(manifestBytes)
	require.Contains(t, manifest, "id: tui-proof")
	require.Contains(t, manifest, "owner_scope: internal/tui")
	require.Contains(t, manifest, "gate_command:")
	require.NotContains(t, manifest, "id: scope")
	require.NotContains(t, manifest, "id: implement")
}

func TestWorkflowExportPreservesResearchDriveOnlyManifest(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	created := createWorkflowForTest(t, ctx, cs,
		"research the different testing approaches in the repo with a dynamic workflow; inspect Go tests, TypeScript/JavaScript tests, story flow fixtures, Playwright/e2e tests, coverage gates, cassettes, and no-LLM policies",
		"mcp-research-export-test",
	)
	exportDir := filepath.Join(t.TempDir(), "exported", "mcp-research-export-test")
	export, err := callTool(ctx, cs, "workflow.export", map[string]any{
		"workflow_id": created.WorkflowID,
		"target":      exportDir,
	})
	require.NoError(t, err)
	require.False(t, export.IsError, "workflow.export errored: %s", contentText(export))

	manifestBytes, err := os.ReadFile(filepath.Join(exportDir, "manifest.yaml"))
	require.NoError(t, err)
	manifest := string(manifestBytes)
	require.Contains(t, manifest, "id: research-scope")
	require.NotContains(t, manifest, "implementation_story:")
	require.NotContains(t, manifest, "implementation_prompt:")
}

func TestWorkflowLaunchStartsGeneratedCoverageAndResearchWorkflows(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	cases := []struct {
		name       string
		goal       string
		slug       string
		wantLoaded string
		wantFrame  string
	}{
		{
			name:       "coverage",
			goal:       "fan out glm-5.2 agents with the claude-synthetic harness to get Go, TypeScript/JavaScript, stories, and e2e coverage toward 80%",
			slug:       "coverage-launch-e2e",
			wantLoaded: "Loaded 6 item(s).",
			wantFrame:  "measure-coverage",
		},
		{
			name:       "research",
			goal:       "research the different testing approaches in the repo with a dynamic workflow; inspect Go tests, TypeScript/JavaScript tests, story flow fixtures, Playwright/e2e tests, coverage gates, cassettes, and no-LLM policies",
			slug:       "research-launch-e2e",
			wantLoaded: "Loaded 5 item(s).",
			wantFrame:  "research-scope",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created := createWorkflowForTest(t, ctx, cs, tc.goal, tc.slug)
			launch, err := callTool(ctx, cs, "workflow.launch", map[string]any{
				"workflow_id": created.WorkflowID,
			})
			require.NoError(t, err)
			require.False(t, launch.IsError, "workflow.launch errored: %s", contentText(launch))
			var launched dynamicworkflow.Receipt
			require.NoError(t, json.Unmarshal([]byte(contentText(launch)), &launched))
			require.NotEmpty(t, launched.SessionHandle)
			require.NotEmpty(t, launched.StatePath)

			start, err := callTool(ctx, cs, "session.submit", map[string]any{
				"handle": launched.SessionHandle,
				"intent": "start",
				"cols":   100,
				"rows":   30,
			})
			require.NoError(t, err)
			require.False(t, start.IsError, "session.submit start errored: %s", contentText(start))
			var turn struct {
				OK      bool `json:"ok"`
				Outcome struct {
					State string `json:"state"`
				} `json:"outcome"`
				Frame struct {
					Text string `json:"text"`
				} `json:"frame"`
			}
			require.NoError(t, json.Unmarshal([]byte(contentText(start)), &turn))
			require.True(t, turn.OK)
			require.Equal(t, "load", turn.Outcome.State)
			require.Contains(t, turn.Frame.Text, tc.wantLoaded)
			require.NotContains(t, turn.Frame.Text, "must be repo-relative or under /tmp")

			next, err := callTool(ctx, cs, "session.submit", map[string]any{
				"handle": launched.SessionHandle,
				"intent": "next_item",
				"cols":   100,
				"rows":   30,
			})
			require.NoError(t, err)
			require.False(t, next.IsError, "session.submit next_item errored: %s", contentText(next))
			require.Contains(t, contentText(next), tc.wantFrame)
			require.NotContains(t, contentText(next), `"state":"needs_human"`)

			drive, err := callTool(ctx, cs, "session.submit", map[string]any{
				"handle": launched.SessionHandle,
				"intent": "next_item",
				"cols":   100,
				"rows":   30,
			})
			require.NoError(t, err)
			require.False(t, drive.IsError, "session.submit drive errored: %s", contentText(drive))

			trace, err := callTool(ctx, cs, "session.trace", map[string]any{
				"handle": launched.SessionHandle,
			})
			require.NoError(t, err)
			require.False(t, trace.IsError, "session.trace errored: %s", contentText(trace))
			require.Contains(t, contentText(trace), "harness_ladder")
			require.Contains(t, contentText(trace), "synthetic-claude")
			require.Contains(t, contentText(trace), "hf:zai-org/GLM-5.2")

			exportDir := filepath.Join(t.TempDir(), "exported", tc.slug)
			export, err := callTool(ctx, cs, "workflow.export", map[string]any{
				"workflow_id": created.WorkflowID,
				"target":      exportDir,
			})
			require.NoError(t, err)
			require.False(t, export.IsError, "workflow.export errored: %s", contentText(export))
			var exported dynamicworkflow.Receipt
			require.NoError(t, json.Unmarshal([]byte(contentText(export)), &exported))
			require.NotNil(t, exported.ExportReport)
			require.NotNil(t, exported.ExportReport.StarterFlowReplay)
			require.True(t, exported.ExportReport.StarterFlowReplay.OK, "workflow.export response should surface starter replay status: %+v", exported.ExportReport.StarterFlowReplay)
			require.Equal(t, 1, exported.ExportReport.StarterFlowReplay.Passed)
			require.Zero(t, exported.ExportReport.StarterFlowReplay.Failed)

			flowPath := filepath.Join(exportDir, "flows", "generated.yaml")
			flowBytes, err := os.ReadFile(flowPath)
			require.NoError(t, err)
			flowYAML := string(flowBytes)
			require.Contains(t, flowYAML, "host_cassette: generated.cassette.yaml")
			require.Contains(t, flowYAML, "manifest_path: "+filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml")))
			require.NotContains(t, flowYAML, "policy_ok")
			require.NotContains(t, flowYAML, "__on_complete_target__")

			report, err := testrunner.RunFlows(t.Context(), filepath.Join(exportDir, "app", "app.yaml"), flowPath, testrunner.FlowOptions{FailFast: true})
			require.NoError(t, err)
			require.Zero(t, report.Failed)
			require.Equal(t, 1, report.Passed)
		})
	}
}

func createWorkflowForTest(t *testing.T, ctx context.Context, cs *mcpsdk.ClientSession, goal, slug string) dynamicworkflow.Receipt {
	t.Helper()
	res, err := callTool(ctx, cs, "workflow.create", map[string]any{
		"goal": goal,
		"slug": slug,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "workflow.create errored: %s", contentText(res))
	var receipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &receipt))
	require.True(t, receipt.Validation.OK)
	return receipt
}
