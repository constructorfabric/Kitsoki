// Package mcp — operator-question forwarding MCP server.
//
// OperatorAskServer is a stdio MCP server that exposes a single `ask` tool —
// the supported replacement for the built-in AskUserQuestion tool, which is
// hard-denied on every agent subprocess because headless `claude -p` has no TTY
// and auto-resolves it with empty answers (anthropics/claude-code#50728).
//
// When a dispatched agent calls `ask`, this server does NOT answer the question
// itself. It forwards the questions over a per-call unix socket to the kitsoki
// host handler that spawned the agent (the socket path arrives via
// OperatorAskConfig.SocketPath, set from $KITSOKI_OPERATOR_ASK_SOCK), BLOCKS
// until the host returns the operator's answer (collected from the live web/TUI
// surface), and returns that answer to the model as the tool result. The host
// side of the wire (the listener + OperatorPrompter bridge) is wired in phase 3;
// this file owns the agent-facing tool and the client half of the protocol.
//
// Wire protocol (newline-delimited JSON, one request → one response, same
// framing as agent-serve):
//
//	→ {"questions":[{"question":…,"header":…,"options":[{"label":…,"description":…}],"multiSelect":…}]}
//	← {"answers":{"<question text>":"<label-or-custom-answer>"|["<label>",…]}} // success
//	← {"error":"operator did not answer"}                                // surfaced to the model
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultOperatorAskTool is the tool name advertised over the wire when the
// config leaves ToolName empty. Kept distinct from AskUserQuestion so the model
// never confuses the two; the dispatch layer's system-prompt clause points the
// agent at this name.
const defaultOperatorAskTool = "ask"

// operatorAskInputSchema is the `ask` tool's JSON Schema, mirroring the built-in
// AskUserQuestion input shape verbatim so the replacement is a drop-in and the
// model's existing instinct for that tool transfers directly.
const operatorAskInputSchema = `{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "description": "Questions to ask the operator.",
      "items": {
        "type": "object",
        "required": ["question", "header", "options"],
        "properties": {
          "question": { "type": "string", "description": "The full question to ask the operator." },
          "header": { "type": "string", "description": "Short label/chip categorising the question (max 12 chars)." },
          "multiSelect": { "type": "boolean", "description": "When true the operator may select more than one option." },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "required": ["label"],
              "properties": {
                "label": { "type": "string", "description": "The choice the operator picks; this label is returned to you." },
                "description": { "type": "string", "description": "What this choice means." }
              }
            }
          }
        }
      }
    }
  },
  "required": ["questions"]
}`

// OperatorAskOption mirrors one AskUserQuestion option on the wire.
type OperatorAskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// OperatorAskQuestion mirrors one AskUserQuestion question on the wire.
type OperatorAskQuestion struct {
	Question    string              `json:"question"`
	Header      string              `json:"header,omitempty"`
	Options     []OperatorAskOption `json:"options"`
	MultiSelect bool                `json:"multiSelect,omitempty"`
}

// OperatorAskRequest is the JSON frame sent from the `ask` tool to the host
// listener. It is exactly the tool's validated arguments.
type OperatorAskRequest struct {
	Questions []OperatorAskQuestion `json:"questions"`
}

