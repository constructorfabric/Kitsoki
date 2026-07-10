package dynamicworkflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goyaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func dynamicWorkflowTestDir(t *testing.T, repoRoot string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, ".artifacts", "dynamic-workflows-test", strings.ReplaceAll(t.Name(), "/", "-"))
	require.NoError(t, os.RemoveAll(dir))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestServiceCreateValidateExport(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	fixedNow := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return fixedNow }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "implement dynamic workflows",
		Slug: "dynamic-workflows",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)
	require.True(t, strings.Contains(receipt.WorkflowID, "dynamic-workflows"))
	require.FileExists(t, receipt.ManifestPath)
	require.FileExists(t, receipt.EventsPath)
	require.FileExists(t, filepath.Join(receipt.AppPath, "app.yaml"))
	require.NotEmpty(t, receipt.StatePath)
	require.Contains(t, receipt.LaunchCommand, "kitsoki run")
	require.Equal(t, ModelPolicy{
		Harness:           "live",
		Profile:           "codex-native",
		Model:             "gpt-5.5",
		RequireTraceModel: true,
		RequireGPT55:      true,
	}, receipt.ModelPolicy)
	events, err := os.ReadFile(receipt.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.generated")
	require.Contains(t, string(events), "dynamic.workflow.validated")
	require.Contains(t, string(events), `"model_policy"`)

	loaded, err := svc.ReadReceipt(receipt.WorkflowID)
	require.NoError(t, err)
	require.Equal(t, receipt.WorkflowID, loaded.WorkflowID)
	require.True(t, loaded.Validation.OK)

	exportDir := filepath.Join(outDir, "exported", "dynamic-workflows")
	receipt, err = svc.Export(context.Background(), receipt.WorkflowID, ExportRequest{TargetDir: exportDir})
	require.NoError(t, err)
	require.Equal(t, exportDir, receipt.ExportPath)
	require.FileExists(t, filepath.Join(exportDir, "app", "app.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "manifest.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "launch.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "export-report.json"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
	events, err = os.ReadFile(receipt.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.exported")

	manifest, err := readManifest(filepath.Join(exportDir, "manifest.yaml"))
	require.NoError(t, err)
	for _, item := range manifest.Items {
		require.True(t, strings.HasPrefix(item.Story, filepath.ToSlash(filepath.Join(exportDir, "app"))))
		require.True(t, strings.HasPrefix(item.ImplementationStory, filepath.ToSlash(filepath.Join(exportDir, "app"))))
	}

	var launch map[string]any
	b, err := os.ReadFile(filepath.Join(exportDir, "launch.yaml"))
	require.NoError(t, err)
	require.NoError(t, goyaml.Unmarshal(b, &launch))
	world := launch["world"].(map[string]any)
	require.Equal(t, filepath.ToSlash(runtimePath(repoRoot, filepath.Join(exportDir, "manifest.yaml"))), world["manifest_path"])
	require.Equal(t, filepath.ToSlash(runtimePath(repoRoot, filepath.Join(exportDir, "punch-list.state.json"))), world["state_path"])
}

func TestServiceCreatePreservesStructuredDecomposition(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fix dynamic workflow audit problems",
		Slug: "structured-dwf",
		Items: []CreateItem{
			{
				ID:         "tui-proof",
				Title:      "TUI operation handle proof",
				OwnerScope: "internal/tui",
				Gate:       "go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1",
			},
			{
				ID:         "corpus-report",
				Title:      "Demo corpus runner/report clarity",
				OwnerScope: "internal/testrunner/operation_demo_corpus_test.go",
				Gate:       "go test ./internal/testrunner -run TestOperationDemoCorpus -count=1",
			},
		},
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)
	require.Equal(t, ModelPolicy{
		Harness:           "live",
		Profile:           "codex-native",
		Model:             "gpt-5.5",
		RequireTraceModel: true,
		RequireGPT55:      true,
	}, receipt.ModelPolicy)

	manifest, err := readManifest(receipt.ManifestPath)
	require.NoError(t, err)
	require.Len(t, manifest.Items, 2)
	require.Equal(t, "tui-proof", manifest.Items[0].ID)
	require.Equal(t, "TUI operation handle proof", manifest.Items[0].Title)
	require.Equal(t, "internal/tui", manifest.Items[0].OwnerScope)
	require.Equal(t, "go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1", manifest.Items[0].GateCommand)
	require.Empty(t, manifest.Items[0].Verify)
	require.Contains(t, manifest.Items[0].Prompt, "Owner scope: internal/tui")
	require.Contains(t, manifest.Items[0].Prompt, "Deterministic gate: go test ./internal/tui")
	require.Equal(t, filepath.ToSlash(runtimePath(repoRoot, filepath.Join(receipt.AppPath, "app.yaml"))), manifest.Items[0].ImplementationStory)
	require.Equal(t, "corpus-report", manifest.Items[1].ID)

	manifestBytes, err := os.ReadFile(receipt.ManifestPath)
	require.NoError(t, err)
	manifestYAML := string(manifestBytes)
	require.Contains(t, manifestYAML, "owner_scope: internal/tui")
	require.Contains(t, manifestYAML, "gate_command: go test ./internal/tui")

	loaded, err := svc.ReadReceipt(receipt.WorkflowID)
	require.NoError(t, err)
	require.Equal(t, receipt.ModelPolicy, loaded.ModelPolicy)
}

func TestServiceCreateAvoidsSameSecondSlugCollisions(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	fixedNow := time.Date(2026, 7, 6, 6, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return fixedNow }

	first, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out agents to increase coverage",
		Slug: "duplicate",
	})
	require.NoError(t, err)
	second, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out agents to increase coverage",
		Slug: "duplicate",
	})
	require.NoError(t, err)

	require.NotEqual(t, first.WorkflowID, second.WorkflowID)
	require.NotEqual(t, first.DraftDir, second.DraftDir)
	require.NotEqual(t, first.TracePath, second.TracePath)
	require.DirExists(t, first.DraftDir)
	require.DirExists(t, second.DraftDir)
	require.FileExists(t, filepath.Join(first.DraftDir, "receipt.json"))
	require.FileExists(t, filepath.Join(second.DraftDir, "receipt.json"))
}

