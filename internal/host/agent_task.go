// Package host — host.agent.task handler.
//
// host.agent.task is the agentic call: the LLM may Edit/Write/Bash freely
// within the declared working directory. Every tool call produces a task.tool
// journal event. The handler drives an acceptance loop (schema + optional
// post_cmd) until the LLM's submit() passes or the retry budget is exhausted.
//
// Mandatory args:
//   - agent: (string) — named agent from the agents: block (required; loader
//     enforces this at app-load time). The agent declares tools, model, cwd,
//     and ExternalSideEffect.
//
// Optional args:
//   - working_dir: (string) — cwd for the agent subprocess; wins over
//     agent.DefaultCwd.
//   - acceptance: (map) — done-condition; sub-keys:
//     schema:      (string) path to JSON schema (required on acceptance:).
//     post_cmd:    (string) verifier command run after schema passes.
//     post_cmd_args: (map[string]string) forwarded as --key value.
//     max_retries: (int) retry budget; 0 means default (5).
//   - context.prompt: (string) prompt text or path injected into the agent's
//     first turn.
//   - context.args:   (map) template variables for context.prompt.
//
// Returns Result.Data with:
//   - submitted       (any):    the JSON payload passed to submit().
//   - task_trace_id   (string): child span ID pointing at the nested trace.
//   - files_changed   ([]string): sorted list of mutated paths.
//   - final_diff      (string): unified diff (in journal only; also returned
//     so callers can bind it when needed).
//   - replay_mode     (string): "file_diff" | "sandboxed_write" |
//     "external_side_effect".
//
// On Mode C (external_side_effect: true) the handler returns the same shape
// but replay tooling will skip this span when running --mode file_diff.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"kitsoki/internal/sysprompt"
)

// kitsokiSessionIDKey is the context key for the KITSOKI_SESSION_ID value.
// The value is propagated per-subprocess rather than via os.Setenv so
// concurrent agent-serve RPC calls each see their own session ID.
type kitsokiSessionIDKey struct{}

// WithKitsokiSessionID returns a child context carrying the given session ID.
// Every subprocess spawned through AgentStreamer picks it up via the
// AgentStreamer.SessionID field. Tests use this to verify propagation without
// touching the process-global environment.
func WithKitsokiSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, kitsokiSessionIDKey{}, sessionID)
}

// kitsokiSessionIDFromCtx returns the session ID stored in ctx by
// WithKitsokiSessionID, falling back to KITSOKI_SESSION_ID in the process env.
func kitsokiSessionIDFromCtx(ctx context.Context) string {
	if v, _ := ctx.Value(kitsokiSessionIDKey{}).(string); v != "" {
		return v
	}
	return os.Getenv("KITSOKI_SESSION_ID")
}

// KitsokiSessionIDFromCtx is the exported form of kitsokiSessionIDFromCtx.
// Used by the cmd/kitsoki tests to verify that injectSessionID stores the
// session ID in context rather than the process-global env.
func KitsokiSessionIDFromCtx(ctx context.Context) string {
	return kitsokiSessionIDFromCtx(ctx)
}

// defaultTaskMaxRetries is the retry budget for the acceptance loop when
// max_retries is not specified in the acceptance: block.
const defaultTaskMaxRetries = 5

// taskAcceptanceOptions carries the parsed acceptance: block.
type taskAcceptanceOptions struct {
	SchemaPath  string
	PostCmd     string
	PostCmdArgs []postCmdKV
	MaxRetries  int
}

