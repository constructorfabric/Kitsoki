package ide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	"github.com/coder/websocket"
)

// protocolVersion is the MCP version kitsoki speaks (matches the editor
// extension's negotiated version, see the wire note §2).
const protocolVersion = "2024-11-05"

// ErrNotConnected is returned by CallTool when the socket is down (drop or
// never-dialed) and by Link.CallTool when no link is connected.
var ErrNotConnected = errors.New("ide: not connected")

// ToolInfo is one entry from tools/list (name + raw input schema).
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// rpcRequest is an outbound JSON-RPC 2.0 request frame.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is an inbound JSON-RPC 2.0 response frame. err is transport-level
// (a drop or ctx cancel) and never appears on the wire.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	err     error
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// rpcNotification is an outbound JSON-RPC 2.0 notification (no id, no response).
type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcFrame is the loose shape used to demultiplex an inbound frame: a response
// carries id + result/error; a server→client request carries method + id; a
// notification carries method with no id.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// authHeader is the WebSocket header the editor checks against the lock file's
// authToken; a mismatch is closed with 1008 (see the wire note §3).
const authHeader = "x-claude-code-ide-authorization"

// Client is one live MCP-over-ws connection to an IDE extension. It owns the
// socket, the read-pump goroutine, and the pending-request map. A Client is
// created already-dialed by [Dial]; lifecycle (reconnect, status) lives on
// [Link]. Safe for concurrent CallTool from multiple goroutines.
type Client struct {
	conn  *websocket.Conn
	tools []ToolInfo

	// ctx is the client-lifetime context the read-pump reads against; cancel
	// tears the pump down on Close.
	ctx    context.Context
	cancel context.CancelFunc

	writeMu sync.Mutex // serialises writes (the ws lib allows one writer)

	mu      sync.Mutex // guards pending, nextID, dead
	pending map[int]chan *rpcResponse
	nextID  int
	dead    bool
}

// Dial connects to ws://127.0.0.1:<lock.Port>, sending the auth header, runs
// the MCP initialize handshake, sends notifications/initialized, calls
// tools/list, and starts the read-pump. On any handshake failure it closes the
// socket and returns the error (so a 1008 auth rejection surfaces clearly). The
// returned Client is ready for CallTool.
func Dial(ctx context.Context, lock Lock) (*Client, error) {
	url := fmt.Sprintf("ws://127.0.0.1:%d", lock.Port)
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			authHeader: {lock.AuthToken},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ide: dial %s: %w", url, err)
	}
	// MCP frames can be larger than the default read limit (diagnostics for a
	// big workspace); lift it generously. -1 disables the cap.
	conn.SetReadLimit(-1)

	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		conn:    conn,
		ctx:     cctx,
		cancel:  cancel,
		pending: make(map[int]chan *rpcResponse),
		nextID:  1,
	}

	// The handshake runs synchronously against the caller's ctx, BEFORE the
	// read-pump owns the socket, so its replies are read inline.
	if err := c.handshake(ctx); err != nil {
		conn.Close(websocket.StatusInternalError, "handshake failed")
		cancel()
		return nil, err
	}

	go c.readPump()
	return c, nil
}

// handshake performs initialize → notifications/initialized → tools/list inline
// (reading replies directly off the socket) before the read-pump starts. Each
// read is bound to the caller's ctx so a 1008 close or a slow editor surfaces
// as a clear error.
func (c *Client) handshake(ctx context.Context) error {
	// initialize (id 1)
	initRes, err := c.roundTripInline(ctx, 1, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "kitsoki", "version": buildVersion()},
	})
	if err != nil {
		return fmt.Errorf("ide: initialize: %w", err)
	}
	if initRes.Error != nil {
		return fmt.Errorf("ide: initialize: %s", initRes.Error.Message)
	}

	// notifications/initialized (no id, no reply)
	if err := c.writeFrame(ctx, rpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}); err != nil {
		return fmt.Errorf("ide: notifications/initialized: %w", err)
	}

	// tools/list (id 2)
	toolsRes, err := c.roundTripInline(ctx, 2, "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("ide: tools/list: %w", err)
	}
	if toolsRes.Error != nil {
		return fmt.Errorf("ide: tools/list: %s", toolsRes.Error.Message)
	}
	var tl struct {
		Tools []ToolInfo `json:"tools"`
	}
	if len(toolsRes.Result) > 0 {
		if err := json.Unmarshal(toolsRes.Result, &tl); err != nil {
			return fmt.Errorf("ide: tools/list result: %w", err)
		}
	}
	c.tools = tl.Tools

	// nextID continues past the handshake ids.
	c.nextID = 3
	return nil
}

// roundTripInline writes a request and reads frames off the socket until the
// matching response arrives. Used only during the handshake, before the
// read-pump owns reads. Notifications/other-id frames during the handshake are
// skipped.
func (c *Client) roundTripInline(ctx context.Context, id int, method string, params any) (*rpcResponse, error) {
	if err := c.writeFrame(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var f rpcFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if f.ID == nil || *f.ID != id {
			continue // a notification or an unrelated frame
		}
		return &rpcResponse{JSONRPC: f.JSONRPC, ID: *f.ID, Result: f.Result, Error: f.Error}, nil
	}
}