func TestValidateDraftRejectsRuntimeManifestFailure(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 6, 30, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out agents to increase coverage",
		Slug: "runtime-manifest-failure",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	manifest, err := readManifest(receipt.ManifestPath)
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Items)
	manifest.Items[0].GateCommand = "claude -p 'spend tokens'"
	require.NoError(t, writeYAML(receipt.ManifestPath, manifest))

	report := svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
	require.False(t, report.OK)
	require.Contains(t, strings.Join(report.Errors, "\n"), "runtime launch-readiness failed")
	require.Contains(t, strings.Join(report.Errors, "\n"), "gate_command appears to invoke an LLM or live run")
}

func TestValidateDraftRejectsBrokenLaunchBasis(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 7, 0, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out agents to increase coverage",
		Slug: "broken-launch-basis",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	require.NoError(t, writeYAML(receipt.LaunchBasisPath, map[string]any{
		"state": "idle",
		"world": map[string]any{
			"manifest_path": runtimePath(repoRoot, receipt.ManifestPath),
		},
	}))

	report := svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
	require.False(t, report.OK)
	require.Contains(t, strings.Join(report.Errors, "\n"), "launch basis invalid")
	require.Contains(t, strings.Join(report.Errors, "\n"), "world.state_path is required")
}

func TestValidateDraftPreservesRuntimeStateFile(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 7, 15, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out agents to increase coverage",
		Slug: "preserve-runtime-state",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)
	require.NoFileExists(t, receipt.StatePath, "create-time validation should clean up its readiness state")

	original := []byte(`{"sentinel":"keep"}
`)
	require.NoError(t, os.WriteFile(receipt.StatePath, original, 0o644))

	report := svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
	require.True(t, report.OK, "revalidation should pass: %v", report.Errors)
	after, err := os.ReadFile(receipt.StatePath)
	require.NoError(t, err)
	require.Equal(t, original, after, "validation must not overwrite runtime progress state")
}

