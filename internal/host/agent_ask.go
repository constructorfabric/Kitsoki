// Package host — host.agent.ask: read-only inspection handler.
//
// host.agent.ask is the "read-only inspection" rung of the agent verb ladder
// (see docs/guide/agents/cli.md). The LLM may use read tools (Read, Grep, Glob,
// WebFetch, WebSearch, Bash under a profile, read-only MCP servers) but cannot
// mutate anything. Returns prose output; when a schema: is supplied the LLM also
// calls a submit MCP tool and the handler returns typed JSON alongside stdout.
//
// Backward compatibility:
//   - The legacy host.agent.ask (text-only, no schema) is this handler.
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

	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"
)

// AgentAskHandler implements host.agent.ask — the read-only inspection verb.
//
// Required args (one of):
//   - prompt_path (string): path to a prompt template file. If relative,
//     resolved against KITSOKI_APP_DIR or cwd.
//   - prompt (string): either a file path (resolved as above when the path
//     exists and the value is a single-line string) or inline prompt content.
//     Inline content is used directly without template rendering — this is the
//     M8 path: `kitsoki agent ask --prompt -` reads stdin bytes and stores
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
func AgentAskHandler(ctx context.Context, args map[string]any) (Result, error) {
	// Resolve the prompt. Two cases:
	//   prompt_path: (or the legacy prompt: alias used as a file path) — read
	//     from disk, render as a pongo2 template, strip source-color sentinels.
	//   prompt: with inline content — used directly when the value is not a
	//     resolvable file (M8: `kitsoki agent ask --prompt -` reads stdin and
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
		resolved := resolvePromptPathCtx(ctx, strings.TrimSpace(promptPath))
		raw, readErr := readPromptFile(resolved)
		if readErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask: read prompt %q: %v", resolved, readErr)}, nil
		}
		templateArgs, _ := args["args"].(map[string]any)
		if templateArgs == nil {
			templateArgs = args
		}
		templateArgs = mergeIDEAmbient(ctx, templateArgs)
		templateArgs = mergeVisualAmbient(ctx, templateArgs)
		r, tmplErr := renderPromptBytes(ctx, string(raw), templateArgs)
		if tmplErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask: render prompt %q: %v", resolved, tmplErr)}, nil
		}
		rendered = sourcecolor.Strip(r)
		promptFileDir = filepath.Dir(resolved)

	case strings.TrimSpace(inlinePrompt) != "":
		// Check if it looks like a file path (single-line, no whitespace other
		// than leading/trailing, resolves to an existing file).
		candidate := strings.TrimSpace(inlinePrompt)
		if !strings.ContainsAny(candidate, "\n\r") {
			resolved := resolvePromptPathCtx(ctx, candidate)
			if raw, readErr := readPromptFile(resolved); readErr == nil {
				// File exists — treat as a path alias (backward compat).
				templateArgs, _ := args["args"].(map[string]any)
				if templateArgs == nil {
					templateArgs = args
				}
				templateArgs = mergeIDEAmbient(ctx, templateArgs)
				templateArgs = mergeVisualAmbient(ctx, templateArgs)
				r, tmplErr := renderPromptBytes(ctx, string(raw), templateArgs)
				if tmplErr != nil {
					return Result{Error: fmt.Sprintf("host.agent.ask: render prompt %q: %v", resolved, tmplErr)}, nil
				}
				rendered = sourcecolor.Strip(r)
				promptFileDir = filepath.Dir(resolved)
				break
			}
		}
		// Not a file (or multi-line): use the value as inline content directly.
		rendered = sourcecolor.Strip(inlinePrompt)

	default:
		return Result{Error: "host.agent.ask: prompt_path (or prompt) argument is required"}, nil
	}

	// Always-on editor context: append the operator's live `/ide` selection so
	// it feeds the request without the prompt having to reference args.ide. A
	// no-op when no selection rode the turn. Done before plugin dispatch so both
	// the Dispatch and subprocess paths carry it.
	rendered = appendIDEAmbient(ctx, rendered)
	// Always-on screen context: append the operator's pointed-at element/frame
	// beside the editor selection. A no-op when no surface attached a bundle.
	rendered = appendVisualAmbient(ctx, rendered)

	// B-7: If an agent plugin registry is wired in context, route through
	// host.Dispatch (the Agent plugin interface) instead of the subprocess.
	// This is the production wiring for the `agent:` field on effects.
	// Falls through transparently when no registry is present.
	schemaArg, _ := args["schema"].(string)
	var pluginSchemaJSON json.RawMessage
	if strings.TrimSpace(schemaArg) != "" {
		pluginSchemaJSON = json.RawMessage(`"` + strings.TrimSpace(schemaArg) + `"`) // pass schema path as hint
	}
	withArgs, _ := args["with"].(map[string]any)
	if pluginRes, handled, pluginErr := TryDispatchVerb(ctx, "ask", rendered, "", agentNameFromArgs(args), "", withArgs, pluginSchemaJSON); handled {
		if pluginErr != nil {
			return Result{Error: pluginErr.Error()}, nil
		}
		return pluginRes, nil
	}

	// Choose template scope: prefer explicit `args:` map for new callers;
	// fall back to full call-args for backward compatibility with rooms that
	// pass vars at the top level.
	_ = rendered // already set above

	// Resolve the agent (optional) and compute effective tools.
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	policy := enforceToolbox(ctx, args, agent, "default", ToolboxEnforcementOptions{
		EffectCeiling:       "read",
		ReadOnlyDeniedTools: readOnlyAgentVerbDeniedTools,
	})
	tools := policy.AllowedTools

	// Bash gate: if Bash is in the effective tool list, the agent must declare
	// a BashProfile. When no profile is set we deny the call rather than
	// silently allowing unrestricted Bash.
	hasBash, bashErrMsg := validateBashProfile("host.agent.ask", tools, agent)
	if bashErrMsg != "" {
		return Result{Error: bashErrMsg}, nil
	}

	bin, err := resolveAgentBin(ctx)
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

	agent = applyNoToolsContract(agent, policy)
	cliArgs := buildBaseCLIArgs(ctx, sysprompt.Ask, args, agent)
	cliArgs = setPermissionMode(cliArgs, policy.CLIMode)
	cliArgs = appendDisallowedToolsFlag(cliArgs, policy.DeniedTools)
	// When a schema is set the validator MCP server is attached below as
	// "validator", exposing mcp__validator__submit. Add it to the allowed
	// tools so the agent can call submit() even when the CLI permission mode
	// is "default" (the read-only ceiling this handler always enforces) —
	// without this the tool is outside --allowedTools, so the CLI treats it
	// as ungranted and blocks on an interactive permission prompt that a
	// headless `-p` run can never answer (matches the mcp__validator__submit
	// wiring in agent_task.go's acceptance-schema path).
	if strings.TrimSpace(schemaArg) != "" {
		tools = append(tools, "mcp__validator__submit")
	}
	// Forward operator questions into kitsoki when a live surface is attached.
	var opAskCleanup func()
	cliArgs, tools, opAskCleanup, _ = attachOperatorAsk(ctx, cliArgs, tools)
	defer opAskCleanup()
	policy = policy.WithAllowed(tools)
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)

	// Build the MCP servers map. Contract-level servers are the floor; per-call
	// mcp/mcp_servers entries override by server name. When Bash is in use we attach the kitsoki-bash
	// server; when a schema: is given we attach the submit validator. Both can
	// coexist in the same --mcp-config file.
	mcpServers := effectiveMCPServers(args, agent)
	if mcpServers == nil {
		mcpServers = make(map[string]any)
	}

	if hasBash {
		bashEntry, bashConfigPath, bashErr := BuildBashMCPEntry(agent.BashProfile, workingDir)
		if bashErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask: build bash MCP server: %v", bashErr)}, nil
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
			return Result{Error: fmt.Sprintf("host.agent.ask: create submit tempfile: %v", err)}, nil
		}
		submittedFile.Close()
		submittedOutputPath = submittedFile.Name()
		defer func() {
			if submittedOutputPath != "" {
				_ = os.Remove(submittedOutputPath)
			}
		}()

		validatorEntry, buildErr := buildValidatorMCPServer(ctx, schemaPath, submittedOutputPath, validatorOptions{})
		if buildErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask: build validator MCP server: %v", buildErr)}, nil
		}
		mcpServers["validator"] = validatorEntry
	}

	mcpServers = attachStudioMCPServer(mcpServers, tools)

	if len(mcpServers) > 0 {
		mcpConfigPath, cleanup, cfgErr := writeMCPConfigTempfile(mcpServers, "kitsoki-ask-mcp")
		if cfgErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask: %v", cfgErr)}, nil
		}
		defer cleanup()
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	// Wave 3-agent: write AgentCalled to the JSONL sink (if wired) at
	// dispatch time, before the subprocess is started. Note: Prompt and
	// SystemPrompt are omitted from the event to stay under PIPE_BUF (4096 bytes).
	// The full prompt is available in AskRequest context (live) or cassette (replay).
	provRef := strings.TrimSpace(promptPath)
	if provRef == "" {
		provRef = strings.TrimSpace(inlinePrompt)
	}
	promptOverlay, specDefaulted, specOverridden := promptTraceProvenance(ctx, provRef)
	// Record the operator's screen-context bundle (slice 1's VisualAmbient) as
	// the call's `input.visual` — the auditable INPUT to the decision, frame by
	// handle, never inlined bytes. Reject a frame_handle that does not resolve to
	// a recorded artifact (no dangling frame reference).
	askInput := map[string]any{}
	if visualBlock, hasVisual, visualErr := recordedVisualInput(ctx); visualErr != nil {
		return Result{Error: fmt.Sprintf("host.agent.ask: %v", visualErr)}, nil
	} else if hasVisual {
		askInput["visual"] = visualBlock
	}
	appendAgentCalledEvent(ctx, callStart, callID, rendered, policy.AgentCalledFields(AgentCalledPayload{
		Verb:           "ask",
		Agent:          agentNameFromArgs(args),
		Model:          agent.Model,
		Input:          marshalInput(askInput),
		PromptOverlay:  promptOverlay,
		SpecDefaulted:  specDefaulted,
		SpecOverridden: specOverridden,
	}))

	cr, _, runErr := AgentStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      rendered,
		WorkingDir: workingDir,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		errMsg := agentRunErrorMessage("ask", runErr, cr.Stderr)
		slog.InfoContext(ctx, "agent.ask.complete",
			"call_id", callID,
			"agent", agent.Model,
			"model", agent.Model,
			"duration_ms", durationMS,
			"error", errMsg,
		)
		appendAgentErrorEvent(ctx, time.Now(), callID, AgentErrorPayload{
			Verb:       "ask",
			Agent:      agentNameFromArgs(args),
			DurationMS: durationMS,
			Error:      errMsg,
		})
		return Result{Error: errMsg, FailureKind: FailureInfra}, nil
	}

	var errMsg string
	if cr.Infra != nil {
		errMsg = fmt.Sprintf("host.agent.ask: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			errMsg = fmt.Sprintf("%s\nstderr: %s", errMsg, s)
		}
		callEnd := time.Now()
		// Emit lean slog + journal before returning
		slog.InfoContext(ctx, "agent.ask.complete",
			"call_id", callID,
			"agent", agent.Model,
			"model", agent.Model,
			"duration_ms", durationMS,
			"error", errMsg,
		)
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       "ask",
			Agent:      agentNameFromArgs(args),
			DurationMS: durationMS,
			Error:      errMsg,
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

	// Emit lean slog agent.ask.complete.
	slog.InfoContext(ctx, "agent.ask.complete",
		"call_id", callID,
		"model", agent.Model,
		"duration_ms", durationMS,
	)

	// Write full KindAgentCall journal entry.
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
	callEnd := time.Now()
	appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
		Verb:       "ask",
		Agent:      agentNameFromArgs(args),
		Model:      agent.Model,
		DurationMS: durationMS,
		Response:   marshalResponse(responseDesc),
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
