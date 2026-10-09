package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	starlarkhost "kitsoki/internal/host/starlark"
)

const codeactEvalInputSchema = `{
  "type": "object",
  "properties": {
    "snippet": {
      "type": "string",
      "description": "Starlark source defining def main(ctx): ... . The return value must be a dict."
    },
    "inputs": {
      "type": "object",
      "description": "Optional ctx.inputs values for this snippet.",
      "additionalProperties": true
    },
    "world": {
      "type": "object",
      "description": "Optional read-only ctx.world values for this snippet.",
      "additionalProperties": true
    }
  },
  "required": ["snippet"],
  "additionalProperties": false
}`

// CodeactConfig configures the standalone codeact MCP server. Capabilities are
// a server-side ceiling: tool callers can provide snippets and data, but cannot
// grant themselves new ctx surfaces from inside a tool call.
type CodeactConfig struct {
	ToolName        string
	ToolDescription string
	WorkingDir      string
	Capabilities    starlarkhost.CapabilitySpec
}

// CodeactServer is a stdio MCP server exposing one deterministic code-action
// tool. It runs caller-supplied Starlark snippets through the same sandbox used
// by host.starlark.run and host.agent.codeact, without launching a nested LLM.
type CodeactServer struct {
	mcpSrv       *mcpsdk.Server
	toolName     string
	workingDir   string
	capabilities starlarkhost.CapabilitySpec
}

// CodeactEvalArgs is the input to the codeact_eval MCP tool.
type CodeactEvalArgs struct {
	Snippet string         `json:"snippet"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	World   map[string]any `json:"world,omitempty"`
}

// CodeactEvalOK is the structured result of a successful Starlark code action.
type CodeactEvalOK struct {
	OK           bool                           `json:"ok"`
	Outputs      map[string]any                 `json:"outputs"`
	Exchanges    []starlarkhost.HTTPExchange    `json:"http_exchanges,omitempty"`
	Inspections  []starlarkhost.InspectExchange `json:"inspections,omitempty"`
	Capabilities []string                       `json:"capabilities,omitempty"`
	WorkingDir   string                         `json:"working_dir,omitempty"`
}

// CodeactEvalError is the structured result shape used for tool-visible
// Starlark/domain errors.
type CodeactEvalError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// NewCodeactServer constructs a standalone MCP server exposing codeact_eval.
func NewCodeactServer(cfg CodeactConfig) (*CodeactServer, error) {
	toolName := strings.TrimSpace(cfg.ToolName)
	if toolName == "" {
		toolName = "codeact_eval"
	}
	desc := strings.TrimSpace(cfg.ToolDescription)
	if desc == "" {
		desc = "Run one Starlark code action inside Kitsoki's capability-scoped sandbox. " +
			"Use this instead of Bash, Python, or Node when the attached harness should only act through declared ctx surfaces. " +
			"The server configuration, not the tool call, controls available capabilities."
	}
	cap := cfg.Capabilities
	if cap.Stdlib == nil && !cap.World && !cap.NeedsHTTP() && !cap.NeedsInspector() && !cap.AllowsHost() {
		cap = starlarkhost.DefaultCapabilities()
	}
	if cap.AllowsHost() {
		return nil, fmt.Errorf("codeact mcp: host capabilities are not supported by standalone mcp-codeact")
	}
	if cap.RequiresInjectedHTTP() {
		return nil, fmt.Errorf("codeact mcp: http.cassette_required is not supported by standalone mcp-codeact")
	}
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("codeact mcp: resolve working directory: %w", err)
		}
	}
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("codeact mcp: resolve working directory %q: %w", workingDir, err)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		return nil, fmt.Errorf("codeact mcp: working directory %q is not accessible: %w", abs, err)
	}

	s := &CodeactServer{
		toolName:     toolName,
		workingDir:   abs,
		capabilities: cap,
	}
	s.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki-codeact",
		Version: "0.1.0",
	}, nil)
	s.mcpSrv.AddTool(&mcpsdk.Tool{
		Name:        toolName,
		Description: desc,
		InputSchema: json.RawMessage(codeactEvalInputSchema),
	}, s.handleEval)
	return s, nil
}

// Run starts the codeact MCP server on stdio and blocks until the peer
// disconnects or ctx is cancelled.
func (s *CodeactServer) Run(ctx context.Context) error {
	return s.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying SDK server for in-process tests.
func (s *CodeactServer) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return s.mcpSrv.Connect(ctx, t, opts)
}

func (s *CodeactServer) handleEval(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	if len(req.Params.Arguments) == 0 {
		return codeactToolError("codeact_eval: no arguments provided"), nil
	}
	var args CodeactEvalArgs
	if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
		return codeactToolError(fmt.Sprintf("codeact_eval: arguments are not valid JSON: %v", err)), nil
	}
	if strings.TrimSpace(args.Snippet) == "" {
		return codeactToolError("codeact_eval: snippet is required"), nil
	}

	runCtx := ctx
	if s.capabilities.NeedsHTTP() && !starlarkhost.HasHTTPClient(runCtx) {
		runCtx = starlarkhost.WithHTTP(runCtx, starlarkhost.NewRecordingClient())
	}
	if s.capabilities.NeedsInspector() && !starlarkhost.HasInspector(runCtx) {
		runCtx = starlarkhost.WithInspector(runCtx, starlarkhost.NewProductionInspector(s.workingDir))
	}

	res, err := starlarkhost.Run(runCtx, starlarkhost.Params{
		Script:       "<mcp-codeact-snippet>",
		Source:       []byte(args.Snippet),
		Inputs:       args.Inputs,
		World:        args.World,
		Capabilities: s.capabilities,
	})
	if err != nil {
		if msg, ok := starlarkhost.AsDomainError(err); ok {
			return codeactToolError("codeact_eval: " + msg), nil
		}
		return nil, fmt.Errorf("codeact_eval: %w", err)
	}

	out := CodeactEvalOK{
		OK:           true,
		Outputs:      res.Outputs,
		Exchanges:    res.Exchanges,
		Inspections:  res.Inspections,
		Capabilities: s.capabilities.CapabilityLabels(),
		WorkingDir:   s.workingDir,
	}
	return codeactToolJSON(out, false), nil
}

func codeactToolError(msg string) *mcpsdk.CallToolResult {
	return codeactToolJSON(CodeactEvalError{OK: false, Error: msg}, true)
}

func codeactToolJSON(v any, isError bool) *mcpsdk.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"ok":false,"error":"marshal tool result: %s"}`, err))
		isError = true
	}
	return &mcpsdk.CallToolResult{
		IsError:           isError,
		StructuredContent: v,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}
}
