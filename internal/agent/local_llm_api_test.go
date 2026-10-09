// local_llm_api_test.go covers the authenticated-endpoint / OpenAI-native
// constrained-output additions to LocalLLMAgent (api_key_env, json_schema):
// the Authorization: Bearer header, the fail-fast on an unset api-key env var,
// and the response_format: json_schema path that sends for schemas the legacy
// grammar gate would leave unconstrained. All hermetic — a fake RoundTripper,
// no live model, no subprocess.

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// outOfSubsetSchema is a discriminated-union schema (if/then/const/enum) that
// is outside llama.cpp's GBNF subset — the shape codeact's step schema has.
// Used to prove the JSONSchema knob sends response_format for schemas the
// grammar path would leave unconstrained.
const outOfSubsetSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "action": {"type": "string", "enum": ["snippet", "done"]},
    "snippet": {"type": "string"},
    "payload": {"type": "object"}
  },
  "required": ["action"],
  "if": {"properties": {"action": {"const": "snippet"}}},
  "then": {"required": ["action", "snippet"]},
  "else": {"required": ["action", "payload"]}
}`

// TestLocalLLM_APIKeyEnv_SendsBearerHeader asserts that when APIKeyEnv names a
// set environment variable, Ask sends `Authorization: Bearer <value>` on the
// HTTP request. t.Setenv cannot be combined with t.Parallel, so this test is
// serial.
func TestLocalLLM_APIKeyEnv_SendsBearerHeader(t *testing.T) {
	t.Setenv("KITSOKI_TEST_LLM_KEY", "secret-token")
	h := &localChatHandler{content: `{"ok":true}`}
	o := NewLocalLLM("glm-4.6", 0, "", false, "https://api.test", nil).
		WithHTTPClient(&http.Client{Transport: h}).
		WithAPIKeyEnv("KITSOKI_TEST_LLM_KEY")
	resp, err := o.Ask(context.Background(), AskRequest{PromptText: "hi"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if len(resp.Submission) == 0 {
		t.Fatal("expected a non-empty submission")
	}
	if got := h.lastAuthHeader(); got != "Bearer secret-token" {
		t.Errorf("Authorization header: got %q, want %q", got, "Bearer secret-token")
	}
}

// TestLocalLLM_APIKeyEnv_MissingEnvVarErrors asserts that an APIKeyEnv whose
// env var is unset is a hard transport_error (naming the var) rather than a
// silent unauthenticated request, and that no HTTP call is made.
func TestLocalLLM_APIKeyEnv_MissingEnvVarErrors(t *testing.T) {
	t.Parallel()
	h := &localChatHandler{content: `{"ok":true}`}
	o := NewLocalLLM("glm-4.6", 0, "", false, "https://api.test", nil).
		WithHTTPClient(&http.Client{Transport: h}).
		WithAPIKeyEnv("KITSOKI_TEST_LLM_KEY_DEFINITELY_UNSET")
	_, err := o.Ask(context.Background(), AskRequest{PromptText: "hi"})
	if err == nil {
		t.Fatal("expected an error when the api_key_env var is unset")
	}
	ae, ok := err.(*AskError)
	if !ok || ae.Kind != "transport_error" {
		t.Fatalf("expected transport_error, got %v", err)
	}
	if !strings.Contains(ae.Error(), "KITSOKI_TEST_LLM_KEY_DEFINITELY_UNSET") {
		t.Errorf("error should name the env var, got: %v", ae)
	}
	if req := h.lastRequest(); req.Model != "" {
		t.Errorf("expected no HTTP call to be made, but a request was sent (model=%q)", req.Model)
	}
}

// TestLocalLLM_JSONSchema_SentForOutOfSubsetSchema proves the OpenAI-native
// constrained-output path: with JSONSchema enabled, response_format: json_schema
// is sent for a discriminated-union schema the grammar gate would reject. It
// also pins the legacy behavior — with JSONSchema off (with or without grammar),
// the same out-of-subset schema gets NO response_format — so existing
// managed-mode local_llm usage is unchanged.
func TestLocalLLM_JSONSchema_SentForOutOfSubsetSchema(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(outOfSubsetSchema)

	// Sanity: the schema is genuinely outside the grammar subset, so the grammar
	// path would NOT send response_format for it.
	if GrammarSubsetOK(schema) == nil {
		t.Fatal("expected outOfSubsetSchema to be outside the grammar subset")
	}

	// JSONSchema enabled → response_format: json_schema sent regardless of subset.
	hOn := &localChatHandler{content: `{"action":"done","payload":{}}`}
	on := NewLocalLLM("glm-4.6", 0, "", false, "https://api.test", nil).
		WithHTTPClient(&http.Client{Transport: hOn}).
		WithJSONSchema(true)
	if _, err := on.Ask(context.Background(), AskRequest{PromptText: "hi", SchemaJSON: schema}); err != nil {
		t.Fatalf("Ask (json_schema on): %v", err)
	}
	reqOn := hOn.lastRequest()
	if reqOn.ResponseFormat == nil {
		t.Fatal("expected response_format to be set when JSONSchema=true")
	}
	if reqOn.ResponseFormat.Type != "json_schema" {
		t.Errorf("response_format.type: got %q, want json_schema", reqOn.ResponseFormat.Type)
	}
	if reqOn.ResponseFormat.JSONSchema == nil || !reqOn.ResponseFormat.JSONSchema.Strict {
		t.Errorf("expected strict json_schema, got %+v", reqOn.ResponseFormat.JSONSchema)
	}

	// JSONSchema off, grammar off → response_format omitted for out-of-subset
	// schema (existing behavior preserved).
	hOff := &localChatHandler{content: `{"action":"done","payload":{}}`}
	off := NewLocalLLM("glm-4.6", 0, "", false, "https://api.test", nil).
		WithHTTPClient(&http.Client{Transport: hOff})
	if _, err := off.Ask(context.Background(), AskRequest{PromptText: "hi", SchemaJSON: schema}); err != nil {
		t.Fatalf("Ask (json_schema off, grammar off): %v", err)
	}
	if req := hOff.lastRequest(); req.ResponseFormat != nil {
		t.Errorf("expected response_format omitted for out-of-subset schema when JSONSchema=false, got %+v", req.ResponseFormat)
	}

	// JSONSchema off, grammar on → STILL omitted for the out-of-subset schema
	// (the subset gate rejects it). This is the gap the JSONSchema knob closes.
	hGram := &localChatHandler{content: `{"action":"done","payload":{}}`}
	gram := NewLocalLLM("glm-4.6", 0, "", true, "https://api.test", nil).
		WithHTTPClient(&http.Client{Transport: hGram})
	if _, err := gram.Ask(context.Background(), AskRequest{PromptText: "hi", SchemaJSON: schema}); err != nil {
		t.Fatalf("Ask (grammar=true, out of subset): %v", err)
	}
	if req := hGram.lastRequest(); req.ResponseFormat != nil {
		t.Errorf("expected response_format omitted for out-of-subset schema even with grammar=true, got %+v", req.ResponseFormat)
	}
}