// AgentTaskHandler implements host.agent.task.
func AgentTaskHandler(ctx context.Context, args map[string]any) (Result, error) {
	// ── Mandatory: agent: ─────────────────────────────────────────────────
	agentName, _ := args["agent"].(string)
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return Result{Error: "host.agent.task: agent: argument is required — declare a named agent in the agents: block"}, nil
	}

	agent, agentOK := resolveAgent(ctx, args)
	if !agentOK {
		return Result{Error: fmt.Sprintf("host.agent.task: unknown agent %q — check the agents: block in app.yaml", agentName)}, nil
	}
	ctx, agent = applyProvider(ctx, args, agent)

	// B-7: If an agent plugin registry is wired in context, route through
	// host.Dispatch. For task the prompt is the context.prompt field.
	withArgs, _ := args["with"].(map[string]any)
	contextPromptForPlugin, _ := args["context"].(map[string]any)
	taskPromptForPlugin := ""
	if cp, ok := contextPromptForPlugin["prompt"].(string); ok {
		taskPromptForPlugin = cp
	}
	// Try plugin dispatch before processing acceptance/tools (which subprocess needs).
	if pluginRes, handled, pluginErr := TryDispatchVerb(ctx, "task", taskPromptForPlugin, "", agentName, "", withArgs, nil); handled {
		if pluginErr != nil {
			return Result{Error: pluginErr.Error()}, nil
		}
		return pluginRes, nil
	}

	// ── Acceptance block ──────────────────────────────────────────────────
	acceptance, acceptErr := parseTaskAcceptance(args)
	if acceptErr != "" {
		return Result{Error: "host.agent.task: " + acceptErr}, nil
	}
	if acceptance.SchemaPath == "" {
		return Result{Error: "host.agent.task: acceptance.schema is required"}, nil
	}

	maxRetries := acceptance.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultTaskMaxRetries
	}

	// ── Working directory ─────────────────────────────────────────────────
	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)

	// ── Resolve binary ────────────────────────────────────────────────────
	bin, binErr := resolveAgentBin(ctx)
	if binErr != nil {
		return Result{Error: binErr.Error()}, nil
	}

	// ── Context prompt (optional) ─────────────────────────────────────────
	contextPrompt, contextErr := resolveTaskContextPrompt(ctx, args)
	if contextErr != "" {
		return Result{Error: "host.agent.task: " + contextErr}, nil
	}

	// ── Effective tools ───────────────────────────────────────────────────
	tools := effectiveTools(ctx, args, agent)

	// ── Replay mode ───────────────────────────────────────────────────────
	replayMode := inferReplayMode(agent, tools)

	// ── Capture initial state hash ────────────────────────────────────────
	taskTraceID := newUUID()
	initialHash := captureInitialStateHash(ctx, workingDir)

	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its RawEvents into
	// the agent-action-transcript sidecar keyed by THIS call's id — the sidecar
	// filename pairs by value with the agent.call.complete event below. (Live
	// path only; replay writes the recorded transcript via the cassette path.)
	ctx = WithCallID(ctx, callID)

	// Wave 3-agent: write AgentCalled to the JSONL sink at dispatch time.
	// The rendered context prompt is recorded as a reference (inline when
	// small, else a sidecar file); see docs/tracing/trace-format.md.
	var taskPromptRef string
	if cm, ok := args["context"].(map[string]any); ok {
		taskPromptRef, _ = cm["prompt"].(string)
	}
	tOverlay, tDefaulted, tOverridden := promptTraceProvenance(ctx, taskPromptRef)
	appendAgentCalledEvent(ctx, callStart, callID, contextPrompt, AgentCalledPayload{
		Verb:           "task",
		Agent:          agentName,
		Model:          agent.Model,
		PromptOverlay:  tOverlay,
		SpecDefaulted:  tDefaulted,
		SpecOverridden: tOverridden,
	})

	slog.InfoContext(ctx, "task.start",
		"agent", agentName,
		"working_dir", workingDir,
		"replay_mode", string(replayMode),
		"initial_state_hash", initialHash,
		"task_trace_id", taskTraceID,
	)

	// ── Write-mode posture ────────────────────────────────────────────────
	// A write_mode: read_only room boots the agent read-only: the bypassPermissions
	// default is downgraded to the read-only converse posture (the allowlist binds
	// and readOnlyDeniedTools are hard-denied), and Bash is routed through the
	// kitsoki-bash MCP wrapper under a read-only profile, with a write-mode gate
	// attached so a mutating command becomes an operator action proposal rather
	// than a flat deny. Absent / open ⇒ this whole block is skipped and dispatch is
	// byte-for-byte today's. See docs/proposals/agent-write-mode-opt-in.md.
	oc := AgentCallCtxFrom(ctx)
	writeModeReadOnly := IsReadOnlyWriteMode(oc.WriteMode)
	var writeModeGate *WriteModeGate
	if writeModeReadOnly {
		writeModeGate = NewWriteModeGate(true, GrantScope(oc.WriteModeScope), gateAskerFor(ctx))
		ctx = WithWriteModeGate(ctx, writeModeGate)
		// Force a read-only Bash profile (overriding any agent profile) so the
		// read-only floor is uniform regardless of the agent's declaration.
		agent.BashProfile = &BashProfile{Kind: BashProfileReadOnly}
	}

	// ── Build CLI args ────────────────────────────────────────────────────
	baseCLIArgs := buildBaseCLIArgs(ctx, sysprompt.Task, args, agent)
	if writeModeReadOnly {
		baseCLIArgs = applyReadOnlyFloorCLIArgs(baseCLIArgs)
		tools = rewriteToolsForBashMCP(tools)
	}
	// When an acceptance schema is set the validator MCP server is attached as
	// "validator", exposing mcp__validator__submit. Add it to the allowed tools
	// so the agent can call submit() even when the tool list is otherwise
	// restricted — without this the tool is hidden by --allowedTools filtering.
	if acceptance.SchemaPath != "" {
		tools = append(tools, "mcp__validator__submit")
	}
	// Forward operator questions into kitsoki when a live surface is attached.
	// The listener outlives the per-attempt --resume loop (cleanup is deferred at
	// handler scope), so a question asked on attempt N is still answerable later.
	var opAskCleanup func()
	baseCLIArgs, tools, opAskCleanup, _ = attachOperatorAsk(ctx, baseCLIArgs, tools)
	defer opAskCleanup()
	// Read-only floor: route Bash (if requested) through the kitsoki-bash MCP
	// wrapper under a read-only profile so the subprocess can only run read-only
	// commands; a mutating command is denied by the profile (the gate's operator
	// action proposal runs in-process — see WriteModeGate). The tool list was
	// already rewritten (Bash → mcp__kitsoki-bash__Bash) above.
	if writeModeReadOnly && containsBashMCPTool(tools) {
		bashEntry, bashCfgPath, bErr := BuildBashMCPEntry(agent.BashProfile, workingDir)
		if bErr != nil {
			return Result{Error: "host.agent.task: build read-only bash MCP: " + bErr.Error()}, nil
		}
		defer os.Remove(bashCfgPath)
		bashMCPPath, bashMCPCleanup, mErr := writeMCPConfigTempfile(map[string]any{"kitsoki-bash": bashEntry}, "kitsoki-task-bash-mcp")
		if mErr != nil {
			return Result{Error: "host.agent.task: write read-only bash MCP config: " + mErr.Error()}, nil
		}
		defer bashMCPCleanup()
		baseCLIArgs = append(baseCLIArgs, "--mcp-config", bashMCPPath)
	}
	if len(tools) > 0 {
		baseCLIArgs = appendAllowedToolsFlag(baseCLIArgs, tools)
	}

	// Attach the validator MCP server (schema + optional post_cmd).
	outputFile, outputCleanup, mcpErr := attachTaskValidator(bin, acceptance)
	if mcpErr != "" {
		return Result{Error: "host.agent.task: " + mcpErr}, nil
	}
	if outputCleanup != nil {
		defer outputCleanup()
	}

	// Build the MCP config if we have a validator to attach.
	var mcpConfigPath string
	if outputFile != "" {
		mcpCfg, validatorCfg, vcErr := buildTaskValidatorMCPConfig(ctx, acceptance, outputFile)
		if vcErr != nil {
			return Result{Error: "host.agent.task: build validator MCP config: " + vcErr.Error()}, nil
		}
		mcpConfigPath = mcpCfg
		if mcpConfigPath != "" {
			defer os.Remove(mcpConfigPath)
			baseCLIArgs = append(baseCLIArgs, "--mcp-config", mcpConfigPath)
		}
		_ = validatorCfg
	}

	// Obtain the parent kitsoki session ID for trace-continuity propagation
	// into subprocesses. This is read from context (set by agent-serve per-RPC)
	// or falls back to the process env; it is NEVER injected via os.Setenv so
	// concurrent callers don't race on the global env.
	parentSessionID := kitsokiSessionIDFromCtx(ctx)

	// ── Run the acceptance loop ───────────────────────────────────────────
	var (
		lastSubmitted any
		lastStdout    string
		attempt       int
	)

	// Use a temp file for state persistence across retries (same pattern
	// as agent_ask_with_mcp.go).
	stateFile, sfErr := os.CreateTemp("", "kitsoki-task-state-*.json")
	if sfErr == nil {
		stateFilePath := stateFile.Name()
		stateFile.Close()
		defer os.Remove(stateFilePath)
		if outputFile != "" {
			// Re-build with state file path.
			mcpCfg2, _, vcErr2 := buildTaskValidatorMCPConfigWithState(ctx, acceptance, outputFile, stateFilePath)
			if vcErr2 == nil && mcpCfg2 != "" {
				// Replace the config.
				os.Remove(mcpConfigPath)
				mcpConfigPath = mcpCfg2
				defer os.Remove(mcpConfigPath)
				// Rebuild baseCLIArgs without the old --mcp-config.
				var filtered []string
				skipNext := false
				for _, a := range baseCLIArgs {
					if skipNext {
						skipNext = false
						continue
					}
					if a == "--mcp-config" {
						skipNext = true
						continue
					}
					filtered = append(filtered, a)
				}
				filtered = append(filtered, "--mcp-config", mcpConfigPath)
				baseCLIArgs = filtered
			}
		}
	}

	claudeSID := newUUID()
	firstRun := true

	for attempt = 1; attempt <= maxRetries; attempt++ {
		cliArgs := append([]string(nil), baseCLIArgs...)
		if firstRun {
			cliArgs = append(cliArgs, "--session-id", claudeSID)
			firstRun = false
		} else {
			cliArgs = append(cliArgs, "--resume", claudeSID)
		}

		cr, returnedSID, runErr := AgentStreamer{
			Bin:        bin,
			CLIArgs:    cliArgs,
			Stdin:      contextPrompt,
			WorkingDir: workingDir,
			SessionID:  parentSessionID,
		}.Run(ctx)
		// H5: capture the claude session ID returned from system.init so
		// --resume works correctly across iterations (mirrors decide's pattern).
		if returnedSID != "" {
			claudeSID = returnedSID
		}

		if runErr != nil {
			return Result{}, runErr
		}
		if cr.Infra != nil {
			return Result{Error: fmt.Sprintf("host.agent.task: claude exec failed: %v", cr.Infra)}, nil
		}

		// Observe tool calls in the output for tracing.
		_ = observeTaskToolCalls(ctx, cr, taskTraceID)

		lastStdout = cr.Stdout

		// Read the submitted payload from the validator output file.
		submitted, readSubmitErr := readSubmittedPayload(outputFile)
		if readSubmitErr == nil && submitted != nil {
			lastSubmitted = submitted
			// Acceptance passed — exit loop.
			emitAcceptanceAttempt(ctx, attempt, 0, onelinePreview(cr.Stdout, 200), "")
			break
		}

		// No valid submission yet or validator rejected.
		rejectedReason := ""
		if cr.ExitCode != 0 {
			rejectedReason = claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
		}
		emitAcceptanceAttempt(ctx, attempt, cr.ExitCode, onelinePreview(cr.Stdout, 200), rejectedReason)

		if attempt == maxRetries {
			// Budget exhausted.
			finalDiff := captureFinalDiff(ctx, workingDir)
			filesChanged := captureFilesChanged(ctx, workingDir)
			emitTaskEnd(ctx, "exhausted", filesChanged, replayMode)
			exhaustedErr := fmt.Sprintf("host.agent.task: acceptance failed after %d attempt(s); last output: %s",
				maxRetries, onelinePreview(lastStdout, 300))
			exhaustedDurationMS := time.Since(callStart).Milliseconds()
			slog.InfoContext(ctx, "agent.task.complete",
				"call_id", callID,
				"model", agent.Model,
				"duration_ms", exhaustedDurationMS,
				"task_trace_id", taskTraceID,
				"error", exhaustedErr,
			)
			appendAgentErrorEvent(ctx, time.Now(), callID, AgentErrorPayload{
				Verb:       "task",
				Agent:      agentName,
				DurationMS: exhaustedDurationMS,
				Error:      exhaustedErr,
			})
			return Result{
				Error: exhaustedErr,
				Data: map[string]any{
					"task_trace_id": taskTraceID,
					"files_changed": filesChanged,
					"final_diff":    finalDiff,
					"replay_mode":   string(replayMode),
				},
			}, nil
		}

		// Nudge the LLM with the rejection reason on subsequent turns.
		if rejectedReason != "" {
			contextPrompt = "The previous submission was rejected. Reason: " + rejectedReason + "\nPlease try again."
		} else {
			contextPrompt = "The submission was not accepted. Please call submit() with a valid payload that matches the schema."
		}
	}

	// ── Capture final state ───────────────────────────────────────────────
	finalDiff := captureFinalDiff(ctx, workingDir)
	filesChanged := captureFilesChanged(ctx, workingDir)

	emitTaskEnd(ctx, "success", filesChanged, replayMode)

	taskDurationMS := time.Since(callStart).Milliseconds()

	slog.InfoContext(ctx, "agent.task.complete",
		"call_id", callID,
		"model", agent.Model,
		"duration_ms", taskDurationMS,
		"task_trace_id", taskTraceID,
	)

	slog.InfoContext(ctx, "task.end",
		"outcome", "success",
		"task_trace_id", taskTraceID,
		"files_changed", len(filesChanged),
		"replay_mode", string(replayMode),
		"initial_state_hash", initialHash,
	)

	appendAgentReturnedEvent(ctx, time.Now(), callID, AgentReturnedPayload{
		Verb:       "task",
		Agent:      agentName,
		Model:      agent.Model,
		DurationMS: taskDurationMS,
		Response:   marshalResponse(map[string]any{"text": lastStdout}),
	})

	return Result{
		Data: map[string]any{
			"submitted":     lastSubmitted,
			"task_trace_id": taskTraceID,
			"files_changed": filesChanged,
			"final_diff":    finalDiff,
			"replay_mode":   string(replayMode),
		},
	}, nil
}

