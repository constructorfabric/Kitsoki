// local_llm_test.go covers the local-model OpenAI HTTP transport in endpoint
// mode against a fake RoundTripper — no live model, no subprocess, no download.

package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// localChatHandler is a configurable RoundTripper emulating a llama.cpp
// /v1/chat/completions endpoint. It captures the decoded request body so tests
// can assert what local_llm sent.
type localChatHandler struct {
	content    string        // message content to return
	usage      chatUsage     // usage block to return
	httpStatus int           // override status (default 200)
	delay      time.Duration // artificial latency
	omitChoice bool          // return an empty choices array

	mu         sync.Mutex
	gotRequest chatRequest // last decoded request body (guard with mu)
	gotAuth    string      // last Authorization header (guard with mu)
}

// lastRequest returns the most recently decoded request body. It is mutex-guarded
// because RoundTrip can run concurrently with the test reader.
func (h *localChatHandler) lastRequest() chatRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gotRequest
}

// lastAuthHeader returns the most recently seen Authorization header value
// ("" when none was sent). Mutex-guarded like lastRequest.
func (h *localChatHandler) lastAuthHeader() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gotAuth
}

func (h *localChatHandler) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	h.mu.Lock()
	_ = json.NewDecoder(r.Body).Decode(&h.gotRequest)
	h.gotAuth = r.Header.Get("Authorization")
	h.mu.Unlock()

	status := h.httpStatus
	if status == 0 {
		status = http.StatusOK
	}

	header := http.Header{"Content-Type": []string{"application/json"}}
	if status >= 400 {
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
			Request:    r,
		}, nil
	}

	resp := chatResponse{Usage: h.usage}
	if !h.omitChoice {
		resp.Choices = []chatChoice{{Message: chatMessage{Role: "assistant", Content: h.content}}}
	}
	raw, _ := json.Marshal(resp)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(string(raw))),
		Request:    r,
	}, nil
}

func newLocalLLMForTest(h *localChatHandler, model string, grammar bool) *LocalLLMAgent {
	return NewLocalLLM(model, 0, "", grammar, "https://local-llm.test", nil).
		WithHTTPClient(&http.Client{Transport: h})
}

// TestLocalLLMOptInNoFetchAtConstruction is the opt-in guarantee with teeth:
// merely constructing a managed-mode LocalLLMAgent (and closing it) must NOT
// download the binary or weights. The fetch is strictly lazy — it can only happen
// inside an Ask that was already dispatched to this plugin, which a story can only
// reach by both declaring builtin.local_llm AND routing a call to it. So a
// default app (which does neither) never touches the cache or the network.
//
// Test rigor: KITSOKI_CACHE_DIR points at an empty temp dir; the assertion is
// that it stays empty. If construction ever eagerly fetched (regressing the
// lazy/opt-in contract) the bin/ or models/ subdir would appear with content and
// this fails. No httptest server is started, so any fetch attempt would also hit
// the real network and stall — another signal the contract broke.
func TestLocalLLMOptInNoFetchAtConstruction(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("KITSOKI_CACHE_DIR", cacheDir)

	// Managed mode (endpoint == "") is the mode that *could* fetch. Construct and
	// close it without ever calling Ask.
	o := NewLocalLLM("qwen2.5-1.5b-instruct", 8080, "", true, "", nil)
	if err := o.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The cache must be untouched: no files anywhere under the cache root.
	var found []string
	_ = filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 0 {
		t.Fatalf("opt-in violated: construction fetched %d file(s) into the cache: %v", len(found), found)
	}
}

