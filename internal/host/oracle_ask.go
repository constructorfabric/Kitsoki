// Package host — host.oracle.ask: read-only inspection handler (oracle-split Phase 3).
//
// host.oracle.ask is the "read-only inspection" rung of the oracle verb ladder
// (oracle-split proposal §2.3). The LLM may use read tools (Read, Grep, Glob,
// WebFetch, WebSearch, Bash under a profile, read-only MCP servers) but cannot
// mutate anything. Returns prose output; when a schema: is supplied the LLM also
// calls a submit MCP tool and the handler returns typed JSON alongside stdout.
//
// Backward compatibility:
//   - The legacy host.oracle.ask (text-only, no schema) is this handler.
//     Call sites that pass no schema see the same { stdout, exit_code, ok }
//     result shape.
//
// Tool surface enforcement:
//   - Mutation tools (Edit, Write) are rejected at the handler level as a
//     safety net; the loader already rejects them at app-load time.
//   - Bash is allowed only when the agent declares a bash_profile:. Every Bash
//     invocation passes through ApplyBashProfile before reaching the shell.
//   - MCP servers must carry read_only: true in the app declaration; the handler
//     verifies this at run time.
//
// No acceptance loop. One call, one answer.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// mutationTools is the set of tool names that are never permitted in a
// read-only oracle call (ask, decide, extract LLM tier). The loader rejects
// these at app-load; the handler is a safety net so a manually assembled call
// cannot sneak mutation tools through.
var mutationTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"NotebookEdit": true,
}