// parseTaskAcceptance extracts the acceptance: block from handler args.
func parseTaskAcceptance(args map[string]any) (taskAcceptanceOptions, string) {
	rawBlock, ok := args["acceptance"]
	if !ok || rawBlock == nil {
		return taskAcceptanceOptions{}, ""
	}
	blk, ok := rawBlock.(map[string]any)
	if !ok {
		return taskAcceptanceOptions{}, "acceptance: must be a mapping"
	}
	var opts taskAcceptanceOptions

	if v, _ := blk["schema"].(string); strings.TrimSpace(v) != "" {
		opts.SchemaPath = v
	}
	if v, _ := blk["post_cmd"].(string); strings.TrimSpace(v) != "" {
		opts.PostCmd = v
	}
	// max_retries: handle the same numeric gymnastics as validatorOptions.
	switch v := blk["max_retries"].(type) {
	case int:
		opts.MaxRetries = v
	case int64:
		opts.MaxRetries = int(v)
	case uint64:
		opts.MaxRetries = int(v)
	case float64:
		opts.MaxRetries = int(v)
	case float32:
		opts.MaxRetries = int(v)
	case int8, int16, int32:
		// Best effort.
	}

	if rawArgs, present := blk["post_cmd_args"]; present && rawArgs != nil {
		argsMap, ok2 := rawArgs.(map[string]any)
		if !ok2 {
			return taskAcceptanceOptions{}, "acceptance.post_cmd_args: must be a mapping"
		}
		for k, v := range argsMap {
			val, ok3 := v.(string)
			if !ok3 {
				return taskAcceptanceOptions{}, fmt.Sprintf("acceptance.post_cmd_args[%q]: must be a string", k)
			}
			opts.PostCmdArgs = append(opts.PostCmdArgs, postCmdKV{Key: k, Value: val})
		}
	}
	return opts, ""
}

