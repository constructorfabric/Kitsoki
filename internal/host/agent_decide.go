// Package host — host.agent.decide handler.
//
// host.agent.decide is the reasoning verdict verb: LLM judgment is required,
// schema: is mandatory, submit is auto-attached, and the read-only tool surface
// (same allowlist as host.agent.ask) is optional. It does not mutate anything.
//
// Contract:
//
//   - schema: required — the verdict must be typed. The handler auto-attaches the
//     kitsoki mcp-validator so the LLM must call submit() before exiting.
//   - Read-only tools: optional. The agent declares the subset (Read, Grep, Glob,
//     WebFetch, WebSearch, Bash under a profile, read-only MCP). Mutation tools
//     (Edit, Write) are rejected at call time as a safety net (loader rejects at
//     load time; this is the runtime double-check).
//   - validator: optional read-only post_cmd block. Runs under validator_sandbox.
//     Reject-with-reason triggers re-submit (same retry shape as ask_with_mcp).
//   - Streaming: funneled through AgentStreamer.Run so reasoning tokens and
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

	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// mutationTools is shared with agent_ask.go and now covers decide too.

// decideDefaultMaxRetries is the per-submission retry budget for the decide
// validator. Mirrors validatorDefaultMaxRetries in agent_ask_with_mcp.go.
const decideDefaultMaxRetries = 5

// decideMaxOuterIterations caps the outer `claude --resume` loop.
const decideMaxOuterIterations = 3

// decideToolBypassedKey is an internal Result.Data key set when a decide
// verdict was recovered from a fenced code block in stdout (the model skipped
// the submit() tool). emitDecideJournal reads it to annotate the trace + slog,
// then deletes it so it never binds into world. Leading underscore marks it
// internal — story bind: specs never reference it.
const decideToolBypassedKey = "_tool_bypassed"

// decideAbandonmentNudgeTemplate is the nudge sent on each --resume re-engagement.
const decideAbandonmentNudgeTemplate = `Your previous turn ended without successfully calling submit.{{LAST_ERROR_BLOCK}}

You MUST call the validator's submit tool with a valid JSON verdict to continue.
Review the schema, identify the correct verdict shape, and call submit. Do not
exit the conversation without submitting.`

