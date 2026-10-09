package graphsrv

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRecorded_RecoversHandlerPanic exercises the `recorded` wrapper's
// panic firewall: mcpsdk v1.0.0 does not recover tool-handler panics, so
// without this recovery a single panicking call would kill the whole stdio
// server process (observed risk: the server dying mid-session takes every
// bound catalog's tools with it). The wrapper must convert the panic into
// a CodeInternal error payload, return err == nil, and still append an
// evidence entry to the ring buffer.
func TestRecorded_RecoversHandlerPanic(t *testing.T) {
	deps := &Deps{Recorder: NewRecorder()}
	h := recorded(deps, "graph.test", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		panic("boom: simulated handler bug")
	})

	req := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Name: "graph.test"}}
	res, err := h(context.Background(), req) // must not panic
	if err != nil {
		t.Fatalf("recovered call must return err == nil, got: %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("recovered call must return an isError result, got: %+v", res)
	}
	ep, ok := res.StructuredContent.(*ErrorPayload)
	if !ok {
		t.Fatalf("StructuredContent is not an *ErrorPayload: %T", res.StructuredContent)
	}
	if ep.Code != CodeInternal {
		t.Fatalf("expected code %q, got %q (%s)", CodeInternal, ep.Code, ep.Error)
	}

	snap := deps.Recorder.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 evidence entry after the recovered call, got %d", len(snap))
	}
	if snap[0].ResultCode != CodeInternal {
		t.Fatalf("evidence result_code: expected %q, got %q", CodeInternal, snap[0].ResultCode)
	}
}

// TestRecorded_PassthroughStillRecords guards the wrapper's original
// contract after the panic-firewall change: a normal (non-panicking)
// handler's result passes through unmodified and is recorded once.
func TestRecorded_PassthroughStillRecords(t *testing.T) {
	deps := &Deps{Recorder: NewRecorder()}
	want := okResult(map[string]any{"ok": true})
	h := recorded(deps, "graph.test", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return want, nil
	})

	req := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Name: "graph.test"}}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != want {
		t.Fatalf("result must pass through unmodified")
	}
	snap := deps.Recorder.Snapshot()
	if len(snap) != 1 || snap[0].ResultCode != "ok" {
		t.Fatalf("expected exactly one ok evidence entry, got %+v", snap)
	}
}
