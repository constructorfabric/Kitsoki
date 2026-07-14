package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/dynamicworkflow"
)

func TestWorkflow_CreateValidateExport(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	slug := "cli-dwf-test"

	stdout, err := runKitsoki(t,
		"workflow", "create", "implement dynamic workflows end to end",
		"--root", repoRoot,
		"--slug", slug,
	)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	require.NotEmpty(t, lines)
	fields := strings.Fields(lines[0])
	require.Len(t, fields, 2)
	workflowID := fields[1]

	draftDir := filepath.Join(repoRoot, ".artifacts", "dynamic-workflows", workflowID)
	t.Cleanup(func() { _ = os.RemoveAll(draftDir) })
	require.FileExists(t, filepath.Join(draftDir, "events.jsonl"))

	stdout, err = runKitsoki(t, "workflow", "validate", workflowID, "--root", repoRoot, "--json")
	require.NoError(t, err)
	require.Contains(t, stdout, `"ok": true`)
	require.FileExists(t, filepath.Join(draftDir, "events.jsonl"))

	exportDir := filepath.Join(t.TempDir(), "exported", slug)
	writeMinimalWorkflowTrace(t, filepath.Join(draftDir, "trace.jsonl"), exportDir)
	stdout, err = runKitsoki(t, "workflow", "export", workflowID, "--root", repoRoot, "--target", exportDir)
	require.NoError(t, err)
	require.Contains(t, stdout, "export: "+exportDir)
	require.Contains(t, stdout, "starter flow replay: ok (1 passed, 0 failed)")
	require.FileExists(t, filepath.Join(exportDir, "app", "app.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "manifest.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "launch.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "export-report.json"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
	require.FileExists(t, filepath.Join(draftDir, "events.jsonl"))
}

func TestWorkflow_CreateFromStructuredBrief(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	briefPath := filepath.Join(t.TempDir(), "brief.yaml")
	require.NoError(t, os.WriteFile(briefPath, []byte(`goal: fix dynamic workflow audit problems
slug: cli-structured-dwf
items:
  - id: tui-proof
    title: TUI operation handle proof
    owner_scope: internal/tui
    gate: go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1
  - id: corpus-report
    title: Demo corpus runner/report clarity
    owner_scope: internal/testrunner/operation_demo_corpus_test.go
    gate: go test ./internal/testrunner -run TestOperationDemoCorpus -count=1
`), 0o644))

	stdout, err := runKitsoki(t,
		"workflow", "create",
		"--root", repoRoot,
		"--brief", briefPath,
		"--json",
	)
	require.NoError(t, err)
	var receipt dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(stdout), &receipt))
	t.Cleanup(func() { _ = os.RemoveAll(receipt.DraftDir) })
	require.Equal(t, "cli-structured-dwf", receipt.Slug)
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

func writeMinimalWorkflowTrace(t *testing.T, tracePath, exportDir string) {
	t.Helper()
	exportManifestPath := filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml"))
	exportStatePath := filepath.ToSlash(filepath.Join(exportDir, "flows", "generated.state.json"))
	trace := fmt.Sprintf(`{"kind":"session.header","schema_version":1,"written_at":"2026-07-06T08:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-07-06T08:00:00.001Z","kind":"turn.input","state_path":"idle","payload":{"input":"start","intent":""}}
{"turn":1,"seq":1,"ts":"2026-07-06T08:00:00.002Z","kind":"harness.returned","state_path":"load","payload":{"namespace":"host.starlark.run","data":{"manifest_path":%q,"state_path":%q,"items":[],"item_count":"0","error":""}}}
{"turn":1,"seq":2,"ts":"2026-07-06T08:00:00.003Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"load","intent":"start","slots":{}}}
`, exportManifestPath, exportStatePath)
	require.NoError(t, os.WriteFile(tracePath, []byte(trace), 0o644))
}
