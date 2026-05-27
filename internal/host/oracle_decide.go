// Package host — host.oracle.decide handler (oracle-split Phase 2).
//
// host.oracle.decide is the reasoning verdict verb: LLM judgment is required,
// schema: is mandatory, submit is auto-attached, and the read-only tool surface
// (same allowlist as host.oracle.ask) is optional. It does not mutate anything.
//
// Contract (oracle-split proposal §2.2):
//
//   - schema: required — the verdict must be typed. The handler auto-attaches the
//     kitsoki mcp-validator so the LLM must call submit() before exiting.
//   - Read-only tools: optional. The agent declares the subset (Read, Grep, Glob,
//     WebFetch, WebSearch, Bash under a profile, read-only MCP). Mutation tools
//     (Edit, Write) are rejected at call time as a safety net (loader rejects at
//     load time; this is the runtime double-check).
//   - validator: optional read-only post_cmd block. Runs under validator_sandbox.
//     Reject-with-reason triggers re-submit (same retry shape as ask_with_mcp).
//   - Streaming: funneled through OracleStreamer.Run so reasoning tokens and
//     validator-rejection nudge round-trips all stream.
//   - Returns: submitted (typed JSON), rationale (stdout text), validator_attempts
//     (when validator ran), claude_session_id (in trace; not surfaced to YAML).
//
// Mutation-tool rejection (runtime safety net):
// The loader already rejects agents with Edit/Write/unrestricted Bash at
// load time when the agent is declared for decide. The handler re-checks at
// call time so ad-hoc test scaffolding or future refactors don't silently
// bypass the contract.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"kitsoki/internal/expr"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// mutationTools is shared with oracle_ask.go and now covers decide too.

// decideDefaultMaxRetries is the per-submission retry budget for the decide
// validator. Mirrors validatorDefaultMaxRetries in oracle_ask_with_mcp.go.
const decideDefaultMaxRetries = 5

// decideMaxOuterIterations caps the outer `claude --resume` loop.
const decideMaxOuterIterations = 3

// decideAbandonmentNudgeTemplate is the nudge sent on each --resume re-engagement.
const decideAbandonmentNudgeTemplate = `Your previous turn ended without successfully calling submit.{{LAST_ERROR_BLOCK}}

You MUST call the validator's submit tool with a valid JSON verdict to continue.
Review the schema, identify the correct verdict shape, and call submit. Do not
exit the conversation without submitting.`

