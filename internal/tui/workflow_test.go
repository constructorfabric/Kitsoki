package tui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

func TestWorkflowSlashCreateValidateExport(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	slug := "tui-dwf-test"
	m = runTurnBlocking(t, m, "/workflow create --slug "+slug+" implement workflow commands from the TUI")
	tx := extractTranscript(t, m)
	workflowID := extractWorkflowID(t, tx)
	require.NotEmpty(t, workflowID)

	draftDir := filepath.Join(repoRoot, ".artifacts", "dynamic-workflows", workflowID)
	t.Cleanup(func() { _ = os.RemoveAll(draftDir) })
	require.FileExists(t, filepath.Join(draftDir, "receipt.json"))

	m = runTurnBlocking(t, m, "/workflow validate "+workflowID)
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "validation: ok")

	exportDir := filepath.Join(t.TempDir(), "exported", slug)
	m = runTurnBlocking(t, m, fmt.Sprintf("/workflow export %s --target %s", workflowID, exportDir))
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "export report:")
	require.FileExists(t, filepath.Join(exportDir, "app", "app.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "manifest.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
}

func extractWorkflowID(t *testing.T, transcript string) string {
	t.Helper()
	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "workflow dwf_") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1]
			}
		}
	}
	return ""
}

var _ = tuipkg.ModeOnPath
