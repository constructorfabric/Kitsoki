// Package host — host.agent.extract handler.
//
// Implements the tiered resolver. See docs/guide/agents/cli.md.
// Three resolver tiers are tried in declaration order; the first to produce a
// schema-valid payload returns it:
//
//  1. synonyms — author-curated phrase → typed payload (YAML file, load-time
//     validated against the schema). Delegates to the existing semroute
//     Matcher so synonym indexing, stemming, and template matching reuse the
//     same battle-tested logic as transport-level routing.
//
//  2. slot_template — slot-grammar parse via semroute.Matcher templates.
//     Currently shares the semroute code path; the resolver is exposed as a
//     distinct tier so authors can declare ONLY template-style resolvers without
//     needing a full synonym synonym file.
//
//  3. llm — LLM fallback with the same read-only tool surface as decide/ask.
//     Streams tokens through AgentStreamer; honours agent.Tools (D5).
//
// Optional validator: runs after any tier match in a read-only sandbox (same
// ValidatorSandbox as decide.validator). Rejection from a deterministic tier
// falls through to the next tier; rejection from the LLM tier counts against
// the LLM retry budget.
//
// Read-tool snapshot cap (256 KiB): the LLM tier enforces this cap on any
// read-tool output captured for the journal. Over-cap outputs are stored as
// sha256 hash + first 4 KiB (D9).
//
// Returns:
//
//	submitted   any    — the typed payload (nil on no-match)
//	resolved_by string — "synonyms" | "slot_template" | "llm" | "no_match"
//	claude_session_id string — populated when the llm tier matched
package host

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"
)

// ReadSnapshotSummary returns (verbatim, sha256hex, over_cap) for output, using
// the shared ReadSnapshotCap from read_snapshot.go (D9). When len(output) <=
// the cap the original string is returned and over_cap is false. When over_cap
// is true the first ReadSnapshotPrefix bytes are returned as verbatim and
// sha256hex holds the hex-encoded digest of the full original for divergence
// detection.
func ReadSnapshotSummary(output string) (verbatim, sha256hex string, overCap bool) {
	if len(output) <= ReadSnapshotCap {
		return output, "", false
	}
	sum := sha256.Sum256([]byte(output))
	head := output
	if len(head) > ReadSnapshotPrefix {
		head = head[:ReadSnapshotPrefix]
	}
	return head, fmt.Sprintf("%x", sum), true
}

// extractResolverKind names the four possible resolved_by values.
const (
	resolvedBySynonyms     = "synonyms"
	resolvedBySlotTemplate = "slot_template"
	resolvedByLLM          = "llm"
	resolvedByNoMatch      = "no_match"
)

// ResolvedByNoMatch returns the no_match resolved_by value. Exported so
// callers outside this package (e.g. the orchestrator) can compare without
// an import cycle. Use this instead of the unexported constant.
func ResolvedByNoMatch() string { return resolvedByNoMatch }

// ExtractResolverDef is one entry in the resolvers: list. Exactly one of
// SynonymsPath, SlotTemplatePath, or LLMConfig is set.
type ExtractResolverDef struct {
	SynonymsPath     string
	SlotTemplatePath string
	LLMConfig        *ExtractLLMConfig
}

// ExtractLLMConfig is the llm: block inside a resolver entry.
type ExtractLLMConfig struct {
	PromptPath string
	AgentName  string
}

// ExtractValidatorDef is the optional validator: block on host.agent.extract.
type ExtractValidatorDef struct {
	PostCmd     string
	PostCmdArgs map[string]any
}

// ExtractArgs is the parsed args for an AgentExtractHandler call.
type ExtractArgs struct {
	Input      string
	SchemaPath string
	Resolvers  []ExtractResolverDef
	Validator  *ExtractValidatorDef
	WorkingDir string
	AgentName  string
	PromptPath string
}

