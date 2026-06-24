// local_llm.go implements the local-model transport: an Agent that talks to a
// llama.cpp server over its OpenAI-compatible /v1/chat/completions HTTP API.
//
// Why this transport exists: routing and schema-bounded `decide` do not need
// Claude's reasoning — they need cheap, offline, schema-shaped JSON. A small
// local model behind llama.cpp delivers that, and (for schemas inside the
// translatable grammar subset, see grammar_subset.go) can be grammar-constrained
// so its first decode is strongly biased toward valid JSON, collapsing the retry
// loop. This is additive and opt-in; agent.claude stays the default.
//
// Request shape (one HTTP POST to base + "/v1/chat/completions"):
//
//	{
//	  "model": "<model>",
//	  "messages": [{"role": "user", "content": "<rendered prompt>"}],
//	  "response_format": {                       // omitted unless grammar applies
//	    "type": "json_schema",
//	    "json_schema": {"name": "submission", "strict": true, "schema": <SchemaJSON>}
//	  }
//	}
//
// Success response (OpenAI chat completion):
//
//	{
//	  "choices": [{"message": {"content": "<json-encoded submission>"}}],
//	  "usage":   {"prompt_tokens": N, "completion_tokens": M}
//	}
//
// Grammar is best-effort: response_format is attached only when grammar is
// enabled AND the schema is inside the translatable subset. llama.cpp fails open
// (decodes unconstrained on a translation error, still returns 200), so this
// transport does NOT validate — ValidateSubmission (validate.go) remains the
// sole authority. Meta["grammar"] records whether grammar was actually requested
// so the trace tells the truth about what enforced the shape.
//
// Base URL resolution: when endpoint != "" the transport talks to it directly
// and never spawns or fetches anything (bring-your-own-server / test mode);
// otherwise it lazily ensures a managed sidecar (server package, step 2).
//
// Deadline: AskRequest.Deadline cancels the request context; the caller's ctx
// cancelling first propagates instead. Close releases idle connections and, in
// managed mode, terminates the sidecar.
//
// Non-goals: no system-prompt handling (AskRequest carries only the rendered
// PromptText; any system framing is folded in upstream), no streaming, no
// multi-turn — one prompt, one schema-shaped reply.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// chatMessage is one entry in the OpenAI chat `messages` array (request side)
// and the `choices[].message` object (response side). ToolCalls is response-only
// and absent on the request; it is populated when a tool-using model returns
// function calls. Today local_llm serves schema-shaped output and makes none.
type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