// AgentDecideHandler implements host.agent.decide.
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
func AgentDecideHandler(ctx context.Context, args map[string]any) (Result, error) {
	if args == nil {
		args = map[string]any{}
	}

	// schema: is mandatory for decide.
	schemaArg, _ := args["schema"].(string)
	if strings.TrimSpace(schemaArg) == "" {
		return Result{Error: "host.agent.decide: schema: argument is required"}, nil
	}

	// Resolve prompt.
	rendered, errMsg := resolveDecidePrompt(ctx, args)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}

	// B-7: If an agent plugin registry is wired in context, route through
	// host.Dispatch (the Agent plugin interface) instead of the subprocess.
	withArgs, _ := args["with"].(map[string]any)
	var pluginSchemaJSON json.RawMessage
	if strings.TrimSpace(schemaArg) != "" {
		// Resolve the schema to its actual content so the plugin can use it for
		// grammar constraints and ValidateSubmission can compile it. Passing a
		// path string is not valid JSON Schema (causes "schema compilation failed").
		if schemaBytes, readErr := os.ReadFile(strings.TrimSpace(schemaArg)); readErr == nil {
			pluginSchemaJSON = json.RawMessage(schemaBytes)
		} else {
			pluginSchemaJSON = json.RawMessage(`"` + strings.TrimSpace(schemaArg) + `"`)
		}
	}
	if pluginRes, handled, pluginErr := TryDispatchVerb(ctx, "decide", rendered, "", agentNameFromArgs(args), "", withArgs, pluginSchemaJSON); handled {
		if pluginErr != nil {
			return Result{Error: pluginErr.Error()}, nil
		}
		return pluginRes, nil
	}

	// Resolve agent and validate tools.
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	if errMsg := rejectMutationTools(ctx, args, agent); errMsg != "" {
		return Result{Error: errMsg}, nil
	}

	bin, err := resolveAgentBin(ctx)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	workingDir, _ := args["working_dir"].(string)
	workingDir = appendDefaultCwd(workingDir, agent)

	// Build base CLI args.
	cliArgs := buildBaseCLIArgs(ctx, sysprompt.Decide, args, agent)

	// Resolve effective tools and apply Bash MCP rewrite if Bash is present.
	// For decide calls, Bash must have a BashProfile (enforced by the loader;
	// the runtime check below is a safety net).
	tools := effectiveTools(ctx, args, agent)
	hasBash, bashErrMsg := validateBashProfile("host.agent.decide", tools, agent)
	if bashErrMsg != "" {
		return Result{Error: bashErrMsg}, nil
	}
	if hasBash {
		tools = rewriteToolsForBashMCP(tools)
	}
	// Forward operator questions into kitsoki when a live surface is attached.
	var opAskCleanup func()
	cliArgs, tools, opAskCleanup, _ = attachOperatorAsk(ctx, cliArgs, tools)
	defer opAskCleanup()
	if len(tools) > 0 {
		cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	}

	// Parse optional validator block.
	vopts, vparseErr := parseValidatorOptions(args)
	if vparseErr != "" {
		return Result{Error: fmt.Sprintf("host.agent.decide: %s", vparseErr)}, nil
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
			return Result{Error: fmt.Sprintf("host.agent.decide: build bash MCP server: %v", bashErr)}, nil
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
			return Result{Error: fmt.Sprintf("host.agent.decide: create validator output tempfile: %v", ofErr)}, nil
		}
		validatorOutputPath = outFile.Name()
		_ = outFile.Close()
		// Remove so we can detect "validator never captured anything" via ErrNotExist.
		_ = os.Remove(validatorOutputPath)
		defer os.Remove(validatorOutputPath)

		if validatorBlockPresent {
			stFile, sfErr := os.CreateTemp("", "kitsoki-decide-validator-state-*.json")
			if sfErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.decide: create validator state tempfile: %v", sfErr)}, nil
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
			validatorEntry, vErr := buildValidatorMCPServer(ctx, schemaArg, validatorOutputPath, schemaOnlyOpts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.decide: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		} else {
			validatorEntry, vErr := buildValidatorMCPServer(ctx, schemaArg, validatorOutputPath, vopts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.decide: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		}
	}

	// Materialize mcp_servers into a temp config file.
	if len(mcpServers) > 0 {
		mcpConfigPath, cleanup, cfgErr := writeMCPConfigTempfile(mcpServers, "kitsoki-decide-mcp")
		if cfgErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.decide: %v", cfgErr)}, nil
		}
		defer cleanup()
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	// Capture pre-call context for journal recording.
	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call. The decide
	// retry loop runs several `claude --resume` sessions under this one call_id;
	// the accumulating TranscriptWriter folds them into one sidecar (live path).
	ctx = WithCallID(ctx, callID)
	systemPrompt := effectiveSystemPrompt(args, agent)

	// Build input descriptor for the journal.
	decideInputDesc := map[string]any{
		"schema_path": schemaArg,
	}

	// Wave 3-agent: write AgentCalled to the JSONL sink at dispatch time.
	decidePromptRef, _ := args["prompt_path"].(string)
	dOverlay, dDefaulted, dOverridden := promptTraceProvenance(ctx, decidePromptRef)
	appendAgentCalledEvent(ctx, callStart, callID, rendered, AgentCalledPayload{
		Verb:           "decide",
		Agent:          agentNameFromArgs(args),
		Model:          agent.Model,
		Input:          marshalInput(decideInputDesc),
		PromptOverlay:  dOverlay,
		SpecDefaulted:  dDefaulted,
		SpecOverridden: dOverridden,
	})

	// Always use the retry loop so the abandonment-nudge cycle fires for every
	// decide call, not just those with an explicit validator: block.
	// When validatorBlockPresent is false the loop uses the output-file
	// presence (not the state file) to detect a successful submit — see
	// runDecideWithValidatorRetryLoop.
	effectiveMaxRetries := vopts.MaxRetries
	if effectiveMaxRetries <= 0 {
		effectiveMaxRetries = decideDefaultMaxRetries
	}
	var sandboxOpts *validatorOptions
	if validatorBlockPresent {
		sandboxOpts = &vopts
	}
	// Resolve the schema to its raw JSON so the retry loop can compile it once
	// and validate any code-block-recovered verdict before trusting it (the
	// recovery path otherwise bypasses the mcp-validator's only schema check).
	var loopSchemaJSON json.RawMessage
	if schemaBytes, readErr := os.ReadFile(strings.TrimSpace(schemaArg)); readErr == nil {
		loopSchemaJSON = json.RawMessage(schemaBytes)
	}
	res := runDecideWithValidatorRetryLoop(ctx, decideLoopParams{
		Bin:                  bin,
		BaseCLIArgs:          cliArgs,
		Rendered:             rendered,
		WorkingDir:           workingDir,
		ValidatorOutputPath:  validatorOutputPath,
		ValidatorStatePath:   validatorStateFilePath,
		SchemaJSON:           loopSchemaJSON,
		MaxOuterIterations:   decideMaxOuterIterations,
		ValidatorMaxRetries:  effectiveMaxRetries,
		SandboxValidatorOpts: sandboxOpts,
	})
	durationMS := time.Since(callStart).Milliseconds()
	emitDecideJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
		systemPrompt, rendered, decideInputDesc, res)
	return res, nil
}

