package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
)

// defaultHostRunTruncate caps host.run's returned stdout by default. Gates are
// go/no-go on the exit code, so the full multi-megabyte log of a test run only
// inflates the payload; the tail (where failures print) is kept and the full
// output is spilled to a sidecar. A caller passes truncate_output<=0 to opt out.
const defaultHostRunTruncate = 4096

// hostRunArtifactsDir is where host.run spills full output when it truncates.
const hostRunArtifactsDir = ".artifacts/mcp-host-run"

// host_tools.go — the standalone gate-runner.
//
// `host.run` exposes the host.RunHandler execution primitive as a studio tool so
// an MCP-driving agent can run a command against a worktree directory and read
// its exit code + combined output WITHOUT a live session turn. It is the
// "gate on the deliverable, not the agent's self-report" capability the dogfood
// pipelines lean on: after a maker commits a fix, the driver re-confirms the
// committed tip is GREEN (e.g. `go test ./...`, the story's gate_command)
// independently of any room dispatch.
//
// Why it can't be done through the existing surface: `story.validate` runs the
// SERVER's compiled internal/app, never the worktree's Go, so it cannot exercise
// a worktree's fix; gate execution was otherwise reachable only inside a live
// session's room host.run invocations. See
// issues/bugs/2026-06-23T092410Z-mcp-no-standalone-gate-runner.md.
//
// Semantics are identical to a story's host.run effect because it reuses the
// same handler: bash-mode unless `args` is given (then direct exec, no shell),
// optional `timeout`, combined stdout/stderr, exit code as data. fail_on_error
// is intentionally NOT set — a non-zero exit is returned as {ok:false, exit_code}
// data for the caller to gate on, never a transport error.

// registerHostTools wires the host.* execution tools onto the server. Called
// from NewServer alongside the other tool families. Omitted on a read-only
// server: a command runner is a write surface (it can run builds/tests that
// mutate the worktree), so the Q&A surface must not expose it.
func (srv *Server) registerHostTools() {
	if srv.readOnly {
		return
	}
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "host.run",
		Description: "Run a command against a worktree directory and return its exit code + combined output, OUTSIDE any live session — the standalone gate-runner. Use it to independently re-confirm a committed tip is GREEN (e.g. cmd:\"go test ./...\" or a story's gate_command) rather than trusting an agent's self-report. {dir (required, the working directory — a worktree path), cmd (required, a shell command unless args is given), args? ([]any → exec cmd directly, no shell), timeout? (seconds as number, or a Go duration string like \"5m\"), truncate_output? (cap stdout bytes; default 4096, tail kept + full output spilled to a sidecar; <=0 for full)} → {ok, exit_code, stdout, truncated?, output_path?}. Stdout is tail-truncated by default since gates are go/no-go on exit_code. A non-zero exit is data (ok:false), not an error. Same semantics as a story's host.run effect.",
		// HostRunArgs.Timeout is a polymorphic `any` (number-of-seconds OR a Go
		// duration string), which the jsonschema reflector emits as the bare
		// boolean schema `true`. Claude Code's tools/list validator rejects a
		// non-object property schema ("Invalid input") and then drops the ENTIRE
		// tool list for the session — so this one `any` field silently strands
		// every kitsoki tool from any attached agent. Pre-build the schema and
		// replace `timeout` with a valid object schema (no `type:` ⇒ still
		// accepts number|string, just expressed as an object, not a boolean).
		// Regression: TestHostRun_TimeoutSchemaIsObject.
		InputSchema: hostRunInputSchema(),
	}, srv.handleHostRun)
}

// hostRunInputSchema reflects HostRunArgs and patches the polymorphic `timeout`
// property so it is a valid JSON-Schema object rather than the bare boolean
// `true` the reflector emits for an `any` field. See the call site for why a
// boolean property schema breaks Claude Code's tool-list fetch.
func hostRunInputSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[HostRunArgs](nil)
	if err != nil {
		// Construction-time, like the SDK's own AddTool schema panics.
		panic(fmt.Errorf("host.run: build input schema: %w", err))
	}
	schema.Properties["timeout"] = &jsonschema.Schema{
		Description: "Wall-clock cap. A bare number is seconds; a string is a Go duration (\"90s\", \"5m\"). Omit for uncapped.",
	}
	return schema
}

