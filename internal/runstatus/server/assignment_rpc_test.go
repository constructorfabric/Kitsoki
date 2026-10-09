package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/assignment"
	"kitsoki/internal/runstatus/server"
)

func TestAssignmentRPC_AssignListAndStaleVersion(t *testing.T) {
	store, err := assignment.Open(filepath.Join(t.TempDir(), "assignments.jsonl"))
	require.NoError(t, err)
	def := &app.AppDef{App: app.AppMeta{ID: "assigned", Version: "1"}, States: map[string]*app.State{
		"build": {Assignment: &app.AssignmentPolicy{Role: "developer", AllowReassign: true}},
	}}
	ts := httptest.NewServer(server.New(twoTurnTrace(t), def, server.WithAssignmentStore(store)).Handler())
	defer ts.Close()
	var assigned assignment.Record
	rpcCall(t, ts, "runstatus.assignment.assign", map[string]any{"session_id": "s-1", "room_path": "build", "principal": "operator", "expected_version": 0}, &assigned)
	require.EqualValues(t, 1, assigned.Version)
	var list []assignment.Record
	rpcCall(t, ts, "runstatus.assignment.list", map[string]any{"session_id": "s-1"}, &list)
	require.Len(t, list, 1)
	require.Equal(t, "operator", list[0].Principal)
	code, data := rpcAssignmentError(t, ts, "runstatus.assignment.reassign", map[string]any{"session_id": "s-1", "room_path": "build", "principal": "next", "expected_version": 0})
	require.Equal(t, -32003, code)
	var current assignment.Record
	require.NoError(t, json.Unmarshal([]byte(data), &current))
	require.Equal(t, "operator", current.Principal)
}

func rpcAssignmentError(t *testing.T, ts *httptest.Server, method string, params map[string]any) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	var frame struct {
		Error struct {
			Code int    `json:"code"`
			Data string `json:"data"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&frame))
	return frame.Error.Code, frame.Error.Data
}