// resolveTaskContextPrompt renders the optional context.prompt + context.args
// block into a string to be used as stdin for the agent's first turn.
func resolveTaskContextPrompt(goctx context.Context, args map[string]any) (string, string) {
	ctx, _ := args["context"].(map[string]any)
	if ctx == nil {
		return "", ""
	}
	promptVal, _ := ctx["prompt"].(string)
	if strings.TrimSpace(promptVal) == "" {
		return "", ""
	}
	// Resolve as a file path or inline text.
	rendered := promptVal
	if resolved := resolvePromptPathCtx(goctx, promptVal); resolved != promptVal {
		data, err := readPromptFile(resolved)
		if err == nil {
			rendered = string(data)
		}
	} else {
		// Try reading as a relative file too.
		if data, err := readPromptFile(resolvePromptPathCtx(goctx, promptVal)); err == nil {
			rendered = string(data)
		}
	}
	// Template args.
	templateArgs, _ := ctx["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = map[string]any{}
	}
	// Copy parent args under "args" namespace for the template.
	for k, v := range args {
		if _, alreadySet := templateArgs[k]; !alreadySet {
			templateArgs[k] = v
		}
	}
	result, renderErr := renderAndStripPrompt(goctx, rendered, templateArgs)
	if renderErr != nil {
		return "", fmt.Sprintf("render context.prompt: %v", renderErr)
	}
	return result, ""
}