// chatToolCall tolerates both tool-call wire shapes the research surfaced: the
// OpenAI nested form ({id,type:"function",function:{name,arguments}}) and the
// llama.cpp-flattened form ({name,arguments} with no function wrapper / id).
// arguments is a JSON-encoded string in both. The extractor prefers the nested
// Function fields when present, else falls back to the flat Name/Arguments.
type chatToolCall struct {
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function *chatToolFunction `json:"function,omitempty"`
	// Flattened llama.cpp fallback (no function wrapper).
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// chatToolFunction is the OpenAI nested function object inside a tool call.
type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// jsonSchemaSpec is the `json_schema` object inside an OpenAI response_format.
type jsonSchemaSpec struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

// responseFormat is the OpenAI `response_format` directive. Only the
// json_schema variant is used here; it is omitted entirely (pointer nil) when
// grammar does not apply.
type responseFormat struct {
	Type       string          `json:"type"`
	JSONSchema *jsonSchemaSpec `json:"json_schema,omitempty"`
}

// chatRequest is the POST body for /v1/chat/completions.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

// chatChoice is one element of the response `choices` array.
type chatChoice struct {
	Message chatMessage `json:"message"`
}

// chatUsage mirrors the OpenAI `usage` block. Surfaced verbatim into Meta so the
// trace records token counts without the state machine interpreting them.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// chatResponse is the OpenAI chat-completion response envelope.
type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

// localSidecar is the subset of the server.Sidecar lifecycle local_llm depends
// on. Declaring it as an interface here lets tests inject a fake without pulling
// in the real fetch/spawn machinery, and lets the constructor stay decoupled
// from the server package's concrete type until step 2 wires it.
type localSidecar interface {
	// EnsureRunning lazily resolves (and in managed mode starts) the backend,
	// returning its base URL. It honours ctx for the start/health wait.
	EnsureRunning(ctx context.Context) (baseURL string, err error)
	// Close terminates a managed backend; it is a no-op in endpoint mode.
	Close() error
}

// LocalLLMAgent implements Agent against a llama.cpp OpenAI HTTP server. The
// zero value is not usable — construct it with NewLocalLLM. It is safe for
// concurrent use: the http.Client pools connections and Ask holds no per-call
// state on the receiver.
type LocalLLMAgent struct {
	model    string
	grammar  bool
	endpoint string
	sidecar  localSidecar
	client   *http.Client
}

// NewLocalLLM creates a LocalLLMAgent.
//
// model is the GGUF model id passed to llama.cpp (and used to provision weights
// in managed mode); port/serverBin/env configure a managed sidecar; grammar
// requests best-effort grammar-constrained decoding for in-subset schemas;
// endpoint, when non-empty, points at an already-running server and disables all
// fetching/spawning. The sidecar is created here but only contacted lazily on
// the first managed Ask, mirroring NewMCPHTTP's eager-client / lazy-work split.
func NewLocalLLM(model string, port int, serverBin string, grammar bool, endpoint string, env map[string]string) *LocalLLMAgent {
	return &LocalLLMAgent{
		model:    model,
		grammar:  grammar,
		endpoint: endpoint,
		sidecar:  newSidecar(model, port, serverBin, endpoint, env),
		client: &http.Client{
			// No global timeout; the per-request deadline is enforced via the
			// http.Request context derived from AskRequest.Deadline.
			Transport: &http.Transport{},
		},
	}
}

// WithHTTPClient replaces the HTTP client used for endpoint calls. It is a test
// seam for callers that need to validate request/response handling without
// opening a loopback listener. Nil is ignored.
func (o *LocalLLMAgent) WithHTTPClient(client *http.Client) *LocalLLMAgent {
	if client != nil {
		o.client = client
	}
	return o
}

// Ask sends the rendered prompt to the local model and returns its
// schema-shaped reply. It does not validate the Submission — ValidateSubmission
// is the sole authority — but it does record in Meta whether grammar was
// requested so downstream knows what (if anything) constrained the decode.
func (o *LocalLLMAgent) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	// Honour an already-cancelled caller context up front so we never spawn a
	// sidecar or open a connection for a turn that is already over.
	if ctx.Err() != nil {
		return AskResponse{}, &AskError{
			Kind:       "deadline_exceeded",
			Underlying: ctx.Err(),
			Detail:     fmt.Sprintf("local_llm agent: context already done: %v", ctx.Err()),
		}
	}

	// Build deadline context: whichever fires first — caller ctx or req.Deadline.
	callCtx := ctx
	if !req.Deadline.IsZero() {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithDeadline(ctx, req.Deadline)
		defer cancel()
	}

	// Resolve the base URL. Endpoint mode talks directly to a running server;
	// managed mode lazily ensures the sidecar (download/spawn/health) here.
	base := o.endpoint
	if base == "" {
		var err error
		base, err = o.sidecar.EnsureRunning(callCtx)
		if err != nil {
			return AskResponse{}, o.translateContextErr(callCtx, ctx, err,
				fmt.Sprintf("local_llm agent: ensure backend: %v", err))
		}
	}

	// Decide whether to grammar-constrain. Only when grammar is enabled, a
	// schema is present, and that schema is inside the translatable subset.
	grammarApplied := false
	var rf *responseFormat
	if o.grammar && req.SchemaJSON != nil && GrammarSubsetOK(req.SchemaJSON) == nil {
		grammarApplied = true
		rf = &responseFormat{
			Type: "json_schema",
			JSONSchema: &jsonSchemaSpec{
				Name:   "submission",
				Strict: true,
				Schema: req.SchemaJSON,
			},
		}
	}

	bodyBytes, err := json.Marshal(chatRequest{
		Model:          o.model,
		Messages:       []chatMessage{{Role: "user", Content: req.PromptText}},
		ResponseFormat: rf,
	})
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("local_llm agent: marshal chat request: %v", err),
		}
	}

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("local_llm agent: build http request: %v", err),
		}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := o.client.Do(httpReq)
	if err != nil {
		return AskResponse{}, o.translateContextErr(callCtx, ctx, err,
			fmt.Sprintf("local_llm agent: http do: %v", err))
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, MaxHTTPResponseSize))
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("local_llm agent: read response body: %v", err),
		}
	}

	if httpResp.StatusCode >= 400 {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("local_llm agent: http %d: %s", httpResp.StatusCode, truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}

	var cr chatResponse
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return AskResponse{}, &AskError{
			Kind:       "transport_error",
			Underlying: err,
			Detail:     fmt.Sprintf("local_llm agent: unmarshal chat response: %v (raw: %q)", err, truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}

	if len(cr.Choices) == 0 {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("local_llm agent: response has no choices (raw: %q)", truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}
	content := cr.Choices[0].Message.Content
	if content == "" {
		return AskResponse{}, &AskError{
			Kind:   "transport_error",
			Detail: fmt.Sprintf("local_llm agent: first choice has empty content (raw: %q)", truncateBytes(respBody, ErrorDetailTruncateBytes)),
		}
	}

	// Strip markdown code fences that small models add despite grammar
	// constraints. Matches ```json\n...\n``` and ```\n...\n``` wrappers.
	content = stripCodeFence(content)

	return AskResponse{
		Submission: json.RawMessage(content),
		Meta: map[string]any{
			"model":             o.model,
			"prompt_tokens":     cr.Usage.PromptTokens,
			"completion_tokens": cr.Usage.CompletionTokens,
			"grammar":           grammarApplied,
		},
		Transcript: o.buildTranscript(req.PromptText, cr),
	}, nil
}

// stripCodeFence removes a leading ```json\n or ```\n fence and a trailing ```
// from s, returning the trimmed inner content. Returns s unchanged when no
// fence is present. Small models sometimes add these despite grammar constraints.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```json\n", "```json\r\n", "```\n", "```\r\n"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			if idx := strings.LastIndex(s, "```"); idx >= 0 {
				s = strings.TrimSpace(s[:idx])
			}
			return s
		}
	}
	return s
}