// OracleDecideHandler implements host.oracle.decide.
//
// Required args:
//   - prompt (string) or prompt_path (string): the reasoning prompt. Rendered
//     with expr.Env{Args: <args map>}. Mutually exclusive.
//   - schema (string): path to the JSON schema the verdict must conform to.
//     Auto-attaches the kitsoki mcp-validator as an MCP tool named "validator".
//
// Optional args:
//   - agent (string): named agent for system prompt + model + tools.
//   - working_dir (string): CWD for the claude subprocess.
//   - args (map): template variables for the prompt.
//   - validator (map): optional read-only post_cmd block. Same shape as
//     task.acceptance.post_cmd but runs under a read-only sandbox. A non-zero
//     exit code triggers re-submit with the rejection reason as a nudge.
//   - mcp_servers (map): additional MCP servers to attach. Merged with the
//     auto-attached validator.
//   - tools (list): per-call tool override (D5: wins over agent.Tools).
//
// Returns Result.Data with:
//   - submitted (any): the schema-validated verdict JSON.
//   - rationale (string): Claude's free-text reasoning emitted alongside submit.
//   - exit_code (int): claude's exit code.
//   - ok (bool): exit_code == 0.
//   - validator_attempts (int): number of validator subprocess runs (only when
//     validator: was declared).
//   - claude_session_id (string): recorded in trace; not meant for YAML binding.
func OracleDecideHandler(ctx context.Context, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}

	// schema: is mandatory for decide.
	schemaArg, _ := args["schema"].(string)
	if strings.TrimSpace(schemaArg) == "" {
		return Result{Error: "host.oracle.decide: schema: argument is required"}, nil
	}

	// Resolve prompt.
	rendered, errMsg := resolveDecidePrompt(ctx, args)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}

	// Resolve agent and validate tools.
	agent, _ := resolveAgent(ctx, args)
	if errMsg := rejectMutationTools(ctx, args, agent); errMsg != "" {
		return Result{Error: errMsg}, nil
	}

	bin, err := resolveOracleBin(ctx)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)

	// Build base CLI args.
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

	// Resolve effective tools and apply Bash MCP rewrite if Bash is present.
	// For decide calls, Bash must have a BashProfile (enforced by the loader;
	// the runtime check below is a safety net).
	tools := effectiveTools(ctx, args, agent)
	hasBash := false
	for _, t := range tools {
		if t == "Bash" {
			hasBash = true
			break
		}
	}
	if hasBash && agent.BashProfile == nil {
		return Result{Error: "host.oracle.decide: Bash is in the tool list but the agent declares no bash_profile; " +
			"set bash_profile: read-only, commands, or sandboxed-write on the agent declaration"}, nil
	}
	if hasBash {
		tools = rewriteToolsForBashMCP(tools)
	}
	if len(tools) > 0 {
		cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	}

	// Parse optional validator block.
	vopts, vparseErr := parseValidatorOptions(args)
	if vparseErr != "" {
		return Result{Error: fmt.Sprintf("host.oracle.decide: %s", vparseErr)}, nil
	}
	_, validatorBlockPresent := args["validator"]

	// Build merged mcp_servers map with auto-attached submit validator.
	callerServers, _ := args["mcp_servers"].(map[string]any)
	mcpServers := make(map[string]any, len(callerServers)+1)
	for k, v := range callerServers {
		mcpServers[k] = v
	}

	// Attach kitsoki-bash MCP server when Bash is in the tool list.
	if hasBash {
		bashEntry, bashConfigPath, bashErr := BuildBashMCPEntry(agent.BashProfile, workingDir)
		if bashErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.decide: build bash MCP server: %v", bashErr)}, nil
		}
		defer os.Remove(bashConfigPath)
		if _, alreadyHasBash := mcpServers["kitsoki-bash"]; !alreadyHasBash {
			mcpServers["kitsoki-bash"] = bashEntry
		}
	}

	// Allocate tempfiles for validator output and (when retry loop active) state.
	var validatorOutputPath string
	var validatorStateFilePath string

	if _, alreadyHasValidator := mcpServers["validator"]; !alreadyHasValidator {
		outFile, ofErr := os.CreateTemp("", "kitsoki-decide-validated-*.json")
		if ofErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.decide: create validator output tempfile: %v", ofErr)}, nil
		}
		validatorOutputPath = outFile.Name()
		_ = outFile.Close()
		// Remove so we can detect "validator never captured anything" via ErrNotExist.
		_ = os.Remove(validatorOutputPath)
		defer os.Remove(validatorOutputPath)

		if validatorBlockPresent {
			stFile, sfErr := os.CreateTemp("", "kitsoki-decide-validator-state-*.json")
			if sfErr != nil {
				return Result{Error: fmt.Sprintf("host.oracle.decide: create validator state tempfile: %v", sfErr)}, nil
			}
			validatorStateFilePath = stFile.Name()
			_ = stFile.Close()
			_ = os.Remove(validatorStateFilePath)
			defer os.Remove(validatorStateFilePath)
			// C1 fix: pass a schema-only validatorOptions to buildValidatorMCPServer
			// so post_cmd never runs unsandboxed inside mcp-validator. The decide
			// handler runs post_cmd itself via RunValidatorSandboxed (below).
			schemaOnlyOpts := validatorOptions{
				MaxRetries:    vopts.MaxRetries,
				StateFilePath: validatorStateFilePath,
			}
			// Add the state file path to vopts so the sandbox loop can read state.
			vopts.StateFilePath = validatorStateFilePath
			validatorEntry, vErr := buildValidatorMCPServer(schemaArg, validatorOutputPath, schemaOnlyOpts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.oracle.decide: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		} else {
			validatorEntry, vErr := buildValidatorMCPServer(schemaArg, validatorOutputPath, vopts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.oracle.decide: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		}
	}

	// Materialize mcp_servers into a temp config file.
	var mcpConfigPath string
	if len(mcpServers) > 0 {
		mcpConfig := map[string]any{"mcpServers": mcpServers}
		mcpBytes, mErr := json.Marshal(mcpConfig)
		if mErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.decide: marshal mcp_servers: %v", mErr)}, nil
		}
		f, fErr := os.CreateTemp("", "kitsoki-decide-mcp-*.json")
		if fErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.decide: create mcp config tempfile: %v", fErr)}, nil
		}
		if _, wErr := f.Write(mcpBytes); wErr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return Result{Error: fmt.Sprintf("host.oracle.decide: write mcp config: %v", wErr)}, nil
		}
		_ = f.Close()
		mcpConfigPath = f.Name()
		defer os.Remove(mcpConfigPath)
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	// Capture pre-call context for journal recording.
	callID := newUUID()
	callStart := time.Now()
	systemPrompt := effectiveSystemPrompt(args, agent)

	// Build input descriptor for the journal.
	decideInputDesc := map[string]any{
		"schema_path": schemaArg,
	}

	// If a validator block is present, run the retry loop. Otherwise use
	// OracleStreamer for the single-shot streaming path.
	if validatorBlockPresent {
		effectiveMaxRetries := vopts.MaxRetries
		if effectiveMaxRetries <= 0 {
			effectiveMaxRetries = decideDefaultMaxRetries
		}
		res := runDecideWithValidatorRetryLoop(ctx, decideLoopParams{
			Bin:                  bin,
			BaseCLIArgs:          cliArgs,
			Rendered:             rendered,
			WorkingDir:           workingDir,
			ValidatorOutputPath:  validatorOutputPath,
			ValidatorStatePath:   validatorStateFilePath,
			MaxOuterIterations:   decideMaxOuterIterations,
			ValidatorMaxRetries:  effectiveMaxRetries,
			SandboxValidatorOpts: &vopts,
		})
		durationMS := time.Since(callStart).Milliseconds()
		emitDecideJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
			systemPrompt, rendered, decideInputDesc, res)
		return res, nil
	}

	// Single-shot path through OracleStreamer (streams reasoning tokens).
	sessionID := newUUID()
	streamCLIArgs := append(append([]string{}, cliArgs...), "--session-id", sessionID)
	cr, streamSID, runErr := OracleStreamer{
		Bin:        bin,
		CLIArgs:    streamCLIArgs,
		Stdin:      rendered,
		WorkingDir: workingDir,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}
	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.decide: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		infraRes := Result{Error: msg}
		emitDecideJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
			systemPrompt, rendered, decideInputDesc, infraRes)
		return infraRes, nil
	}

	usedSessionID := sessionID
	if streamSID != "" {
		usedSessionID = streamSID
	}

	res := buildDecideResult(cr.Stdout, cr.ExitCode, cr.Stderr, validatorOutputPath, usedSessionID, "")
	emitDecideJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
		systemPrompt, rendered, decideInputDesc, res)
	return res, nil
}

