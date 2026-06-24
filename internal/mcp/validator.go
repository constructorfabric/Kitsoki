// Package mcp — schema-validating MCP server.
//
// ValidatorServer is a stdio MCP server that exposes a single `submit` tool.
// The tool's input schema is the user-supplied JSON Schema; on call, the
// arguments are validated against it. On invalid input the handler returns
// `isError: true` with a human-readable error list so the calling LLM can
// self-correct and call again — all within the same `claude -p` conversation.
//
// Used by `host.agent.ask_with_mcp` (auto-attached when the effect declares
// a `schema:` arg) and exposed standalone via `kitsoki mcp-validator`.
//
// Design notes vs. an external phase-keyed validator MCP:
//   - No artifact directory side-effect: claude's stdout carries the
//     validated payload back to kitsoki via `output_format: json` →
//     `bind: stdout_json`.
//   - Schema is per-invocation, not phase-keyed; one server, one schema.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// postCmdStderrCap caps captured stderr at this many bytes (tail-preserved)
// before being returned to the LLM. Verifiers can produce verbose output;
// 2 KiB is enough to convey one or two distinct failures while keeping the
// prompt cheap.
const postCmdStderrCap = 2000

// ansiEscapeRE strips CSI sequences (most ANSI colours) from captured
// stderr. Verifiers like pytest/rich often emit colour codes that would
// otherwise leak into the LLM's context as noise.
var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// postCmdArgKeyRe constrains post-cmd-arg keys so they render to exactly one
// `--<Key>` argv slot. A key with spaces (or other metacharacters) would
// otherwise create stray argv slots in the post-cmd subprocess.
var postCmdArgKeyRe = regexp.MustCompile(`^[a-z0-9-]+$`)

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
	// host.agent.ask_with_mcp uses to recover the canonical, validated
	// payload after `claude -p` exits — independent of whatever the model
	// chooses to write as its final response.
	outputPath string

	// postCmd, when non-empty, is run after schema-pass to layer a
	// semantic verifier on top of the structural check. See ValidatorConfig.
	postCmd     string
	postCmdArgs []PostCmdArg
	postCmdCwd  string
	maxRetries  int

	// stateFilePath, when non-empty, makes counters (attempts /
	// successfulSubmits / lastError) survive process restarts. See
	// ValidatorConfig.StateFilePath for rationale. Empty = volatile.
	stateFilePath string

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

