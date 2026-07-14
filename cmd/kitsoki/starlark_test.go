package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeStarlarkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "derive.star")
	require.NoError(t, os.WriteFile(script, []byte(`def main(ctx):
    return {"greeting": "hello " + ctx.inputs["name"]}
`), 0o644))
	require.NoError(t, os.WriteFile(script+".yaml", []byte(`inputs:
  name: {type: string, required: true}
outputs:
  greeting: {type: string}
`), 0o644))
	return script
}

func TestStarlarkRunCLIUsesStorySidecar(t *testing.T) {
	script := writeStarlarkFixture(t)
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"starlark", "run", script, "--inputs", `{"name":"Ada"}`})
	require.NoError(t, root.Execute())

	var got map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "hello Ada", got["greeting"])
}

func TestStarlarkRunJSONRPCUsesSameHandler(t *testing.T) {
	script := writeStarlarkFixture(t)
	result, err := dispatchAgentRPC(context.Background(), "starlark.run", map[string]any{
		"script": script,
		"inputs": map[string]any{"name": "Lin"},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "hello Lin", result.Data["greeting"])
}

func TestAgentServeStarlarkRunOverJSONRPC(t *testing.T) {
	script := writeStarlarkFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sockPath := startTestAgentServer(t, ctx)

	response := sendRPC(t, sockPath, rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`7`),
		Method:  "starlark.run",
		Params: map[string]any{
			"script": script,
			"inputs": map[string]any{"name": "Socket"},
		},
	})
	require.Nil(t, response.Error)
	result, ok := response.Result.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "hello Socket", result["greeting"])
}