// parseExtractArgs converts the raw map[string]any from the effect's with:
// block into a typed ExtractArgs. Returns an error string on bad input.
func parseExtractArgs(args map[string]any) (ExtractArgs, string) {
	var ea ExtractArgs

	ea.Input, _ = args["input"].(string)

	ea.SchemaPath, _ = args["schema"].(string)
	if ea.SchemaPath == "" {
		return ea, "host.agent.extract: schema argument is required"
	}

	ea.WorkingDir, _ = args["working_dir"].(string)
	ea.AgentName, _ = args["agent"].(string)
	ea.PromptPath, _ = args["prompt"].(string)

	// Parse resolvers list.
	rawResolvers, _ := args["resolvers"].([]any)
	if len(rawResolvers) == 0 {
		// No resolvers — treat as a single LLM resolver using the top-level prompt/agent.
		if ea.PromptPath != "" || ea.AgentName != "" {
			ea.Resolvers = []ExtractResolverDef{{
				LLMConfig: &ExtractLLMConfig{
					PromptPath: ea.PromptPath,
					AgentName:  ea.AgentName,
				},
			}}
		}
	} else {
		for _, raw := range rawResolvers {
			rm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			var rd ExtractResolverDef
			if sp, _ := rm["synonyms"].(string); sp != "" {
				rd.SynonymsPath = sp
			} else if stp, _ := rm["slot_template"].(string); stp != "" {
				rd.SlotTemplatePath = stp
			} else if llmRaw, ok := rm["llm"]; ok {
				llmMap, _ := llmRaw.(map[string]any)
				lc := &ExtractLLMConfig{}
				if llmMap != nil {
					lc.PromptPath, _ = llmMap["prompt"].(string)
					lc.AgentName, _ = llmMap["agent"].(string)
				}
				rd.LLMConfig = lc
			} else {
				continue
			}
			ea.Resolvers = append(ea.Resolvers, rd)
		}
	}

	// Parse validator block.
	if rawVal, ok := args["validator"]; ok && rawVal != nil {
		vm, _ := rawVal.(map[string]any)
		if vm != nil {
			if cmd, _ := vm["post_cmd"].(string); cmd != "" {
				vd := &ExtractValidatorDef{PostCmd: cmd}
				if cmdArgs, _ := vm["post_cmd_args"].(map[string]any); cmdArgs != nil {
					vd.PostCmdArgs = cmdArgs
				}
				ea.Validator = vd
			}
		}
	}

	return ea, ""
}

// AgentExtractHandler implements host.agent.extract. It runs the tiered
// resolver (synonyms → slot_template → llm) in declaration order and returns
// the first schema-valid payload. An optional read-only validator runs after
// each successful tier.
//
// Required args:
//   - input  (string): the free text to extract a typed value from.
//   - schema (string): path to a JSON Schema file (applied to every tier).
//
// Optional args:
//   - resolvers ([]any): ordered resolver list; each entry is one of:
//     { synonyms: <path> }, { slot_template: <path> }, { llm: { prompt, agent } }
//   - validator (map): { post_cmd, post_cmd_args } — read-only sandbox validator.
//   - working_dir (string): cwd for the LLM tier.
//   - agent (string): fallback agent name when no llm: block sets one.
//   - prompt (string): fallback prompt path when no llm: block sets one.
//
// Returns Result.Data with:
//   - submitted    (any)    — the resolved payload (nil on no-match).
//   - resolved_by  (string) — "synonyms"|"slot_template"|"llm"|"no_match".
//   - claude_session_id (string) — only present on llm-tier hits.
func AgentExtractHandler(ctx context.Context, args map[string]any) (Result, error) {
	ea, errMsg := parseExtractArgs(args)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}

	// B-7: If an agent plugin registry is wired in context, route through
	// host.Dispatch. For extract the rendered "prompt" is the input text.
	withArgs, _ := args["with"].(map[string]any)
	var pluginSchemaJSON json.RawMessage
	if ea.SchemaPath != "" {
		pluginSchemaJSON = json.RawMessage(`"` + ea.SchemaPath + `"`)
	}
	if pluginRes, handled, pluginErr := TryDispatchVerb(ctx, "extract", ea.Input, "", ea.AgentName, "", withArgs, pluginSchemaJSON); handled {
		if pluginErr != nil {
			return Result{Error: pluginErr.Error()}, nil
		}
		return pluginRes, nil
	}

	bin, err := resolveAgentBin(ctx)
	if err != nil {
		// No claude binary — deterministic tiers can still function; LLM tier
		// will fail gracefully later.
		_ = err
	}

	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	// Wave 3-agent: write AgentCalled to the JSONL sink at dispatch time.
	appendAgentCalledEvent(ctx, callStart, callID, ea.Input, AgentCalledPayload{
		Verb:  "extract",
		Agent: agentNameFromArgs(args),
		Input: marshalInput(map[string]any{"schema": ea.SchemaPath, "input": ea.Input}),
	})

	res, runErr := runExtract(ctx, ea, bin, args)
	durationMS := time.Since(callStart).Milliseconds()

	// Only emit journal entries for calls that hit the LLM tier (or all calls
	// for tracing purposes). We always emit so runstatus can show the routing
	// decision.
	agentName := agentNameFromArgs(args)
	agent, _ := resolveAgent(ctx, map[string]any{"agent": agentName})

	// Build response descriptor.
	var responseDesc map[string]any
	if res.Data != nil {
		responseDesc = map[string]any{}
		if v, ok := res.Data["submitted"]; ok {
			responseDesc["extracted"] = v
			responseDesc["json"] = v
		}
		if v, ok := res.Data["resolved_by"]; ok {
			responseDesc["resolved_by"] = v
		}
	}

	errStr := ""
	if runErr != nil {
		errStr = agentRunErrorMessage("extract", runErr, "")
	}
	if res.Error != "" {
		errStr = res.Error
	}

	// Lean slog.
	slogAttrs := []any{
		"call_id", callID,
		"model", agent.Model,
		"duration_ms", durationMS,
	}
	if errStr != "" {
		slogAttrs = append(slogAttrs, "error", errStr)
	}
	slog.InfoContext(ctx, "agent.extract.complete", slogAttrs...)

	callEnd := time.Now()
	if errStr != "" {
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       "extract",
			Agent:      agentName,
			DurationMS: durationMS,
			Error:      errStr,
		})
	} else {
		appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
			Verb:       "extract",
			Agent:      agentName,
			Model:      agent.Model,
			DurationMS: durationMS,
			Response:   marshalResponse(responseDesc),
		})
	}

	if runErr != nil {
		res.Error = errStr
		res.FailureKind = FailureInfra
		return res, nil
	}
	return res, nil
}