func TestServiceCreatePreservesSyntheticGLMCoverageFanout(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 5, 30, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out glm-5.2 agents with the claude-synthetic harness to get Go, TypeScript/JavaScript, stories, and e2e coverage toward 80%",
		Slug: "glm-coverage",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	manifest, err := readManifest(receipt.ManifestPath)
	require.NoError(t, err)
	manifestBytes, err := os.ReadFile(receipt.ManifestPath)
	require.NoError(t, err)
	manifestYAML := string(manifestBytes)
	require.Equal(t, "synthetic-claude", manifest.Defaults.Profile)
	require.Equal(t, "hf:zai-org/GLM-5.2", manifest.Defaults.Model)
	require.Equal(t, "ladder", manifest.Defaults.Harness)
	require.False(t, manifest.Defaults.RequireTraceModel)
	require.NotNil(t, manifest.Defaults.RequireGPT55)
	require.False(t, *manifest.Defaults.RequireGPT55)
	require.Contains(t, manifestYAML, "require_gpt55: false")
	require.Contains(t, manifestYAML, "require_trace_model: false")
	require.NotNil(t, manifest.Defaults.HarnessLadder)
	require.Equal(t, []HarnessLadderModel{
		{Backend: "claude", Provider: "synthetic-claude", Model: "hf:zai-org/GLM-5.2"},
		{Backend: "codex", Provider: "codex-native", Model: "gpt-5.5"},
	}, manifest.Defaults.HarnessLadder.Models)
	require.Equal(t, ".artifacts/dynamic-workflows-test/TestServiceCreatePreservesSyntheticGLMCoverageFanout/dwf_20260706T053000Z_glm-coverage/traces", manifest.Defaults.TraceRoot)

	ids := make([]string, 0, len(manifest.Items))
	for _, item := range manifest.Items {
		ids = append(ids, item.ID)
		require.Contains(t, item.Prompt, "coverage")
	}
	require.Equal(t, []string{
		"measure-coverage",
		"go-coverage",
		"story-workflow-coverage",
		"js-ts-coverage",
		"e2e-flow-coverage",
		"supervisor-verify",
	}, ids)

	var launch map[string]any
	b, err := os.ReadFile(receipt.LaunchBasisPath)
	require.NoError(t, err)
	require.NoError(t, goyaml.Unmarshal(b, &launch))
	world := launch["world"].(map[string]any)
	require.Equal(t, ".artifacts/dynamic-workflows-test/TestServiceCreatePreservesSyntheticGLMCoverageFanout/dwf_20260706T053000Z_glm-coverage/manifest.yaml", world["manifest_path"])
	require.Equal(t, ".artifacts/dynamic-workflows-test/TestServiceCreatePreservesSyntheticGLMCoverageFanout/dwf_20260706T053000Z_glm-coverage/punch-list.state.json", world["state_path"])
}

func TestServiceCreateResearchTestingApproachesFanout(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 5, 45, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "research the different testing approaches in the repo with a dynamic workflow; inspect Go tests, TypeScript/JavaScript tests, story flow fixtures, Playwright/e2e tests, coverage gates, cassettes, and no-LLM policies; write a concise research report under .context and propose follow-up validation gates",
		Slug: "testing-approaches-research",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	manifest, err := readManifest(receipt.ManifestPath)
	require.NoError(t, err)
	manifestBytes, err := os.ReadFile(receipt.ManifestPath)
	require.NoError(t, err)
	manifestYAML := string(manifestBytes)
	require.Equal(t, "synthetic-claude", manifest.Defaults.Profile)
	require.Equal(t, "hf:zai-org/GLM-5.2", manifest.Defaults.Model)
	require.Equal(t, "ladder", manifest.Defaults.Harness)
	require.False(t, manifest.Defaults.RequireTraceModel)
	require.NotNil(t, manifest.Defaults.RequireGPT55)
	require.False(t, *manifest.Defaults.RequireGPT55)
	require.Contains(t, manifestYAML, "require_gpt55: false")
	require.Contains(t, manifestYAML, "require_trace_model: false")
	require.NotNil(t, manifest.Defaults.HarnessLadder)
	require.Equal(t, []HarnessLadderModel{
		{Backend: "claude", Provider: "synthetic-claude", Model: "hf:zai-org/GLM-5.2"},
		{Backend: "codex", Provider: "codex-native", Model: "gpt-5.5"},
	}, manifest.Defaults.HarnessLadder.Models)

	ids := make([]string, 0, len(manifest.Items))
	for _, item := range manifest.Items {
		ids = append(ids, item.ID)
		require.Contains(t, item.Prompt, ".context")
		require.Contains(t, item.Prompt, "Goal: research")
		require.Empty(t, item.ImplementationStory, "research item %s must not dispatch an implementation worker", item.ID)
		require.Empty(t, item.ImplementationPrompt, "research item %s must not dispatch an implementation worker", item.ID)
		require.NotContains(t, item.Title, "Add Go coverage")
		require.NotContains(t, item.Prompt, "Add focused deterministic Go tests")
	}
	require.Equal(t, []string{
		"research-scope",
		"go-testing-research",
		"js-e2e-testing-research",
		"story-flow-research",
		"research-synthesis",
	}, ids)
}

