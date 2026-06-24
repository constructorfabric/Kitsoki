package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitsokimcp "kitsoki/internal/mcp"
)

// fakeHost stands in for the phase-3 host listener: it accepts one connection,
// reads the forwarded question, and replies with a canned response frame.
type fakeHost struct {
	ln       net.Listener
	reply    kitsokimcp.OperatorAskResponse
	gotReq   kitsokimcp.OperatorAskRequest
	gotReqCh chan struct{}
}

// shortSock returns a unix socket path short enough for the ~104-char macOS
// sun_path limit (t.TempDir() under /var/folders is too long to bind).
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "opk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func startFakeHost(t *testing.T, reply kitsokimcp.OperatorAskResponse) *fakeHost {
	t.Helper()
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	h := &fakeHost{ln: ln, reply: reply, gotReqCh: make(chan struct{}, 1)}
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadBytes('\n')
		_ = json.Unmarshal(line, &h.gotReq)
		h.gotReqCh <- struct{}{}
		out, _ := json.Marshal(reply)
		_, _ = conn.Write(append(out, '\n'))
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return h
}

func (h *fakeHost) socket() string { return h.ln.Addr().String() }

// connectOperatorAsk wires an in-memory MCP client to a server pointed at sock.
func connectOperatorAsk(t *testing.T, sock string) *mcpsdk.ClientSession {
	t.Helper()
	srv, err := kitsokimcp.NewOperatorAskServer(kitsokimcp.OperatorAskConfig{SocketPath: sock})
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestOperatorAsk_RequiresSocket(t *testing.T) {
	_, err := kitsokimcp.NewOperatorAskServer(kitsokimcp.OperatorAskConfig{})
	require.Error(t, err)
}

func TestOperatorAsk_ListsAskTool(t *testing.T) {
	cs := connectOperatorAsk(t, shortSock(t))
	res, err := cs.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)
	assert.Equal(t, "ask", res.Tools[0].Name)
	rawSchema, err := json.Marshal(res.Tools[0].InputSchema)
	require.NoError(t, err)
	assert.Contains(t, string(rawSchema), `"questions"`)
	assert.Contains(t, string(rawSchema), `"multiSelect"`)
}

func TestOperatorAsk_ForwardsAndReturnsAnswer(t *testing.T) {
	host := startFakeHost(t, kitsokimcp.OperatorAskResponse{
		Answers: map[string]any{"Which backend?": "Postgres"},
	})
	cs := connectOperatorAsk(t, host.socket())

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "ask",
		Arguments: map[string]any{
			"questions": []map[string]any{{
				"question": "Which backend?",
				"header":   "Backend",
				"options": []map[string]any{
					{"label": "Postgres", "description": "managed pg"},
					{"label": "SQLite", "description": "embedded"},
				},
			}},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// The host received the forwarded question verbatim.
	<-host.gotReqCh
	require.Len(t, host.gotReq.Questions, 1)
	assert.Equal(t, "Which backend?", host.gotReq.Questions[0].Question)
	assert.Equal(t, "Backend", host.gotReq.Questions[0].Header)
	require.Len(t, host.gotReq.Questions[0].Options, 2)
	assert.Equal(t, "Postgres", host.gotReq.Questions[0].Options[0].Label)

	// The model receives the operator's answer.
	text := res.Content[0].(*mcpsdk.TextContent).Text
	assert.Contains(t, text, "Postgres")
	assert.Contains(t, text, "answered")
}

func TestOperatorAsk_HostErrorIsLLMVisible(t *testing.T) {
	host := startFakeHost(t, kitsokimcp.OperatorAskResponse{Error: "operator did not answer"})
	cs := connectOperatorAsk(t, host.socket())

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "ask",
		Arguments: map[string]any{
			"questions": []map[string]any{{
				"question": "x?",
				"header":   "X",
				"options":  []map[string]any{{"label": "a"}, {"label": "b"}},
			}},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "a host error must surface as an LLM-visible tool error")
	text := res.Content[0].(*mcpsdk.TextContent).Text
	assert.Contains(t, text, "operator did not answer")
	assert.Contains(t, text, "best judgement")
}

func TestOperatorAsk_UnreachableHostIsLLMVisible(t *testing.T) {
	// Point at a socket nobody is listening on.
	cs := connectOperatorAsk(t, shortSock(t))
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "ask",
		Arguments: map[string]any{
			"questions": []map[string]any{{
				"question": "x?",
				"header":   "X",
				"options":  []map[string]any{{"label": "a"}, {"label": "b"}},
			}},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcpsdk.TextContent).Text, "could not reach the operator")
}

func TestOperatorAsk_EmptyQuestionsRejected(t *testing.T) {
	cs := connectOperatorAsk(t, shortSock(t))
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "ask",
		Arguments: map[string]any{"questions": []map[string]any{}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, res.Content[0].(*mcpsdk.TextContent).Text, "empty")
}

// belt-and-suspenders: the dial path honours a short timeout when nothing ever
// accepts, rather than hanging the test.
func TestOperatorAsk_DialTimeoutBounded(t *testing.T) {
	srv, err := kitsokimcp.NewOperatorAskServer(kitsokimcp.OperatorAskConfig{
		SocketPath:  shortSock(t),
		DialTimeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	clientT, serverT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _, _ = srv.Connect(ctx, serverT, nil) }()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "t", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)
	defer cs.Close()

	start := time.Now()
	_, err = cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "ask",
		Arguments: map[string]any{"questions": []map[string]any{{
			"question": "x?", "header": "X",
			"options": []map[string]any{{"label": "a"}, {"label": "b"}},
		}}},
	})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "dial must time out promptly")
}