// readPump is the single goroutine that owns socket reads after the handshake.
// It demultiplexes each frame to its waiting CallTool, declines server→client
// requests, ignores notifications, and on socket exit fails every in-flight
// call with ErrNotConnected.
func (c *Client) readPump() {
	var exitErr error
	defer func() {
		// A panic in the pump must still fail in-flight calls, not deadlock them.
		if r := recover(); r != nil {
			exitErr = fmt.Errorf("ide: read-pump panic: %v\n%s", r, debug.Stack())
		}
		c.drain(exitErr)
	}()

	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			exitErr = err
			return
		}
		var f rpcFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue // unparseable frame; stay alive
		}
		switch {
		case f.ID != nil && (len(f.Result) > 0 || f.Error != nil):
			// A response to one of our requests.
			c.deliver(&rpcResponse{JSONRPC: f.JSONRPC, ID: *f.ID, Result: f.Result, Error: f.Error})
		case f.ID != nil && f.Method != "":
			// A server→client request: kitsoki advertises no server-side
			// capabilities, so decline. Never block the pump on app code.
			c.declineRequest(*f.ID)
		default:
			// A notification (method, no id) or a frame we don't route: ignore.
		}
	}
}

// deliver routes a response to its pending waiter (buffered chan, never blocks).
func (c *Client) deliver(resp *rpcResponse) {
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	if ok {
		delete(c.pending, resp.ID)
	}
	c.mu.Unlock()
	if ok {
		ch <- resp // buffered size 1: the pump never blocks
	}
}

// declineRequest replies to a server→client request with method-not-found.
func (c *Client) declineRequest(id int) {
	_ = c.writeFrame(c.ctx, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32601, Message: "method not found"},
	})
}

// drain marks the client dead and fails every in-flight call with
// ErrNotConnected. Called once when the read-pump exits.
func (c *Client) drain(cause error) {
	c.mu.Lock()
	c.dead = true
	pending := c.pending
	c.pending = make(map[int]chan *rpcResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- &rpcResponse{err: ErrNotConnected}
	}
	_ = cause // cause is the raw read error; in-flight calls see ErrNotConnected.
}

// CallTool issues a tools/call request and blocks until the matching response
// arrives or ctx is done. args is marshalled as the "arguments" object; a nil
// args sends {}. The returned json.RawMessage is the tools/call RESULT object
// (the MCP result envelope) — callers unwrap content[]. A JSON-RPC error
// response is returned as a Go error. A socket drop while in flight fails the
// call with ErrNotConnected.
func (c *Client) CallTool(ctx context.Context, name string, args any) (json.RawMessage, error) {
	// Honour an already-cancelled/expired context before any connection-state
	// check: the caller cancelled, so context.Canceled is the right answer even
	// if the link has since dropped (c.dead). Without this, a cancel that races
	// a drop can surface as ErrNotConnected instead of ctx.Err() — a flaky
	// "want context.Canceled, got ide: not connected" under tight scheduling.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]any{"name": name, "arguments": args}

	// Register the waiter under the mutex, then write.
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	id := c.nextID
	c.nextID++
	ch := make(chan *rpcResponse, 1) // buffered so the pump never blocks
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeFrame(ctx, rpcRequest{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params}); err != nil {
		c.unregister(id)
		// A ctx cancel/timeout surfaces as itself; any other write failure means
		// the socket is gone (a drop racing the read-pump's drain), which is the
		// not-connected condition Link reconnects on and host treats as a domain
		// state — not an infra Go error. Wrap so errors.Is(…, ErrNotConnected).
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: write: %v", ErrNotConnected, err)
	}

	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("ide: tool %q: %s", name, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.unregister(id)
		return nil, ctx.Err()
	}
}

// unregister removes a pending waiter (ctx cancel / write failure).
func (c *Client) unregister(id int) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// writeFrame marshals v and writes it as a single ws text message under writeMu.
func (c *Client) writeFrame(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, websocket.MessageText, b)
}

// Tools returns the tool list captured at Dial (advertised by the IDE). Used to
// probe capability before mapping host.ide.* verbs.
func (c *Client) Tools() []ToolInfo {
	return c.tools
}

// Close tears down the read-pump and the socket. Idempotent. In-flight calls
// fail with ErrNotConnected.
func (c *Client) Close() error {
	c.mu.Lock()
	already := c.dead
	c.mu.Unlock()
	c.cancel() // stop the read-pump's blocking read
	err := c.conn.Close(websocket.StatusNormalClosure, "")
	if already {
		// Already drained by the pump's exit; a redundant Close is fine.
		return nil
	}
	return err
}

// buildVersion returns the kitsoki build version for the MCP clientInfo, or
// "dev" when unavailable (test/`go run`).
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}