func TestServiceExportPreservesResearchDriveOnlyItems(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 5, 55, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "research the different testing approaches in the repo with a dynamic workflow; inspect Go tests, TypeScript/JavaScript tests, story flow fixtures, Playwright/e2e tests, coverage gates, cassettes, and no-LLM policies",
		Slug: "testing-approaches-research",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	exportDir := filepath.Join(outDir, "exported", "testing-approaches-research")
	receipt, err = svc.Export(context.Background(), receipt.WorkflowID, ExportRequest{TargetDir: exportDir})
	require.NoError(t, err)
	require.Equal(t, exportDir, receipt.ExportPath)

	manifest, err := readManifest(filepath.Join(exportDir, "manifest.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, manifest.Items)
	for _, item := range manifest.Items {
		require.Equal(t, "drive", item.Mode)
		require.True(t, strings.HasPrefix(item.Story, filepath.ToSlash(filepath.Join(exportDir, "app"))))
		require.Empty(t, item.ImplementationStory, "exported research item %s must stay drive-only", item.ID)
		require.Empty(t, item.ImplementationPrompt, "exported research item %s must stay drive-only", item.ID)
	}
}

func TestServiceExportGeneratedFlowReplaysSiblingCassette(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 8, 0, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "research testing approaches with a dynamic workflow",
		Slug: "export-replay",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	exportDir := filepath.Join(outDir, "exported", "export-replay")
	exportManifestPath := filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml"))
	exportStatePath := filepath.ToSlash(filepath.Join(exportDir, "flows", "generated.state.json"))
	trace := fmt.Sprintf(`{"kind":"session.header","schema_version":1,"written_at":"2026-07-06T08:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-07-06T08:00:00.001Z","kind":"turn.input","state_path":"idle","payload":{"input":"start","intent":""}}
{"turn":1,"seq":1,"ts":"2026-07-06T08:00:00.002Z","kind":"harness.returned","state_path":"load","payload":{"namespace":"host.starlark.run","data":{"manifest_path":%q,"state_path":%q,"items":[],"item_count":"0","error":""}}}
{"turn":1,"seq":2,"ts":"2026-07-06T08:00:00.003Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"load","intent":"start","slots":{}}}
`, exportManifestPath, exportStatePath)
	require.NoError(t, os.WriteFile(receipt.TracePath, []byte(trace), 0o644))

	receipt, err = svc.Export(context.Background(), receipt.WorkflowID, ExportRequest{TargetDir: exportDir})
	require.NoError(t, err)
	require.NotNil(t, receipt.ExportReport)
	require.NotNil(t, receipt.ExportReport.StarterFlowReplay)
	require.True(t, receipt.ExportReport.StarterFlowReplay.OK)

	flowPath := filepath.Join(exportDir, "flows", "generated.yaml")
	cassettePath := filepath.Join(exportDir, "flows", "generated.cassette.yaml")
	require.FileExists(t, flowPath)
	require.FileExists(t, cassettePath)

	flowBytes, err := os.ReadFile(flowPath)
	require.NoError(t, err)
	flowYAML := string(flowBytes)
	require.Contains(t, flowYAML, "host_cassette: generated.cassette.yaml")
	require.NotContains(t, flowYAML, "flows/generated.cassette.yaml")
	require.Contains(t, flowYAML, "manifest_path: "+exportManifestPath)

	var exportReport ExportReport
	require.NoError(t, readJSON(filepath.Join(exportDir, "export-report.json"), &exportReport))
	require.NotNil(t, exportReport.StarterFlowReplay)
	require.True(t, exportReport.StarterFlowReplay.OK, "starter flow replay should pass: %+v", exportReport.StarterFlowReplay)
	require.Equal(t, 1, exportReport.StarterFlowReplay.Passed)
	require.Zero(t, exportReport.StarterFlowReplay.Failed)
	require.NoFileExists(t, filepath.Join(exportDir, "flows", "generated.state.json"))

	report, err := testrunner.RunFlows(t.Context(), filepath.Join(exportDir, "app", "app.yaml"), flowPath, testrunner.FlowOptions{FailFast: true})
	require.NoError(t, err)
	require.Zero(t, report.Failed)
	require.Equal(t, 1, report.Passed)
}

func TestServiceExportGeneratedFlowSkipsInternalDispatchTransitions(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 7, 6, 8, 30, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "fan out glm-5.2 agents with the claude-synthetic harness to get coverage toward 80%",
		Slug: "export-dispatch-replay",
	})
	require.NoError(t, err)
	require.True(t, receipt.Validation.OK, "receipt should validate: %v", receipt.Validation.Errors)

	exportDir := filepath.Join(outDir, "exported", "export-dispatch-replay")
	exportManifestPath := filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml"))
	exportStatePath := filepath.ToSlash(filepath.Join(exportDir, "flows", "generated.state.json"))
	exportAppPath := filepath.ToSlash(filepath.Join(exportDir, "app", "app.yaml"))
	itemJSON := fmt.Sprintf(`{"id":"measure-coverage","title":"Measure current coverage","status":"pending","mode":"drive","story":%q,"prompt":"Measure coverage","profile":"synthetic-claude","model":"hf:zai-org/GLM-5.2","trace_path":"%s/traces/deterministic/measure-coverage.jsonl","trace_root":"%s/traces","trace_run_id":"deterministic","require_trace_model":false,"require_gpt55":false,"implementation_story":%q,"implementation_prompt":"Measure coverage","implementation_trace_path":"%s/traces/deterministic/measure-coverage-implementation.jsonl","verify":[{"kind":"story_validate","story":%q}]}`,
		exportAppPath,
		filepath.ToSlash(exportDir),
		filepath.ToSlash(exportDir),
		exportAppPath,
		filepath.ToSlash(exportDir),
		exportAppPath,
	)
	trace := fmt.Sprintf(`{"kind":"session.header","schema_version":1,"written_at":"2026-07-06T08:30:00Z"}
{"turn":1,"seq":0,"ts":"2026-07-06T08:30:00.001Z","kind":"turn.input","state_path":"idle","payload":{"input":"","intent":"start"}}
{"turn":1,"seq":1,"ts":"2026-07-06T08:30:00.002Z","kind":"harness.returned","state_path":"load","payload":{"namespace":"host.starlark.run","data":{"manifest_path":%q,"state_path":%q,"items":[%s],"item_count":"1","error":""}}}
{"turn":1,"seq":2,"ts":"2026-07-06T08:30:00.003Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"load","intent":"start","slots":{}}}
{"turn":2,"seq":0,"ts":"2026-07-06T08:30:00.004Z","kind":"turn.input","state_path":"load","payload":{"input":"","intent":"next_item"}}
{"turn":2,"seq":1,"ts":"2026-07-06T08:30:00.005Z","kind":"harness.returned","state_path":"board","payload":{"namespace":"host.starlark.run","data":{"items":[%s],"next_item":%s,"next_item_id":"measure-coverage","results":{"items":[]},"route":"dispatch","has_pending":true,"processed_count":0,"count_summary":"Processed 0 | Passed 0 | Partial 0 | Failed 0 | Skipped 0 | Pending 1","passed_count":0,"partial_count":0,"failed_count":0,"skipped_count":0,"pending_count":1}}}
{"turn":2,"seq":2,"ts":"2026-07-06T08:30:00.006Z","kind":"machine.transition","state_path":"load","payload":{"from":"load","to":"board","intent":"next_item","slots":{}}}
{"turn":3,"seq":0,"ts":"2026-07-06T08:30:00.007Z","kind":"turn.input","state_path":"board","payload":{"input":"","intent":"next_item"}}
{"turn":3,"seq":1,"ts":"2026-07-06T08:30:00.008Z","kind":"harness.returned","state_path":"policy_check","payload":{"namespace":"host.starlark.run","data":{"policy_result":{"status":"ok","message":"profile/model/verifier policy passed"},"error":""}}}
{"turn":3,"seq":2,"ts":"2026-07-06T08:30:00.009Z","kind":"machine.transition","state_path":"board","payload":{"from":"board","to":"policy_check","intent":"next_item","slots":{}}}
{"turn":3,"seq":3,"ts":"2026-07-06T08:30:00.010Z","kind":"machine.transition","state_path":"board","payload":{"from":"policy_check","to":"drive","intent":"policy_ok","slots":{},"synthetic":true}}
{"turn":3,"seq":4,"ts":"2026-07-06T08:30:00.011Z","kind":"harness.returned","state_path":"drive","payload":{"namespace":"host.agent.task","data":{"job_id":"job-export-dispatch"}}}
{"turn":4,"seq":0,"ts":"2026-07-06T08:30:00.012Z","kind":"machine.transition","state_path":"drive","payload":{"from":"drive","to":"needs_human","intent":"__on_complete_target__","slots":{}}}
`, exportManifestPath, exportStatePath, itemJSON, itemJSON, itemJSON)
	require.NoError(t, os.WriteFile(receipt.TracePath, []byte(trace), 0o644))

	receipt, err = svc.Export(context.Background(), receipt.WorkflowID, ExportRequest{TargetDir: exportDir})
	require.NoError(t, err)
	require.NotNil(t, receipt.ExportReport)
	require.NotNil(t, receipt.ExportReport.StarterFlowReplay)
	require.True(t, receipt.ExportReport.StarterFlowReplay.OK)

	flowPath := filepath.Join(exportDir, "flows", "generated.yaml")
	flowBytes, err := os.ReadFile(flowPath)
	require.NoError(t, err)
	flowYAML := string(flowBytes)
	require.Contains(t, flowYAML, "host_cassette: generated.cassette.yaml")
	require.Contains(t, flowYAML, "manifest_path: "+exportManifestPath)
	require.Contains(t, flowYAML, "name: start")
	require.Contains(t, flowYAML, "name: next_item")
	require.NotContains(t, flowYAML, "policy_ok")
	require.NotContains(t, flowYAML, "__on_complete_target__")

	var exportReport ExportReport
	require.NoError(t, readJSON(filepath.Join(exportDir, "export-report.json"), &exportReport))
	require.NotNil(t, exportReport.StarterFlowReplay)
	require.True(t, exportReport.StarterFlowReplay.OK, "starter flow replay should pass: %+v", exportReport.StarterFlowReplay)
	require.Equal(t, 1, exportReport.StarterFlowReplay.Passed)
	require.Zero(t, exportReport.StarterFlowReplay.Failed)
	require.NoFileExists(t, filepath.Join(exportDir, "flows", "generated.state.json"))

	report, err := testrunner.RunFlows(t.Context(), filepath.Join(exportDir, "app", "app.yaml"), flowPath, testrunner.FlowOptions{FailFast: true})
	require.NoError(t, err)
	require.Zero(t, report.Failed)
	require.Equal(t, 1, report.Passed)
	require.Len(t, report.Results, 1)
	require.Len(t, report.Results[0].Turns, 3)
	require.Equal(t, "drive", string(report.Results[0].Turns[2].NewState))
}