// runExtract is the implementation extracted so tests can call it directly
// with pre-parsed args.
func runExtract(ctx context.Context, ea ExtractArgs, bin string, rawArgs map[string]any) (Result, error) {
	if len(ea.Resolvers) == 0 {
		return Result{
			Data: map[string]any{
				"submitted":   nil,
				"resolved_by": resolvedByNoMatch,
			},
			Error: "host.agent.extract: no resolvers declared and no match",
		}, nil
	}

	// Read-only contract safety net: reject any LLM-tier agent whose tools
	// include a mutation tool before we attempt any resolver. Same contract as
	// ask / decide (precedence rule D5; the read-only contract invariant).
	agents := AgentsFromContext(ctx)
	for _, rd := range ea.Resolvers {
		if rd.LLMConfig == nil || rd.LLMConfig.AgentName == "" {
			continue
		}
		agent, ok := agents[rd.LLMConfig.AgentName]
		if !ok {
			continue
		}
		mergedArgs := map[string]any{"agent": rd.LLMConfig.AgentName}
		if perCallTools := stringSliceArg(rawArgs, "tools"); len(perCallTools) > 0 {
			mergedArgs["tools"] = perCallTools
		}
		for _, t := range effectiveTools(ctx, mergedArgs, agent) {
			if fileMutationTools[t] {
				return Result{Error: fmt.Sprintf(
					"host.agent.extract: mutation tool %q not permitted in the LLM tier", t)}, nil
			}
		}
	}

	for _, rd := range ea.Resolvers {
		var payload any
		var kind string
		var sessionID string

		switch {
		case rd.SynonymsPath != "":
			p, ok, runErr := trySynonymsResolver(ctx, ea.Input, rd.SynonymsPath, ea.SchemaPath)
			if runErr != nil {
				slog.WarnContext(ctx, "extract.resolver.synonyms",
					slog.String("path", rd.SynonymsPath),
					slog.String("err", runErr.Error()))
				continue
			}
			if !ok {
				continue
			}
			payload = p
			kind = resolvedBySynonyms

		case rd.SlotTemplatePath != "":
			p, ok, runErr := trySlotTemplateResolver(ctx, ea.Input, rd.SlotTemplatePath, ea.SchemaPath)
			if runErr != nil {
				slog.WarnContext(ctx, "extract.resolver.slot_template",
					slog.String("path", rd.SlotTemplatePath),
					slog.String("err", runErr.Error()))
				continue
			}
			if !ok {
				continue
			}
			payload = p
			kind = resolvedBySlotTemplate

		case rd.LLMConfig != nil:
			if bin == "" {
				// No binary available; skip LLM tier.
				slog.WarnContext(ctx, "extract.resolver.llm", slog.String("reason", "claude binary not found"))
				continue
			}
			p, sid, ok, runErr := tryLLMResolver(ctx, ea, rd.LLMConfig, bin, rawArgs)
			if runErr != nil {
				slog.WarnContext(ctx, "extract.resolver.llm", slog.String("err", runErr.Error()))
				continue
			}
			if !ok {
				continue
			}
			payload = p
			kind = resolvedByLLM
			sessionID = sid
		}

		if payload == nil && kind == "" {
			continue
		}

		// Schema validation of the tier output (runtime safety net).
		if ea.SchemaPath != "" {
			if !validateExtractPayload(ctx, ea.SchemaPath, payload) {
				// Deterministic tier output failed schema — fall through.
				slog.WarnContext(ctx, "extract.resolver.schema_fail",
					slog.String("tier", kind),
					slog.String("schema", ea.SchemaPath))
				continue
			}
		}

		// Optional validator.
		if ea.Validator != nil {
			accepted, valErr := runExtractValidator(ctx, ea.Validator, payload)
			if valErr != nil {
				slog.WarnContext(ctx, "extract.validator.error", slog.String("err", valErr.Error()))
			}
			if !accepted {
				// Validator rejected. For deterministic tiers, fall through to
				// the next resolver. For LLM tier this counts against the retry
				// budget (currently budget = 1 attempt; no retry loop in Phase 5).
				slog.WarnContext(ctx, "extract.validator.rejected", slog.String("tier", kind))
				continue
			}
		}

		// Emit a synthetic journal event so the TUI gets a visible signal.
		emitExtractResolverMatched(ctx, kind, ea.Input)

		data := map[string]any{
			"submitted":   payload,
			"resolved_by": kind,
		}
		if kind == resolvedByLLM {
			data["claude_session_id"] = sessionID
		}
		return Result{Data: data}, nil
	}

	// All tiers declined.
	emitExtractResolverMatched(ctx, resolvedByNoMatch, ea.Input)
	return Result{
		Data: map[string]any{
			"submitted":   nil,
			"resolved_by": resolvedByNoMatch,
		},
		Error: "host.agent.extract: no resolver matched",
	}, nil
}