// attachTaskValidator prepares the output file for capturing the submitted
// payload. Returns (outputFilePath, cleanup, errMsg).
func attachTaskValidator(bin string, acceptance taskAcceptanceOptions) (string, func(), string) {
	if acceptance.SchemaPath == "" {
		return "", nil, ""
	}
	f, err := os.CreateTemp("", "kitsoki-task-submit-*.json")
	if err != nil {
		return "", nil, fmt.Sprintf("create submit output file: %v", err)
	}
	path := f.Name()
	f.Close()
	cleanup := func() { os.Remove(path) }
	return path, cleanup, ""
}

// buildTaskValidatorMCPConfig builds an MCP config file containing the
// kitsoki mcp-validator entry for the task's acceptance schema + optional
// post_cmd. Returns (configFilePath, validatorEntry, error).
func buildTaskValidatorMCPConfig(ctx context.Context, acceptance taskAcceptanceOptions, outputFile string) (string, map[string]any, error) {
	return buildTaskValidatorMCPConfigWithState(ctx, acceptance, outputFile, "")
}

// buildTaskValidatorMCPConfigWithState is the same but includes a state file
// path for the validator's retry-state persistence across resumptions. ctx
// carries the per-call prompt renderer so the acceptance schema resolves against
// THIS session's story dir (isolated per concurrent driving session), not the
// process-global KITSOKI_APP_DIR — see buildValidatorMCPServer's doc.
func buildTaskValidatorMCPConfigWithState(ctx context.Context, acceptance taskAcceptanceOptions, outputFile, stateFilePath string) (string, map[string]any, error) {
	opts := validatorOptions{
		PostCmd:       acceptance.PostCmd,
		PostCmdArgs:   acceptance.PostCmdArgs,
		MaxRetries:    acceptance.MaxRetries,
		StateFilePath: stateFilePath,
	}
	validatorEntry, err := buildValidatorMCPServer(ctx, acceptance.SchemaPath, outputFile, opts)
	if err != nil {
		return "", nil, err
	}

	mcpServers := map[string]any{
		"validator": validatorEntry,
	}

	// Write a temp JSON MCP config file. The caller owns removal of the
	// returned path (it tracks mcpConfigPath across the state-file rebuild),
	// so we discard the cleanup func here.
	path, _, cfgErr := writeMCPConfigTempfile(mcpServers, "kitsoki-task-mcp")
	if cfgErr != nil {
		return "", nil, fmt.Errorf("MCP config: %w", cfgErr)
	}
	return path, validatorEntry, nil
}

// readSubmittedPayload reads the submitted JSON from the validator output file.
// Returns (nil, err) when the file is empty or missing.
func readSubmittedPayload(outputFile string) (any, error) {
	if outputFile == "" {
		return nil, fmt.Errorf("no output file")
	}
	data, err := os.ReadFile(outputFile)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return nil, fmt.Errorf("empty or missing")
	}
	var v any
	if jsonErr := json.Unmarshal(data, &v); jsonErr != nil {
		return nil, jsonErr
	}
	return v, nil
}

// extractSessionID reads the kitsoki session ID for trace continuity.
// It checks the context first (set by WithKitsokiSessionID for per-call
// isolation in agent-serve), then falls back to the process environment.
// Kept as a shim so export_test.go's ExtractSessionIDExport continues to work.
func extractSessionID(ctx context.Context) string {
	return kitsokiSessionIDFromCtx(ctx)
}