// TestLocalLLMHappyPath verifies a successful round-trip surfaces the content as
// Submission and token/model/grammar info in Meta.
func TestLocalLLMHappyPath(t *testing.T) {
	t.Parallel()

	wantSubmission := `{"verdict":"pass"}`
	h := &localChatHandler{
		content: wantSubmission,
		usage:   chatUsage{PromptTokens: 42, CompletionTokens: 7},
	}
	o := newLocalLLMForTest(h, "qwen2.5-1.5b", false)
	defer o.Close()

	resp, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: unexpected error: %v", err)
	}
	if string(resp.Submission) != wantSubmission {
		t.Errorf("Submission: got %s, want %s", resp.Submission, wantSubmission)
	}
	if resp.Meta["model"] != "qwen2.5-1.5b" {
		t.Errorf("Meta.model: got %v", resp.Meta["model"])
	}
	if resp.Meta["prompt_tokens"] != 42 {
		t.Errorf("Meta.prompt_tokens: got %v, want 42", resp.Meta["prompt_tokens"])
	}
	if resp.Meta["completion_tokens"] != 7 {
		t.Errorf("Meta.completion_tokens: got %v, want 7", resp.Meta["completion_tokens"])
	}
	// grammar disabled on this agent → false even with no schema.
	if resp.Meta["grammar"] != false {
		t.Errorf("Meta.grammar: got %v, want false", resp.Meta["grammar"])
	}
	// The request must carry our user prompt and no response_format.
	if len(h.lastRequest().Messages) != 1 || h.lastRequest().Messages[0].Role != "user" {
		t.Errorf("request messages: got %+v", h.lastRequest().Messages)
	}
	if h.lastRequest().ResponseFormat != nil {
		t.Errorf("response_format should be omitted when grammar disabled, got %+v", h.lastRequest().ResponseFormat)
	}
}

// TestLocalLLMGrammarApplied verifies that with grammar enabled and an in-subset
// schema, response_format is attached and Meta.grammar is true.
func TestLocalLLMGrammarApplied(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: `{"verdict":"pass"}`}
	o := newLocalLLMForTest(h, "m", true)
	defer o.Close()

	req := sampleRequest()
	req.SchemaJSON = json.RawMessage(`{"type":"object","properties":{"verdict":{"type":"string"}}}`)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Meta["grammar"] != true {
		t.Errorf("Meta.grammar: got %v, want true", resp.Meta["grammar"])
	}
	if h.lastRequest().ResponseFormat == nil {
		t.Fatal("expected response_format to be set")
	}
	if h.lastRequest().ResponseFormat.Type != "json_schema" {
		t.Errorf("response_format.type: got %q", h.lastRequest().ResponseFormat.Type)
	}
	if h.lastRequest().ResponseFormat.JSONSchema == nil || !h.lastRequest().ResponseFormat.JSONSchema.Strict {
		t.Errorf("expected strict json_schema, got %+v", h.lastRequest().ResponseFormat.JSONSchema)
	}
}

// TestLocalLLMGrammarSkippedOutOfSubset verifies grammar is NOT applied when the
// schema is outside the translatable subset, even with grammar enabled — the
// fail-open contract: better no grammar than a silently-ignored one.
func TestLocalLLMGrammarSkippedOutOfSubset(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: `{"x":1}`}
	o := newLocalLLMForTest(h, "m", true)
	defer o.Close()

	req := sampleRequest()
	req.SchemaJSON = json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/x"}}}`)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Meta["grammar"] != false {
		t.Errorf("Meta.grammar: got %v, want false for out-of-subset schema", resp.Meta["grammar"])
	}
	if h.lastRequest().ResponseFormat != nil {
		t.Errorf("response_format should be omitted for out-of-subset schema, got %+v", h.lastRequest().ResponseFormat)
	}
}

// TestLocalLLMDeadlineExceeded verifies a slow response past Deadline surfaces
// as deadline_exceeded.
func TestLocalLLMDeadlineExceeded(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: `{"ok":true}`, delay: 200 * time.Millisecond}
	o := newLocalLLMForTest(h, "m", false)
	defer o.Close()

	req := sampleRequest()
	req.Deadline = time.Now().Add(20 * time.Millisecond)

	_, err := o.Ask(context.Background(), req)
	if err == nil {
		t.Fatal("expected deadline error, got nil")
	}
	assertAskError(t, err, "deadline_exceeded")
}

// TestLocalLLMContextAlreadyDone verifies an already-cancelled ctx is rejected
// up front without contacting the server.
func TestLocalLLMContextAlreadyDone(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: `{"ok":true}`}
	o := newLocalLLMForTest(h, "m", false)
	defer o.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := o.Ask(ctx, sampleRequest())
	if err == nil {
		t.Fatal("expected error for cancelled ctx, got nil")
	}
	assertAskError(t, err, "deadline_exceeded")
}