// validatorState is the on-disk shape of the persisted counters when
// ValidatorConfig.StateFilePath is set. JSON-encoded, atomically rewritten
// after every mutation under the validator's lock.
type validatorState struct {
	Attempts          int    `json:"attempts"`
	SuccessfulSubmits int    `json:"successful_submits"`
	LastError         string `json:"last_error"`
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
	// host.agent.ask_with_mcp to capture the canonical payload from the
	// tool call rather than from the LLM's final stdout text.
	OutputPath string

	// PostCmd is the shell-quoted command run after schema-pass. Empty =
	// schema-only validation (backwards-compatible behaviour).
	PostCmd string
	// PostCmdArgs are repeatable key/value pairs forwarded as
	// --<Key> <Value>. Order is preserved.
	PostCmdArgs []PostCmdArg
	// PostCmdCwd is the working directory for the post-cmd subprocess.
	// Empty = inherit kitsoki's cwd.
	PostCmdCwd string
	// MaxRetries caps the total number of submit attempts (schema-fail +
	// post-cmd-fail combined). Zero or negative is treated as the default
	// (5). On exhaustion the validator returns a final-error response and
	// Run() reports OutcomeRetriesExhausted.
	MaxRetries int

	// StateFilePath, when non-empty, makes session counters
	// (attempts/successfulSubmits/lastError) survive a process restart.
	// At startup the validator reads JSON state from this file (if it
	// exists) to seed its counters; after every submit it rewrites the
	// file atomically. host.agent.ask_with_mcp uses this to keep one
	// logical "validator session" across multiple `claude --resume`
	// re-engagements (each re-engagement spawns a fresh kitsoki
	// mcp-validator subprocess but they all share the same state file).
	// Empty = volatile in-memory counters only (the default).
	StateFilePath string
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

	// Compile via the shared helper so the validator MCP server and the
	// host.agent.decide recovery path enforce byte-for-byte identical schema
	// semantics (same jql format registration + assertion mode).
	compiled, err := CompileSchema(cfg.SchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("mcp.NewValidatorServer: %w", err)
	}

	// Validate post-cmd arg keys at parse time so a key with spaces (or other
	// metacharacters) can't smuggle stray argv slots into the subprocess via
	// the `--<Key> <Value>` rendering in runPostCmd.
	for _, kv := range cfg.PostCmdArgs {
		if !postCmdArgKeyRe.MatchString(kv.Key) {
			return nil, fmt.Errorf("mcp.NewValidatorServer: invalid post-cmd-arg key %q: must match %s", kv.Key, postCmdArgKeyRe.String())
		}
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
		schemaRaw:     append(json.RawMessage(nil), cfg.SchemaJSON...),
		compiled:      compiled,
		toolName:      toolName,
		description:   desc,
		outputPath:    cfg.OutputPath,
		postCmd:       strings.TrimSpace(cfg.PostCmd),
		postCmdArgs:   append([]PostCmdArg(nil), cfg.PostCmdArgs...),
		postCmdCwd:    cfg.PostCmdCwd,
		maxRetries:    maxRetries,
		stateFilePath: cfg.StateFilePath,
	}

	// Seed counters from the state file (if present and well-formed).
	// A missing file is the normal "first iteration" case; a malformed
	// file is treated as "no prior state" rather than fatal so an
	// operator can hand-edit / wipe the file without crashing the run.
	if srv.stateFilePath != "" {
		if data, rerr := os.ReadFile(srv.stateFilePath); rerr == nil && len(data) > 0 {
			var st validatorState
			if jerr := json.Unmarshal(data, &st); jerr == nil {
				srv.attempts = st.Attempts
				srv.successfulSubmits = st.SuccessfulSubmits
				srv.lastError = st.LastError
			}
		}
	}

	srv.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki-validator",
		Version: "0.1.0",
	}, nil)

	srv.mcpSrv.AddTool(&mcpsdk.Tool{
		Name:        toolName,
		Description: desc,
		InputSchema: srv.schemaRaw,
	}, srv.handleSubmit)

	return srv, nil
}

// Outcome reports the validator's session-level disposition. The
// orchestrator (host.agent.ask_with_mcp, sub-task B) reads this after
// the MCP session ends to decide whether to (a) accept the captured
// payload, (b) re-engage the same `claude --continue` conversation with
// a "you forgot to call submit_result" nudge, or (c) fire on_error.
type Outcome int

const (
	// OutcomeUnknown is the zero value — no submit calls observed yet.
	// In practice this means the LLM never even attempted submit; the
	// orchestrator should treat it as OutcomeAbandoned at the session
	// boundary.
	OutcomeUnknown Outcome = iota
	// OutcomeSuccess means at least one submit passed schema (and
	// post-cmd, when configured). The captured payload is in the side-
	// channel file.
	OutcomeSuccess
	// OutcomeRetriesExhausted means the LLM burned through `MaxRetries`
	// submit attempts without ever producing an accepted payload. The
	// orchestrator should fire on_error: no further re-engagement helps.
	OutcomeRetriesExhausted
	// OutcomeAbandoned means the LLM session ended (stdin EOF) with no
	// successful submit and the retry budget not yet spent. The
	// orchestrator may re-engage via `claude --continue` with a nudge
	// prompt before falling back to on_error.
	OutcomeAbandoned
)

