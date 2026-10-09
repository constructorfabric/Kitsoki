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

func writeMCPProvider(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.star")
	require.NoError(t, os.WriteFile(path, []byte(`
def search(ctx):
    return {"tickets": [{"id": "T-1", "title": ctx.inputs["query"]}]}
`), 0o644))
	require.NoError(t, os.WriteFile(path+".yaml", []byte(`
kind: ticket_provider/v1
http:
  enabled: false
`), 0o644))
	return path
}

func TestTicketCallRunsStarlarkProvider(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)
	path := writeMCPProvider(t)

	res, err := callTool(ctx, cs, "ticket.call", map[string]any{
		"script": path,
		"op":     "search",
		"args": map[string]any{
			"query": "from mcp",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "ticket.call: %s", contentText(res))

	var out studio.TicketCallResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	require.True(t, out.OK)
	tickets, ok := out.Data["tickets"].([]any)
	require.True(t, ok, "tickets should decode as an array: %#v", out.Data["tickets"])
	require.Len(t, tickets, 1)
	row := tickets[0].(map[string]any)
	assert.Equal(t, "from mcp", row["title"])
}

func TestTicketCallOmittedReadOnly(t *testing.T) {
	ctx := context.Background()
	cs := newStudioReadOnly(ctx, t)

	tools, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	assert.False(t, names["ticket.call"], "generic ticket.call can mutate remote providers and must be omitted read-only")
}
