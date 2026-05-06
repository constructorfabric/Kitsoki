// Package mcp — schema-validating MCP server.
//
// ValidatorServer is a stdio MCP server that exposes a single `submit` tool.
// The tool's input schema is the user-supplied JSON Schema; on call, the
// arguments are validated against it. On invalid input the handler returns
// `isError: true` with a human-readable error list so the calling LLM can
// self-correct and call again — all within the same `claude -p` conversation.
//
// Used by `host.oracle.ask_with_mcp` (auto-attached when the effect declares
// a `schema:` arg) and exposed standalone via `hally mcp-validator`.
//
// Compared to cyber-repo's `tools/loopy/wiggum-mcp.py`:
//   - No artifact directory side-effect: claude's stdout carries the
//     validated payload back to hally via `output_format: json` →
//     `bind: stdout_json`.
//   - Schema is per-invocation, not phase-keyed; one server, one schema.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidatorServer is the MCP-protocol surface of the schema validator.
type ValidatorServer struct {
	mcpSrv *mcpsdk.Server
	// schemaRaw is the user-supplied schema as raw JSON, used as the
	// `submit` tool's InputSchema so the LLM sees the constraints in
	// `tools/list`.
	schemaRaw json.RawMessage
	// compiled is the same schema compiled into the validator.
	compiled *jsonschema.Schema
	// toolName is the tool advertised over the wire (default "submit").
	toolName string
	// description is the tool description shown to the LLM.
	description string
	// outputPath, when non-empty, receives the validated JSON every time
	// `submit` is called with a payload that passes schema validation.
	// The file is overwritten (last-call-wins). This is the side channel
	// host.oracle.ask_with_mcp uses to recover the canonical, validated
	// payload after `claude -p` exits — independent of whatever the model
	// chooses to write as its final response.
	outputPath string

	// postCmd, when non-empty, is run after schema-pass to layer a
	// semantic verifier on top of the structural check. See ValidatorConfig.
	postCmd     string
	postCmdArgs []PostCmdArg
	postCmdCwd  string
	maxRetries  int

	// Session state — protected by mu. Each `submit` call mutates these;
	// Outcome() reads them. The validator instance is shared across calls
	// within a single Run(ctx), so this state is the source of truth for
	// "have we successfully captured a payload yet" and "are we out of
	// retries".
	mu                sync.Mutex
	attempts          int    // total submit calls (any outcome)
	successfulSubmits int    // submits that passed schema + post-cmd
	lastError         string // most recent rejection reason (for diagnostics)
}

// PostCmdArg is a single key/value pair forwarded to the --post-cmd
// subprocess as `--<Key> <Value>`. The CLI parses --post-cmd-arg
// key=value flags into a slice of these (preserving order so deterministic
// argv composition is possible).
type PostCmdArg struct {
	Key   string
	Value string
}

// ValidatorConfig configures a ValidatorServer.
type ValidatorConfig struct {
	// SchemaJSON is the JSON Schema document. Must be a JSON object.
	SchemaJSON []byte
	// ToolName overrides the default "submit" tool name.
	ToolName string
	// ToolDescription overrides the default description shown to the LLM.
	ToolDescription string
	// OutputPath, when non-empty, instructs the validator to write the
	// validated JSON to that path on each successful submit. Used by
	// host.oracle.ask_with_mcp to capture the canonical payload from the
	// tool call rather than from the LLM's final stdout text.
	OutputPath string

	// PostCmd is the shell-quoted command run after schema-pass. Empty =
	// schema-only validation (backwards-compatible behaviour).
	PostCmd string
	// PostCmdArgs are repeatable key/value pairs forwarded as
	// --<Key> <Value>. Order is preserved.
	PostCmdArgs []PostCmdArg
	// PostCmdCwd is the working directory for the post-cmd subprocess.
	// Empty = inherit hally's cwd.
	PostCmdCwd string
	// MaxRetries caps the total number of submit attempts (schema-fail +
	// post-cmd-fail combined). Zero or negative is treated as the default
	// (5). On exhaustion the validator returns a final-error response and
	// Run() reports OutcomeRetriesExhausted.
	MaxRetries int
}

