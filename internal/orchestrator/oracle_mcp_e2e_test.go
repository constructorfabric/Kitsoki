package orchestrator_test

// oracle_mcp_e2e_test.go — exit-gate test: MCP-over-HTTP oracle end-to-end.
//
// TestStoryOracle_MCPHTTPEndToEnd is the stories-side exit gate that verifies
// the full production wiring: a story with oracle_plugins: { oracle.test_fixer:
// { plugin: mcp_http, endpoint: <server.URL>, tool: ask } } and an effect with
// oracle: oracle.test_fixer runs a kitsoki turn that actually invokes the plugin
// and writes OracleCalled / OracleReturned to the trace.
//
// No Anthropic SDK on the call path — the "LLM" is an httptest server.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/oracle"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestStoryOracle_MCPHTTPEndToEnd builds a one-room story with an
// oracle_plugins: block pointing at an httptest MCP server. The room has a
// single on_enter effect with oracle: oracle.test_fixer, which routes through
// host.Dispatch + oracle.MCPHTTPOracle. The test asserts:
//
//  1. OracleCalled event in trace with verb / prompt / call_id.
//  2. OracleReturned event in trace with the canned submission.
//  3. world.fixer_result is bound to the canned submission value after the turn.
func TestStoryOracle_MCPHTTPEndToEnd(t *testing.T) {
	t.Parallel()

	// ── 1. Stand up a canned MCP server ──────────────────────────────────────
	// The server returns a fixed AskResponse with Submission {"fixed": true}.
	cannedSubmission := json.RawMessage(`{"fixed":true}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type jsonrpcReq struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
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

		askResp := oracle.AskResponse{
			Submission: cannedSubmission,
			Meta:       map[string]any{"transport": "mcp_http", "test": true},
		}
		respBytes, _ := json.Marshal(askResp)
		rpcResp := jsonrpcResp{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: &mcpResult{
				Content: []mcpContentItem{{Type: "text", Text: string(respBytes)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResp)
	}))
	defer srv.Close()

	// ── 2. Build the story YAML ───────────────────────────────────────────────
	storyYAML := fmt.Sprintf(`
app:
  id: mcp-e2e-test
  version: 0.1.0

oracle_plugins:
  oracle.test_fixer:
    plugin: mcp_http
    endpoint: %s
    tool: ask

world:
  fixer_result:
    type: any

root: fixing

states:
  fixing:
    terminal: true
    on_enter:
      - invoke: host.oracle.ask
        oracle: oracle.test_fixer
        with:
          prompt: "fix this bug please"
        bind:
          fixer_result: submission
`, srv.URL)

	def, err := app.LoadBytes([]byte(storyYAML))
	require.NoError(t, err, "LoadBytes should succeed")

	// Verify oracle_plugins was parsed.
	require.NotNil(t, def.OraclePlugins, "oracle_plugins should be populated")
	fixerDecl, ok := def.OraclePlugins["oracle.test_fixer"]
	require.True(t, ok, "oracle.test_fixer should be in OraclePlugins")
	require.Equal(t, "mcp_http", fixerDecl.Plugin)
	require.Equal(t, srv.URL, fixerDecl.Endpoint)

	// ── 3. Build the oracle registry ─────────────────────────────────────────
	// Pass the noopHarness for the default oracle.claude entry (builtin.claude_cli).
	// The test only routes through oracle.test_fixer (mcp_http); oracle.claude
	// is registered but never called.
	oracleReg, err := oracle.BuildRegistryFromDef(def, noopHarness{})
	require.NoError(t, err, "BuildRegistryFromDef should succeed")
	defer oracleReg.Close()

	// ── 4. Build the orchestrator ─────────────────────────────────────────────
	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Set up a JSONL event sink to capture oracle events.
	sink := &e2eMemSink{}

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithOracleRegistry(oracleReg),
		orchestrator.WithEventSink(sink),
	)

	// ── 5. Run a session turn ─────────────────────────────────────────────────
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// RunInitialOnEnter fires the initial state's on_enter chain (turn 0),
	// which includes the oracle effect. This is the production code path that
	// would run when the session is started from kitsoki turn.
	err = orch.RunInitialOnEnter(ctx, sid)
	require.NoError(t, err, "RunInitialOnEnter should succeed")

	// ── 6. Assert world binding ───────────────────────────────────────────────
	// The Submission must bind into world via `bind: { fixer_result: submission }`.
	// This is the load-bearing guarantee for the control-inversion use cases (a
	// driver round-trips the trace and reads the bound result), so it is a HARD
	// assertion on both presence and value, not a log.
	events := sink.Events()
	var boundValue any
	var fixerResultBound bool
	for _, ev := range events {
		if ev.Kind == store.EffectApplied {
			var payload map[string]any
			if json.Unmarshal(ev.Payload, &payload) == nil {
				if set, ok := payload["set"].(map[string]any); ok {
					if v, ok := set["fixer_result"]; ok {
						fixerResultBound = true
						boundValue = v
					}
				}
			}
		}
	}
	require.True(t, fixerResultBound, "fixer_result must bind via an EffectApplied event (bind: { fixer_result: submission })")
	require.Equal(t, map[string]any{"fixed": true}, boundValue,
		"fixer_result must bind to the plugin's Submission value {\"fixed\":true}")

	// ── 7. Assert trace events ─────────────────────────────────────────────────
	events = sink.Events()
	var calledEvt, returnedEvt *store.Event
	for i := range events {
		ev := events[i]
		switch ev.Kind {
		case store.OracleCalled:
			calledEvt = &ev
		case store.OracleReturned:
			returnedEvt = &ev
		}
	}

	require.NotNil(t, calledEvt, "OracleCalled event must appear in trace")
	require.NotNil(t, returnedEvt, "OracleReturned event must appear in trace")

	// OracleCalled must carry verb and call_id.
	require.NotEmpty(t, calledEvt.CallID, "OracleCalled.call_id must be set")
	// OracleReturned must carry the same call_id.
	require.Equal(t, calledEvt.CallID, returnedEvt.CallID, "OracleCalled and OracleReturned must share call_id")

	// Parse OracleCalled payload and check verb.
	var calledPayload host.OracleCalledPayload
	require.NoError(t, json.Unmarshal(calledEvt.Payload, &calledPayload), "OracleCalled payload must be valid JSON")
	require.Equal(t, "ask", calledPayload.Verb, "OracleCalled.verb must be 'ask'")

	// Parse OracleReturned payload and check submission.
	var retPayload host.OracleReturnedPayload
	require.NoError(t, json.Unmarshal(returnedEvt.Payload, &retPayload), "OracleReturned payload must be valid JSON")
	require.NotNil(t, retPayload.Response, "OracleReturned.response must be set")

	t.Logf("OracleCalled call_id=%s verb=%s", calledEvt.CallID, calledPayload.Verb)
	t.Logf("OracleReturned call_id=%s response=%s", returnedEvt.CallID, retPayload.Response)
}

// cannedMCPServer stands up an httptest MCP-over-HTTP server that always
// returns the given Submission. Shared by the e2e oracle tests.
func cannedMCPServer(t *testing.T, submission json.RawMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type jsonrpcReq struct {
			ID int `json:"id"`
		}
		var req jsonrpcReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		askResp := oracle.AskResponse{
			Submission: submission,
			Meta:       map[string]any{"transport": "mcp_http", "test": true},
		}
		respBytes, _ := json.Marshal(askResp)
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": string(respBytes)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// TestStoryOracle_TransitionStampsForegroundTurn is the regression guard for
// foreground-turn trace stamping: when an oracle call fires from the on_enter
// chain of a state entered by a real transition (not the initial state), the
// OracleCalled / OracleReturned events must carry the FOREGROUND turn of that
// transition — not turn=0 — and the destination phase as state_path. Before the
// fix, on_enter oracle dispatch fell through to a default OracleCallCtx{turn:0}
// (the runstatus loader then hand-patched turns with an off-by-one nearestTurn
// hack); stamping the real turn at the source makes that hack unnecessary.
func TestStoryOracle_TransitionStampsForegroundTurn(t *testing.T) {
	t.Parallel()

	srv := cannedMCPServer(t, json.RawMessage(`{"fixed":true}`))
	defer srv.Close()

	storyYAML := fmt.Sprintf(`
app:
  id: oracle-turn-stamp-test
  version: 0.1.0

oracle_plugins:
  oracle.test_fixer:
    plugin: mcp_http
    endpoint: %s
    tool: ask

world:
  fixer_result:
    type: any

root: idle

intents:
  go:
    description: advance into the fixing phase

states:
  idle:
    on:
      go:
        - target: fixing
  fixing:
    terminal: true
    on_enter:
      - invoke: host.oracle.ask
        oracle: oracle.test_fixer
        with:
          prompt: "fix this bug please"
        bind:
          fixer_result: submission
`, srv.URL)

	def, err := app.LoadBytes([]byte(storyYAML))
	require.NoError(t, err)

	oracleReg, err := oracle.BuildRegistryFromDef(def, noopHarness{})
	require.NoError(t, err)
	defer oracleReg.Close()

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	sink := &e2eMemSink{}

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithOracleRegistry(oracleReg),
		orchestrator.WithEventSink(sink),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Drive the transition idle --go--> fixing. The on_enter oracle call fires
	// as part of THIS turn (turn 1), not the synthetic turn-0 init path.
	out, err := orch.RunIntent(ctx, sid, "go", nil)
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeCompleted, out.Mode)

	var called, returned *store.Event
	for i := range sink.Events() {
		ev := sink.Events()[i]
		switch ev.Kind {
		case store.OracleCalled:
			called = &ev
		case store.OracleReturned:
			returned = &ev
		}
	}
	require.NotNil(t, called, "OracleCalled event must appear in trace")
	require.NotNil(t, returned, "OracleReturned event must appear in trace")

	// Foreground turn, not 0.
	require.Equal(t, app.TurnNumber(1), called.Turn,
		"OracleCalled must carry the foreground turn (1), not turn=0")
	require.Equal(t, app.TurnNumber(1), returned.Turn,
		"OracleReturned must carry the same foreground turn as its start")

	// state_path is the destination phase, not the pre-transition state.
	require.Equal(t, app.StatePath("fixing"), called.StatePath,
		"OracleCalled.state_path must be the destination phase (fixing)")
	require.Equal(t, app.StatePath("fixing"), returned.StatePath,
		"OracleReturned.state_path must be the destination phase (fixing)")
}

// e2eMemSink is a thread-safe in-memory EventSink for the e2e test.
type e2eMemSink struct {
	events []store.Event
}

func (s *e2eMemSink) Append(ev store.Event) error {
	s.events = append(s.events, ev)
	return nil
}

func (s *e2eMemSink) History() store.History {
	out := make(store.History, len(s.events))
	copy(out, s.events)
	return out
}

func (s *e2eMemSink) Events() []store.Event {
	return s.events
}
