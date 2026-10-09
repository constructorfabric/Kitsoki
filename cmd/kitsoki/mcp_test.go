package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
	"kitsoki/internal/webshot"
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

func TestShouldWriteMCPStartupError(t *testing.T) {
	assert.False(t, shouldWriteMCPStartupError(nil))
	assert.False(t, shouldWriteMCPStartupError(context.Canceled))
	assert.False(t, shouldWriteMCPStartupError(fmt.Errorf("stdio peer closed: %w", context.Canceled)))
	assert.True(t, shouldWriteMCPStartupError(errors.New("listen failed")))
	assert.True(t, shouldWriteMCPStartupError(context.DeadlineExceeded))
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
			for _, f := range []string{"stories-dir", "db", "harness", "workspace", "flow"} {
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

func TestMCPWebShotFunc_RendersLiveStudioHandle(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(nil)
	sh, err := sess.OpenDrivingSession(ctx, studio.OpenDrivingSessionParams{
		Key:       "web",
		StoryPath: "../../testdata/apps/cloak/app.yaml",
		TracePath: filepath.Join(t.TempDir(), "trace.jsonl"),
	})
	require.NoError(t, err)

	repoRoot := t.TempDir()
	helper := filepath.Join(repoRoot, "tools", "runstatus", "web-shot.ts")
	require.NoError(t, os.MkdirAll(filepath.Dir(helper), 0o755))
	require.NoError(t, os.WriteFile(helper, []byte("// test helper\n"), 0o644))

	browser := &fakeWebShotBrowser{png: []byte("png")}
	fn := mcpWebShotResultFuncWithOptions(sess, mcpWebShotOptions{
		RepoRoot: repoRoot,
		Browser:  browser,
		Server: func(h http.Handler) webshot.ServerProvider {
			require.NotNil(t, h)
			return fakeWebShotServer{base: "http://127.0.0.1:12345"}
		},
	})

	result, err := fn(ctx, studio.WebRenderSpec{SessionID: string(sh.SID), Query: map[string]string{"chat": "chat-123"}})
	require.NoError(t, err)
	assert.Equal(t, []byte("png"), result.PNG)
	assert.JSONEq(t, `{"ok":true}`, string(result.SemanticJSON))
	assert.JSONEq(t, `{"schemaVersion":1,"events":[{"type":4},{"type":2}]}`, string(result.RRWebJSON))
	assert.Equal(t, "http://127.0.0.1:12345#/s/"+string(sh.SID)+"?chat=chat-123", browser.url)
}

func TestMCPWebShotFunc_RejectsSpecForm(t *testing.T) {
	fn := mcpWebShotFuncWithOptions(studio.NewStudioSession(nil), mcpWebShotOptions{})
	_, err := fn(context.Background(), studio.WebRenderSpec{StoryPath: "stories/bugfix", State: "idle"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supports live handles")
}

func TestMCPTestCmd_Registered(t *testing.T) {
	root := newRootCmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "mcp-test" {
			found = true
			for _, f := range []string{"server-command", "server-arg", "stories-dir", "workspace", "read-only", "timeout", "list-tools", "tool", "tool-args", "expect", "expect-contains", "expect-exists", "expect-error", "calls"} {
				require.NotNil(t, c.Flags().Lookup(f), "mcp-test must declare --%s", f)
			}
			assert.Equal(t, "10s", c.Flags().Lookup("timeout").DefValue)
			break
		}
	}
	require.True(t, found, "root command tree must include `mcp-test`")
}

type fakeWebShotServer struct {
	base string
}

func (s fakeWebShotServer) Serve(context.Context) (string, func(), error) {
	return s.base, func() {}, nil
}

type fakeWebShotBrowser struct {
	png []byte
	url string
}

func (b *fakeWebShotBrowser) Capture(_ context.Context, req webshot.CaptureRequest) error {
	b.url = req.URL
	if req.SemanticOutPath != "" {
		if err := os.WriteFile(req.SemanticOutPath, []byte(`{"ok":true}`), 0o644); err != nil {
			return err
		}
	}
	if req.RRWebOutPath != "" {
		if err := os.WriteFile(req.RRWebOutPath, []byte(`{"schemaVersion":1,"events":[{"type":4},{"type":2}]}`), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(req.OutPath, b.png, 0o644)
}

func TestMCPTestServerArgs_DefaultsUseIsolatedDB(t *testing.T) {
	args := studioMCPTestServerArgs(nil, "/work/stories", "/work/story", true, "/tmp/kitsoki-mcp-test-root")
	require.Len(t, args, 8)
	assert.Equal(t, []string{"mcp", "--stories-dir", "/work/stories", "--workspace", "/work/story", "--read-only", "--db"}, args[:7])
	assert.Contains(t, args[7], "/tmp/kitsoki-mcp-test-root/kitsoki-mcp-test-")
	assert.Contains(t, args[7], "/sessions.db")
}

func TestMCPTestServerArgs_OverrideWins(t *testing.T) {
	args := studioMCPTestServerArgs([]string{"mcp", "--workspace", "custom"}, "/ignored", "/ignored-too", true, "/ignored-temp")
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

func TestRunStudioMCPTestSession_SingleToolExpectedError(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	report, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ListTools:     false,
		ToolName:      "story.validate",
		ToolExpectContains: map[string]string{
			"content.0.text": "NO_WORKSPACE",
		},
		ToolExpectError: true,
	})
	require.NoError(t, err)

	assert.True(t, report.OK)
	require.Len(t, report.ToolRuns, 1)
	assert.Equal(t, "story.validate", report.ToolRuns[0].Name)
	assert.True(t, report.ToolRuns[0].IsError)
}

func TestRunStudioMCPTestSession_ExpectedErrorButToolSucceedsFailsRun(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	_, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand:   "kitsoki",
		ServerArgs:      []string{"mcp"},
		ListTools:       false,
		ToolName:        "studio.ping",
		ToolExpectError: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "studio.ping expected a structured tool error")
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
				Expect: map[string]any{
					"structuredContent.state":             "cloakroom",
					"structuredContent.allowed_intents.0": "go",
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
	assert.Equal(t, 1, report.ToolRuns[2].Attempts)

	structured, ok := report.ToolRuns[2].Result["structuredContent"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cloakroom", structured["state"])
}

func TestRunStudioMCPTestSession_SaveFeedsLaterCall(t *testing.T) {
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
					"key":        "saved-handle",
				},
				Save: map[string]string{
					"handle": "structuredContent.handle",
				},
			},
			{
				Name: "session.inspect",
				Args: map[string]any{
					"handle": "${handle}",
				},
				Expect: map[string]any{
					"structuredContent.state": "foyer",
				},
			},
		},
	})
	require.NoError(t, err)

	assert.True(t, report.OK)
	require.Len(t, report.ToolRuns, 2)
	assert.Equal(t, "session.inspect", report.ToolRuns[1].Name)
	assert.False(t, report.ToolRuns[1].IsError)
}

