package studio_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/dynamicworkflow"
	studio "kitsoki/internal/mcp/studio"
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
	require.NotEmpty(t, launched.SessionID, "launch should open a studio session")
	require.NotEmpty(t, launched.SessionHandle, "launch should return a studio handle")
	require.FileExists(t, launched.EventsPath)
	events, err := os.ReadFile(launched.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.launched")

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