// emitDecideJournal writes the lean slog oracle.decide.complete record and the
// full KindOracleCall journal entry after a decide call completes.
func emitDecideJournal(ctx context.Context, callID string, callStart time.Time, durationMS int64,
	agentName, model, systemPrompt, prompt string, inputDesc map[string]any, res Result) {

	// Lean slog record.
	attrs := []any{
		"call_id", callID,
		"model", model,
		"duration_ms", durationMS,
	}
	if res.Error != "" {
		attrs = append(attrs, "error", res.Error)
	}
	slog.InfoContext(ctx, "oracle.decide.complete", attrs...)

	// Build response descriptor.
	var responseDesc map[string]any
	if res.Data != nil {
		responseDesc = map[string]any{}
		if v, ok := res.Data["submitted"]; ok {
			responseDesc["json"] = v
			if decision, ok2 := v.(map[string]any); ok2 {
				if d, ok3 := decision["decision"].(string); ok3 {
					responseDesc["decision"] = d
				}
			}
		}
	}

	appendOracleCallJournal(ctx, callStart, 0, OracleCallBody{
		CallID:       callID,
		Verb:         "decide",
		Agent:        agentName,
		Model:        model,
		DurationMS:   durationMS,
		SystemPrompt: systemPrompt,
		Prompt:       prompt,
		Input:        marshalInput(inputDesc),
		Response:     marshalResponse(responseDesc),
		Error:        res.Error,
	})
}

