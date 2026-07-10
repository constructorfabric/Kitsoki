package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	starlarkhost "kitsoki/internal/host/starlark"
	kitsokimcp "kitsoki/internal/mcp"
)

func connectCodeactInProcess(ctx context.Context, t *testing.T, srv *kitsokimcp.CodeactServer) *mcpsdk.ClientSession {
	t.Helper()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err, "server connect")
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err, "client connect")
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callCodeactEval(ctx context.Context, cs *mcpsdk.ClientSession, args map[string]any) (*mcpsdk.CallToolResult, error) {
	return cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "codeact_eval",
		Arguments: args,
	})
}

func decodeCodeactOK(t *testing.T, res *mcpsdk.CallToolResult) kitsokimcp.CodeactEvalOK {
	t.Helper()
	require.False(t, res.IsError, "expected ok result, got error: %s", contentText(res))
	var out kitsokimcp.CodeactEvalOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	return out
}

func decodeCodeactError(t *testing.T, res *mcpsdk.CallToolResult) kitsokimcp.CodeactEvalError {
	t.Helper()
	require.True(t, res.IsError, "expected error result, got ok: %s", contentText(res))
	var out kitsokimcp.CodeactEvalError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	return out
}

func TestCodeactMCP_EvalRunsSnippetWithInputsAndWorld(t *testing.T) {
	ctx := context.Background()
	srv, err := kitsokimcp.NewCodeactServer(kitsokimcp.CodeactConfig{
		WorkingDir:   ".",
		Capabilities: starlarkhost.DefaultCapabilities(),
	})
	require.NoError(t, err)
	cs := connectCodeactInProcess(ctx, t, srv)

	res, err := callCodeactEval(ctx, cs, map[string]any{
		"snippet": `def main(ctx):
    return {
        "sum": ctx.inputs["a"] + ctx.inputs["b"],
        "ticket": ctx.world.get("ticket_id"),
    }
`,
		"inputs": map[string]any{"a": 2, "b": 3},
		"world":  map[string]any{"ticket_id": "KIT-7"},
	})
	require.NoError(t, err)
	out := decodeCodeactOK(t, res)
	assert.True(t, out.OK)
	assert.EqualValues(t, 5, out.Outputs["sum"])
	assert.Equal(t, "KIT-7", out.Outputs["ticket"])
	assert.Contains(t, out.Capabilities, "world")
}

func TestCodeactMCP_DefaultCapabilitiesDoNotExposeFS(t *testing.T) {
	ctx := context.Background()
	srv, err := kitsokimcp.NewCodeactServer(kitsokimcp.CodeactConfig{WorkingDir: "."})
	require.NoError(t, err)
	cs := connectCodeactInProcess(ctx, t, srv)

	res, err := callCodeactEval(ctx, cs, map[string]any{
		"snippet": `def main(ctx):
    return {"body": ctx.fs.read("go.mod")}
`,
	})
	require.NoError(t, err)
	out := decodeCodeactError(t, res)
	assert.False(t, out.OK)
	assert.Contains(t, out.Error, "codeact_eval")
	assert.Contains(t, out.Error, "fs")
}

func TestCodeactMCP_ExplicitFSReadCapability(t *testing.T) {
	caps, err := starlarkhost.ParseCapabilities(map[string]any{
		"fs": map[string]any{"read": []any{"go.mod"}},
	})
	require.NoError(t, err)

	ctx := context.Background()
	srv, err := kitsokimcp.NewCodeactServer(kitsokimcp.CodeactConfig{
		WorkingDir:   "../..",
		Capabilities: caps,
	})
	require.NoError(t, err)
	cs := connectCodeactInProcess(ctx, t, srv)

	res, err := callCodeactEval(ctx, cs, map[string]any{
		"snippet": `def main(ctx):
    return {"has_module": "module kitsoki" in ctx.fs.read("go.mod")}
`,
	})
	require.NoError(t, err)
	out := decodeCodeactOK(t, res)
	assert.Equal(t, true, out.Outputs["has_module"])
	require.Len(t, out.Inspections, 1)
	assert.Equal(t, "read", out.Inspections[0].Op)
	assert.Equal(t, "go.mod", out.Inspections[0].Target)
}

func TestCodeactMCP_RejectsStandaloneHostCapability(t *testing.T) {
	caps, err := starlarkhost.ParseCapabilities(map[string]any{
		"host": map[string]any{"verbs": []any{"host.graph.load"}},
	})
	require.NoError(t, err)

	_, err = kitsokimcp.NewCodeactServer(kitsokimcp.CodeactConfig{
		WorkingDir:   ".",
		Capabilities: caps,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host capabilities")
}
