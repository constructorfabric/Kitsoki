package studio_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// trace_tools_test.go — verification for trace.read / trace.to_flow. Every test
// is deterministic and LLM-free: it writes a hand-authored JSONL trace and reads
// it back / converts it through the shipped testrunner.ConvertTraceToFlow.

// newStudioNoWorkspace builds a studio server with NO bound workspace — the
// posture the path-based tools (trace.*, vcs.*, gh.*) operate in.
func newStudioNoWorkspace(ctx context.Context, t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	return connectInProcess(ctx, t, srv)
}

// newStudioReadOnly builds a read-only studio server (the Q&A surface) so a test
// can assert which write tools are dropped.
func newStudioReadOnly(ctx context.Context, t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()), studio.ReadOnly())
	return connectInProcess(ctx, t, srv)
}

// assertRejected asserts a call was rejected — either by the SDK's schema
// validation (a transport error, for a missing schema-required field) or by the
// handler's own guard (a structured tool error). Both are valid rejections.
func assertRejected(t *testing.T, res *mcpsdk.CallToolResult, err error) {
	t.Helper()
	if err != nil {
		return
	}
	require.NotNil(t, res)
	assert.True(t, res.IsError, "expected a rejection, got: %s", contentText(res))
}

// traceFixture is a small trace with a transition and an error event, so the
// summary/error projections have something to find.
const traceFixture = `{"kind":"session.header","schema_version":1,"written_at":"2026-06-01T00:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-06-01T00:00:00.001Z","kind":"turn.input","state_path":"idle","payload":{"input":"go","intent":""}}
{"turn":1,"seq":1,"ts":"2026-06-01T00:00:00.002Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"reproducing","intent":"start","slots":{}}}
{"turn":2,"seq":0,"ts":"2026-06-01T00:00:00.003Z","kind":"harness.error","state_path":"reproducing","payload":{"phase":"dispatch","error":"boom: provider down"}}
`

// tinyTraceToFlow is a 2-turn trace with host calls — mirrors the testrunner
// converter's own fixture so trace.to_flow's wrapper (path defaulting + file
// writes) is exercised on a trace the converter accepts.
const tinyTraceToFlow = `{"kind":"session.header","schema_version":1,"written_at":"2026-06-01T00:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-06-01T00:00:00.002Z","kind":"turn.input","state_path":"idle","payload":{"input":"hello","intent":""}}
{"turn":1,"seq":1,"ts":"2026-06-01T00:00:00.003Z","kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.agent.converse","data":{"answer":"first reply"}}}
{"turn":1,"seq":2,"ts":"2026-06-01T00:00:00.004Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"idle","intent":"discuss","slots":{"message":"hello"}}}
`

func TestTraceRead_FiltersAndSummarizes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(traceFixture), 0o644))
	cs := newStudioNoWorkspace(ctx, t)

	type errDigest struct {
		Turn    int64  `json:"turn"`
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	type readOut struct {
		OK         bool              `json:"ok"`
		SourcePath string            `json:"source_path"`
		Events     []json.RawMessage `json:"events"`
		LastTurn   int64             `json:"last_turn"`
		Summary    struct {
			ByKind map[string]int `json:"by_kind"`
			Errors []errDigest    `json:"errors"`
		} `json:"summary"`
	}

	// Full read: summary counts every kind; the error event is surfaced.
	var full readOut
	res, err := callTool(ctx, cs, "trace.read", map[string]any{"path": path})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &full))
	assert.True(t, full.OK)
	assert.Equal(t, path, full.SourcePath)
	assert.Equal(t, int64(2), full.LastTurn)
	assert.Equal(t, 1, full.Summary.ByKind["machine.transition"])
	require.Len(t, full.Summary.Errors, 1)
	assert.Equal(t, "harness.error", full.Summary.Errors[0].Kind)
	assert.Contains(t, full.Summary.Errors[0].Message, "boom: provider down")

	// errors_only: returns just the error event.
	var errsOnly readOut
	res, err = callTool(ctx, cs, "trace.read", map[string]any{"path": path, "errors_only": true})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &errsOnly))
	require.Len(t, errsOnly.Events, 1)

	// kinds filter: only the transition event comes back.
	var byKind readOut
	res, err = callTool(ctx, cs, "trace.read", map[string]any{"path": path, "kinds": []string{"machine.transition"}})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &byKind))
	require.Len(t, byKind.Events, 1)

	// session_id resolution under a root: the basename substring matches.
	var byID readOut
	res, err = callTool(ctx, cs, "trace.read", map[string]any{"session_id": "sess", "root": dir})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &byID))
	assert.Equal(t, path, byID.SourcePath)

	// ticket_id resolution scans qualified imported world keys, so a root app
	// trace can still be found by the bugfix ticket it imported.
	ticketPath := filepath.Join(dir, "root-app-session.jsonl")
	require.NoError(t, os.WriteFile(ticketPath, []byte(`{"turn":2,"kind":"world.update","payload":{"set":{"root__bf__ticket_id":"64"}}}`+"\n"), 0o644))
	var byTicket readOut
	res, err = callTool(ctx, cs, "trace.read", map[string]any{"ticket_id": "64", "root": dir})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &byTicket))
	assert.Equal(t, ticketPath, byTicket.SourcePath)

	// A miss is a structured error, not a panic.
	res, err = callTool(ctx, cs, "trace.read", map[string]any{"session_id": "no-such-trace", "root": dir})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestTraceToFlow_WritesFixtureAndCassette(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "t.jsonl")
	require.NoError(t, os.WriteFile(tracePath, []byte(tinyTraceToFlow), 0o644))
	cs := newStudioNoWorkspace(ctx, t)

	outPath := filepath.Join(dir, "demo.yaml")
	var out struct {
		OK           bool   `json:"ok"`
		FlowPath     string `json:"flow_path"`
		CassettePath string `json:"cassette_path"`
		NumTurns     int    `json:"num_turns"`
		NumEpisodes  int    `json:"num_episodes"`
	}
	res, err := callTool(ctx, cs, "trace.to_flow", map[string]any{
		"trace": tracePath, "app": "../app.yaml", "out": outPath,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.True(t, out.OK)
	assert.Equal(t, outPath, out.FlowPath)
	assert.Equal(t, 1, out.NumTurns)
	assert.FileExists(t, outPath)
	// The trace has a host call → a cassette is written beside the fixture.
	assert.Equal(t, filepath.Join(dir, "demo.cassette.yaml"), out.CassettePath)
	assert.FileExists(t, out.CassettePath)

	// Arg validation: trace is required.
	res, err = callTool(ctx, cs, "trace.to_flow", map[string]any{"app": "../app.yaml", "out": outPath})
	assertRejected(t, res, err)
}