// resolveDecidePrompt renders the prompt for a decide call. Accepts either
// a `prompt:` inline string or a `prompt_path:` file reference. Returns
// (rendered, errorMsg). On success errorMsg is "".
func resolveDecidePrompt(_ context.Context, args map[string]any) (string, string) {
	promptInline, _ := args["prompt"].(string)
	promptPath, _ := args["prompt_path"].(string)
	promptInline = strings.TrimSpace(promptInline)
	promptPath = strings.TrimSpace(promptPath)

	if promptInline != "" && promptPath != "" {
		return "", "host.oracle.decide: prompt: and prompt_path: are mutually exclusive — set only one"
	}
	if promptInline == "" && promptPath == "" {
		return "", "host.oracle.decide: prompt: or prompt_path: argument is required"
	}

	var raw string
	if promptPath != "" {
		resolved := resolvePromptPath(promptPath)
		b, err := os.ReadFile(resolved)
		if err != nil {
			return "", fmt.Sprintf("host.oracle.decide: read prompt %q: %v", resolved, err)
		}
		raw = string(b)
	} else {
		raw = promptInline
	}

	// Render template args. Prefer explicit `args:` map; fall back to full call-args.
	templateArgs, _ := args["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = args
	}
	rendered, err := renderDecidePrompt(raw, templateArgs)
	if err != nil {
		return "", fmt.Sprintf("host.oracle.decide: render prompt: %v", err)
	}
	rendered = sourcecolor.Strip(rendered)
	return rendered, ""
}

// renderDecidePrompt renders the prompt template with the given args using
// the same pongo2 renderer as the other oracle handlers.
func renderDecidePrompt(tmpl string, args map[string]any) (string, error) {
	return render.Pongo(tmpl, expr.Env{Args: args})
}

// rejectMutationTools checks that neither per-call tools nor agent tools contain
// mutation capabilities. Returns an error message if any mutation tool is found.
// This is the runtime safety net; the loader is the primary enforcement.
func rejectMutationTools(ctx context.Context, args map[string]any, agent Agent) string {
	tools := effectiveTools(ctx, args, agent)
	for _, t := range tools {
		if mutationTools[t] {
			return fmt.Sprintf("host.oracle.decide: mutation tool %q is not permitted on decide calls; use host.oracle.task for agentic work", t)
		}
	}
	return ""
}

// errValidatorExceededContract is the sentinel returned when RunValidatorSandboxed
// detects a write or network access attempt — i.e. the validator tried to mutate
// state outside /tmp. Proposal §6: "validator exceeded read-only contract —
// migrate this call to host.oracle.task".
const errValidatorExceededContract = "validator exceeded read-only contract — migrate this call to host.oracle.task"

// isSandboxContractViolation reports whether the sandbox result looks like a
// mutation/network attempt rather than a clean semantic rejection or an
// infrastructure failure.
//
// Heuristic: OS-level denial strings in stderr/stdout that come from the
// *subprocess* hitting EACCES (sandbox-exec on macOS, or the process itself
// on Linux). We exclude lines starting with "unshare:" which indicate the
// sandbox harness itself failed to set up the namespace — that's an infra
// issue, not a validator that tried to mutate.
func isSandboxContractViolation(vr ValidatorResult) bool {
	combined := vr.Stderr + vr.Stdout
	// Walk line by line so we can skip unshare harness error lines.
	for _, line := range strings.Split(combined, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		// Skip unshare infrastructure failures — those are not the validator
		// exceeding its read-only contract.
		if strings.HasPrefix(lower, "unshare:") {
			continue
		}
		for _, signal := range []string{
			"operation not permitted",
			"permission denied",
			"sandbox denied",
			"eacces",
		} {
			if strings.Contains(lower, signal) {
				return true
			}
		}
	}
	return false
}

