package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/dynamicworkflow"
	"kitsoki/internal/runstatus"
	server "kitsoki/internal/runstatus/server"
)

type workflowProviderStub struct{}

func (workflowProviderStub) Get(string) (server.Entry, bool) { return server.Entry{}, false }
func (workflowProviderStub) List() []runstatus.SessionHeader { return nil }
func (workflowProviderStub) NewSession(context.Context, string) (string, error) {
	return "sess-launch", nil
}
func (workflowProviderStub) Reload(context.Context, string) (bool, error) { return false, nil }
func (workflowProviderStub) Staleness(context.Context, string) (bool, string, error) {
	return false, "", nil
}
func (workflowProviderStub) ListStories() []server.StoryHeader     { return nil }
func (workflowProviderStub) Rescan() ([]server.StoryHeader, error) { return nil, nil }

func TestWorkflowRPCs_CreateValidateLaunchExport(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	srv := server.NewMulti(workflowProviderStub{}, server.WithWorkflowRoot(root))
	handler := srv.Handler()

	res := rpcResultRawHandler(t, handler, "runstatus.workflow.create", map[string]any{
		"goal": "implement dynamic workflows over runstatus",
		"slug": "rpc-dwf-test",
	})
	var created dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(res), &created))
	require.NotEmpty(t, created.WorkflowID)

	draftDir := filepath.Join(root, ".artifacts", "dynamic-workflows", created.WorkflowID)
	t.Cleanup(func() { _ = os.RemoveAll(draftDir) })
	require.FileExists(t, filepath.Join(draftDir, "receipt.json"))

	res = rpcResultRawHandler(t, handler, "runstatus.workflow.validate", map[string]any{
		"workflow_id": created.WorkflowID,
	})
	var validated dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(res), &validated))
	require.True(t, validated.Validation.OK)

	events, err := os.ReadFile(validated.EventsPath)
	require.NoError(t, err)
	require.Contains(t, string(events), "dynamic.workflow.validated")

	res = rpcResultRawHandler(t, handler, "runstatus.workflow.launch", map[string]any{
		"workflow_id": created.WorkflowID,
	})
	var launched dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(res), &launched))
	require.Equal(t, "sess-launch", launched.SessionID)
	require.Equal(t, "/s/sess-launch", launched.URL)
	require.NotEmpty(t, launched.TracePath)
	require.Contains(t, launched.TracePath, ".artifacts")
	require.Contains(t, launched.TracePath, "dynamic-workflows")

	events, err = os.ReadFile(launched.EventsPath)
	require.NoError(t, err)
	content := string(events)
	require.Contains(t, content, "dynamic.workflow.launched")
	require.Contains(t, content, "dynamic.workflow.url_assigned")

	exportDir := filepath.Join(t.TempDir(), "exported", "rpc-dwf-test")
	exportManifestPath := filepath.ToSlash(filepath.Join(exportDir, "manifest.yaml"))
	exportStatePath := filepath.ToSlash(filepath.Join(exportDir, "flows", "generated.state.json"))
	trace := fmt.Sprintf(`{"kind":"session.header","schema_version":1,"written_at":"2026-07-06T08:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-07-06T08:00:00.001Z","kind":"turn.input","state_path":"idle","payload":{"input":"start","intent":""}}
{"turn":1,"seq":1,"ts":"2026-07-06T08:00:00.002Z","kind":"harness.returned","state_path":"load","payload":{"namespace":"host.starlark.run","data":{"manifest_path":%q,"state_path":%q,"items":[],"item_count":"0","error":""}}}
{"turn":1,"seq":2,"ts":"2026-07-06T08:00:00.003Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"load","intent":"start","slots":{}}}
`, exportManifestPath, exportStatePath)
	require.NoError(t, os.WriteFile(launched.TracePath, []byte(trace), 0o644))

	res = rpcResultRawHandler(t, handler, "runstatus.workflow.export", map[string]any{
		"workflow_id": created.WorkflowID,
		"target":      exportDir,
	})
	var exported dynamicworkflow.Receipt
	require.NoError(t, json.Unmarshal([]byte(res), &exported))
	require.Equal(t, exportDir, exported.ExportPath)
	require.FileExists(t, filepath.Join(exportDir, "README.md"))
	require.FileExists(t, filepath.Join(exportDir, "flows", "generated.yaml"))
	require.FileExists(t, filepath.Join(exportDir, "export-report.json"))
	require.NotNil(t, exported.ExportReport)
	require.NotNil(t, exported.ExportReport.StarterFlowReplay)
	require.True(t, exported.ExportReport.StarterFlowReplay.OK, "RPC export response should surface starter replay status: %+v", exported.ExportReport.StarterFlowReplay)
	require.Equal(t, 1, exported.ExportReport.StarterFlowReplay.Passed)
	require.Zero(t, exported.ExportReport.StarterFlowReplay.Failed)

	var report dynamicworkflow.ExportReport
	require.NoError(t, readJSONFile(filepath.Join(exportDir, "export-report.json"), &report))
	require.NotNil(t, report.StarterFlowReplay)
	require.True(t, report.StarterFlowReplay.OK)
}

func readJSONFile(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func rpcResultRawHandler(t *testing.T, handler http.Handler, method string, params map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var frame struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&frame))
	require.Nil(t, frame.Error, "rpc %s returned error: %+v", method, frame.Error)
	return string(frame.Result)
}
