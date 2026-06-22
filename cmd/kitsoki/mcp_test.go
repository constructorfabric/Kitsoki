package main

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

// TestMCPAttachEntry_RoundTrips covers slice task 2.4: the emitted .mcp.json
// entry resolves command/args to this binary and round-trips through JSON. It
// mirrors the writeMCPConfigTempfile shape: {"mcpServers":{"kitsoki":{"command":
// ...,"args":["mcp","--stories-dir",...]}}}.
func TestMCPAttachEntry_RoundTrips(t *testing.T) {
	b, err := mcpAttachJSON("/usr/local/bin/kitsoki", "/work/stories")
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got), "emitted entry must be valid JSON")

	servers, ok := got["mcpServers"].(map[string]any)
	require.True(t, ok, "mcpServers object present")
	entry, ok := servers["kitsoki"].(map[string]any)
	require.True(t, ok, "kitsoki server entry present")

	assert.Equal(t, "/usr/local/bin/kitsoki", entry["command"], "command resolves to this binary")

	args, ok := entry["args"].([]any)
	require.True(t, ok, "args is an array")
	assert.Equal(t, []any{"mcp", "--stories-dir", "/work/stories"}, args,
		"args invoke `mcp` with the stories dir")
}

// TestMCPAttachEntry_OmitsEmptyStoriesDir confirms the --stories-dir flag is
// dropped when no dir is given (the server resolves stories elsewhere).
func TestMCPAttachEntry_OmitsEmptyStoriesDir(t *testing.T) {
	entry := mcpAttachEntry("kitsoki", "")
	servers := entry["mcpServers"].(map[string]any)
	kit := servers["kitsoki"].(map[string]any)
	assert.Equal(t, []any{"mcp"}, kit["args"], "no --stories-dir when dir is empty")
}

// TestMCPCmd_Registered confirms `mcp` is wired into the root command tree and
// declares its documented flags (--stories-dir, --db, --harness, --workspace)
// with the no-LLM replay default.
func TestMCPCmd_Registered(t *testing.T) {
	root := newRootCmd()
	var mcp *cobraCommandStub
	for _, c := range root.Commands() {
		if c.Name() == "mcp" {
			mcp = &cobraCommandStub{}
			// Verify the documented flags exist.
			for _, f := range []string{"stories-dir", "db", "harness", "workspace"} {
				require.NotNil(t, c.Flags().Lookup(f), "mcp must declare --%s", f)
			}
			// No-LLM default: --harness defaults to replay.
			h := c.Flags().Lookup("harness")
			require.NotNil(t, h)
			assert.Equal(t, "replay", h.DefValue, "--harness defaults to the no-LLM replay harness")
			break
		}
	}
	require.NotNil(t, mcp, "root command tree must include `mcp`")
}

func TestMCPTestCmd_Registered(t *testing.T) {
	root := newRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "mcp-test" {
			found = true
			for _, f := range []string{"server-command", "server-arg", "stories-dir", "workspace", "read-only", "timeout", "list-tools", "tool", "tool-args", "calls"} {
				require.NotNil(t, c.Flags().Lookup(f), "mcp-test must declare --%s", f)
			}
			assert.Equal(t, "10s", c.Flags().Lookup("timeout").DefValue)
			break
		}
	}
	require.True(t, found, "root command tree must include `mcp-test`")
}

func TestMCPTestServerArgs_DefaultsMirrorMCPCmd(t *testing.T) {
	args := studioMCPTestServerArgs(nil, "/work/stories", "/work/story", true)
	assert.Equal(t, []string{"mcp", "--stories-dir", "/work/stories", "--workspace", "/work/story", "--read-only"}, args)
}

func TestMCPTestServerArgs_OverrideWins(t *testing.T) {
	args := studioMCPTestServerArgs([]string{"mcp", "--workspace", "custom"}, "/ignored", "/ignored-too", true)
	assert.Equal(t, []string{"mcp", "--workspace", "custom"}, args)
}

func TestRunStudioMCPTestSession_DefaultSmoke(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	report, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ListTools:     true,
	})
	require.NoError(t, err)

	assert.True(t, report.OK)
	assert.Equal(t, []string{"kitsoki", "mcp"}, report.Server)
	assert.Contains(t, report.Tools, "studio.ping")
	assert.Contains(t, report.Tools, "studio.handles")
	require.Len(t, report.ToolRuns, 2)
	assert.Equal(t, "studio.ping", report.ToolRuns[0].Name)
	assert.False(t, report.ToolRuns[0].IsError)
	assert.Equal(t, "studio.handles", report.ToolRuns[1].Name)
}

func TestRunStudioMCPTestSession_SingleTool(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	report, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ToolName:      "studio.ping",
	})
	require.NoError(t, err)

	assert.True(t, report.OK)
	require.Len(t, report.ToolRuns, 1)
	assert.Equal(t, "studio.ping", report.ToolRuns[0].Name)
}

func TestRunStudioMCPTestSession_SequentialCallsShareSession(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	report, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ListTools:     false,
		Calls: []studioMCPTestCall{
			{
				Name: "session.new",
				Args: map[string]any{
					"story_path": "../../testdata/apps/cloak/app.yaml",
					"key":        "workflow-smoke",
				},
			},
			{
				Name: "session.submit",
				Args: map[string]any{
					"handle": "workflow-smoke",
					"intent": "go",
					"slots": map[string]any{
						"direction": "west",
					},
				},
			},
			{
				Name: "session.inspect",
				Args: map[string]any{
					"handle": "workflow-smoke",
				},
			},
		},
	})
	require.NoError(t, err)

	assert.True(t, report.OK)
	require.Len(t, report.ToolRuns, 3)
	assert.Equal(t, "session.new", report.ToolRuns[0].Name)
	assert.Equal(t, "session.submit", report.ToolRuns[1].Name)
	assert.Equal(t, "session.inspect", report.ToolRuns[2].Name)
	assert.False(t, report.ToolRuns[0].IsError)
	assert.False(t, report.ToolRuns[1].IsError)
	assert.False(t, report.ToolRuns[2].IsError)

	structured, ok := report.ToolRuns[2].Result["structuredContent"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cloakroom", structured["state"])
}

// cobraCommandStub is a presence marker for the registration test (the real
// *cobra.Command is exercised directly above).
type cobraCommandStub struct{}

func connectStudioTestClient(ctx context.Context, t *testing.T, srv *studio.Server) *mcpsdk.ClientSession {
	t.Helper()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}