// decideLoopParams bundles everything the retry loop needs.
type decideLoopParams struct {
	Bin                 string
	BaseCLIArgs         []string
	Rendered            string
	WorkingDir          string
	ValidatorOutputPath string
	ValidatorStatePath  string
	MaxOuterIterations  int
	ValidatorMaxRetries int
	// SandboxValidatorOpts, when non-nil and PostCmd is set, causes the loop to
	// run the post_cmd via RunValidatorSandboxed after each schema-passing
	// submission rather than delegating to mcp-validator's unsandboxed path.
	// C1 fix: this ensures decide.validator always executes under the sandbox.
	SandboxValidatorOpts *validatorOptions
}

// runDecideWithValidatorRetryLoop runs the decide call with the validator retry
// loop. Streams through OracleStreamer on all iterations (H2 fix: was using
// runClaudeOneShot on iter > 0, dropping streaming on retry round-trips).
// When SandboxValidatorOpts is set, post_cmd is run via RunValidatorSandboxed
// after each schema pass (C1 fix).
func runDecideWithValidatorRetryLoop(ctx context.Context, p decideLoopParams) Result {
	maxOuter := p.MaxOuterIterations
	if maxOuter <= 0 {
		maxOuter = decideMaxOuterIterations
	}

	sessionID := newUUID()

	var lastStdout string
	var lastExitCode int
	var lastStderr string
	var lastInfraErr error
	var sandboxLastRejection string

	for iter := 0; iter < maxOuter; iter++ {
		var cr ClaudeRun
		var runErr error
		var stdin string

		if iter == 0 {
			stdin = p.Rendered
			iterArgs := append(append([]string{}, p.BaseCLIArgs...), "--session-id", sessionID)
			var streamSID string
			cr, streamSID, runErr = OracleStreamer{
				Bin:        p.Bin,
				CLIArgs:    iterArgs,
				Stdin:      stdin,
				WorkingDir: p.WorkingDir,
			}.Run(ctx)
			if streamSID != "" {
				sessionID = streamSID
			}
		} else {
			// H2 fix: subsequent iterations also go through OracleStreamer so they
			// stream to any installed StreamSink, not just the first round-trip.
			_, _, stateLastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
			nudgeErr := stateLastErr
			if strings.TrimSpace(nudgeErr) == "" && strings.TrimSpace(sandboxLastRejection) != "" {
				nudgeErr = sandboxLastRejection
			}
			stdin = renderDecideNudge(nudgeErr)
			iterArgs := append(append([]string{}, p.BaseCLIArgs...), "--resume", sessionID)
			var streamSID string
			cr, streamSID, runErr = OracleStreamer{
				Bin:        p.Bin,
				CLIArgs:    iterArgs,
				Stdin:      stdin,
				WorkingDir: p.WorkingDir,
			}.Run(ctx)
			if streamSID != "" {
				sessionID = streamSID
			}
		}

		if runErr != nil {
			lastInfraErr = runErr
			break
		}
		if cr.Infra != nil {
			lastInfraErr = cr.Infra
			lastStderr = cr.Stderr
			break
		}
		lastStdout = cr.Stdout
		lastExitCode = cr.ExitCode
		lastStderr = cr.Stderr

		attempts, success, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
		switch outcomeFromState(attempts, success, p.ValidatorMaxRetries) {
		case mcpOutcomeSuccess:
			// Schema passed. If a sandboxed post_cmd is configured, run it now.
			if p.SandboxValidatorOpts != nil && strings.TrimSpace(p.SandboxValidatorOpts.PostCmd) != "" {
				rejection, contractErr := runDecideSandboxValidator(ctx, p.ValidatorOutputPath, p.SandboxValidatorOpts)
				if contractErr != "" {
					// M12: mutation/network attempt detected.
					return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, contractErr)
				}
				if rejection != "" {
					// Semantic rejection — nudge and retry.
					sandboxLastRejection = rejection
					continue
				}
			}
			return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, "")
		case mcpOutcomeRetriesExhausted:
			msg := lastErr
			if strings.TrimSpace(msg) == "" {
				msg = fmt.Sprintf("validator: max retries exhausted after %d attempts", attempts)
			}
			return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, msg)
		case mcpOutcomeAbandoned:
			continue
		}
	}

	// Outer budget spent or infra failure.
	if lastInfraErr != nil {
		msg := fmt.Sprintf("host.oracle.decide: claude exec failed: %v", lastInfraErr)
		if s := strings.TrimSpace(lastStderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}
	}

	attempts, _, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
	msg := lastErr
	if strings.TrimSpace(msg) == "" {
		msg = fmt.Sprintf("validator: session abandoned without successful submit after %d outer iteration(s), %d attempt(s)", maxOuter, attempts)
	}
	return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, msg)
}

