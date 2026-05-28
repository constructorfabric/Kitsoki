// Package testrunner — four-transport conformance test (B-4).
//
// TestConformance_FourTransports is the headline conformance test: one story
// fixture, four oracle transports, byte-identical Submission modulo Meta and ts.
//
// The test lives in internal/testrunner (not internal/oracle) because the
// cassette transport is implemented here and oracle → testrunner would be circular.
// All four oracle implementations are reachable from this package without cycles:
//   - in-process:      oracle.New(...)
//   - subprocess:      oracle.NewSubprocess(...)
//   - MCP-over-HTTP:   oracle.NewMCPHTTP(...)
//   - cassette:        testrunner.NewCassetteOracle(...)
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"kitsoki/internal/oracle"
)

// conformancePrompt is the fixed prompt used across all transports.
// The echo oracle returns the first 50 chars + the verb.
const conformancePrompt = "which is the best option for this context?"
const conformanceVerb = "decide"

// referenceEchoSubmission is the expected byte-identical Submission from all
// four transports. It is what the echo oracle produces for conformanceVerb + conformancePrompt.
func referenceEchoSubmission(t *testing.T) json.RawMessage {
	t.Helper()
	head := conformancePrompt
	if len(head) > 50 {
		head = head[:50]
	}
	b, err := json.Marshal(map[string]any{
		"echo_verb":        conformanceVerb,
		"echo_prompt_head": head,
	})
	if err != nil {
		t.Fatalf("referenceEchoSubmission marshal: %v", err)
	}
	return json.RawMessage(b)
}

// conformanceAskRequest returns the deterministic AskRequest used across all transports.
func conformanceAskRequest() oracle.AskRequest {
	return oracle.AskRequest{
		Verb:       conformanceVerb,
		PromptText: conformancePrompt,
	}
}

// buildEchoOracleBinaryForConformance compiles the echo_oracle binary and returns its path.
// Reuses the same testdata binary as oracle_test.go in internal/oracle.
func buildEchoOracleBinaryForConformance(t *testing.T) string {
	t.Helper()
	// Build from internal/oracle/testdata/echo_oracle — same binary as B-3 tests.
	dir := t.TempDir()
	binPath := filepath.Join(dir, "echo_oracle")

	// Find the source package relative to the module root.
	cmd := exec.Command("go", "build", "-o", binPath, "kitsoki/internal/oracle/testdata/echo_oracle")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build echo_oracle: %v", err)
	}
	return binPath
}

// buildEchoHTTPOracleForConformance creates an MCP-over-HTTP test server that
// implements the echo oracle behaviour.
func buildEchoHTTPOracleForConformance(t *testing.T) (*oracle.MCPHTTPOracle, *httptest.Server) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type jsonrpcReq struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		type mcpToolParams struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		type mcpContentItem struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		type mcpResult struct {
			Content []mcpContentItem `json:"content"`
		}
		type jsonrpcResp struct {
			JSONRPC string     `json:"jsonrpc"`
			ID      int        `json:"id"`
			Result  *mcpResult `json:"result,omitempty"`
		}

		var req jsonrpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var params mcpToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			http.Error(w, "bad params", http.StatusBadRequest)
			return
		}
		var askReq oracle.AskRequest
		_ = json.Unmarshal(params.Arguments, &askReq)

		head := askReq.PromptText
		if len(head) > 50 {
			head = head[:50]
		}
		sub, _ := json.Marshal(map[string]any{
			"echo_verb":        askReq.Verb,
			"echo_prompt_head": head,
		})
		askResp := oracle.AskResponse{
			Submission: json.RawMessage(sub),
			Meta:       map[string]any{"transport": "mcp_http", "echo": true},
		}
		respBytes, _ := json.Marshal(askResp)
		rpcResp := jsonrpcResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  &mcpResult{Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResp)
	})
	srv := httptest.NewServer(handler)
	o := oracle.NewMCPHTTP(srv.URL, "ask", nil)
	return o, srv
}