// OracleAskHandler implements host.oracle.ask — the read-only inspection verb.
//
// Required args (one of):
//   - prompt_path (string): path to a prompt template file. If relative,
//     resolved against KITSOKI_APP_DIR or cwd.
//   - prompt (string): either a file path (resolved as above when the path
//     exists and the value is a single-line string) or inline prompt content.
//     Inline content is used directly without template rendering — this is the
//     M8 path: `kitsoki oracle ask --prompt -` reads stdin bytes and stores
//     them here; treating them as a file path would ENOENT.
//
// Optional args:
//   - agent       (string): name of an entry in AppDef.Agents (injected via
//     WithAgents). Supplies SystemPrompt, Model, Tools, BashProfile, DefaultCwd.
//   - system_prompt (string): inline persona; wins over agent.SystemPrompt.
//   - working_dir (string): cwd for the claude subprocess. Defaults to
//     agent.DefaultCwd, then the prompt file's directory.
//   - args        (map): explicit prompt-template variables ({{ args.X }}).
//     Falls back to the full call-args map for legacy compatibility.
//   - schema      (string): path to a JSON schema. When set, kitsoki attaches
//     a submit MCP tool and the LLM must call it; the handler returns
//     `submitted` alongside `stdout`.
//   - tools       ([]string): per-call tool override. Wins over agent.Tools
//     (D5). Must still be a subset of the read-only allowlist.
//
// Returns Result.Data with:
//   - stdout    (string): claude's text reply (source-color wrapped)
//   - exit_code (int):    claude's exit code
//   - ok        (bool):   exit_code == 0
//   - submitted (any):    parsed JSON payload — only present when schema: is set
//     and the LLM called submit().
//
// Tool safety net: if the effective tool list contains Edit or Write the
// handler returns Result{Error: ...} immediately. This mirrors the loader
// check and protects call sites that bypass the loader (tests, CLI).
func OracleAskHandler(ctx context.Context, args map[string]any) (Result, error) {
	// Resolve the prompt. Two cases:
	//   prompt_path: (or the legacy prompt: alias used as a file path) — read
	//     from disk, render as a pongo2 template, strip source-color sentinels.
	//   prompt: with inline content — used directly when the value is not a
	//     resolvable file (M8: `kitsoki oracle ask --prompt -` reads stdin and
	//     stores the bytes under the "prompt" key; treating them as a file path
	//     would ENOENT).
	//
	// Resolution order:
	//   1. prompt_path: — always treated as a file path.
	//   2. prompt: — if the resolved path is a readable file, read it (backward
	//      compat with existing call sites that alias prompt → prompt_path).
	//      If the path does not exist or the value is multi-line inline content,
	//      use the raw string directly as the rendered prompt.
	var rendered string
	var promptFileDir string

	promptPath, _ := args["prompt_path"].(string)
	inlinePrompt, _ := args["prompt"].(string)

	switch {
	case strings.TrimSpace(promptPath) != "":
		resolved := resolvePromptPath(strings.TrimSpace(promptPath))
		raw, readErr := os.ReadFile(resolved)
		if readErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: read prompt %q: %v", resolved, readErr)}, nil
		}
		templateArgs, _ := args["args"].(map[string]any)
		if templateArgs == nil {
			templateArgs = args
		}
		r, tmplErr := render.Pongo(string(raw), expr.Env{Args: templateArgs})
		if tmplErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: render prompt %q: %v", resolved, tmplErr)}, nil
		}
		rendered = sourcecolor.Strip(r)
		promptFileDir = filepath.Dir(resolved)

	case strings.TrimSpace(inlinePrompt) != "":
		// Check if it looks like a file path (single-line, no whitespace other
		// than leading/trailing, resolves to an existing file).
		candidate := strings.TrimSpace(inlinePrompt)
		if !strings.ContainsAny(candidate, "\n\r") {
			resolved := resolvePromptPath(candidate)
			if raw, readErr := os.ReadFile(resolved); readErr == nil {
				// File exists — treat as a path alias (backward compat).
				templateArgs, _ := args["args"].(map[string]any)
				if templateArgs == nil {
					templateArgs = args
				}
				r, tmplErr := render.Pongo(string(raw), expr.Env{Args: templateArgs})
				if tmplErr != nil {
					return Result{Error: fmt.Sprintf("host.oracle.ask: render prompt %q: %v", resolved, tmplErr)}, nil
				}
				rendered = sourcecolor.Strip(r)
				promptFileDir = filepath.Dir(resolved)
				break
			}
		}
		// Not a file (or multi-line): use the value as inline content directly.
		rendered = sourcecolor.Strip(inlinePrompt)

	default:
		return Result{Error: "host.oracle.ask: prompt_path (or prompt) argument is required"}, nil
	}

	// Choose template scope: prefer explicit `args:` map for new callers;
	// fall back to full call-args for backward compatibility with rooms that
	// pass vars at the top level.
	_ = rendered // already set above

	// Resolve the agent (optional) and compute effective tools.
	agent, _ := resolveAgent(ctx, args)
	tools := effectiveTools(ctx, args, agent)

	// Safety net: reject mutation tools regardless of source.
	for _, t := range tools {
		if mutationTools[t] {
			return Result{Error: fmt.Sprintf(
				"host.oracle.ask: tool %q is not permitted in a read-only ask call (use host.oracle.task for mutation tools)",
				t,
			)}, nil
		}
	}

	// Bash gate: if Bash is in the effective tool list, the agent must declare
	// a BashProfile. When no profile is set we deny the call rather than
	// silently allowing unrestricted Bash.
	hasBash := false
	for _, t := range tools {
		if t == "Bash" {
			hasBash = true
			break
		}
	}
	if hasBash && agent.BashProfile == nil {
		return Result{Error: "host.oracle.ask: Bash is in the tool list but the agent declares no bash_profile; " +
			"set bash_profile: read-only, commands, or sandboxed-write on the agent declaration"}, nil
	}

	bin, err := resolveOracleBin(ctx)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	// Resolve working directory: per-call > agent.DefaultCwd > prompt file dir.
	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)
	if workingDir == "" && promptFileDir != "" {
		workingDir = promptFileDir
	}

	// Bash MCP wiring: when Bash is in the effective tool list, replace it with
	// the namespaced mcp__kitsoki-bash__Bash entry and attach the kitsoki-bash
	// MCP server. This routes every Bash call through ApplyBashProfile before
	// execution rather than letting claude use the unrestricted built-in.
	if hasBash {
		tools = rewriteToolsForBashMCP(tools)
	}

	cliArgs := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
	}
	if sp := effectiveSystemPrompt(args, agent); strings.TrimSpace(sp) != "" {
		cliArgs = append(cliArgs, "--append-system-prompt", sp)
	}
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)

	// Build the MCP servers map. When Bash is in use we attach the kitsoki-bash
	// server; when a schema: is given we attach the submit validator. Both can
	// coexist in the same --mcp-config file.
	mcpServers := make(map[string]any)

	if hasBash {
		bashEntry, bashConfigPath, bashErr := BuildBashMCPEntry(agent.BashProfile, workingDir)
		if bashErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: build bash MCP server: %v", bashErr)}, nil
		}
		defer os.Remove(bashConfigPath)
		mcpServers["kitsoki-bash"] = bashEntry
	}

	// Schema mode: attach a submit MCP tool so the LLM can return typed JSON
	// alongside its prose answer. The validator binary is the same kitsoki
	// mcp-validator used by ask_with_mcp, reused here without the retry loop.
	schemaPath, _ := args["schema"].(string)
	schemaPath = strings.TrimSpace(schemaPath)
	var submittedOutputPath string
	if schemaPath != "" {
		var submittedFile *os.File
		submittedFile, err = os.CreateTemp("", "kitsoki-ask-submit-*.json")
		if err != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: create submit tempfile: %v", err)}, nil
		}
		submittedFile.Close()
		submittedOutputPath = submittedFile.Name()
		defer func() {
			if submittedOutputPath != "" {
				_ = os.Remove(submittedOutputPath)
			}
		}()

		validatorEntry, buildErr := buildValidatorMCPServer(schemaPath, submittedOutputPath, validatorOptions{})
		if buildErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: build validator MCP server: %v", buildErr)}, nil
		}
		mcpServers["validator"] = validatorEntry
	}

	var mcpConfigPath string
	if len(mcpServers) > 0 {
		mcpConfig := map[string]any{"mcpServers": mcpServers}
		mcpBytes, mErr := json.Marshal(mcpConfig)
		if mErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: marshal mcp config: %v", mErr)}, nil
		}
		f, fErr := os.CreateTemp("", "kitsoki-ask-mcp-*.json")
		if fErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask: create mcp config tempfile: %v", fErr)}, nil
		}
		if _, wErr := f.Write(mcpBytes); wErr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return Result{Error: fmt.Sprintf("host.oracle.ask: write mcp config: %v", wErr)}, nil
		}
		_ = f.Close()
		mcpConfigPath = f.Name()
		defer os.Remove(mcpConfigPath)
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	callID := newUUID()
	callStart := time.Now()
	systemPrompt := effectiveSystemPrompt(args, agent)

	cr, _, runErr := OracleStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      rendered,
		WorkingDir: workingDir,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}

	var errMsg string
	if cr.Infra != nil {
		errMsg = fmt.Sprintf("host.oracle.ask: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			errMsg = fmt.Sprintf("%s\nstderr: %s", errMsg, s)
		}
		// Emit lean slog + journal before returning
		slog.InfoContext(ctx, "oracle.ask.complete",
			"call_id", callID,
			"agent", agent.Model,
			"model", agent.Model,
			"duration_ms", durationMS,
			"error", errMsg,
		)
		appendOracleCallJournal(ctx, callStart, 0, OracleCallBody{
			CallID:       callID,
			Verb:         "ask",
			Agent:        agentNameFromArgs(args),
			Model:        agent.Model,
			DurationMS:   durationMS,
			SystemPrompt: systemPrompt,
			Prompt:       rendered,
			Input:        marshalInput(map[string]any{}),
			Error:        errMsg,
		})
		return Result{Error: errMsg}, nil
	}

	data := map[string]any{
		"stdout":    sourcecolor.Wrap(cr.Stdout),
		"exit_code": cr.ExitCode,
		"ok":        cr.ExitCode == 0,
	}
	if cr.ExitCode != 0 {
		errMsg = claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
	}

	// Schema mode: read the submitted payload from the tempfile (written by
	// kitsoki mcp-validator on successful submit).
	var submitted any
	if schemaPath != "" && submittedOutputPath != "" {
		if payload, readErr := os.ReadFile(submittedOutputPath); readErr == nil && len(payload) > 0 {
			var parsed any
			if jErr := json.Unmarshal(payload, &parsed); jErr == nil {
				data["submitted"] = parsed
				submitted = parsed
			}
		}
		// submittedOutputPath is cleaned up by the defer above.
	}

	// Emit lean slog oracle.ask.complete.
	slog.InfoContext(ctx, "oracle.ask.complete",
		"call_id", callID,
		"model", agent.Model,
		"duration_ms", durationMS,
	)

	// Write full KindOracleCall journal entry.
	inputDesc := map[string]any{}
	if schemaPath != "" {
		inputDesc["schema_path"] = schemaPath
	}
	responseDesc := map[string]any{
		"text": cr.Stdout,
	}
	if submitted != nil {
		responseDesc["intent"] = submitted
	}
	appendOracleCallJournal(ctx, callStart, 0, OracleCallBody{
		CallID:       callID,
		Verb:         "ask",
		Agent:        agentNameFromArgs(args),
		Model:        agent.Model,
		DurationMS:   durationMS,
		SystemPrompt: systemPrompt,
		Prompt:       rendered,
		Input:        marshalInput(inputDesc),
		Response:     marshalResponse(responseDesc),
		Error:        errMsg,
	})

	res := Result{Data: data}
	if errMsg != "" {
		res.Error = errMsg
	}
	return res, nil
}

// agentNameFromArgs extracts the agent name string from handler args,
// returning "" if not set.
func agentNameFromArgs(args map[string]any) string {
	if v, ok := args["agent"].(string); ok {
		return v
	}
	return ""
}

// AppDirEnv is the env var loaders set to the directory containing app.yaml,
// so handlers can resolve relative paths (prompt files, scripts) deterministically.
const AppDirEnv = "KITSOKI_APP_DIR"

// resolvePromptPath expands a prompt_path arg to an absolute path.
// Relative paths are resolved against KITSOKI_APP_DIR when set, otherwise
// against the current working directory.
func resolvePromptPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if base := os.Getenv(AppDirEnv); base != "" {
		return filepath.Join(base, p)
	}
	return p
}