// buildTranscript synthesizes an "openai-chat"-format Transcript from the single
// request/response pair this transport makes. The local model serves
// schema-shaped decide/routing today and makes NO tool calls, so the baseline is
// a three-event triple — request, assistant content, terminal result with usage
// — that the "Agent actions" drawer renders uniformly with the claude path.
//
// The events deliberately imitate the OpenAI chat-completion wire shape (a
// `messages`-style user object, an assistant `message` object, a `result`
// envelope) so the consumer's openai-chat renderer needs no local_llm-specific
// branch. Should a future tool-using local model populate
// choices[0].message.tool_calls, each tool call is emitted as its own event,
// tolerating BOTH the OpenAI nested {id,function:{name,arguments}} and the
// llama.cpp-flattened {name,arguments} shapes (see the proposal's cross-backend
// note); today none are present, so only the triple is produced.
func (o *LocalLLMAgent) buildTranscript(prompt string, cr chatResponse) *Transcript {
	events := make([]json.RawMessage, 0, 4)

	// 1. Request: the user prompt that was sent.
	if ev, err := json.Marshal(map[string]any{
		"type":    "request",
		"model":   o.model,
		"message": map[string]any{"role": "user", "content": prompt},
	}); err == nil {
		events = append(events, ev)
	}

	// 2. Assistant response content.
	msg := cr.Choices[0].Message
	if ev, err := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"role": "assistant", "content": msg.Content},
	}); err == nil {
		events = append(events, ev)
	}

	// (future) Tool calls, if the model emits any. Tolerate both wire shapes.
	for _, tc := range msg.ToolCalls {
		name := tc.Name
		args := tc.Arguments
		if tc.Function != nil {
			name = tc.Function.Name
			args = tc.Function.Arguments
		}
		if ev, err := json.Marshal(map[string]any{
			"type": "tool_use",
			"id":   tc.ID,
			"name": name,
			// arguments stay a JSON-encoded string on the OpenAI wire; surface
			// verbatim so the renderer can pretty-print or pass through.
			"arguments": args,
		}); err == nil {
			events = append(events, ev)
		}
	}

	// 3. Terminal result carrying usage tokens.
	if ev, err := json.Marshal(map[string]any{
		"type":   "result",
		"result": msg.Content,
		"usage": map[string]any{
			"input_tokens":  cr.Usage.PromptTokens,
			"output_tokens": cr.Usage.CompletionTokens,
		},
	}); err == nil {
		events = append(events, ev)
	}

	if len(events) == 0 {
		return nil
	}
	return &Transcript{Format: "openai-chat", Events: events}
}

// translateContextErr maps a transport-level failure to a typed AskError,
// classifying it as deadline_exceeded when either context was cancelled or timed
// out (so a slow model or a cancelled turn surfaces correctly) and
// transport_error otherwise.
func (o *LocalLLMAgent) translateContextErr(callCtx, ctx context.Context, err error, detail string) *AskError {
	if callCtx.Err() == context.DeadlineExceeded || ctx.Err() == context.DeadlineExceeded ||
		callCtx.Err() == context.Canceled || ctx.Err() == context.Canceled {
		return &AskError{
			Kind:       "deadline_exceeded",
			Underlying: err,
			Detail:     detail,
		}
	}
	return &AskError{
		Kind:       "transport_error",
		Underlying: err,
		Detail:     detail,
	}
}

// Close releases idle HTTP connections and, in managed mode, terminates the
// sidecar. In endpoint mode the sidecar Close is a no-op (we did not start it).
func (o *LocalLLMAgent) Close() error {
	if t, ok := o.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	if o.endpoint == "" && o.sidecar != nil {
		return o.sidecar.Close()
	}
	return nil
}
