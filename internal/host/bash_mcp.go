// Package host — wrapped Bash MCP server for host.agent.ask and host.agent.decide.
//
// Problem (H1 from the agent code review):
// ApplyBashProfile, MakeSandboxEnv, and EnsureScratchDir were defined but
// never called from production code. The Bash tool flowed directly through
// claude's built-in implementation; kitsoki couldn't intercept argv. An ask
// agent declared with bash_profile: read-only happily ran destructive commands.
//
// Solution:
// BashMCPServer is a stdio MCP server that exposes a single "Bash" tool. When
// claude invokes the tool, the server:
//  1. Reads the BashProfile from its configuration.
//  2. Calls ApplyBashProfile to validate the command.
//  3. If rejected, returns a tool error to claude so the LLM sees the denial.
//  4. If accepted, execs the command via exec.CommandContext with
//     profile-appropriate env (MakeSandboxEnv for sandboxed-write) and cwd
//     set to the scratch dir for sandboxed-write profiles.
//  5. Returns stdout+stderr+exit-code as the tool result.
//
// Handlers wire the server as follows (agent_ask.go, agent_decide.go):
//   - When Bash appears in the effective tool list, BuildBashMCPEntry appends
//     a "kitsoki-bash" MCP server entry to the --mcp-config JSON.
//   - The tool is namespaced "mcp__kitsoki-bash__Bash" in claude's tool list.
//   - Handlers pass "--allowedTools mcp__kitsoki-bash__Bash" (via the renamed
//     tool list) and omit the plain "Bash" entry so claude uses THIS server's
//     Bash, not the built-in.
//
// For task and converse, the agent gets unrestricted Bash from claude's
// built-in implementation. Those verbs do not call BuildBashMCPEntry; the
// profile enforcement in this file is skipped entirely for them. This is by
// design: task/converse are the mutation verbs and their Bash surface is
// intentionally unrestricted within the agent's declared permissions.
//
// The subprocess for the actual command is exec'd directly (not via sh -c) to
// prevent shell metacharacter injection. Since ApplyBashProfile has already
// rejected commands containing metacharacters, the command at this point is a
// single program with whitespace-delimited arguments.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	shellwords "github.com/mattn/go-shellwords"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// BashMCPServer is an MCP stdio server that exposes a "Bash" tool with
// profile-based command restriction.
type BashMCPServer struct {
	profile    *BashProfile
	workingDir string
	mcpSrv     *mcpsdk.Server
	// gate, when non-nil, is the write-mode gate for a write_mode: read_only
	// room: a Bash command the read-only profile would reject is not denied
	// outright but routed through the gate (which forwards an action proposal to
	// the operator and records a WriteModeGranted decision). On a grant the
	// command executes; on a deny the LLM sees the gate's tool-error. nil keeps
	// the static bash-profile behavior verbatim (ask/decide, open rooms).
	gate *WriteModeGate
}