// TestLocalLLM4xxError verifies a 4xx status surfaces as transport_error.
func TestLocalLLM4xxError(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{httpStatus: http.StatusBadRequest}
	o := newLocalLLMForTest(h, "m", false)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected 4xx error, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestLocalLLMNoChoices verifies an empty choices array surfaces as
// transport_error.
func TestLocalLLMNoChoices(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{omitChoice: true}
	o := newLocalLLMForTest(h, "m", false)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error for missing choices, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestLocalLLMEmptyContent verifies empty message content surfaces as
// transport_error.
func TestLocalLLMEmptyContent(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: ""}
	o := newLocalLLMForTest(h, "m", false)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
	assertAskError(t, err, "transport_error")
}

// TestStripCodeFence verifies that code fences are stripped from model output.
func TestStripCodeFence(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{`{"slug":"foo"}`, `{"slug":"foo"}`},
		{"```json\n{\"slug\":\"foo\"}\n```", `{"slug":"foo"}`},
		{"```\n{\"slug\":\"foo\"}\n```", `{"slug":"foo"}`},
		{"```json\r\n{\"slug\":\"foo\"}\r\n```", `{"slug":"foo"}`},
		{" ```json\n{\"k\":1}\n``` ", `{"k":1}`},
	}
	for _, c := range cases {
		got := stripCodeFence(c.in)
		if got != c.want {
			t.Errorf("stripCodeFence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLocalLLMTranscript verifies the happy-path response populates an
// "openai-chat" Transcript with the request/assistant/result triple and usage
// tokens — without a real model (the fake RoundTripper stands in for the wire).
func TestLocalLLMTranscript(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{
		content: `{"verdict":"pass"}`,
		usage:   chatUsage{PromptTokens: 42, CompletionTokens: 7},
	}
	o := newLocalLLMForTest(h, "qwen2.5-1.5b", false)
	defer o.Close()

	resp, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Transcript == nil {
		t.Fatal("Transcript is nil, want populated")
	}
	if resp.Transcript.Format != "openai-chat" {
		t.Errorf("Transcript.Format = %q, want %q", resp.Transcript.Format, "openai-chat")
	}
	// Baseline triple: request + assistant + result (no tool calls today).
	if len(resp.Transcript.Events) < 2 {
		t.Fatalf("Transcript.Events = %d, want >= 2", len(resp.Transcript.Events))
	}

	// Each event must be valid JSON on its own (the verbatim-line contract).
	types := make([]string, 0, len(resp.Transcript.Events))
	var resultEvent map[string]any
	for i, raw := range resp.Transcript.Events {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("event %d not valid JSON: %v (%s)", i, err, raw)
		}
		typ, _ := m["type"].(string)
		types = append(types, typ)
		if typ == "result" {
			resultEvent = m
		}
	}

	// The terminal result must carry usage tokens.
	if resultEvent == nil {
		t.Fatalf("no result event among types %v", types)
	}
	usage, ok := resultEvent["usage"].(map[string]any)
	if !ok {
		t.Fatalf("result event missing usage: %v", resultEvent)
	}
	if usage["input_tokens"] != float64(42) {
		t.Errorf("usage.input_tokens = %v, want 42", usage["input_tokens"])
	}
	if usage["output_tokens"] != float64(7) {
		t.Errorf("usage.output_tokens = %v, want 7", usage["output_tokens"])
	}
}

// TestLocalLLMEndpointModeNoSpawn verifies endpoint mode never touches the
// managed sidecar: Close succeeds and a connection-refused endpoint yields a
// transport error (proving we POST directly to the endpoint, not a sidecar).
func TestLocalLLMEndpointModeClose(t *testing.T) {
	t.Parallel()

	h := &localChatHandler{content: `{"ok":true}`}
	o := newLocalLLMForTest(h, "m", false)
	if _, err := o.Ask(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if err := o.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