// HostRunArgs is the input to host.run.
type HostRunArgs struct {
	// Dir is the working directory the command runs in — a worktree path.
	// Required: a gate must name the tree it gates, never the server's cwd.
	Dir string `json:"dir"`
	// Cmd is the program (args-mode) or shell command (bash-mode). Required.
	Cmd string `json:"cmd"`
	// Args, when present, runs Cmd directly with these positional arguments —
	// no shell, no word-splitting/glob expansion. Mirrors host.run's `args`.
	Args []any `json:"args,omitempty"`
	// Timeout caps the child's wall-clock time. A bare number is seconds; a
	// string is a Go duration ("90s", "5m"). Off by default (uncapped).
	Timeout any `json:"timeout,omitempty"`
	// TruncateOutput caps the returned stdout to this many bytes (the tail is
	// kept, where failures print, and a marker is appended). Zero defaults to
	// defaultHostRunTruncate; a negative value returns the full output.
	TruncateOutput int `json:"truncate_output,omitempty"`
}

// HostRunOK is the host.run success result: the command ran (whatever its exit
// code). ok mirrors exit_code == 0 so a caller can gate on it directly.
type HostRunOK struct {
	OK       bool   `json:"ok"`        // true iff exit_code == 0
	ExitCode int    `json:"exit_code"` // the command's exit code (-1 on timeout)
	Stdout   string `json:"stdout"`    // combined stdout+stderr (tail-truncated by default)
	// Truncated is set when Stdout was capped. OutputPath then points at a sidecar
	// file holding the full combined output.
	Truncated  bool   `json:"truncated,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
}

// handleHostRun executes a command in a worktree and returns its exit code +
// output. It is a thin shell over host.RunHandler (cwd = dir): the same handler
// a story's `invoke: host.run` effect uses, so gate semantics never drift
// between an in-session gate and this standalone one.
//
// A non-zero exit is a normal result ({ok:false, exit_code}), not a tool error —
// the caller gates on it. Only a missing dir, missing cmd, or an infra failure
// (exec could not start) maps to a tool error.
func (srv *Server) handleHostRun(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args HostRunArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Dir == "" {
		return buildToolError(ErrBadRequest, "host.run: dir is required (the worktree to gate)"), nil, nil
	}
	if args.Cmd == "" {
		return buildToolError(ErrBadRequest, "host.run: cmd is required"), nil, nil
	}
	if info, err := os.Stat(args.Dir); err != nil || !info.IsDir() {
		return buildToolError(ErrBadRequest, fmt.Sprintf("host.run: dir %q is not an accessible directory", args.Dir)), nil, nil
	}

	hargs := map[string]any{
		"cmd": args.Cmd,
		"cwd": args.Dir,
	}
	if len(args.Args) > 0 {
		hargs["args"] = args.Args
	}
	if args.Timeout != nil {
		hargs["timeout"] = args.Timeout
	}

	res, err := host.RunHandler(ctx, hargs)
	if err != nil {
		// exec could not start (e.g. a non-existent program in args-mode).
		return buildToolError(ErrBadRequest, fmt.Sprintf("host.run: %v", err)), nil, nil
	}
	// RunHandler sets Error only when fail_on_error is set (we don't set it),
	// or on a parse/argument error before exec — surface those as a tool error.
	if res.Error != "" && res.Data == nil {
		return buildToolError(ErrBadRequest, res.Error), nil, nil
	}

	exitCode, _ := res.Data["exit_code"].(int)
	stdout, _ := res.Data["stdout"].(string)

	limit := args.TruncateOutput
	if limit == 0 {
		limit = defaultHostRunTruncate
	}
	out := HostRunOK{
		OK:       exitCode == 0,
		ExitCode: exitCode,
		Stdout:   stdout,
	}
	if limit > 0 && len(stdout) > limit {
		out.Truncated = true
		// Keep the tail — that's where a failing build/test prints. Spill the full
		// output to a sidecar so nothing is lost.
		if path, werr := writeHostRunOutput(stdout); werr == nil {
			out.OutputPath = path
		}
		marker := fmt.Sprintf("… output truncated (%d of %d bytes shown; tail kept", limit, len(stdout))
		if out.OutputPath != "" {
			marker += "; full: " + out.OutputPath
		}
		marker += ") …\n"
		out.Stdout = marker + stdout[len(stdout)-limit:]
	}
	return nil, out, nil
}

// writeHostRunOutput spills a command's full combined output to a sidecar file
// under hostRunArtifactsDir so truncating the returned stdout never loses it.
func writeHostRunOutput(stdout string) (string, error) {
	if err := os.MkdirAll(hostRunArtifactsDir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(hostRunArtifactsDir, "host-run-*.log")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(stdout); err != nil {
		return "", err
	}
	return filepath.Clean(f.Name()), nil
}