// NewBashMCPServer constructs a BashMCPServer. profile must not be nil for
// ask/decide calls; workingDir is the cwd for commands under read-only and
// commands profiles (sandboxed-write overrides it with a per-call scratch dir).
func NewBashMCPServer(profile *BashProfile, workingDir string) *BashMCPServer {
	s := &BashMCPServer{
		profile:    profile,
		workingDir: workingDir,
	}

	s.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "kitsoki-bash",
		Version: "0.1.0",
	}, nil)

	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The bash command to execute. Shell metacharacters are not permitted; multi-command chains will be rejected."
			},
			"restart": {
				"type": "boolean",
				"description": "Ignored by the kitsoki-bash wrapper; accepted for API compatibility with the built-in Bash tool."
			}
		},
		"required": ["command"]
	}`)

	s.mcpSrv.AddTool(&mcpsdk.Tool{
		Name: "Bash",
		Description: "Execute a bash command with profile-based restrictions. " +
			"The command is validated against the agent's bash_profile before execution. " +
			"Multi-command chains (using ; | & etc.) are rejected. " +
			"Returns stdout, stderr, and exit code.",
		InputSchema: inputSchema,
	}, s.handleBash)

	return s
}

// WithGate attaches a write-mode gate to the server so a read-only-profile
// rejection becomes a gated action proposal rather than an outright deny. Returns
// the receiver for chaining. Used by the in-process write_mode: read_only path
// (and its tests); the subprocess path keeps gate nil (the read-only floor is
// enforced by the bash profile + --disallowedTools at dispatch).
func (s *BashMCPServer) WithGate(gate *WriteModeGate) *BashMCPServer {
	s.gate = gate
	return s
}

// Run starts the server on stdio and blocks until ctx is cancelled or stdin closes.
func (s *BashMCPServer) Run(ctx context.Context) error {
	return s.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying server for in-process tests.
func (s *BashMCPServer) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return s.mcpSrv.Connect(ctx, t, opts)
}

// BashMCPTestResult is the outcome of an in-process Bash tool invocation via
// InvokeForTest. Tests use this rather than calling the server over stdio.
type BashMCPTestResult struct {
	// IsError is true when the server returned an error result (profile
	// rejection or exec failure).
	IsError bool
	// Text is the first TextContent block from the result.
	Text string
}

// InvokeForTest calls the "Bash" tool in-process with the given JSON arguments
// string (e.g. `{"command":"git log"}`). Used by tests to exercise profile
// enforcement without spawning a subprocess MCP session.
//
// t is a *testing.T; InvokeForTest calls t.Fatal on unexpected infrastructure
// errors (JSON parse failure, connect failure).
func (s *BashMCPServer) InvokeForTest(t interface {
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Helper()
}, argsJSON string) BashMCPTestResult {
	t.Helper()
	clientT, serverT := mcpsdk.NewInMemoryTransports()

	ctx := context.Background()
	go func() {
		if _, err := s.mcpSrv.Connect(ctx, serverT, nil); err != nil {
			// Server connection errors are non-fatal in tests; the client will
			// get a connect error that surfaces through t.Fatal below.
			_ = err
		}
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "test-client",
		Version: "0",
	}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("BashMCPServer.InvokeForTest: connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	res, callErr := cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "Bash",
		Arguments: json.RawMessage(argsJSON),
	})
	if callErr != nil {
		t.Fatalf("BashMCPServer.InvokeForTest: call tool: %v", callErr)
	}

	out := BashMCPTestResult{IsError: res.IsError}
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			out.Text = tc.Text
			break
		}
	}
	return out
}

// handleBash is the MCP tool handler. It applies ApplyBashProfile, then either
// rejects the command (returning a tool error) or executes it and returns the
// combined output.
func (s *BashMCPServer) handleBash(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	if len(req.Params.Arguments) == 0 {
		return bashErrorResult("Bash: no arguments provided"), nil
	}

	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(req.Params.Arguments, &input); err != nil {
		return bashErrorResult(fmt.Sprintf("Bash: parse arguments: %v", err)), nil
	}
	command := strings.TrimSpace(input.Command)
	if command == "" {
		return bashErrorResult("Bash: command is empty"), nil
	}

	// Profile enforcement: ApplyBashProfile returns a non-empty string when
	// the command is denied. Surface that string as a tool error to the LLM.
	if msg := ApplyBashProfile(s.profile, command); msg != "" {
		// Write-mode gate: in a write_mode: read_only room a profile rejection is
		// a MUTATING step, not a hard deny. Route it through the gate, which
		// short-circuits an active turn/session grant, else forwards an action
		// proposal to the operator (headless ⇒ deny). On a grant the command
		// proceeds; on a deny the LLM sees a gate tool-error.
		if s.gate != nil && s.gate.ReadOnly {
			tc := ToolCall{Name: "Bash", Command: command}
			dec := s.gate.Resolve(ctx, tc)
			if !dec.Granted {
				return bashErrorResult(describeGateErrorForLLM(describeAction(tc, EffectWrite), dec.By)), nil
			}
			// Granted: fall through to execute the command.
		} else {
			return bashErrorResult("Bash command rejected by profile: " + msg), nil
		}
	}

	// Execute the command.
	result, execErr := s.execCommand(ctx, command)
	if execErr != nil {
		return bashErrorResult(fmt.Sprintf("Bash: exec failed: %v", execErr)), nil
	}
	return result, nil
}

// execCommand parses the command with POSIX-style shell-word splitting
// (honouring single and double quotes, backslash escapes) and runs it
// directly via exec.CommandContext — no shell is spawned. Shell
// metacharacters (;, |, &, etc.) were already rejected by ApplyBashProfile
// before this function is called; shellwords.Parse is used only to strip
// the quoting that the LLM wraps around arguments.
//
// For sandboxed-write profiles, a fresh scratch dir is created per call
// and the env includes MakeSandboxEnv.
func (s *BashMCPServer) execCommand(ctx context.Context, command string) (*mcpsdk.CallToolResult, error) {
	tokens, parseErr := shellwords.Parse(command)
	if parseErr != nil {
		return bashErrorResult("kitsoki-bash: parse command: " + parseErr.Error()), nil
	}
	if len(tokens) == 0 {
		return bashErrorResult("Bash: empty command after tokenisation"), nil
	}

	cwd := s.workingDir
	var extraEnv []string

	if s.profile != nil && s.profile.Kind == BashProfileSandboxWrite {
		scratch, err := EnsureScratchDir(s.profile)
		if err != nil {
			return nil, fmt.Errorf("bash_mcp: create scratch dir: %w", err)
		}
		defer os.RemoveAll(scratch)
		cwd = scratch
		extraEnv = MakeSandboxEnv()
	}

	cmd := exec.CommandContext(ctx, tokens[0], tokens[1:]...)
	cmd.Dir = cwd

	env := os.Environ()
	if len(extraEnv) > 0 {
		env = append(env, extraEnv...)
	}
	// IDE auto-connect scrub (shared decision #1) — when kitsoki holds the one
	// IDE link, a bash_mcp child must not open its own socket to the editor.
	// Outermost wrap, gated on a connected link in ctx; no-op otherwise so the
	// child env is byte-identical to today on every headless/flow path.
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		env = envScrubIDE(env)
	}
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	exitCode := 0
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, runErr
		}
	}

	output := stdout.String()
	stderrText := stderr.String()

	var sb strings.Builder
	if output != "" {
		sb.WriteString(output)
	}
	if stderrText != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("stderr: ")
		sb.WriteString(stderrText)
	}
	if exitCode != 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("exit code: %d", exitCode))
	}

	text := sb.String()
	if text == "" {
		text = "(no output)"
	}

	if exitCode != 0 {
		return bashErrorResult(text), nil
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: text},
		},
	}, nil
}

// bashErrorResult builds an MCP tool error result — isError: true with a text
// body. This is what the LLM sees when a command is denied or fails.
func bashErrorResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: text},
		},
	}
}

// BashMCPConfig holds everything BuildBashMCPEntry needs to produce the
// --mcp-config entry for the kitsoki-bash server. It is written to a temp
// file and passed via --profile-config to the subprocess.
type BashMCPConfig struct {
	ProfileKind int      `json:"profile_kind"`
	Commands    []string `json:"commands,omitempty"`
	ScratchDir  string   `json:"scratch_dir,omitempty"`
	WorkingDir  string   `json:"working_dir,omitempty"`
}

// BuildBashMCPEntry builds the mcp_servers entry that launches the
// kitsoki-bash wrapper subprocess. Returns (entry, configFilePath, error).
// configFilePath is a temp file the caller must remove after the claude call.
//
// The entry runs `kitsoki mcp-bash --profile-config <path>` so the subprocess
// knows which BashProfile to enforce without receiving the profile inline in
// the MCP config (which would require quoting).
//
// Callers must also:
//   - Replace "Bash" in --allowedTools with "mcp__kitsoki-bash__Bash" so
//     claude routes Bash calls through this server.
//   - Remove the plain "Bash" tool name from --allowedTools to prevent the
//     built-in from being used in parallel.
func BuildBashMCPEntry(profile *BashProfile, workingDir string) (entry map[string]any, configFilePath string, err error) {
	cfg := BashMCPConfig{
		WorkingDir: workingDir,
	}
	if profile != nil {
		cfg.ProfileKind = int(profile.Kind)
		cfg.Commands = profile.Commands
		cfg.ScratchDir = profile.ScratchDir
	}

	cfgBytes, mErr := json.Marshal(cfg)
	if mErr != nil {
		return nil, "", fmt.Errorf("bash_mcp.BuildBashMCPEntry: marshal config: %w", mErr)
	}

	f, fErr := os.CreateTemp("", "kitsoki-bash-profile-*.json")
	if fErr != nil {
		return nil, "", fmt.Errorf("bash_mcp.BuildBashMCPEntry: create config temp: %w", fErr)
	}
	if _, wErr := f.Write(cfgBytes); wErr != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, "", fmt.Errorf("bash_mcp.BuildBashMCPEntry: write config temp: %w", wErr)
	}
	_ = f.Close()

	bin := os.Getenv(kitsokiBinaryEnv)
	if bin == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			_ = os.Remove(f.Name())
			return nil, "", fmt.Errorf("bash_mcp.BuildBashMCPEntry: locate kitsoki binary: %w", exeErr)
		}
		bin = exe
	}

	entry = map[string]any{
		"command": bin,
		"args":    []any{"mcp-bash", "--profile-config", f.Name()},
	}
	return entry, f.Name(), nil
}

// RunBashMCPServerFromConfig reads a BashMCPConfig from configPath and runs a
// BashMCPServer on stdio until ctx is cancelled or stdin closes. This is the
// entry point for `kitsoki mcp-bash --profile-config <path>`.
//
// Group B (cmd/kitsoki/mcp_bash.go) wires this into the cobra command tree.
func RunBashMCPServerFromConfig(ctx context.Context, configPath string, stdin io.Reader, stdout, stderr io.Writer) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("mcp-bash: read profile config %q: %w", configPath, err)
	}
	var cfg BashMCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("mcp-bash: parse profile config: %w", err)
	}

	profile := &BashProfile{
		Kind:       BashProfileKind(cfg.ProfileKind),
		Commands:   cfg.Commands,
		ScratchDir: cfg.ScratchDir,
	}

	srv := NewBashMCPServer(profile, cfg.WorkingDir)
	return srv.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// rewriteToolsForBashMCP transforms the effective tool list so that "Bash" is
// replaced with "mcp__kitsoki-bash__Bash". This ensures claude routes Bash
// calls through the kitsoki-bash MCP server instead of the built-in. The
// built-in "Bash" is removed from the list.
//
// Returns the rewritten list. If "Bash" is not present, returns tools unchanged.
func rewriteToolsForBashMCP(tools []string) []string {
	hasBash := false
	for _, t := range tools {
		if t == "Bash" {
			hasBash = true
			break
		}
	}
	if !hasBash {
		return tools
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == "Bash" {
			out = append(out, "mcp__kitsoki-bash__Bash")
		} else {
			out = append(out, t)
		}
	}
	return out
}