func TestValidateManifestRejectsRuntimeLiveModelPolicyMismatch(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	errs := validateManifest(repoRoot, Manifest{
		Version: ManifestVersion,
		Defaults: ManifestDefaults{
			Harness:   "live",
			Profile:   "synthetic-claude",
			Model:     "hf:zai-org/GLM-5.2",
			TraceRoot: ".artifacts/test/traces",
		},
		Items: []ManifestItem{
			{
				ID:    "bad-live-model",
				Title: "Bad live model",
				Story: "stories/punch-list/app.yaml",
				Mode:  "drive",
				Verify: []ManifestVerify{
					{Kind: "story_validate", Story: "stories/punch-list/app.yaml"},
				},
			},
		},
	}, filepath.Join(repoRoot, DefaultTemplateStoryDir))
	require.Contains(t, strings.Join(errs, "\n"), "live work must use profile codex-native or harness: ladder")
	require.Contains(t, strings.Join(errs, "\n"), "live work must use model gpt-5.5 or harness: ladder")
}

func TestServiceExportBlocksBaseStoryWithoutApproval(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := dynamicWorkflowTestDir(t, repoRoot)
	svc := NewService(repoRoot)
	svc.OutputDir = outDir
	svc.TemplateStoryDir = filepath.Join(repoRoot, DefaultTemplateStoryDir)
	svc.Now = func() time.Time { return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC) }

	receipt, err := svc.Create(context.Background(), CreateRequest{
		Goal: "implement dynamic workflows",
		Slug: "dynamic-workflows",
	})
	require.NoError(t, err)

	_, err = svc.Export(context.Background(), receipt.WorkflowID, ExportRequest{
		TargetDir: filepath.Join(repoRoot, "internal", "basestories", "stories", "dynamic-workflows"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "base-story export")
}