// runDecideSandboxValidator reads the captured payload from outputPath and
// runs the configured post_cmd via RunValidatorSandboxed.
//
// Returns:
//   - ("", "")                   — sandbox accepted (zero exit, no contract violation)
//   - (rejectionMsg, "")         — sandbox rejected (non-zero exit); caller nudges LLM
//   - ("", contractErrMsg)       — sandbox detected mutation/network: M12 sentinel
func runDecideSandboxValidator(ctx context.Context, validatorOutputPath string, opts *validatorOptions) (rejection, contractErr string) {
	if opts == nil || strings.TrimSpace(opts.PostCmd) == "" {
		return "", ""
	}

	// Read the submitted payload so the validator can inspect it.
	payload, err := kitsokimcp.ReadCapturedPayload(validatorOutputPath)
	if err != nil || len(payload) == 0 {
		// No payload captured yet — schema-only submission hasn't landed.
		return "validator: no schema-validated payload captured yet", ""
	}

	parts := strings.Fields(opts.PostCmd)
	if len(parts) == 0 {
		return "validator: post_cmd is empty", ""
	}
	argv := append([]string(nil), parts[1:]...)
	for _, kv := range opts.PostCmdArgs {
		argv = append(argv, "--"+kv.Key, kv.Value)
	}

	vr, runErr := RunValidatorSandboxed(ctx, ValidatorSandboxOptions{
		Cmd:   parts[0],
		Args:  argv,
		Stdin: string(payload),
	})
	if runErr != nil {
		// Infrastructure error — surface as a semantic rejection so the loop can retry.
		return fmt.Sprintf("validator sandbox: infrastructure error: %v", runErr), ""
	}

	if vr.ExitCode == 0 {
		return "", ""
	}

	// M12: detect contract violation (attempted write/network outside /tmp).
	if isSandboxContractViolation(vr) {
		return "", errValidatorExceededContract
	}

	// Normal semantic rejection — return the captured stderr as the nudge.
	reason := strings.TrimSpace(vr.Stderr)
	if reason == "" {
		reason = strings.TrimSpace(vr.Stdout)
	}
	if reason == "" {
		reason = fmt.Sprintf("validator exited with code %d", vr.ExitCode)
	}
	return reason, ""
}

// buildDecideResult assembles a Result from a decide run outcome.
func buildDecideResult(stdout string, exitCode int, stderr, validatorOutputPath, sessionID, errMsg string) Result {
	res := Result{
		Data: map[string]any{
			"rationale": sourcecolor.Wrap(stdout),
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}
	if sessionID != "" {
		res.Data["claude_session_id"] = sessionID
	}

	if validatorOutputPath != "" {
		vBytes, vErr := kitsokimcp.ReadCapturedPayload(validatorOutputPath)
		if vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				res.Data["submitted"] = unescapeOverEscapedStrings(parsed)
			} else {
				if errMsg == "" {
					errMsg = fmt.Sprintf("host.oracle.decide: parse validator output: %v", jErr)
				}
			}
		}
	}

	if errMsg != "" {
		res.Error = errMsg
	} else if exitCode != 0 {
		res.Error = claudeExitErrorMessage(exitCode, stderr, stdout)
	}
	return res
}

// renderDecideNudge renders the validator-rejection nudge prompt, injecting
// the most recent rejection reason when present.
func renderDecideNudge(lastError string) string {
	block := ""
	if strings.TrimSpace(lastError) != "" {
		block = "\n\nThe last submission attempt was rejected:\n" + lastError
	}
	return strings.Replace(decideAbandonmentNudgeTemplate, "{{LAST_ERROR_BLOCK}}", block, 1)
}