// trySynonymsResolver attempts to match input against a synonyms YAML file.
// The file format is a YAML map from phrase to typed payload. Each key is a
// phrase (or comma-separated list of phrases); the value is the typed payload.
//
// When a compiled semroute Matcher is injected into ctx via WithExtractMatcher,
// the YAML file path is used as an opaque identifier (the in-process Matcher
// takes precedence and returns a Verdict-derived payload). This is the
// transport-routing seam: the orchestrator injects its compiled matcher and the
// extract handler uses it instead of re-loading the YAML.
//
// Returns (payload, true, nil) on a hit, (nil, false, nil) on a miss, and
// (nil, false, err) on a load/parse error.
func trySynonymsResolver(ctx context.Context, input, path, _ string) (any, bool, error) {
	// If there is an injected Matcher in ctx, prefer it (transport-routing path).
	if mc := extractMatcherFromContext(ctx); mc != nil {
		verdict, ok, matchErr := tryMatcherSynonyms(ctx, mc, input)
		if matchErr != nil {
			return nil, false, matchErr
		}
		if !ok {
			return nil, false, nil
		}
		// Encode the verdict as the payload. Transport callers translate this back.
		payload := map[string]any{
			"_semroute_verdict": verdict,
		}
		return payload, true, nil
	}

	resolved := resolvePromptPath(path)
	entries, err := loadSynonymFile(resolved)
	if err != nil {
		return nil, false, fmt.Errorf("extract.synonyms: load %q: %w", resolved, err)
	}

	normalised := normaliseSynonymInput(input)
	for _, entry := range entries {
		for _, phrase := range entry.Phrases {
			if normaliseSynonymInput(phrase) == normalised {
				return entry.Payload, true, nil
			}
		}
	}
	return nil, false, nil
}

