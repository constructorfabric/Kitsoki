// mcp_http.go implements the MCP-over-HTTP transport.
//
// MCPHTTPAgent is an Agent that talks to a long-running external service
// via HTTP. The plugin exposes a single MCP tool (default name "ask"); kitsoki
// is the client.
//
// Request shape (one HTTP POST to the endpoint):
//
//	{
//	  "jsonrpc": "2.0",
//	  "id": 1,
//	  "method": "tools/call",
//	  "params": {
//	    "name": "<tool>",          // defaults to "ask"
//	    "arguments": {             // AskRequest fields passed as MCP tool arguments
//	      "session_id":  "...",
//	      "turn":        3,
//	      "state_path":  "...",
//	      "verb":        "decide",
//	      "prompt":      "...",
//	      "schema":      {...},    // omitempty
//	      "with":        {...},    // omitempty
//	      "world":       {...},
//	      "deadline":    "...",
//	      "call_id":     "..."
//	    }
//	  }
//	}
//
// Success response:
//
//	{
//	  "jsonrpc": "2.0",
//	  "id": 1,
//	  "result": {
//	    "content": [{"type": "text", "text": "<json-encoded AskResponse>"}]
//	  }
//	}
//
// Error response codes: 4xx/5xx HTTP status, or JSON-RPC error object, both
// surfaced as *AskError{Kind: "transport_error"}.
//
// Deadline: AskRequest.Deadline is enforced by cancelling the http.Request
// context. If the caller's ctx is cancelled first, that propagates instead.
//
// Auth: headers are injected from the plugin declaration (already
// ${VAR}-substituted by the plugin loader at init time).
//
// Close: flushes idle connections via Transport.CloseIdleConnections.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// mcpToolCallParams is the "params" object for the MCP tools/call method.
type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// mcpContentItem is one item in the MCP result.content array.
type mcpContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// mcpToolCallResult is the MCP tools/call success response payload.
type mcpToolCallResult struct {
	Content []mcpContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// mcpHTTPResponse is the full JSON-RPC 2.0 response envelope from the HTTP transport.
type mcpHTTPResponse struct {
	JSONRPC string             `json:"jsonrpc"`
	ID      int                `json:"id"`
	Result  *mcpToolCallResult `json:"result,omitempty"`
	Error   *jsonrpcErrorObj   `json:"error,omitempty"`
}

// MCPHTTPAgent implements Agent via HTTP MCP tool calls. The zero value is
// not usable — construct it with NewMCPHTTP. It is safe for concurrent use:
// the underlying http.Client pools connections, and Ask holds no per-call
// state on the receiver.
type MCPHTTPAgent struct {
	endpoint string
	tool     string
	headers  map[string]string
	client   *http.Client
}

// NewMCPHTTP creates an MCPHTTPAgent. endpoint is the MCP server URL
// (e.g. "http://localhost:7301/mcp"); tool is the MCP tool name to call
// (defaults to "ask"); headers are pre-resolved HTTP request headers.
func NewMCPHTTP(endpoint, tool string, headers map[string]string) *MCPHTTPAgent {
	if tool == "" {
		tool = "ask"
	}
	return &MCPHTTPAgent{
		endpoint: endpoint,
		tool:     tool,
		headers:  headers,
		client: &http.Client{
			// No global timeout on the client itself; per-request deadline is
			// enforced via the http.Request context (set from AskRequest.Deadline).
			Transport: &http.Transport{},
		},
	}
}

// WithHTTPClient replaces the HTTP client used for MCP calls. It is a test seam
// for callers that need to validate request/response handling without opening a
// loopback listener. Nil is ignored.
func (o *MCPHTTPAgent) WithHTTPClient(client *http.Client) *MCPHTTPAgent {
	if client != nil {
		o.client = client
	}
	return o
}

// Ask sends an MCP tools/call request to the configured HTTP endpoint.
// The AskRequest is encoded as the tool's arguments. The response must contain
// a JSON-encoded AskResponse in the first content item's text field.
func (o *MCPHTTPAgent) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	// Build deadline context: whichever fires first — caller ctx or req.Deadline.
	callCtx := ctx
	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithDeadline(ctx, req.Deadline)
		defer cancel()
	}

	// Marshal AskRequest as the tool arguments.
	argsBytes, err := json.Marshal(req)
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: marshal AskRequest: %v", err),
		}
	}

	// Build JSON-RPC 2.0 request body.
	rpcReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
	}
	callParams := mcpToolCallParams{
		Name:      o.tool,
		Arguments: json.RawMessage(argsBytes),
	}
	paramsBytes, err := json.Marshal(callParams)
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: marshal tool call params: %v", err),
		}
	}
	rpcReq.Params = json.RawMessage(paramsBytes)

	bodyBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: marshal rpc request: %v", err),
		}
	}

	// Build HTTP request.
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, o.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: build http request: %v", err),
		}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range o.headers {
		httpReq.Header.Set(k, v)
	}

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		// Translate deadline exceeded context errors.
		if callCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded {
			return AskResponse{}, &AskError{
				Kind:       "deadline_exceeded",
				Underlying: err,
				Detail:     fmt.Sprintf("mcp_http agent: request timed out: %v", err),
			}
		}
		if callCtx.Err() == context.Canceled || ctx.Err() == context.Canceled {
			return AskResponse{}, &AskError{
				Kind:       "deadline_exceeded",
				Underlying: err,
				Detail:     fmt.Sprintf("mcp_http agent: request cancelled: %v", err),
			}
		}
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: http do: %v", err),
		}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, MaxHTTPResponseSize))
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: read response body: %v", err),
		}
	}

	// Surface HTTP-level errors (4xx/5xx).
	if httpResp.StatusCode >= 400 {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("mcp_http agent: http %d: %s", httpResp.StatusCode, truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}

	// Parse JSON-RPC 2.0 response.
	var rpcResp mcpHTTPResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: unmarshal rpc response: %v (raw: %q)", err, truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}

	if rpcResp.Error != nil {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("mcp_http agent: rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message),
		}
	}

	if rpcResp.Result == nil {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: "mcp_http agent: response has neither result nor error",
		}
	}

	if rpcResp.Result.IsError {
		// The tool itself reported an error.
		var errText string
		for _, item := range rpcResp.Result.Content {
			if item.Type == "text" {
				errText = item.Text
				break
			}
		}
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("mcp_http agent: tool returned isError=true: %s", truncateBytes([]byte(errText), ErrorDetailTruncateBytes)),
		}
	}

	// Extract the AskResponse from the first text content item.
	var askRespText string
	for _, item := range rpcResp.Result.Content {
		if item.Type == "text" {
			askRespText = item.Text
			break
		}
	}
	if askRespText == "" {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: "mcp_http agent: response content has no text item",
		}
	}

	var askResp AskResponse
	if err := json.Unmarshal([]byte(askRespText), &askResp); err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("mcp_http agent: unmarshal AskResponse from content text: %v (raw: %q)", err, truncateBytes([]byte(askRespText), ErrorDetailTruncateBytes)),
		}
	}

	return askResp, nil
}

// Close releases idle HTTP connections.
func (o *MCPHTTPAgent) Close() error {
	if t, ok := o.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	return nil
}