// TestConformance_FourTransports is the B-4 headline gate:
// in-process + subprocess + MCP-over-HTTP + cassette all produce byte-identical
// AskResponse.Submission for the same AskRequest, modulo Meta (transport-specific)
// and ts (not present in AskResponse).
func TestConformance_FourTransports(t *testing.T) {
	t.Parallel()

	req := conformanceAskRequest()
	refSub := referenceEchoSubmission(t)

	// ── 1. In-process oracle ──────────────────────────────────────────────────
	inprocOracle := oracle.New(oracle.AskFunc(func(_ context.Context, r oracle.AskRequest) (oracle.AskResponse, error) {
		head := r.PromptText
		if len(head) > 50 {
			head = head[:50]
		}
		sub, _ := json.Marshal(map[string]any{
			"echo_verb":        r.Verb,
			"echo_prompt_head": head,
		})
		return oracle.AskResponse{
			Submission: json.RawMessage(sub),
			Meta:       map[string]any{"transport": "inprocess"},
		}, nil
	}))
	defer inprocOracle.Close()

	inprocResp, err := inprocOracle.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("in-process oracle: %v", err)
	}

	// ── 2. Subprocess oracle ──────────────────────────────────────────────────
	echobin := buildEchoOracleBinaryForConformance(t)
	subprocOracle := oracle.NewSubprocess(echobin, nil, nil)
	defer subprocOracle.Close()

	subprocResp, err := subprocOracle.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("subprocess oracle: %v", err)
	}

	// ── 3. MCP-over-HTTP oracle ───────────────────────────────────────────────
	httpOracle, httpSrv := buildEchoHTTPOracleForConformance(t)
	defer httpOracle.Close()
	defer httpSrv.Close()

	httpResp, err := httpOracle.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("mcp_http oracle: %v", err)
	}

	// ── 4. Cassette oracle ────────────────────────────────────────────────────
	// Pre-record the reference oracle response in a cassette.
	// The cassette oracle block response is the same JSON the echo oracle returns.
	casDir := t.TempDir()
	casPath := filepath.Join(casDir, "conformance.yaml")

	// The oracle.response field in the cassette is a JSON object string — it starts
	// with "{" so marshalOracleResponseString passes it through verbatim.
	responseField := string(refSub)

	casYAML := fmt.Sprintf(`kind: host_cassette
app_id: conformance
episodes:
  - id: echo_ep
    match:
      handler: oracle.echo
    oracle:
      verb: %s
      response: '%s'
    response:
      data: {}
`, conformanceVerb, responseField)

	if err := os.WriteFile(casPath, []byte(casYAML), 0644); err != nil {
		t.Fatalf("write cassette: %v", err)
	}
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	casOracle := NewCassetteOracle(cas, "oracle.echo", func() string { return "" }, nil)
	defer casOracle.Close()

	casResp, err := casOracle.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("cassette oracle: %v", err)
	}

	// ── Compare all four Submissions ──────────────────────────────────────────
	type pair struct {
		name string
		got  json.RawMessage
	}
	transports := []pair{
		{"in-process", inprocResp.Submission},
		{"subprocess", subprocResp.Submission},
		{"mcp_http", httpResp.Submission},
		{"cassette", casResp.Submission},
	}
	for _, tp := range transports {
		if string(tp.got) != string(refSub) {
			t.Errorf("transport %q Submission mismatch:\n  got:  %s\n  want: %s",
				tp.name, tp.got, refSub)
		}
	}

	// Verify echo_verb field is correct.
	var got map[string]any
	if err := json.Unmarshal(inprocResp.Submission, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["echo_verb"] != conformanceVerb {
		t.Errorf("echo_verb: got %v, want %q", got["echo_verb"], conformanceVerb)
	}

	// Cassette transport meta.
	if casResp.Meta == nil || casResp.Meta["transport"] != "cassette" {
		t.Errorf("cassette Meta.transport: got %v, want cassette", casResp.Meta)
	}
	if casResp.Meta["episode_id"] != "echo_ep" {
		t.Errorf("cassette Meta.episode_id: got %v, want echo_ep", casResp.Meta["episode_id"])
	}

	t.Logf("all 4 transports agree on Submission: %s", refSub)
}

// TestConformance_CassetteReplayAny_MultipleCallIDs verifies the §3.1 guarantee:
// replay:any + oracle: episodes produce N distinct call_ids (different matchIdx)
// sharing one episode_id. This is the full cassette Oracle path (no manual sink.Append).
func TestConformance_CassetteReplayAny_MultipleCallIDs(t *testing.T) {
	t.Parallel()

	const appID = "conform_app"
	const epID = "any_ep"

	casDir := t.TempDir()
	casPath := filepath.Join(casDir, "replay_any.yaml")
	casYAML := fmt.Sprintf(`kind: host_cassette
app_id: %s
episodes:
  - id: %s
    match:
      handler: oracle.conform
    replay: any
    oracle:
      verb: ask
      response: '{"ok": true}'
    response:
      data: {}
`, appID, epID)
	if err := os.WriteFile(casPath, []byte(casYAML), 0644); err != nil {
		t.Fatalf("write cassette: %v", err)
	}
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	o := NewCassetteOracle(cas, "oracle.conform", nil, nil)
	defer o.Close()

	const n = 4
	type callResult struct {
		episodeID string
		matchIdx  int
		callID    string
	}
	results := make([]callResult, n)

	for i := 0; i < n; i++ {
		resp, askErr := o.Ask(context.Background(), oracle.AskRequest{Verb: "ask"})
		if askErr != nil {
			t.Fatalf("Ask[%d]: %v", i, askErr)
		}
		results[i] = callResult{
			episodeID: EpisodeIDFromMeta(resp.Meta),
			matchIdx:  MatchIdxFromMeta(resp.Meta),
			callID:    fmt.Sprintf("%v", resp.Meta["call_id"]),
		}
	}

	// All N calls share episode_id.
	for i, r := range results {
		if r.episodeID != epID {
			t.Errorf("results[%d].episodeID = %q, want %q", i, r.episodeID, epID)
		}
	}

	// matchIdx = 0,1,...,n-1.
	for i, r := range results {
		if r.matchIdx != i {
			t.Errorf("results[%d].matchIdx = %d, want %d", i, r.matchIdx, i)
		}
	}

	// All N call_ids are distinct.
	seen := make(map[string]bool)
	for i, r := range results {
		if seen[r.callID] {
			t.Errorf("duplicate callID %q at results[%d]", r.callID, i)
		}
		seen[r.callID] = true
	}
}