// emitDecideJournal writes the lean slog agent.decide.complete record and the
// full KindAgentCall journal entry after a decide call completes.
func emitDecideJournal(ctx context.Context, callID string, callStart time.Time, durationMS int64,
	agentName, model, systemPrompt, prompt string, inputDesc map[string]any, res Result) {

	// Detect (and strip) the internal bypass flag: the model returned its
	// verdict as a fenced code block instead of a submit() tool call, so it was
	// recovered from stdout. We record this in both the slog record and the
	// auditable trace, then delete the key so it never binds into world.
	toolBypassed := false
	if res.Data != nil {
		if b, ok := res.Data[decideToolBypassedKey].(bool); ok && b {
			toolBypassed = true
		}
		delete(res.Data, decideToolBypassedKey)
	}

	// Lean slog record.
	attrs := []any{
		"call_id", callID,
		"model", model,
		"duration_ms", durationMS,
	}
	if toolBypassed {
		attrs = append(attrs, "tool_bypassed", true)
	}
	if res.Error != "" {
		attrs = append(attrs, "error", res.Error)
	}
	slog.InfoContext(ctx, "agent.decide.complete", attrs...)

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

	callEnd := time.Now()
	if res.Error != "" {
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       "decide",
			Agent:      agentName,
			DurationMS: durationMS,
			Error:      res.Error,
		})
	} else {
		// On a tool bypass, annotate the trace's agent.call.complete Meta so
		// the deviation is auditable in the timeline. Merge into the usage box
		// (appendAgentReturnedEvent only defaults Meta when nil — setting it
		// here would otherwise drop token/cost usage from the trace).
		var meta map[string]any
		if toolBypassed {
			meta = agentUsageMeta(ctx)
			if meta == nil {
				meta = map[string]any{}
			}
			meta["tool_bypassed"] = true
			meta["verdict_recovered_from"] = "code_block"
		}
		appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
			Verb:       "decide",
			Agent:      agentName,
			Model:      model,
			DurationMS: durationMS,
			Response:   marshalResponse(responseDesc),
			Meta:       meta,
		})
	}
}