// trySlotTemplateResolver attempts to match input against a slot-template file.
// Currently backed by the same synonym infrastructure (template synonyms in a
// YAML file). Returns (payload, true, nil) on a hit.
func trySlotTemplateResolver(ctx context.Context, input, path, schemaPath string) (any, bool, error) {
	// For Phase 5 the slot_template resolver re-uses the synonym loader —
	// slot templates are authored as synonyms with {slot} captures. A future
	// phase may provide a dedicated template grammar compiler; the resolver
	// interface isolates the change.
	return trySynonymsResolver(ctx, input, path, schemaPath)
}

// extractLLMMaxRetries is the retry budget for the LLM extraction tier when
// the submit payload fails schema validation. Up to 3 attempts total (1
// initial + 2 retries).
const extractLLMMaxRetries = 3

// tryLLMResolver invokes the claude LLM to extract a typed payload from input.
//
// The LLM tier always attaches the kitsoki mcp-validator with the extraction
// schema so the LLM must call submit() rather than relying on stdout JSON
// parsing (M5). When submit produces a schema-invalid payload the handler
// retries up to extractLLMMaxRetries total attempts (L8).
//
// Returns (payload, sessionID, true, nil) on a successful extraction,
// (nil, "", false, nil) when every attempt fails or produces no valid payload,
// and (nil, "", false, err) on an infrastructure failure.
func tryLLMResolver(ctx context.Context, ea ExtractArgs, lc *ExtractLLMConfig, bin string, rawArgs map[string]any) (any, string, bool, error) {
	var rendered string
	if lc.PromptPath != "" {
		resolved := resolvePromptPath(lc.PromptPath)
		rawPrompt, readErr := readFileForExtract(resolved)
		if readErr != nil {
			return nil, "", false, fmt.Errorf("extract.llm: read prompt %q: %w", resolved, readErr)
		}
		rendered = rawPrompt
	} else {
		// Default prompt: include schema bytes so the LLM knows the required shape.
		schemaBytes, schemaReadErr := osReadFileForExtract(resolvePromptPath(ea.SchemaPath))
		if schemaReadErr != nil {
			// Soft: fall back to a prompt without schema bytes. Log and continue.
			slog.WarnContext(ctx, "extract.llm.default_prompt",
				slog.String("err", "read schema for default prompt: "+schemaReadErr.Error()))
			rendered = fmt.Sprintf(
				"Extract a structured JSON object from the following input matching the declared schema.\nCall the submit tool with the typed JSON.\n\nInput: %s",
				ea.Input)
		} else {
			rendered = fmt.Sprintf(
				"Extract a structured JSON object from the following input matching this schema:\n%s\n\nCall the submit tool with the typed JSON.\n\nInput: %s",
				string(schemaBytes), ea.Input)
		}
	}

	rendered = sourcecolor.Strip(rendered)

	agentName := lc.AgentName
	if agentName == "" {
		agentName = ea.AgentName
	}

	agent, _ := resolveAgent(ctx, map[string]any{"agent": agentName})
	// Provider selection for extract rides on the resolved agent's Provider
	// (extract has no effect-level with: args threaded into this helper).
	ctx, agent = applyProvider(ctx, map[string]any{}, agent)

	cliArgs := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
	}
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, sysprompt.Extract,
		effectiveSystemPrompt(map[string]any{}, agent), agent.InheritClaudeDefault)
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}

	// Per-call tools override wins (D5). Use type-safe accessor (L9).
	mergedArgs := map[string]any{"agent": agentName}
	if perCallTools := stringSliceArg(rawArgs, "tools"); len(perCallTools) > 0 {
		mergedArgs["tools"] = perCallTools
	}
	if tools := effectiveTools(ctx, mergedArgs, agent); len(tools) > 0 {
		cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	}

	workingDir := ea.WorkingDir
	workingDir = appendDefaultCwd(workingDir, agent)

	// Attach mcp-validator with the extraction schema (M5). The LLM must call
	// submit(); the validator writes the parsed payload to a tempfile. The schema
	// path is resolved inside buildValidatorMCPServer through the per-call prompt
	// renderer (story-dir isolated per concurrent session), not the process-global
	// KITSOKI_APP_DIR — see that function's doc and the concurrent-session bug.
	submitFile, sfErr := os.CreateTemp("", "kitsoki-extract-submit-*.json")
	if sfErr != nil {
		return nil, "", false, fmt.Errorf("extract.llm: create submit tempfile: %w", sfErr)
	}
	submitFile.Close()
	submittedOutputPath := submitFile.Name()
	// Pre-remove so we can detect "validator never wrote anything" via ErrNotExist.
	_ = os.Remove(submittedOutputPath)
	defer func() { _ = os.Remove(submittedOutputPath) }()

	validatorEntry, vErr := buildValidatorMCPServer(ctx, ea.SchemaPath, submittedOutputPath, validatorOptions{MaxRetries: extractLLMMaxRetries})
	if vErr != nil {
		return nil, "", false, fmt.Errorf("extract.llm: build validator MCP server: %w", vErr)
	}
	mcpConfigPath, mcpCleanup, mcpCfgErr := writeMCPConfigTempfile(map[string]any{"validator": validatorEntry}, "kitsoki-extract-mcp")
	if mcpCfgErr != nil {
		return nil, "", false, fmt.Errorf("extract.llm: %w", mcpCfgErr)
	}
	defer mcpCleanup()

	fullCLIArgs := append(append([]string{}, cliArgs...), "--mcp-config", mcpConfigPath)

	// Retry loop: up to extractLLMMaxRetries total attempts.
	var lastSessionID string
	for attempt := 0; attempt < extractLLMMaxRetries; attempt++ {
		// Remove the previous submit output so a stale value isn't read on retry.
		_ = os.Remove(submittedOutputPath)

		cr, sessionID, runErr := AgentStreamer{
			Bin:        bin,
			CLIArgs:    fullCLIArgs,
			Stdin:      rendered,
			WorkingDir: workingDir,
		}.Run(ctx)
		if runErr != nil {
			return nil, "", false, runErr
		}
		if cr.Infra != nil {
			return nil, "", false, cr.Infra
		}
		if sessionID != "" {
			lastSessionID = sessionID
		}
		if cr.ExitCode != 0 {
			slog.WarnContext(ctx, "extract.llm.attempt_failed",
				slog.Int("attempt", attempt+1),
				slog.Int("exit_code", cr.ExitCode))
			continue
		}

		// Apply read-snapshot cap for journal recording (D9).
		stdout := cr.Stdout
		_, snapHash, overCap := ReadSnapshotSummary(stdout)
		if overCap {
			slog.InfoContext(ctx, "extract.llm.snapshot_cap",
				slog.String("sha256", snapHash),
				slog.Int("len", len(stdout)))
		}

		// Read the submitted payload from the mcp-validator tempfile.
		payloadBytes, readErr := os.ReadFile(submittedOutputPath)
		if readErr != nil || len(payloadBytes) == 0 {
			slog.WarnContext(ctx, "extract.llm.no_submit",
				slog.Int("attempt", attempt+1))
			continue
		}
		var payload any
		if jErr := json.Unmarshal(payloadBytes, &payload); jErr != nil {
			slog.WarnContext(ctx, "extract.llm.invalid_submit_json",
				slog.Int("attempt", attempt+1),
				slog.String("err", jErr.Error()))
			continue
		}
		return payload, lastSessionID, true, nil
	}

	// All attempts exhausted — treat as no-match for this tier.
	return nil, "", false, nil
}