// NewValidatorServer constructs a ValidatorServer from a raw JSON Schema.
// Returns an error if the schema fails to parse or compile.
func NewValidatorServer(cfg ValidatorConfig) (*ValidatorServer, error) {
	if len(cfg.SchemaJSON) == 0 {
		return nil, fmt.Errorf("mcp.NewValidatorServer: SchemaJSON is required")
	}

	// Parse to confirm it's an object (the SDK requires the input schema
	// to have type == "object").
	var probe map[string]any
	if err := json.Unmarshal(cfg.SchemaJSON, &probe); err != nil {
		return nil, fmt.Errorf("mcp.NewValidatorServer: parse schema: %w", err)
	}
	if t, _ := probe["type"].(string); t != "object" {
		return nil, fmt.Errorf("mcp.NewValidatorServer: top-level schema type must be \"object\" (got %q)", t)
	}

	compiler := jsonschema.NewCompiler()
	// Register custom semantic formats (e.g. JQL) and switch the compiler
	// from annotation-only to assertion mode so format errors surface as
	// kind.Format failures through BasicOutput like any other constraint.
	compiler.RegisterFormat(&jsonschema.Format{Name: "jql", Validate: validateJQL})
	compiler.AssertFormat()
	if err := compiler.AddResource("validator-schema.json", probe); err != nil {
		return nil, fmt.Errorf("mcp.NewValidatorServer: register schema: %w", err)
	}
	compiled, err := compiler.Compile("validator-schema.json")
	if err != nil {
		return nil, fmt.Errorf("mcp.NewValidatorServer: compile schema: %w", err)
	}

	toolName := cfg.ToolName
	if toolName == "" {
		toolName = "submit"
	}
	desc := cfg.ToolDescription
	if desc == "" {
		desc = "Submit a JSON object that conforms to the input schema. " +
			"Validation runs server-side; any errors are returned inline so you can " +
			"correct the payload and call this tool again. Once submit returns " +
			"successfully, the validated payload has been captured — your final " +
			"response can be a brief confirmation; you do not need to repeat the JSON."
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}

	srv := &ValidatorServer{
		schemaRaw:   append(json.RawMessage(nil), cfg.SchemaJSON...),
		compiled:    compiled,
		toolName:    toolName,
		description: desc,
		outputPath:  cfg.OutputPath,
		postCmd:     strings.TrimSpace(cfg.PostCmd),
		postCmdArgs: append([]PostCmdArg(nil), cfg.PostCmdArgs...),
		postCmdCwd:  cfg.PostCmdCwd,
		maxRetries:  maxRetries,
	}

	srv.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "hally-validator",
		Version: "0.1.0",
	}, nil)

	srv.mcpSrv.AddTool(&mcpsdk.Tool{
		Name:        toolName,
		Description: desc,
		InputSchema: srv.schemaRaw,
	}, srv.handleSubmit)

	return srv, nil
}

// Run starts the validator on stdio and blocks until the peer disconnects
// or ctx is cancelled.
func (v *ValidatorServer) Run(ctx context.Context) error {
	return v.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying SDK server for in-process tests.
func (v *ValidatorServer) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return v.mcpSrv.Connect(ctx, t, opts)
}

// handleSubmit validates the incoming arguments against the compiled schema.
// On validation failure returns isError: true with a multi-line error list.
// On success the validated JSON is captured to outputPath (when set) so the
// parent process can recover the canonical payload from the tool call —
// independent of whatever text the LLM eventually writes as its final
// response.
func (v *ValidatorServer) handleSubmit(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	raw := req.Params.Arguments
	if len(raw) == 0 {
		return errorResult("submit: no arguments provided; pass the JSON payload as the tool arguments"), nil
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return errorResult(fmt.Sprintf("submit: arguments are not valid JSON: %v", err)), nil
	}

	if err := v.compiled.Validate(instance); err != nil {
		msg := formatValidationError(err)
		return errorResult(msg), nil
	}

	// Capture to the side-channel file. We use the raw arguments rather
	// than re-marshalling the parsed instance so byte-level fidelity is
	// preserved (numeric precision, key order from the LLM, etc.).
	if v.outputPath != "" {
		if err := writeOutputAtomically(v.outputPath, raw); err != nil {
			return errorResult(fmt.Sprintf("submit: capture validated payload: %v", err)), nil
		}
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{
				Text: "OK: payload validated against the schema and captured. " +
					"You may end your turn now; no need to repeat the JSON.",
			},
		},
	}, nil
}

// writeOutputAtomically writes data to path via a temp file + rename, so
// readers (the parent process) never observe a partial write.
func writeOutputAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hally-validator-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// errorResult wraps an error message as an MCP tool failure that the LLM
// should treat as "fix and retry".
func errorResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: text},
		},
	}
}

// formatValidationError renders a jsonschema.ValidationError into a
// human-readable, LLM-facing message. It uses the library's BasicOutput
// (flat list of leaf failures) so the LLM sees one line per problem and
// the message text is rendered through the library's localizer rather
// than reaching into ErrorKind directly (which crashes when called with
// a nil printer).
func formatValidationError(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return "submit: schema validation failed: " + err.Error()
	}
	out := ve.BasicOutput()
	lines := collectBasicOutputLeaves(out)
	if len(lines) == 0 {
		// Fall back to the library's own multi-line render.
		return "submit: schema validation failed:\n" + ve.Error()
	}
	return "submit: schema validation failed. Fix these errors and call submit again:\n\n" +
		"  - " + strings.Join(lines, "\n  - ")
}

// collectBasicOutputLeaves walks a BasicOutput tree and returns one
// "instance/location: reason" line per leaf failure. Non-leaf nodes
// (those with nested Errors) are skipped — their leaves carry the
// concrete reason.
func collectBasicOutputLeaves(unit *jsonschema.OutputUnit) []string {
	if unit == nil {
		return nil
	}
	var out []string
	walkBasicOutput(unit, &out)
	return out
}

func walkBasicOutput(unit *jsonschema.OutputUnit, out *[]string) {
	if unit == nil {
		return
	}
	if unit.Valid {
		return
	}
	if len(unit.Errors) == 0 && unit.Error != nil {
		loc := unit.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		*out = append(*out, loc+": "+unit.Error.String())
		return
	}
	for i := range unit.Errors {
		walkBasicOutput(&unit.Errors[i], out)
	}
}