// resolveDecidePrompt renders the prompt for a decide call. Accepts either
// a `prompt:` inline string or a `prompt_path:` file reference. Returns
// (rendered, errorMsg). On success errorMsg is "".
func resolveDecidePrompt(ctx context.Context, args map[string]any) (string, string) {
	promptInline, _ := args["prompt"].(string)
	promptPath, _ := args["prompt_path"].(string)
	promptInline = strings.TrimSpace(promptInline)
	promptPath = strings.TrimSpace(promptPath)

	if promptInline != "" && promptPath != "" {
		return "", "host.agent.decide: prompt: and prompt_path: are mutually exclusive — set only one"
	}
	if promptInline == "" && promptPath == "" {
		return "", "host.agent.decide: prompt: or prompt_path: argument is required"
	}

	var raw string
	if promptPath != "" {
		// prompt_path: is an explicit file reference — a missing file is a
		// hard error (the author asserted a path).
		resolved := resolvePromptPathCtx(ctx, promptPath)
		b, err := readPromptFile(resolved)
		if err != nil {
			return "", fmt.Sprintf("host.agent.decide: read prompt %q: %v", resolved, err)
		}
		raw = string(b)
	} else {
		// prompt: is path-or-inline (mirrors resolveTaskContextPrompt): stories
		// commonly pass a file path here (e.g. a pre-rendered prompt path bound
		// from a prior host.run). Treat it as a file first; only when it does
		// not resolve to a readable prompt file do we fall back to using the
		// value as literal inline prompt text. Without this, a `prompt:` holding
		// a path was emitted verbatim (the path string) and the file's
		// `{{ args.context.* }}`/`{{ args.ticket }}` templates were never read
		// or rendered — the decide-vs-task background-render asymmetry.
		raw = promptInline
		if resolved := resolvePromptPathCtx(ctx, promptInline); resolved != "" {
			if b, err := readPromptFile(resolved); err == nil {
				raw = string(b)
			}
		}
	}

	// Render template args. Prefer explicit `args:` map; fall back to full call-args.
	templateArgs, _ := args["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = args
	}
	rendered, err := renderAndStripPrompt(ctx, raw, templateArgs)
	if err != nil {
		return "", fmt.Sprintf("host.agent.decide: render prompt: %v", err)
	}
	return rendered, ""
}

// rejectMutationTools checks that neither per-call tools nor agent tools contain
// mutation capabilities. Returns an error message if any mutation tool is found.
// This is the runtime safety net; the loader is the primary enforcement.
func rejectMutationTools(ctx context.Context, args map[string]any, agent Agent) string {
	tools := effectiveTools(ctx, args, agent)
	for _, t := range tools {
		if mutationTools[t] {
			return fmt.Sprintf("host.agent.decide: mutation tool %q is not permitted on decide calls; use host.agent.task for agentic work", t)
		}
	}
	return ""
}

// errValidatorExceededContract is the sentinel returned when RunValidatorSandboxed
// detects a write or network access attempt — i.e. the validator tried to mutate
// state outside /tmp: "validator exceeded read-only contract —
// migrate this call to host.agent.task".
const errValidatorExceededContract = "validator exceeded read-only contract — migrate this call to host.agent.task"

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
	// SchemaJSON is the raw JSON Schema the verdict must satisfy. When set, the
	// loop compiles it once and validates any verdict recovered from a fenced
	// code block (the submit-tool-bypass path) against it BEFORE writing the
	// output file — so an empty {} or schema-invalid object is rejected and
	// retried instead of silently accepted.
	SchemaJSON          json.RawMessage
	MaxOuterIterations  int
	ValidatorMaxRetries int
	// SandboxValidatorOpts, when non-nil and PostCmd is set, causes the loop to
	// run the post_cmd via RunValidatorSandboxed after each schema-passing
	// submission rather than delegating to mcp-validator's unsandboxed path.
	// C1 fix: this ensures decide.validator always executes under the sandbox.
	SandboxValidatorOpts *validatorOptions
}

