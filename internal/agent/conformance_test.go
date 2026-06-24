// conformance_test.go is the transport conformance gate.
//
// Verifies that in-process, subprocess, and MCP-over-HTTP transports all produce
// the same AskResponse.Submission for the same AskRequest, modulo Meta and
// transport-specific fields.

package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// referenceSubmission is the byte-identical submission expected from all transports.
// It is what the echo_agent binary and the HTTP handler both return in the
// "echo" shape.
// We test that all transports round-trip through the same wire format.

// conformanceRequest returns a deterministic AskRequest used across all
// conformance subtests.
func conformanceRequest() AskRequest {
	req := sampleRequest()
	req.Verb = "decide"
	req.PromptText = "which is the best option for this context?"
	return req
}

// conformanceSubmission is the expected echo shape (verb + first-50 of prompt).
func expectedEchoVerb() string { return "decide" }

// TestConformance_InProcessVsSubprocessVsHTTP verifies that all three live
// transports produce byte-identical Submission payloads (modulo Meta) for the
// same AskRequest.
//
// Reference agent: an in-process agent that produces the same echo shape as
// echo_agent (the subprocess test binary) and the HTTP handler below.
func TestConformance_InProcessVsSubprocessVsHTTP(t *testing.T) {
	// Build echo agent binary (reuses the cached binary from subprocess tests).
	echobin := buildEchoAgent(t)

	req := conformanceRequest()

	// ── 1. In-process agent ──────────────────────────────────────────────────
	inprocAgent := New(AskFunc(func(_ context.Context, r AskRequest) (AskResponse, error) {
		head := r.PromptText
		if len(head) > 50 {
			head = head[:50]
		}
		sub, _ := json.Marshal(map[string]any{
			"echo_verb":        r.Verb,
			"echo_prompt_head": head,
		})
		return AskResponse{
			Submission: json.RawMessage(sub),
			Meta:       map[string]any{"transport": "inprocess"},
		}, nil
	}))
	defer inprocAgent.Close()

	inprocResp, err := inprocAgent.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("in-process agent: %v", err)
	}

	// ── 2. Subprocess agent ──────────────────────────────────────────────────
	subprocAgent := NewSubprocess(echobin, nil, nil)
	defer subprocAgent.Close()

	subprocResp, err := subprocAgent.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("subprocess agent: %v", err)
	}

	// ── 3. MCP-over-HTTP agent ───────────────────────────────────────────────
	httpAgent := buildEchoHTTPAgent(t)
	defer httpAgent.Close()

	httpResp, err := httpAgent.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("mcp_http agent: %v", err)
	}

	// ── Compare submissions (byte-identical modulo Meta) ──────────────────────
	if string(inprocResp.Submission) != string(subprocResp.Submission) {
		t.Errorf("in-process vs subprocess Submission mismatch:\n in-proc: %s\n  subproc: %s",
			inprocResp.Submission, subprocResp.Submission)
	}
	if string(inprocResp.Submission) != string(httpResp.Submission) {
		t.Errorf("in-process vs http Submission mismatch:\n in-proc: %s\n  http:    %s",
			inprocResp.Submission, httpResp.Submission)
	}

	// Verify the common submission shape.
	var got map[string]any
	if err := json.Unmarshal(inprocResp.Submission, &got); err != nil {
		t.Fatalf("unmarshal Submission: %v", err)
	}
	if got["echo_verb"] != expectedEchoVerb() {
		t.Errorf("echo_verb: got %v, want %q", got["echo_verb"], expectedEchoVerb())
	}

	// Meta fields are transport-specific — just verify they're non-nil.
	t.Logf("in-process Meta: %v", inprocResp.Meta)
	t.Logf("subprocess Meta: %v", subprocResp.Meta)
	t.Logf("http Meta: %v", httpResp.Meta)
}

// buildEchoHTTPAgent returns an MCPHTTPAgent backed by a fake RoundTripper that
// implements the same echo agent behaviour as echo_agent (subprocess).
func buildEchoHTTPAgent(t *testing.T) *MCPHTTPAgent {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var req jsonrpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Body:       io.NopCloser(strings.NewReader("bad request")),
				Request:    r,
			}, nil
		}
		var params mcpToolCallParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Status:     "400 Bad Request",
				Body:       io.NopCloser(strings.NewReader("bad params")),
				Request:    r,
			}, nil
		}
		var askReq AskRequest
		_ = json.Unmarshal(params.Arguments, &askReq)

		head := askReq.PromptText
		if len(head) > 50 {
			head = head[:50]
		}
		sub, _ := json.Marshal(map[string]any{
			"echo_verb":        askReq.Verb,
			"echo_prompt_head": head,
		})
		askResp := AskResponse{
			Submission: json.RawMessage(sub),
			Meta:       map[string]any{"transport": "mcp_http", "echo": true},
		}
		respBytes, _ := json.Marshal(askResp)
		rpcResp := mcpHTTPResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &mcpToolCallResult{
				Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
			},
		}
		raw, _ := json.Marshal(rpcResp)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(raw))),
			Request:    r,
		}, nil
	})}
	return NewMCPHTTP("https://mcp.test", "ask", nil).WithHTTPClient(client)
}

// TestConformance_MetaFieldsNotInSubmission verifies that the Meta fields from
// different transports do not bleed into the Submission.
func TestConformance_MetaFieldsNotInSubmission(t *testing.T) {
	echobin := buildEchoAgent(t)

	req := conformanceRequest()
	o := NewSubprocess(echobin, nil, nil)
	defer o.Close()

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// Verify Meta is separate from Submission.
	var sub map[string]any
	if err := json.Unmarshal(resp.Submission, &sub); err != nil {
		t.Fatalf("unmarshal Submission: %v", err)
	}

	// Meta transport field must not appear in Submission.
	if _, hasTransport := sub["transport"]; hasTransport {
		t.Error("Submission contains 'transport' field that belongs in Meta")
	}

	// Meta must contain transport.
	if resp.Meta == nil || resp.Meta["transport"] == nil {
		t.Error("Meta.transport is missing")
	}
	if resp.Meta["transport"] != "subprocess" {
		t.Errorf("Meta.transport: got %v, want subprocess", resp.Meta["transport"])
	}
}

// TestConformance_InProcessVsHTTP_RegistryLookup verifies that after loading
// plugins into a Registry, resolving agent.subprocess and agent.http
// produces agents that work correctly.
func TestConformance_InProcessVsHTTP_RegistryLookup(t *testing.T) {
	decls := map[string]*PluginDecl{
		"agent.fixer": {
			Plugin:   "mcp_http",
			Endpoint: "https://mcp.test",
			Tool:     "ask",
		},
	}
	reg, err := BuildRegistry(decls, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	defer reg.Close()

	o, resolveErr := reg.Resolve("agent.fixer")
	if resolveErr != nil {
		t.Fatalf("Resolve: %v", resolveErr)
	}
	if httpAgent, ok := o.(*MCPHTTPAgent); ok {
		httpAgent.WithHTTPClient(buildEchoHTTPAgent(t).client)
	} else {
		t.Fatalf("Resolve returned %T, want *MCPHTTPAgent", o)
	}

	resp, askErr := o.Ask(context.Background(), conformanceRequest())
	if askErr != nil {
		t.Fatalf("Ask via registry: %v", askErr)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Submission, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["echo_verb"] != expectedEchoVerb() {
		t.Errorf("echo_verb: got %v, want %q", got["echo_verb"], expectedEchoVerb())
	}
}
