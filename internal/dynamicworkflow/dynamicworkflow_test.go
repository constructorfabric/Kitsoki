package dynamicworkflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goyaml "github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

func TestServiceCreateValidateExport(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := t.TempDir()
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
	require.True(t, receipt.Validation.OK, "receipt should validate")
	require.True(t, strings.Contains(receipt.WorkflowID, "dynamic-workflows"))
	require.FileExists(t, receipt.ManifestPath)
	require.FileExists(t, receipt.EventsPath)
	require.FileExists(t, filepath.Join(receipt.AppPath, "app.yaml"))
	require.Contains(t, receipt.LaunchCommand, "kitsoki run")
	events, err := os.ReadFile(receipt.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.generated")
	require.Contains(t, string(events), "dynamic.workflow.validated")

	loaded, err := svc.ReadReceipt(receipt.WorkflowID)
	require.NoError(t, err)
	require.Equal(t, receipt.WorkflowID, loaded.WorkflowID)
	require.True(t, loaded.Validation.OK)

	exportDir := filepath.Join(t.TempDir(), "exported", "dynamic-workflows")
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
	require.Equal(t, filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml")), world["manifest_path"])
}

func TestServiceExportBlocksBaseStoryWithoutApproval(t *testing.T) {
	repoRoot, err := os.Getwd()
	require.NoError(t, err)
	repoRoot, err = filepath.Abs(filepath.Join(repoRoot, "..", ".."))
	require.NoError(t, err)

	outDir := t.TempDir()
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