// runDecideWithValidatorRetryLoop runs the decide call with the validator retry
// loop. Streams through AgentStreamer on all iterations (H2 fix: was using
// runClaudeOneShot on iter > 0, dropping streaming on retry round-trips).
// When SandboxValidatorOpts is set, post_cmd is run via RunValidatorSandboxed
// after each schema pass (C1 fix).
func runDecideWithValidatorRetryLoop(ctx context.Context, p decideLoopParams) Result {
	maxOuter := p.MaxOuterIterations
	if maxOuter <= 0 {
		maxOuter = decideMaxOuterIterations
	}

	sessionID := newUUID()

	// Compile the schema once so the code-block recovery path can validate a
	// bypassed verdict against it. If the schema is absent or fails to compile
	// the recovery path falls back to its prior (unvalidated) behaviour rather
	// than hard-failing the whole call.
	var compiledSchema *jsonschema.Schema
	if len(p.SchemaJSON) > 0 {
		if cs, cerr := kitsokimcp.CompileSchema(p.SchemaJSON); cerr == nil {
			compiledSchema = cs
		} else {
			slog.WarnContext(ctx, "agent.decide: schema compile failed; code-block recovery validation disabled", "err", cerr)
		}
	}

	var lastStdout string
	var lastExitCode int
	var lastStderr string
	var lastInfraErr error
	var sandboxLastRejection string
	// recoveryReject holds the schema-rejection reason for a code-block verdict
	// that failed validation, so the next --resume nudge can surface it.
	var recoveryReject string

	// Agent-action-transcript boundary events (proposal "decide submit → validate
	// → nudge cycle"). The accumulating tee in runClaudeStreamJSON already folds
	// every --resume session's verbatim claude events under this one call_id, but
	// the -p stream-json input prompt is NOT echoed back as an event, so the
	// host's nudge and the rejection reason inside it are otherwise invisible to
	// an operator reviewing the run. We interleave clearly-marked synthetic
	// "_kitsoki" rows at each outer-iteration boundary so the drawer renders the
	// full submit → reject → nudge → re-submit → accept arc. Offsets are
	// monotonic ms since the decide call started, matching the live tee's clock.
	transcriptW := TranscriptWriterFrom(ctx)
	transcriptCallID := CallIDFrom(ctx)
	loopStart := time.Now()
	// Share ONE call-start instant between the verbatim claude tee (each --resume
	// subprocess via runClaudeStreamJSON) and the synthetic rows below, so the
	// .timings sidecar is monotonic across all outer iterations instead of each
	// subprocess resetting its own clock to zero.
	ctx = WithCallStart(ctx, loopStart)
	synthOffsetMs := func() int64 { return time.Since(loopStart).Milliseconds() }
	appendSynth := func(payload any) {
		if transcriptW == nil || transcriptCallID == "" {
			return
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return
		}
		transcriptW.AppendSynthetic(transcriptCallID, claudeTranscriptFormat, json.RawMessage(b), synthOffsetMs())
	}

	for iter := 0; iter < maxOuter; iter++ {
		var cr ClaudeRun
		var runErr error
		var stdin string

		if iter == 0 {
			stdin = p.Rendered
			iterArgs := append(append([]string{}, p.BaseCLIArgs...), "--session-id", sessionID)
			var streamSID string
			cr, streamSID, runErr = AgentStreamer{
				Bin:        p.Bin,
				CLIArgs:    iterArgs,
				Stdin:      stdin,
				WorkingDir: p.WorkingDir,
			}.Run(ctx)
			if streamSID != "" {
				sessionID = streamSID
			}
		} else {
			// H2 fix: subsequent iterations also go through AgentStreamer so they
			// stream to any installed StreamSink, not just the first round-trip.
			_, _, stateLastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
			nudgeErr := stateLastErr
			rejectSource := "schema"
			if strings.TrimSpace(nudgeErr) == "" && strings.TrimSpace(recoveryReject) != "" {
				nudgeErr = recoveryReject
				rejectSource = "schema"
			}
			if strings.TrimSpace(nudgeErr) == "" && strings.TrimSpace(sandboxLastRejection) != "" {
				nudgeErr = sandboxLastRejection
				rejectSource = "semantic"
			}
			stdin = renderDecideNudge(nudgeErr)
			// Synthetic boundary: the rejection that triggered this --resume and
			// the host nudge it injected (which the raw -p stream never echoes).
			if strings.TrimSpace(nudgeErr) != "" {
				appendSynth(map[string]any{
					"_kitsoki": "validator_reject",
					"source":   rejectSource,
					"reason":   nudgeErr,
				})
			}
			appendSynth(map[string]any{
				"_kitsoki":   "nudge",
				"outer_iter": iter,
				"text":       stdin,
			})
			iterArgs := append(append([]string{}, p.BaseCLIArgs...), "--resume", sessionID)
			var streamSID string
			cr, streamSID, runErr = AgentStreamer{
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

		// When there is no validator state file (no validator: block declared),
		// detect success by checking the output file directly — the validator
		// MCP server writes it when submit() is called regardless of whether a
		// post_cmd is configured. Without this branch, ReadStateFile("") always
		// returns (0,0,"") → outcomeFromState always returns Abandoned → the
		// loop could never detect that submit succeeded.
		if p.ValidatorStatePath == "" {
			if data, _ := kitsokimcp.ReadCapturedPayload(p.ValidatorOutputPath); len(data) > 0 {
				appendSynth(map[string]any{"_kitsoki": "validator_accept", "outer_iter": iter})
				return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, "")
			}
			// Output file still empty — model didn't call submit.
			// Before nudging, check whether the model wrote valid JSON in a
			// markdown code block. If so, treat it as a successful submit so
			// we don't burn an outer iteration on something recoverable.
			if extracted := extractJSONFromCodeBlock(lastStdout); extracted != nil {
				// The validator MCP subprocess is normally the ONLY place schema
				// validation runs. This recovery path writes straight to the
				// output file, bypassing it — so re-run the identical schema check
				// HERE before trusting the payload. An empty {} or a verdict missing
				// required fields must be rejected (nudge + retry), never silently
				// written and accepted as a recovered verdict.
				if compiledSchema != nil {
					if verr := compiledSchema.Validate(extracted); verr != nil {
						recoveryReject = kitsokimcp.FormatValidationError(verr)
						slog.WarnContext(ctx, "agent.decide: recovered code-block verdict failed schema validation; nudging for a valid submit", "err", recoveryReject)
						// Do NOT write the output file; continue so the model gets
						// another attempt. If the outer budget exhausts, the tail
						// path returns a non-empty Error → routes to on_error.
						continue
					}
				}
				b, _ := json.Marshal(extracted)
				_ = os.WriteFile(p.ValidatorOutputPath, b, 0o600)
				slog.WarnContext(ctx, "agent.decide: model bypassed submit tool — recovered verdict from code block in stdout; consider adding validator: to enforce tool use")
				res := buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, "")
				// Flag the bypass so emitDecideJournal records it in the trace
				// (and slog). This is WHY a raw ```json block can reach the
				// operator's chat: the verdict came back as narration text, not
				// a clean submit() tool call. Internal-only key — stripped
				// before the Result binds into world (see emitDecideJournal).
				if res.Data != nil {
					res.Data[decideToolBypassedKey] = true
				}
				// Mirror the tool-bypass deviation into the transcript as a banner
				// row (in addition to the trace Meta emitDecideJournal sets), so the
				// drawer shows WHY the verdict arrived as narration text, not a clean
				// submit() tool call. Followed by an accept — the verdict was recovered.
				appendSynth(map[string]any{
					"_kitsoki":               "tool_bypassed",
					"verdict_recovered_from": "code_block",
				})
				appendSynth(map[string]any{"_kitsoki": "validator_accept", "outer_iter": iter})
				return res
			}
			// Nothing recoverable — nudge and retry.
			continue
		}

		attempts, success, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
		switch outcomeFromState(attempts, success, p.ValidatorMaxRetries) {
		case mcpOutcomeSuccess:
			// Schema passed. If a sandboxed post_cmd is configured, run it now.
			if p.SandboxValidatorOpts != nil && strings.TrimSpace(p.SandboxValidatorOpts.PostCmd) != "" {
				rejection, contractErr, infraErr := runDecideSandboxValidator(ctx, p.ValidatorOutputPath, p.SandboxValidatorOpts)
				if contractErr != "" {
					// M12: mutation/network attempt detected.
					return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, contractErr)
				}
				if infraErr != "" {
					// The verifier could not start / never ran its check. This is
					// NOT a retryable semantic rejection — nudging the model cannot
					// fix a missing interpreter or dropped import root. Return a hard
					// Result.Error immediately. The schema-valid captured payload is
					// still surfaced via buildDecideResult so downstream sees it.
					return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID,
						fmt.Sprintf("post_cmd validator failed to start: %s", infraErr))
				}
				if rejection != "" {
					// Semantic rejection — nudge and retry.
					sandboxLastRejection = rejection
					continue
				}
			}
			appendSynth(map[string]any{"_kitsoki": "validator_accept", "outer_iter": iter})
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
		msg := fmt.Sprintf("host.agent.decide: claude exec failed: %v", lastInfraErr)
		if s := strings.TrimSpace(lastStderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}
	}

	attempts, _, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
	msg := lastErr
	if strings.TrimSpace(msg) == "" {
		if strings.TrimSpace(sandboxLastRejection) != "" {
			// The model DID submit and a schema-valid payload WAS captured; the
			// post_cmd gate kept semantically rejecting it until the outer budget
			// ran out. Report the actual cause (the verifier's last rejection)
			// instead of the misleading "abandoned without successful submit",
			// which describes a missing submit that never happened here.
			// buildDecideResult still surfaces the captured payload at
			// validatorOutputPath as `submitted`.
			msg = fmt.Sprintf("post_cmd validator rejected after %d outer iteration(s): %s", maxOuter, strings.TrimSpace(sandboxLastRejection))
		} else {
			msg = fmt.Sprintf("validator: session abandoned without successful submit after %d outer iteration(s), %d attempt(s)", maxOuter, attempts)
		}
	}
	return buildDecideResult(lastStdout, lastExitCode, lastStderr, p.ValidatorOutputPath, sessionID, msg)
}