// OperatorAskResponse is the JSON frame returned by the host listener. Exactly
// one of Answers / Error is populated. Answers is keyed by each question's text;
// each value is the chosen label or custom single-select answer (string) or,
// for a multiSelect question, the chosen labels ([]string) — the AskUserQuestion
// answer shape.
type OperatorAskResponse struct {
	Answers map[string]any `json:"answers,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// OperatorAskConfig configures an OperatorAskServer.
type OperatorAskConfig struct {
	// SocketPath is the unix socket the `ask` tool dials to forward questions
	// to the host. Required.
	SocketPath string
	// ToolName overrides the advertised tool name (default "ask").
	ToolName string
	// ToolDescription overrides the tool description shown to the model.
	ToolDescription string
	// DialTimeout bounds how long the tool waits to CONNECT to the socket
	// (not how long it waits for the operator's answer — that is bounded
	// host-side). Zero uses a small default.
	DialTimeout time.Duration
}

// OperatorAskServer is the MCP-protocol surface of the operator-ask tool.
type OperatorAskServer struct {
	mcpSrv      *mcpsdk.Server
	socketPath  string
	toolName    string
	dialTimeout time.Duration
}

// NewOperatorAskServer builds the server. SocketPath is required.
func NewOperatorAskServer(cfg OperatorAskConfig) (*OperatorAskServer, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("operator-ask: SocketPath is required (set $KITSOKI_OPERATOR_ASK_SOCK)")
	}
	toolName := cfg.ToolName
	if toolName == "" {
		toolName = defaultOperatorAskTool
	}
	desc := cfg.ToolDescription
	if desc == "" {
		desc = "Ask the kitsoki operator a multiple-choice question and receive their answer. " +
			"Use this whenever you need a decision or clarification from the human running this session. " +
			"The call blocks until the operator answers; their selected option label(s), or a custom single-select answer when the operator provides one, are returned to you."
	}
	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}
	s := &OperatorAskServer{
		socketPath:  cfg.SocketPath,
		toolName:    toolName,
		dialTimeout: dialTimeout,
	}
	s.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki-operator-ask",
		Version: "0.1.0",
	}, nil)
	s.mcpSrv.AddTool(&mcpsdk.Tool{
		Name:        toolName,
		Description: desc,
		InputSchema: json.RawMessage(operatorAskInputSchema),
	}, s.handleAsk)
	return s, nil
}

// Run starts the server on stdio and blocks until the peer disconnects or ctx
// is cancelled.
func (s *OperatorAskServer) Run(ctx context.Context) error {
	return s.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying SDK server for in-process tests.
func (s *OperatorAskServer) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return s.mcpSrv.Connect(ctx, t, opts)
}

// handleAsk parses the questions, forwards them to the host over the socket, and
// returns the operator's answer (or an LLM-visible error) as the tool result.
func (s *OperatorAskServer) handleAsk(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	raw := req.Params.Arguments
	if len(raw) == 0 {
		return errorResult("ask: no arguments provided; pass {\"questions\":[…]}"), nil
	}
	var parsed OperatorAskRequest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return errorResult(fmt.Sprintf("ask: arguments are not valid JSON: %v", err)), nil
	}
	if len(parsed.Questions) == 0 {
		return errorResult("ask: questions[] is empty; provide at least one question"), nil
	}

	resp, err := dialOperatorAsk(ctx, s.socketPath, s.dialTimeout, parsed)
	if err != nil {
		// Infrastructure failure (couldn't reach the host). Surface as an
		// LLM-visible error so the agent proceeds without the input rather
		// than the whole run failing opaquely.
		return errorResult(fmt.Sprintf("ask: could not reach the operator (%v); proceed using your best judgement", err)), nil
	}
	if resp.Error != "" {
		return errorResult(fmt.Sprintf("ask: %s; proceed using your best judgement", resp.Error)), nil
	}

	answersJSON, _ := json.Marshal(resp.Answers)
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{
				Text: "The operator answered your question(s). Their selections (keyed by question text):\n" +
					string(answersJSON) +
					"\nContinue using these answers.",
			},
		},
	}, nil
}

// dialOperatorAsk connects to the host listener, sends the request as one JSON
// line, and reads exactly one JSON response line. The connect is bounded by
// dialTimeout; the read is NOT (the operator may take a while to answer) and is
// instead governed by ctx and the host-side timeout, which closes the connection
// on expiry.
func dialOperatorAsk(ctx context.Context, socketPath string, dialTimeout time.Duration, req OperatorAskRequest) (OperatorAskResponse, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return OperatorAskResponse{}, fmt.Errorf("dial %q: %w", socketPath, err)
	}
	defer conn.Close()

	// Close the connection when ctx is cancelled so a blocked read unblocks.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	payload, err := json.Marshal(req)
	if err != nil {
		return OperatorAskResponse{}, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return OperatorAskResponse{}, fmt.Errorf("write request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return OperatorAskResponse{}, fmt.Errorf("read response: %w", err)
	}
	var resp OperatorAskResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return OperatorAskResponse{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}
