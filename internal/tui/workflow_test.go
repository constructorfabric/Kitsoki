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
	writeMinimalWorkflowTrace(t, filepath.Join(draftDir, "trace.jsonl"), exportDir)
	m = runTurnBlocking(t, m, fmt.Sprintf("/workflow export %s --target %s", workflowID, exportDir))
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "export report:")
	require.Contains(t, tx, "starter flow replay: ok (1 passed, 0 failed)")
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

var _ = tuipkg.ModeOnPath