// runDecideSandboxValidator reads the captured payload from outputPath and
// runs the configured post_cmd via RunValidatorSandboxed.
//
// Returns three independent channels so the retry loop can tell a retryable
// semantic verdict apart from a non-retryable failure:
//   - ("", "", "")                 — sandbox accepted (zero exit, no violation)
//   - (rejectionMsg, "", "")       — sandbox rejected (semantic non-zero exit); caller nudges LLM
//   - ("", contractErrMsg, "")     — sandbox detected mutation/network: M12 sentinel (hard error)
//   - ("", "", infraErrMsg)        — verifier could not start / never ran its check (hard error)
//
// The infra channel is the fix for the "captured submit reported abandoned" bug:
// an un-runnable verifier (argv0 missing, import root dropped, sandbox setup
// failed) is NOT a semantic verdict — nudging the model cannot fix it. Folding
// it into `rejection` made the loop retry to exhaustion and discard the already
// schema-valid captured payload. Keeping it separate lets the caller return a
// hard Result.Error immediately.
func runDecideSandboxValidator(ctx context.Context, validatorOutputPath string, opts *validatorOptions) (rejection, contractErr, infraErr string) {
	if opts == nil || strings.TrimSpace(opts.PostCmd) == "" {
		return "", "", ""
	}

	// Read the submitted payload so the validator can inspect it.
	payload, err := kitsokimcp.ReadCapturedPayload(validatorOutputPath)
	if err != nil || len(payload) == 0 {
		// No payload captured yet — schema-only submission hasn't landed.
		return "validator: no schema-validated payload captured yet", "", ""
	}

	parts := strings.Fields(opts.PostCmd)
	if len(parts) == 0 {
		return "validator: post_cmd is empty", "", ""
	}
	argv := append([]string(nil), parts[1:]...)
	for _, kv := range opts.PostCmdArgs {
		argv = append(argv, "--"+kv.Key, kv.Value)
	}

	// Resolve the declared working directory so a verifier importable only from
	// its project root (e.g. `python3 -m bugfix` under tools/loopy) can find its
	// module. Mirrors the legacy ask_with_mcp path (agent_ask_with_mcp.go:215);
	// the decide path lost this in the agent-split. The write-sandbox profile
	// stays scoped to the scratch dir, so the verifier may READ this root but
	// still cannot write outside scratch.
	var cwd string
	if strings.TrimSpace(opts.PostCmdCwd) != "" {
		cwd = resolvePromptPathCtx(ctx, opts.PostCmdCwd)
	}

	vr, runErr := RunValidatorSandboxed(ctx, ValidatorSandboxOptions{
		Cmd:   parts[0],
		Args:  argv,
		Stdin: string(payload),
		Cwd:   cwd,
	})
	if runErr != nil {
		// Infrastructure error — the subprocess could not start or the sandbox
		// failed to set up. Surface on the dedicated infra channel (non-retryable).
		return "", "", fmt.Sprintf("validator sandbox: infrastructure error: %v", runErr)
	}

	if vr.ExitCode == 0 {
		return "", "", ""
	}

	// M12: detect contract violation (attempted write/network outside /tmp).
	if isSandboxContractViolation(vr) {
		return "", errValidatorExceededContract, ""
	}

	// A non-zero exit whose output matches an exec-failure pattern means the
	// verifier never actually ran its check — the sandbox wrapper reports the
	// failure (e.g. `execvp() ... No such file or directory` from sandbox-exec,
	// or `No module named <argv0>` from python). Classify as infra, not a verdict.
	if isSandboxExecFailure(vr) {
		reason := strings.TrimSpace(vr.Stderr)
		if reason == "" {
			reason = strings.TrimSpace(vr.Stdout)
		}
		if reason == "" {
			reason = fmt.Sprintf("verifier exited %d without running", vr.ExitCode)
		}
		return "", "", reason
	}

	// Normal semantic rejection — return the captured stderr as the nudge.
	reason := strings.TrimSpace(vr.Stderr)
	if reason == "" {
		reason = strings.TrimSpace(vr.Stdout)
	}
	if reason == "" {
		reason = fmt.Sprintf("validator exited with code %d", vr.ExitCode)
	}
	return reason, "", ""
}

