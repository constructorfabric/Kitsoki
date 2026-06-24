// mcp_http_test.go covers the MCP-over-HTTP transport.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mcpHandler is a configurable httptest handler for MCP tools/call requests.
type mcpHandler struct {
	// response is the AskResponse to embed in the result content.
	response AskResponse
	// rpcError, when non-nil, is returned as the JSON-RPC error object.
	rpcError *jsonrpcErrorObj
	// httpStatus overrides the HTTP status code (default 200).
	httpStatus int
	// delay adds artificial latency before responding.
	delay time.Duration
	// isToolError causes result.isError to be true.
	isToolError bool
}

func (h *mcpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.delay > 0 {
		time.Sleep(h.delay)
	}

	var req jsonrpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	status := h.httpStatus
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if status >= 400 {
		return // body already handled by HTTP status
	}

	var resp mcpHTTPResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	if h.rpcError != nil {
		resp.Error = h.rpcError
	} else if h.isToolError {
		resp.Result = &mcpToolCallResult{
			Content: []mcpContentItem{{Type: "text", Text: "tool error occurred"}},
			IsError: true,
		}
	} else {
		respBytes, _ := json.Marshal(h.response)
		resp.Result = &mcpToolCallResult{
			Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
		}
	}

	json.NewEncoder(w).Encode(resp)
}

func (h *mcpHandler) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	copy := *h
	copy.delay = 0
	return handlerRoundTrip(http.HandlerFunc(copy.ServeHTTP), r)
}

type handlerRoundTripper struct {
	handler http.Handler
}

func (h handlerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return handlerRoundTrip(h.handler, r)
}

func handlerRoundTrip(handler http.Handler, r *http.Request) (*http.Response, error) {
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)
	return rr.Result(), nil
}

func newMCPHTTPForTest(tool string, headers map[string]string, rt http.RoundTripper) *MCPHTTPAgent {
	return NewMCPHTTP("https://mcp.test", tool, headers).WithHTTPClient(&http.Client{Transport: rt})
}

func newMCPHTTPForHandler(tool string, headers map[string]string, handler http.Handler) *MCPHTTPAgent {
	return newMCPHTTPForTest(tool, headers, handlerRoundTripper{handler: handler})
}

// TestMCPHTTPHappyPath verifies a successful agent call over HTTP.
func TestMCPHTTPHappyPath(t *testing.T) {
	t.Parallel()

	wantSubmission := json.RawMessage(`{"result":"accepted","score":0.9}`)
	handler := &mcpHandler{
		response: AskResponse{
			Submission: wantSubmission,
			Meta:       map[string]any{"transport": "mcp_http"},
		},
	}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	resp, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: unexpected error: %v", err)
	}
	if string(resp.Submission) != string(wantSubmission) {
		t.Errorf("Submission: got %s, want %s", resp.Submission, wantSubmission)
	}
	if resp.Meta["transport"] != "mcp_http" {
		t.Errorf("Meta.transport: got %v, want mcp_http", resp.Meta["transport"])
	}
}

// TestMCPHTTPConnectionRefused verifies that connection refused surfaces as
// *AskError{Kind: "transport_error"}.
func TestMCPHTTPConnectionRefused(t *testing.T) {
	t.Parallel()

	o := NewMCPHTTP("http://127.0.0.1:9", "ask", nil).WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}),
	})
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestMCPHTTPTLSFailure verifies that a TLS handshake failure surfaces as
// *AskError{Kind: "transport_error"}.
func TestMCPHTTPTLSFailure(t *testing.T) {
	t.Parallel()

	o := NewMCPHTTP("https://mcp.test", "ask", nil).WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("tls: failed to verify certificate")
		}),
	})
	defer o.Close()

	_, tlsErr := o.Ask(context.Background(), sampleRequest())
	if tlsErr == nil {
		t.Fatal("expected TLS error, got nil")
	}
	// Should be a transport_error (TLS handshake failure).
	var ae *AskError
	if !errors.As(tlsErr, &ae) {
		t.Fatalf("expected *AskError, got %T: %v", tlsErr, tlsErr)
	}
	if ae.Kind != "transport_error" {
		t.Errorf("expected transport_error, got %q", ae.Kind)
	}
}

// TestMCPHTTPSlowResponseDeadline verifies that a slow response past the
// AskRequest.Deadline results in a deadline_exceeded error.
func TestMCPHTTPSlowResponseDeadline(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{
		delay: 200 * time.Millisecond,
		response: AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
		},
	}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	req := sampleRequest()
	req.Deadline = time.Now().Add(20 * time.Millisecond)

	_, err := o.Ask(context.Background(), req)
	if err == nil {
		t.Fatal("expected deadline error, got nil")
	}
	assertAskError(t, err, "deadline_exceeded")
}