// String renders the outcome for logging.
func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "success"
	case OutcomeRetriesExhausted:
		return "retries_exhausted"
	case OutcomeAbandoned:
		return "abandoned"
	default:
		return "unknown"
	}
}

// Outcome computes the validator's session-level disposition based on
// the current attempts/successfulSubmits counters. It is safe to call
// any time, including before Run(ctx) returns — the result reflects
// whatever the LLM has done so far.
//
// Precedence:
//   - successfulSubmits >= 1                                  → Success
//   - attempts >= maxRetries (no successful submit)           → RetriesExhausted
//   - otherwise (attempts < maxRetries, no successful submit) → Abandoned
//
// Note that Abandoned only makes operational sense once the session has
// ended; while a session is live a low attempt count just means the LLM
// hasn't tried hard enough yet. The caller is responsible for picking
// the right moment to read this — typically right after Run() returns.
func (v *ValidatorServer) Outcome() Outcome {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.successfulSubmits >= 1 {
		return OutcomeSuccess
	}
	if v.maxRetries > 0 && v.attempts >= v.maxRetries {
		return OutcomeRetriesExhausted
	}
	return OutcomeAbandoned
}

// Stats returns a snapshot of the validator's session-level counters.
// Useful for tests and for diagnostic logging from the orchestrator.
func (v *ValidatorServer) Stats() (attempts, successfulSubmits int, lastError string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.attempts, v.successfulSubmits, v.lastError
}

// Run starts the validator on stdio and blocks until the peer disconnects
// or ctx is cancelled. Outcome() can be inspected after Run() returns to
// learn whether the LLM produced a captured payload, exhausted retries,
// or abandoned the session.
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
//
// Session state is tracked across calls within a single Run(ctx): every
// invocation increments `attempts`; a successful submit (schema-pass +
// post-cmd-accept, when configured) increments `successfulSubmits`. Once
// `attempts >= maxRetries` and `successfulSubmits == 0`, all subsequent
// calls return a final-error response signalling exhaustion to the LLM.
func (v *ValidatorServer) handleSubmit(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	v.mu.Lock()
	// Exhaustion check before incrementing: once we've returned the
	// "MAX RETRIES EXHAUSTED" sentinel, every subsequent call returns it
	// too. This is the only path that does not increment `attempts`.
	if v.maxRetries > 0 && v.attempts >= v.maxRetries && v.successfulSubmits == 0 {
		v.mu.Unlock()
		return errorResult(maxRetriesExhaustedMessage(v.lastError)), nil
	}
	v.attempts++
	v.mu.Unlock()

	raw := req.Params.Arguments
	if len(raw) == 0 {
		v.recordFailure("submit: no arguments provided")
		return errorResult("submit: no arguments provided; pass the JSON payload as the tool arguments"), nil
	}

	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		msg := fmt.Sprintf("submit: arguments are not valid JSON: %v", err)
		v.recordFailure(msg)
		return errorResult(msg), nil
	}

	if err := v.compiled.Validate(instance); err != nil {
		msg := formatValidationError(err)
		v.recordFailure(msg)
		return errorResult(msg), nil
	}

	// Run the optional post-cmd verifier. Non-zero exit = LLM-visible
	// rejection (with capped stderr); the side-channel write is skipped
	// so a semantically-bad payload never lands in the canonical output.
	if v.postCmd != "" {
		if rejection, err := v.runPostCmd(ctx, raw); err != nil {
			// Infrastructure failure (couldn't spawn, couldn't write
			// tempfile, etc.) — surface as a retry-eligible rejection
			// so the LLM at least sees what went wrong.
			msg := fmt.Sprintf("submit: post-cmd infrastructure error: %v", err)
			v.recordFailure(msg)
			return errorResult(msg), nil
		} else if rejection != "" {
			v.recordFailure(rejection)
			return errorResult(rejection), nil
		}
	}

	// Capture to the side-channel file. We use the raw arguments rather
	// than re-marshalling the parsed instance so byte-level fidelity is
	// preserved (numeric precision, key order from the LLM, etc.).
	if v.outputPath != "" {
		if err := writeOutputAtomically(v.outputPath, raw); err != nil {
			msg := fmt.Sprintf("submit: capture validated payload: %v", err)
			v.recordFailure(msg)
			return errorResult(msg), nil
		}
	}

	v.recordSuccess()
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{
				Text: "OK: payload validated against the schema and captured. " +
					"You may end your turn now; no need to repeat the JSON.",
			},
		},
	}, nil
}