// isSandboxExecFailure reports whether a non-zero sandbox result reflects the
// verifier failing to start/exec rather than running its check and emitting a
// genuine verdict. These strings come from the loader/interpreter (execvp,
// missing module, command-not-found), so a match means "could not run", which
// must route to the infra channel, not the retryable rejection channel.
func isSandboxExecFailure(vr ValidatorResult) bool {
	combined := strings.ToLower(vr.Stderr + "\n" + vr.Stdout)
	for _, signal := range []string{
		"execvp(",
		"no such file or directory",
		"no module named",
		"command not found",
		"executable file not found",
	} {
		if strings.Contains(combined, signal) {
			return true
		}
	}
	return false
}

// buildDecideResult assembles a Result from a decide run outcome.
//
// Schema validation of a captured payload is NOT re-run here: the normal
// validator-MCP path has already enforced the schema in its subprocess, and the
// code-block recovery path validates its extracted verdict against the compiled
// schema BEFORE writing the output file (see runDecideWithValidatorRetryLoop).
// Re-validating here would double-check the MCP path against a schema the test
// harness's fake validator does not itself enforce, rejecting payloads the
// validator already accepted — so the only schema gate stays at the write sites.
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

	submittedCaptured := false
	if validatorOutputPath != "" {
		vBytes, vErr := kitsokimcp.ReadCapturedPayload(validatorOutputPath)
		if vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				res.Data["submitted"] = unescapeOverEscapedStrings(parsed)
				submittedCaptured = true
			} else {
				if errMsg == "" {
					errMsg = fmt.Sprintf("host.agent.decide: parse validator output: %v", jErr)
				}
			}
		}
	}

	if errMsg != "" {
		res.Error = errMsg
	} else if exitCode != 0 {
		res.Error = claudeExitErrorMessage(exitCode, stderr, stdout)
	} else if validatorOutputPath != "" && !submittedCaptured {
		// The model exited cleanly (code 0) but never called the submit tool.
		// Return an error so on_error: arcs can route gracefully rather than
		// silently leaving bind targets empty.
		res.Error = "host.agent.decide: model exited without calling submit — no verdict captured"
	}
	return res
}

// extractJSONFromCodeBlock tries to recover a JSON verdict from the model's
// text output when it wrote ```json ... ``` instead of calling submit().
// Strips sourcecolor sentinels before scanning. Returns nil when no valid
// JSON block is found.
func extractJSONFromCodeBlock(text string) any {
	// Strip invisible sentinel characters (U+2061–U+2063) that sourcecolor
	// uses; they appear when the model output was already wrapped elsewhere.
	text = sourcecolor.Strip(text)

	for _, fence := range []string{"```json", "```"} {
		start := strings.Index(text, fence)
		if start < 0 {
			continue
		}
		inner := text[start+len(fence):]
		end := strings.Index(inner, "```")
		if end < 0 {
			continue
		}
		candidate := strings.TrimSpace(inner[:end])
		var parsed any
		if err := json.Unmarshal([]byte(candidate), &parsed); err == nil {
			return parsed
		}
	}
	return nil
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