func TestRunStudioMCPTestSession_CallExpectedError(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	report, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ListTools:     false,
		Calls: []studioMCPTestCall{
			{
				Name: "story.validate",
				ExpectContains: map[string]string{
					"content.0.text": "NO_WORKSPACE",
				},
				ExpectError: true,
			},
		},
	})
	require.NoError(t, err)
	assert.True(t, report.OK)
	require.Len(t, report.ToolRuns, 1)
	assert.True(t, report.ToolRuns[0].IsError)
}

func TestRunStudioMCPTestSession_ExpectationFailureFailsRun(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(nil))
	cs := connectStudioTestClient(ctx, t, srv)

	_, err := runStudioMCPTestSession(ctx, cs, studioMCPTestOptions{
		ServerCommand: "kitsoki",
		ServerArgs:    []string{"mcp"},
		ListTools:     false,
		Calls: []studioMCPTestCall{
			{
				Name: "studio.ping",
				Expect: map[string]any{
					"structuredContent.version": "not-the-version",
				},
				Retries:    1,
				IntervalMS: 1,
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `studio.ping expectation "structuredContent.version"`)
}

func TestAssertMCPContainsExpectations(t *testing.T) {
	result := map[string]interface{}{
		"structuredContent": map[string]interface{}{
			"frame": map[string]interface{}{
				"text": "active work (all sessions): 1 item(s)\nAsync MCP chat",
			},
		},
	}

	err := assertMCPContainsExpectations("session.command", result, map[string]string{
		"structuredContent.frame.text": "Async MCP chat",
	})
	require.NoError(t, err)

	err = assertMCPContainsExpectations("session.command", result, map[string]string{
		"frame.text": "Async MCP chat",
	})
	require.NoError(t, err)

	err = assertMCPContainsExpectations("session.command", result, map[string]string{
		"structuredContent.frame.text": "missing title",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `session.command contains expectation "structuredContent.frame.text"`)
}

func TestAssertMCPExistsExpectations(t *testing.T) {
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "render.web: ok"},
			map[string]interface{}{"type": "image", "mimeType": "image/png", "data": "base64"},
		},
		"structuredContent": map[string]interface{}{
			"ok": true,
		},
	}

	err := assertMCPExistsExpectations("render.web", result, []string{
		"content.1.data",
	})
	require.NoError(t, err)

	err = assertMCPExistsExpectations("render.web", result, []string{
		"ok",
	})
	require.NoError(t, err)

	err = assertMCPExistsExpectations("render.web", result, []string{
		"content.2.data",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `render.web exists expectation "content.2.data"`)

	err = assertMCPExistsExpectations("render.web", result, []string{""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exists expectation path is empty")
}

func TestAssertMCPExpectationsDefaultsToStructuredContent(t *testing.T) {
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "render.web: ok"},
		},
		"structuredContent": map[string]interface{}{
			"ok":    true,
			"state": "idle",
		},
	}

	err := assertMCPExpectations("story.validate", result, map[string]any{
		"ok":    true,
		"state": "idle",
	})
	require.NoError(t, err)

	err = assertMCPExpectations("story.validate", result, map[string]any{
		"content.0.type": "text",
	})
	require.NoError(t, err)
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