// recordSuccess increments successfulSubmits under the lock.
func (v *ValidatorServer) recordSuccess() {
	v.mu.Lock()
	v.successfulSubmits++
	v.lastError = ""
	v.persistStateLocked()
	v.mu.Unlock()
}

// recordFailure stores the most recent rejection reason so subsequent
// "max retries exhausted" responses can echo it for diagnostics.
func (v *ValidatorServer) recordFailure(reason string) {
	v.mu.Lock()
	v.lastError = reason
	v.persistStateLocked()
	v.mu.Unlock()
}

// persistStateLocked rewrites the on-disk state file atomically. Caller
// must hold v.mu. No-op when stateFilePath is empty. Failures are
// non-blocking: a state-file write error must not block the LLM's
// response — the worst-case is that a subsequent restart loses one
// iteration of state, which is recoverable. The failure is logged to
// stderr at error level rather than swallowed, because the state file
// drives the retry loop and a silent loss is hard to diagnose.
func (v *ValidatorServer) persistStateLocked() {
	if v.stateFilePath == "" {
		return
	}
	st := validatorState{
		Attempts:          v.attempts,
		SuccessfulSubmits: v.successfulSubmits,
		LastError:         v.lastError,
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := writeOutputAtomically(v.stateFilePath, data); err != nil {
		slog.Error("validator: persist state file failed",
			"path", v.stateFilePath, "err", err)
	}
}

// maxRetriesExhaustedMessage is the LLM-facing sentinel returned once the
// session has burned through its retry budget. The text is deliberately
// blunt so the LLM stops trying; the orchestrator decides what to do via
// Outcome().
func maxRetriesExhaustedMessage(lastErr string) string {
	msg := "submit: MAX RETRIES EXHAUSTED — the validator has rejected too many " +
		"submissions and will not accept more. The orchestrator will route this phase " +
		"to its error state."
	if strings.TrimSpace(lastErr) != "" {
		msg += "\n\nLast rejection reason:\n" + lastErr
	}
	return msg
}

// runPostCmd writes the submitted JSON to a tempfile and runs the
// configured post-cmd verifier. Returns:
//
//   - ("", nil)            — verifier exited 0, payload semantically accepted
//   - (rejectMsg, nil)     — verifier exited non-zero, capped stderr returned
//     for the LLM to react to
//   - ("", err)            — infrastructure failure (couldn't write tempfile,
//     couldn't spawn, etc.) — caller surfaces as such
//
// The post-cmd is split into argv0 + argv-rest by shell-style word
// splitting (spaces only — no quoting). Power users who need quoted args
// can pass them via --post-cmd-arg key=value, which always renders as
// `--key value` (two argv slots).
func (v *ValidatorServer) runPostCmd(ctx context.Context, submittedJSON []byte) (string, error) {
	// Write the submitted JSON to a tempfile that the verifier will read.
	tmp, err := os.CreateTemp("", "kitsoki-validator-submitted-*.json")
	if err != nil {
		return "", fmt.Errorf("create tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(submittedJSON); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close tempfile: %w", err)
	}

	// Build argv. We split postCmd on whitespace (no shell metachars) so
	// `python3 -m bugfix verify-impl` works without spawning sh. Quoted
	// values must come through --post-cmd-arg.
	parts := strings.Fields(v.postCmd)
	if len(parts) == 0 {
		return "", fmt.Errorf("post-cmd is empty after whitespace split")
	}
	argv := append([]string(nil), parts[1:]...)
	for _, kv := range v.postCmdArgs {
		argv = append(argv, "--"+kv.Key, kv.Value)
	}
	argv = append(argv, "--submitted-json", tmpPath)

	cmd := exec.CommandContext(ctx, parts[0], argv...)
	if v.postCmdCwd != "" {
		cmd.Dir = v.postCmdCwd
	}
	// Capture stderr only — stdout is reserved for verifier-internal
	// chatter that we don't want to surface to the LLM.
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = nil // discard

	runErr := cmd.Run()
	if runErr == nil {
		return "", nil
	}

	// Non-zero exit. Cap and ANSI-strip stderr for the LLM.
	capped := capStderr(stderr.String(), postCmdStderrCap)
	rejection := "submit: post-cmd verifier rejected the payload. " +
		"Fix the issues and call submit again.\n\n" +
		"verifier stderr (last " + fmt.Sprintf("%d", postCmdStderrCap) + " bytes; ANSI stripped):\n" +
		capped
	// If the error is *not* an ExitError (e.g. binary missing) treat as
	// infrastructure rather than a verifier rejection so the operator
	// can fix the wiring.
	if _, ok := runErr.(*exec.ExitError); !ok {
		return "", fmt.Errorf("spawn post-cmd %q: %w (stderr: %s)", parts[0], runErr, capped)
	}
	return rejection, nil
}

// capStderr returns the tail of s capped at maxBytes after stripping ANSI
// escape sequences. The tail is preferred because verifier diagnostics
// usually surface errors after a banner of progress noise.
func capStderr(s string, maxBytes int) string {
	clean := ansiEscapeRE.ReplaceAllString(s, "")
	if len(clean) <= maxBytes {
		return clean
	}
	// Preserve the tail; prepend an ellipsis marker so the LLM knows the
	// head was truncated.
	tail := clean[len(clean)-maxBytes:]
	return "…[truncated]…\n" + tail
}

// writeOutputAtomically writes data to path via a temp file + rename, so
// readers (the parent process) never observe a partial write.
func writeOutputAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kitsoki-validator-*.tmp")
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

// CompileSchema compiles a JSON Schema document into a reusable validator.
// It registers the custom "jql" semantic format and switches the compiler into
// assertion mode (AssertFormat) so format violations surface as ordinary
// validation failures — identical to the setup NewValidatorServer uses.
//
// Shared so the validator MCP server and the host.agent.decide code-block
// recovery path validate against one implementation rather than drifting. A
// non-object or unparsable schema returns an error.
func CompileSchema(schemaJSON []byte) (*jsonschema.Schema, error) {
	if len(schemaJSON) == 0 {
		return nil, fmt.Errorf("mcp.CompileSchema: schemaJSON is required")
	}
	var probe any
	if err := json.Unmarshal(schemaJSON, &probe); err != nil {
		return nil, fmt.Errorf("mcp.CompileSchema: parse schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.RegisterFormat(&jsonschema.Format{Name: "jql", Validate: validateJQL})
	compiler.AssertFormat()
	if err := compiler.AddResource("validator-schema.json", probe); err != nil {
		return nil, fmt.Errorf("mcp.CompileSchema: register schema: %w", err)
	}
	compiled, err := compiler.Compile("validator-schema.json")
	if err != nil {
		return nil, fmt.Errorf("mcp.CompileSchema: compile schema: %w", err)
	}
	return compiled, nil
}

// FormatValidationError renders a schema validation error into the same
// LLM-facing message the validator MCP server returns inline. Exported so the
// host.agent.decide recovery path can reuse identical wording when it rejects a
// schema-invalid recovered verdict.
func FormatValidationError(err error) string {
	return formatValidationError(err)
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
