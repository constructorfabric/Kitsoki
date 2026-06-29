package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
	stdout, err = runKitsoki(t, "workflow", "export", workflowID, "--root", repoRoot, "--target", exportDir, "--json")
	require.NoError(t, err)
	require.Contains(t, stdout, `"export_path":`)
	require.FileExists(t, filepath.Join(exportDir, "app", "app.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "manifest.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "launch.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "export-report.json"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
	require.FileExists(t, filepath.Join(draftDir, "events.jsonl"))
}