// readFileForExtract reads a file, applying the read-snapshot cap (D9).
func readFileForExtract(path string) (string, error) {
	raw, err := osReadFileForExtract(path)
	if err != nil {
		return "", err
	}
	s := string(raw)
	verbatim, _, _ := ReadSnapshotSummary(s)
	return verbatim, nil
}

// synonymFileEntry is one parsed entry from a synonyms YAML file.
type synonymFileEntry struct {
	Phrases []string
	Payload any
}

// loadSynonymFile parses a synonyms YAML file. The expected format is a YAML
// mapping from phrase (or comma-separated phrases) to typed payload. Example:
//
//	"go north,head north": { direction: "north" }
//	wade: { action: "wade" }
func loadSynonymFile(path string) ([]synonymFileEntry, error) {
	raw, err := osReadFileForExtract(path)
	if err != nil {
		return nil, err
	}

	var rawMap map[string]any
	if err := unmarshalYAMLForExtract(raw, &rawMap); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	entries := make([]synonymFileEntry, 0, len(rawMap))
	for key, val := range rawMap {
		phrases := splitSynonymPhrases(key)
		entries = append(entries, synonymFileEntry{
			Phrases: phrases,
			Payload: val,
		})
	}
	return entries, nil
}

// normaliseSynonymInput lowercases and trims whitespace for synonym matching.
func normaliseSynonymInput(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// splitSynonymPhrases splits a comma-separated synonym key into individual phrases.
func splitSynonymPhrases(key string) []string {
	parts := strings.Split(key, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// validateExtractPayload checks that payload is schema-valid against the JSON
// Schema at schemaPath. Returns true when valid or when schema validation is
// unavailable (schemaPath empty, schema unreadable). Errors are logged, not
// propagated — runtime schema enforcement is a safety net, not a hard gate
// (authoring errors are caught at load time).
//
// Routing-verdict payloads (those with the _semroute_verdict field) bypass
// JSON schema validation — they are internal transport artifacts, not user data.
func validateExtractPayload(ctx context.Context, schemaPath string, payload any) bool {
	if schemaPath == "" || payload == nil {
		return true
	}
	if m, ok := payload.(map[string]any); ok {
		if _, isVerdict := m["_semroute_verdict"]; isVerdict {
			return true
		}
	}
	// Re-encode payload to JSON then validate against the schema.
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		slog.WarnContext(ctx, "extract.schema_validate",
			slog.String("err", "marshal payload: "+err.Error()))
		return true // soft: let through
	}

	schemaRaw, readErr := osReadFileForExtract(resolvePromptPath(schemaPath))
	if readErr != nil {
		slog.WarnContext(ctx, "extract.schema_validate",
			slog.String("err", "read schema: "+readErr.Error()))
		return true // soft: let through
	}

	return jsonSchemaValidate(ctx, schemaRaw, payloadJSON)
}

// runExtractValidator invokes the optional validator subprocess in a read-only
// sandbox. Returns (accepted, error). accepted=false means the validator
// rejected the payload; accepted=true (or any error) is treated as "pass"
// from the caller's point of view (the caller logs the error but does not
// treat it as a schema failure).
func runExtractValidator(ctx context.Context, vd *ExtractValidatorDef, payload any) (bool, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return true, fmt.Errorf("marshal payload for validator: %w", err)
	}

	parts := strings.Fields(vd.PostCmd)
	if len(parts) == 0 {
		return true, nil
	}
	cmd := parts[0]
	cmdArgs := parts[1:]

	// Inject payload and post_cmd_args as environment variables.
	env := []string{
		"KITSOKI_EXTRACT_PAYLOAD=" + string(payloadJSON),
	}
	for k, v := range vd.PostCmdArgs {
		vJSON, _ := json.Marshal(v)
		env = append(env, "KITSOKI_ARG_"+strings.ToUpper(k)+"="+string(vJSON))
	}

	res, runErr := RunValidatorSandboxed(ctx, ValidatorSandboxOptions{
		Cmd:   cmd,
		Args:  cmdArgs,
		Env:   env,
		Stdin: string(payloadJSON),
	})
	if runErr != nil {
		return true, runErr
	}
	return res.ExitCode == 0, nil
}

// emitExtractResolverMatched emits a synthetic journal/stream event so the TUI
// gets a visible signal when a deterministic tier matches (the streaming
// invariant, via the extract.resolver.matched event kind).
func emitExtractResolverMatched(ctx context.Context, tier, input string) {
	slog.InfoContext(ctx, "extract.resolver.matched",
		slog.String("tier", tier),
		slog.String("input_snippet", truncateForLog(input, 120)))
}

// truncateForLog truncates s to maxLen for log emission. Does not add an
// ellipsis — callers that need one should append it themselves.
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