// TestMCPHTTPContextCancel verifies that cancelling the parent context
// surfaces as deadline_exceeded.
func TestMCPHTTPContextCancel(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{
		delay: 200 * time.Millisecond,
		response: AskResponse{
			Submission: json.RawMessage(`{"ok":true}`),
		},
	}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := o.Ask(ctx, sampleRequest())
	if err == nil {
		t.Fatal("expected context cancel error, got nil")
	}
	assertAskError(t, err, "deadline_exceeded")
}

// TestMCPHTTP4xxError verifies that 4xx HTTP status surfaces as transport_error.
func TestMCPHTTP4xxError(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{httpStatus: http.StatusUnauthorized}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected 4xx error, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestMCPHTTP5xxError verifies that 5xx HTTP status surfaces as transport_error.
func TestMCPHTTP5xxError(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{httpStatus: http.StatusServiceUnavailable}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected 5xx error, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestMCPHTTPRPCError verifies that a JSON-RPC error object surfaces as
// transport_error.
func TestMCPHTTPRPCError(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{
		rpcError: &jsonrpcErrorObj{Code: -32000, Message: "agent crashed"},
	}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected rpc error, got nil")
	}
	assertAskError(t, err, "transport_error")
	ae := mustAskError(t, err)
	if ae.Detail == "" {
		t.Error("expected non-empty Detail for rpc error")
	}
}

// TestMCPHTTPToolError verifies that result.isError=true surfaces as
// transport_error.
func TestMCPHTTPToolError(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{isToolError: true}
	o := newMCPHTTPForTest("ask", nil, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected tool error, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestMCPHTTPHeaders verifies that configured headers are sent.
func TestMCPHTTPHeaders(t *testing.T) {
	t.Parallel()

	var gotAuthHeader string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		resp := AskResponse{Submission: json.RawMessage(`{"ok":true}`)}
		respBytes, _ := json.Marshal(resp)
		rpcResp := mcpHTTPResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result: &mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	})
	o := newMCPHTTPForHandler("ask", map[string]string{
		"Authorization": "Bearer test-token-xyz",
	}, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if gotAuthHeader != "Bearer test-token-xyz" {
		t.Errorf("Authorization header: got %q, want %q", gotAuthHeader, "Bearer test-token-xyz")
	}
}

// TestMCPHTTPDefaultTool verifies that the default tool name "ask" is used
// when no tool name is specified.
func TestMCPHTTPDefaultTool(t *testing.T) {
	t.Parallel()

	var gotToolName string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpcRequest
		json.NewDecoder(r.Body).Decode(&req)
		var params mcpToolCallParams
		json.Unmarshal(req.Params, &params)
		gotToolName = params.Name

		resp := AskResponse{Submission: json.RawMessage(`{"ok":true}`)}
		respBytes, _ := json.Marshal(resp)
		rpcResp := mcpHTTPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	})
	// Pass empty tool name — should default to "ask".
	o := newMCPHTTPForHandler("", nil, handler)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if gotToolName != "ask" {
		t.Errorf("tool name: got %q, want ask", gotToolName)
	}
}

// TestMCPHTTPClose verifies that Close releases idle connections without error.
func TestMCPHTTPClose(t *testing.T) {
	t.Parallel()

	handler := &mcpHandler{
		response: AskResponse{Submission: json.RawMessage(`{"ok":true}`)},
	}
	o := newMCPHTTPForTest("ask", nil, handler)

	if _, err := o.Ask(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if err := o.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestMCPHTTPRequestShape verifies that the request body sent to the server
// follows the MCP tools/call JSON-RPC 2.0 shape.
func TestMCPHTTPRequestShape(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf []byte
		var tmp = make([]byte, 1024)
		for {
			n, readErr := r.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if readErr != nil {
				break
			}
		}
		capturedBody = buf

		resp := AskResponse{Submission: json.RawMessage(`{"ok":true}`)}
		respBytes, _ := json.Marshal(resp)
		rpcResp := mcpHTTPResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result: &mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rpcResp)
	})
	o := newMCPHTTPForHandler("ask", nil, handler)
	defer o.Close()

	if _, err := o.Ask(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// Verify the request shape.
	var req jsonrpcRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("unmarshal captured body: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc: got %q, want 2.0", req.JSONRPC)
	}
	if req.Method != "tools/call" {
		t.Errorf("method: got %q, want tools/call", req.Method)
	}

	var params mcpToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params.Name != "ask" {
		t.Errorf("params.name: got %q, want ask", params.Name)
	}
	// Arguments should contain the AskRequest fields.
	var askReq AskRequest
	if err := json.Unmarshal(params.Arguments, &askReq); err != nil {
		t.Fatalf("unmarshal AskRequest from arguments: %v", err)
	}
	if askReq.Verb != sampleRequest().Verb {
		t.Errorf("AskRequest.Verb: got %q, want %q", askReq.Verb, sampleRequest().Verb)
	}
}
