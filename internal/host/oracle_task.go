// Package host — host.oracle.task handler (oracle-split Phase 4).
//
// host.oracle.task is the agentic call: the LLM may Edit/Write/Bash freely
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
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// kitsokiSessionIDKey is the context key for the KITSOKI_SESSION_ID value.
// The value is propagated per-subprocess rather than via os.Setenv so
// concurrent oracle-serve RPC calls each see their own session ID.
type kitsokiSessionIDKey struct{}

// WithKitsokiSessionID returns a child context carrying the given session ID.
// Every subprocess spawned through OracleStreamer picks it up via the
// OracleStreamer.SessionID field. Tests use this to verify propagation without
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

// OracleTaskHandler implements host.oracle.task.
func OracleTaskHandler(ctx context.Context, args map[string]any) (Result, error) {
	// ── Mandatory: agent: ─────────────────────────────────────────────────
	agentName, _ := args["agent"].(string)
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		return Result{Error: "host.oracle.task: agent: argument is required — declare a named agent in the agents: block"}, nil
	}

	agent, agentOK := resolveAgent(ctx, args)
	if !agentOK {
		return Result{Error: fmt.Sprintf("host.oracle.task: unknown agent %q — check the agents: block in app.yaml", agentName)}, nil
	}

	// ── Acceptance block ──────────────────────────────────────────────────
	acceptance, acceptErr := parseTaskAcceptance(args)
	if acceptErr != "" {
		return Result{Error: "host.oracle.task: " + acceptErr}, nil
	}
	if acceptance.SchemaPath == "" {
		return Result{Error: "host.oracle.task: acceptance.schema is required"}, nil
	}

	maxRetries := acceptance.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultTaskMaxRetries
	}

	// ── Working directory ─────────────────────────────────────────────────
	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)

	// ── Resolve binary ────────────────────────────────────────────────────
	bin, binErr := resolveOracleBin(ctx)
	if binErr != nil {
		return Result{Error: binErr.Error()}, nil
	}

	// ── Context prompt (optional) ─────────────────────────────────────────
	contextPrompt, contextErr := resolveTaskContextPrompt(args)
	if contextErr != "" {
		return Result{Error: "host.oracle.task: " + contextErr}, nil
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
	taskSystemPrompt := effectiveSystemPrompt(args, agent)

	slog.InfoContext(ctx, "task.start",
		"agent", agentName,
		"working_dir", workingDir,
		"replay_mode", string(replayMode),
		"initial_state_hash", initialHash,
		"task_trace_id", taskTraceID,
	)

	// ── Build CLI args ────────────────────────────────────────────────────
	baseCLIArgs := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
	}
	if sp := effectiveSystemPrompt(args, agent); strings.TrimSpace(sp) != "" {
		baseCLIArgs = append(baseCLIArgs, "--append-system-prompt", sp)
	}
	if strings.TrimSpace(agent.Model) != "" {
		baseCLIArgs = append(baseCLIArgs, "--model", agent.Model)
	}
	if len(tools) > 0 {
		baseCLIArgs = appendAllowedToolsFlag(baseCLIArgs, tools)
	}

	// Attach the validator MCP server (schema + optional post_cmd).
	outputFile, outputCleanup, mcpErr := attachTaskValidator(bin, acceptance)
	if mcpErr != "" {
		return Result{Error: "host.oracle.task: " + mcpErr}, nil
	}
	if outputCleanup != nil {
		defer outputCleanup()
	}

	// Build the MCP config if we have a validator to attach.
	var mcpConfigPath string
	if outputFile != "" {
		mcpCfg, validatorCfg, vcErr := buildTaskValidatorMCPConfig(acceptance, outputFile)
		if vcErr != nil {
			return Result{Error: "host.oracle.task: build validator MCP config: " + vcErr.Error()}, nil
		}
		mcpConfigPath = mcpCfg
		if mcpConfigPath != "" {
			defer os.Remove(mcpConfigPath)
			baseCLIArgs = append(baseCLIArgs, "--mcp-config", mcpConfigPath)
		}
		_ = validatorCfg
	}

	// Obtain the parent kitsoki session ID for trace-continuity propagation
	// into subprocesses. This is read from context (set by oracle-serve per-RPC)
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
	// as oracle_ask_with_mcp.go).
	stateFile, sfErr := os.CreateTemp("", "kitsoki-task-state-*.json")
	if sfErr == nil {
		stateFilePath := stateFile.Name()
		stateFile.Close()
		defer os.Remove(stateFilePath)
		if outputFile != "" {
			// Re-build with state file path.
			mcpCfg2, _, vcErr2 := buildTaskValidatorMCPConfigWithState(acceptance, outputFile, stateFilePath)
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

		cr, returnedSID, runErr := OracleStreamer{
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
			return Result{Error: fmt.Sprintf("host.oracle.task: claude exec failed: %v", cr.Infra)}, nil
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
			exhaustedErr := fmt.Sprintf("host.oracle.task: acceptance failed after %d attempt(s); last output: %s",
				maxRetries, onelinePreview(lastStdout, 300))
			exhaustedDurationMS := time.Since(callStart).Milliseconds()
			slog.InfoContext(ctx, "oracle.task.complete",
				"call_id", callID,
				"model", agent.Model,
				"duration_ms", exhaustedDurationMS,
				"task_trace_id", taskTraceID,
				"error", exhaustedErr,
			)
			appendOracleCallJournal(ctx, callStart, 0, OracleCallBody{
				CallID:       callID,
				Verb:         "task",
				Agent:        agentName,
				Model:        agent.Model,
				TaskTraceID:  taskTraceID,
				DurationMS:   exhaustedDurationMS,
				SystemPrompt: taskSystemPrompt,
				Prompt:       contextPrompt,
				Input: marshalInput(map[string]any{
					"instructions": contextPrompt,
					"files_in":     filesChanged,
				}),
				Response: marshalResponse(map[string]any{
					"text": lastStdout,
				}),
				Error: exhaustedErr,
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

	slog.InfoContext(ctx, "oracle.task.complete",
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

	appendOracleCallJournal(ctx, callStart, 0, OracleCallBody{
		CallID:       callID,
		Verb:         "task",
		Agent:        agentName,
		Model:        agent.Model,
		TaskTraceID:  taskTraceID,
		DurationMS:   taskDurationMS,
		SystemPrompt: taskSystemPrompt,
		Prompt:       contextPrompt,
		Input: marshalInput(map[string]any{
			"instructions": contextPrompt,
			"files_in":     filesChanged,
		}),
		Response: marshalResponse(map[string]any{
			"text": lastStdout,
		}),
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
func resolveTaskContextPrompt(args map[string]any) (string, string) {
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
	if resolved := resolvePromptPath(promptVal); resolved != promptVal {
		data, err := os.ReadFile(resolved)
		if err == nil {
			rendered = string(data)
		}
	} else {
		// Try reading as a relative file too.
		if data, err := os.ReadFile(resolvePromptPath(promptVal)); err == nil {
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
	result, renderErr := render.Pongo(rendered, expr.Env{Args: templateArgs})
	if renderErr != nil {
		return "", fmt.Sprintf("render context.prompt: %v", renderErr)
	}
	return sourcecolor.Strip(result), ""
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
func buildTaskValidatorMCPConfig(acceptance taskAcceptanceOptions, outputFile string) (string, map[string]any, error) {
	return buildTaskValidatorMCPConfigWithState(acceptance, outputFile, "")
}

// buildTaskValidatorMCPConfigWithState is the same but includes a state file
// path for the validator's retry-state persistence across resumptions.
func buildTaskValidatorMCPConfigWithState(acceptance taskAcceptanceOptions, outputFile, stateFilePath string) (string, map[string]any, error) {
	opts := validatorOptions{
		PostCmd:       acceptance.PostCmd,
		PostCmdArgs:   acceptance.PostCmdArgs,
		MaxRetries:    acceptance.MaxRetries,
		StateFilePath: stateFilePath,
	}
	validatorEntry, err := buildValidatorMCPServer(acceptance.SchemaPath, outputFile, opts)
	if err != nil {
		return "", nil, err
	}

	mcpServers := map[string]any{
		"validator": validatorEntry,
	}

	// Write a temp JSON MCP config file.
	cfg := map[string]any{"mcpServers": mcpServers}
	data, jsonErr := marshalJSON(cfg)
	if jsonErr != nil {
		return "", nil, fmt.Errorf("marshal MCP config: %w", jsonErr)
	}
	f, fErr := os.CreateTemp("", "kitsoki-task-mcp-*.json")
	if fErr != nil {
		return "", nil, fmt.Errorf("create MCP config: %w", fErr)
	}
	if _, wErr := f.Write(data); wErr != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write MCP config: %w", wErr)
	}
	f.Close()
	return f.Name(), validatorEntry, nil
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
	if jsonErr := unmarshalJSON(data, &v); jsonErr != nil {
		return nil, jsonErr
	}
	return v, nil
}

// extractSessionID reads the kitsoki session ID for trace continuity.
// It checks the context first (set by WithKitsokiSessionID for per-call
// isolation in oracle-serve), then falls back to the process environment.
// Kept as a shim so export_test.go's ExtractSessionIDExport continues to work.
func extractSessionID(ctx context.Context) string {
	return kitsokiSessionIDFromCtx(ctx)
}

// marshalJSON marshals v to JSON.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// unmarshalJSON unmarshals JSON data into v.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// resolveTaskWorkingDir resolves a working_dir path; expands relative paths
// against KITSOKI_APP_DIR when set, same as resolvePromptPath.
func resolveTaskWorkingDir(dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	if base := os.Getenv(AppDirEnv); base != "" {
		return filepath.Join(base, dir)
	}
	return dir
}
