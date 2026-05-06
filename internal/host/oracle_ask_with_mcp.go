// Package host — host.oracle.ask_with_mcp handler for one-shot Claude calls
// that need MCP servers attached (typed JSON via wiggum-style schema validators
// being the primary use-case).
//
// This is host.oracle.ask plus an mcp_servers: arg that is materialized into a
// temporary --mcp-config file and passed to `claude -p`. The bug-fix room
// uses this for every LLM-driven phase (proposal §5.2, §7.1).
//
// Field naming note (N11): the canonical text-output field on this handler
// is `stdout` (consistent with the rest of host.run / host.oracle.* surface).
// In the chat-aware path we additionally expose the same string under the
// alias `answer` so YAML can use a single `answer` key whether the upstream
// handler is host.oracle.talk (which only has `answer`) or
// host.oracle.ask_with_mcp. We do NOT rename `stdout` — established callers
// (validator path, non-chat path, on_complete notification) still bind it.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hally/internal/expr"
)

// hallyBinaryEnv overrides the path to the hally binary used to spawn the
// auto-attached validator. Set in tests; in production callers may also
// set it if `hally` is not the running binary's name.
const hallyBinaryEnv = "HALLY_BIN"

// validatorOptions bundles the optional validator configuration plumbed
// through from a `validator:` sub-block on the YAML `with:` map. All
// fields are optional; when the entire validator: block is absent the
// caller passes a zero value and the validator runs schema-only as
// before. Templating of post_cmd_args (`{{ world.X }}`) is handled
// up-stream by the orchestrator's RawWith re-render machinery — by the
// time these strings reach the handler they are already resolved.
type validatorOptions struct {
	// PostCmd is the verifier command (e.g. "python3 -m bugfix verify-impl").
	// Empty = schema-only.
	PostCmd string
	// PostCmdArgs is an ordered list of key=value pairs forwarded to the
	// post-cmd subprocess as `--key value`. Order is preserved so argv
	// composition is deterministic across iterations.
	PostCmdArgs []postCmdKV
	// PostCmdCwd is the working directory for the verifier subprocess
	// (relative paths resolved against HALLY_APP_DIR via resolvePromptPath).
	// Empty = inherit hally's cwd.
	PostCmdCwd string
	// MaxRetries caps the inner submit-retry budget. Zero = let the
	// validator pick its default (5).
	MaxRetries int
	// StateFilePath, when non-empty, is forwarded to the validator's
	// --state-file flag. The host handler creates the path itself when
	// running multi-iteration retry loops so counters span re-engagements.
	StateFilePath string
}

// postCmdKV is a key/value pair forwarded as `--<key> <value>` to the
// validator's --post-cmd subprocess.
type postCmdKV struct {
	Key   string
	Value string
}

// parseValidatorOptions extracts a `validator:` sub-block from the
// handler's call args. Returns the zero value when the block is absent,
// or an error message when fields are present but malformed (the
// caller surfaces these as Result.Error so on_error: can route).
//
// Expected YAML shape:
//
//	validator:
//	  post_cmd: "python3 -m bugfix verify-impl"
//	  post_cmd_args:
//	    ticket: "{{ world.ticket }}"
//	    worktree: ".bug-fix/{{ world.ticket }}/worktree"
//	  post_cmd_cwd: "tools/loopy"
//	  max_retries: 5
//
// post_cmd_args is a map; iteration order through Go maps is non-
// deterministic but the resulting argv is sorted by key so the
// validator subprocess sees a stable argv across iterations.
func parseValidatorOptions(args map[string]any) (validatorOptions, string) {
	rawBlock, ok := args["validator"]
	if !ok || rawBlock == nil {
		return validatorOptions{}, ""
	}
	blk, ok := rawBlock.(map[string]any)
	if !ok {
		return validatorOptions{}, "validator: must be a mapping"
	}

	var opts validatorOptions
	if v, _ := blk["post_cmd"].(string); strings.TrimSpace(v) != "" {
		opts.PostCmd = v
	}
	if v, _ := blk["post_cmd_cwd"].(string); strings.TrimSpace(v) != "" {
		opts.PostCmdCwd = v
	}
	switch v := blk["max_retries"].(type) {
	case int:
		opts.MaxRetries = v
	case int8:
		opts.MaxRetries = int(v)
	case int16:
		opts.MaxRetries = int(v)
	case int32:
		opts.MaxRetries = int(v)
	case int64:
		opts.MaxRetries = int(v)
	case uint:
		opts.MaxRetries = int(v)
	case uint8:
		opts.MaxRetries = int(v)
	case uint16:
		opts.MaxRetries = int(v)
	case uint32:
		opts.MaxRetries = int(v)
	case uint64:
		// goccy/go-yaml stores positive YAML integers as uint64 in
		// IntegerNode.Value (see ast.IntegerNode godoc: "int64 or uint64").
		// `max_retries: 5` arrives here as uint64.
		opts.MaxRetries = int(v)
	case float32:
		opts.MaxRetries = int(v)
	case float64:
		// YAML ints often arrive as float64 through JSON-shaped paths.
		opts.MaxRetries = int(v)
	case nil:
		// absent — leave as zero
	default:
		return validatorOptions{}, fmt.Sprintf("validator.max_retries: must be a number (got %T)", v)
	}

	if rawArgs, present := blk["post_cmd_args"]; present && rawArgs != nil {
		argsMap, ok := rawArgs.(map[string]any)
		if !ok {
			return validatorOptions{}, "validator.post_cmd_args: must be a mapping of string→string"
		}
		// Sort keys so argv composition is deterministic across iterations
		// (matters for the state-file resume path: identical claude
		// invocations across restarts).
		keys := make([]string, 0, len(argsMap))
		for k := range argsMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val, ok := argsMap[k].(string)
			if !ok {
				return validatorOptions{}, fmt.Sprintf("validator.post_cmd_args[%q]: must be a string (got %T)", k, argsMap[k])
			}
			opts.PostCmdArgs = append(opts.PostCmdArgs, postCmdKV{Key: k, Value: val})
		}
	}

	return opts, ""
}

// buildValidatorMCPServer constructs an mcp_servers entry that runs
// `hally mcp-validator --schema <abs-path> [--output <path>]
// [--post-cmd ... --post-cmd-arg k=v ... --max-retries N --state-file <path>]`.
// The schema path is resolved against HALLY_APP_DIR if relative, mirroring
// resolvePromptPath. When outputPath is non-empty the validator will
// write each successful submit's payload to that file (atomic, last-call
// wins) so the parent can recover the canonical JSON.
func buildValidatorMCPServer(schemaPath, outputPath string, opts validatorOptions) (map[string]any, error) {
	resolved := resolvePromptPath(schemaPath)
	if _, err := os.Stat(resolved); err != nil {
		return nil, fmt.Errorf("schema %q not found: %w", resolved, err)
	}

	bin := os.Getenv(hallyBinaryEnv)
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("locate hally binary: %w", err)
		}
		bin = exe
	}
	cliArgs := []any{"mcp-validator", "--schema", resolved}
	if outputPath != "" {
		cliArgs = append(cliArgs, "--output", outputPath)
	}
	if opts.PostCmd != "" {
		cliArgs = append(cliArgs, "--post-cmd", opts.PostCmd)
		for _, kv := range opts.PostCmdArgs {
			cliArgs = append(cliArgs, "--post-cmd-arg", kv.Key+"="+kv.Value)
		}
		if opts.PostCmdCwd != "" {
			cwd := resolvePromptPath(opts.PostCmdCwd)
			cliArgs = append(cliArgs, "--post-cmd-cwd", cwd)
		}
	}
	if opts.MaxRetries > 0 {
		cliArgs = append(cliArgs, "--max-retries", fmt.Sprintf("%d", opts.MaxRetries))
	}
	if opts.StateFilePath != "" {
		cliArgs = append(cliArgs, "--state-file", opts.StateFilePath)
	}
	return map[string]any{
		"command": bin,
		"args":    cliArgs,
	}, nil
}

// OracleAskWithMCPHandler implements host.oracle.ask_with_mcp.
//
// Required args:
//   - prompt_path | prompt (string): path to a prompt template file. If
//     relative, resolved against HALLY_APP_DIR (set by the loader) or cwd.
//
// Optional args:
//   - working_dir   (string): cwd for the claude subprocess.
//   - mcp_servers   (map):    server-name → { command: str, args: [str],
//                              env: {k:v} }. Materialized into a temp
//                              --mcp-config JSON file for the duration of
//                              the call. Empty/missing → no --mcp-config.
//   - output_format (string): "text" (default) or "json". When "json", the
//                              handler additionally parses stdout as JSON
//                              and exposes it as `stdout_json` for binding.
//   - schema        (string): informational; passed through unchanged. The
//                              MCP server is responsible for enforcement.
//   - args          (map):    explicit prompt-template variables.  The
//                              prompt is rendered with `expr.Env{Args:
//                              <this map>}`, so the prompt references its
//                              variables as `{{ args.X }}` (or any nested
//                              path like `{{ args.context.issue_block }}`,
//                              `{{ args.artifacts.phase_3.fix_description }}`).
//                              When omitted, falls back to passing the full
//                              call-args map as the template scope (legacy
//                              behaviour) so existing rooms keep working —
//                              new rooms should use the explicit `args:`
//                              block to keep handler-control keys
//                              (prompt/schema/etc.) out of the template
//                              namespace.
//   - chat_id       (string, optional): when set AND a ChatStore is in context,
//                              persists the conversation to the chat transcript
//                              and reuses the claude session ID stored on the
//                              chat row across turns (same as host.oracle.talk).
//
// Returns Result.Data with:
//   - stdout      (string): claude's text reply
//   - stdout_json (any):    parsed JSON when output_format=="json" and parse succeeds
//   - exit_code   (int):    claude's exit code
//   - ok          (bool):   exit_code == 0
//   - chat_id            (string, chat-aware path only)
//   - claude_session_id  (string, chat-aware path only)
//   - transcript_seq     (int, chat-aware path only)
//   - answer             (string, chat-aware path only): alias for stdout, so
//                                                        YAML can `bind: answer: answer`
//                                                        consistently with host.oracle.talk.
//
// On all expected errors (binary missing, prompt unreadable, MCP config
// marshal failure, non-zero exit) the handler returns Result{Error: ...}
// rather than a Go error so on_error: routing remains deterministic.
func OracleAskWithMCPHandler(ctx context.Context, args map[string]any) (Result, error) {
	promptPath, _ := args["prompt_path"].(string)
	if strings.TrimSpace(promptPath) == "" {
		// Accept the proposal-style "prompt:" alias too.
		if alt, _ := args["prompt"].(string); strings.TrimSpace(alt) != "" {
			promptPath = alt
		}
	}
	if strings.TrimSpace(promptPath) == "" {
		return Result{Error: "host.oracle.ask_with_mcp: prompt_path (or prompt) argument is required"}, nil
	}

	resolved := resolvePromptPath(promptPath)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: read prompt %q: %v", resolved, err)}, nil
	}

	// Choose the template scope: prefer the explicit `args:` map, fall
	// back to the entire call-args dict for backwards compatibility.
	// Authors should migrate to the explicit form so handler-control
	// keys (prompt/schema/output_format/mcp_servers/working_dir) don't
	// leak into the prompt's `args.X` namespace and so nested objects
	// (`args.context.X`, `args.artifacts.phase_3.Y`) are clean rather
	// than a flat soup of one-deep keys.
	templateArgs, _ := args["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = args
	}
	rendered, err := expr.Render(string(raw), expr.Env{Args: templateArgs})
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: render prompt %q: %v", resolved, err)}, nil
	}

	// Chat-aware path: chat_id provided AND ChatStore available.
	chatID, _ := args["chat_id"].(string)
	if chatID != "" {
		cs := ChatStoreFromContext(ctx)
		if cs == nil {
			return Result{Error: "host.oracle.ask_with_mcp: chat_id provided but no chat store wired"}, nil
		}
		return runOracleAskWithMCPWithChat(ctx, cs, chatID, rendered, resolved, args)
	}

	return oracleAskWithMCPCore(ctx, rendered, resolved, args, nil, "")
}

// runOracleAskWithMCPWithChat executes the chat-aware path: acquires the
// per-chat lock, persists the claude session ID, appends the user message,
// runs the claude invocation, then appends the assistant message.
//
// Step ordering: SetClaudeSessionID runs BEFORE the user-append so a write
// failure on the session ID can't strand an unanswered user message in a
// chat that has no claude session to resume (see I10 in the agent-rooms
// review). Likewise, an assistant-append failure surfaces via Result.Error
// so on_error: routing observes it.
func runOracleAskWithMCPWithChat(ctx context.Context, cs ChatStore, chatID, rendered, resolvedPrompt string, args map[string]any) (Result, error) {
	var out Result
	lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
		chat, err := cs.Get(ctx, chatID)
		if err != nil {
			out = Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: get chat %s: %v", chatID, err)}
			return nil
		}

		// Determine or assign the claude session ID FIRST (before mutating
		// the transcript). If this write fails we bail before appending
		// anything.
		claudeSID := chat.ClaudeSessionID
		if claudeSID == "" {
			claudeSID = newUUID()
			if err := cs.SetClaudeSessionID(ctx, chatID, claudeSID); err != nil {
				out = Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: set claude session id: %v", err)}
				return nil
			}
		}

		// Append the rendered prompt as the user message.
		if _, err := cs.AppendMessage(ctx, chatID, "user", rendered, nil); err != nil {
			out = Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: append user message: %v", err)}
			return nil
		}

		inner, runErr := oracleAskWithMCPCore(ctx, rendered, resolvedPrompt, args, nil, claudeSID)
		if runErr != nil {
			return runErr
		}

		// On success, append the assistant message.  Surface persistence
		// failures via Result.Error so on_error: routing fires (the user
		// has the answer in inner.Data["stdout"], but the orchestrator/TUI
		// is informed the transcript wasn't extended).
		if inner.Error == "" {
			stdout, _ := inner.Data["stdout"].(string)
			exitCode, _ := inner.Data["exit_code"].(int)
			_, hasSubmitted := inner.Data["submitted"]
			_, appendErr := cs.AppendMessage(ctx, chatID, "assistant", stdout, map[string]any{
				"exit_code":           exitCode,
				"validator_submitted": hasSubmitted,
			})
			if appendErr != nil {
				if inner.Data == nil {
					inner.Data = make(map[string]any)
				}
				inner.Data["chat_id"] = chatID
				inner.Data["claude_session_id"] = claudeSID
				inner.Error = fmt.Sprintf("host.oracle.ask_with_mcp: persist assistant message: %v", appendErr)
				out = inner
				return nil
			}
		}

		seq, _ := cs.LatestSeq(ctx, chatID)

		// Enrich the result with chat metadata.
		if inner.Data == nil {
			inner.Data = make(map[string]any)
		}
		inner.Data["chat_id"] = chatID
		inner.Data["claude_session_id"] = claudeSID
		inner.Data["transcript_seq"] = seq
		// N11: expose `answer` as an alias for `stdout` in the chat-aware
		// path so YAML can write `bind: answer: answer` regardless of which
		// chat-aware handler produced the result. `stdout` remains the
		// canonical name for the non-chat path; we only add this in the
		// chat-aware branch where a transcript-anchored "answer" is more
		// meaningful than a raw stdout dump.
		if stdout, ok := inner.Data["stdout"].(string); ok {
			inner.Data["answer"] = stdout
		}

		out = inner
		return nil
	})
	if errors.Is(lockErr, ErrChatBusy) {
		return Result{Error: lockErr.Error()}, nil
	}
	if lockErr != nil {
		return Result{}, lockErr
	}
	return out, nil
}

// oracleAskWithMCPCore executes the claude one-shot invocation. When
// claudeSessionID is non-empty, --session-id is added to the CLI args so the
// call participates in a Claude-side conversation.
func oracleAskWithMCPCore(ctx context.Context, rendered, resolvedPrompt string, args map[string]any, _ any, claudeSessionID string) (Result, error) {
	bin, err := resolveOracleBin()
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	workingDir, _ := args["working_dir"].(string)
	if workingDir == "" {
		workingDir = filepath.Dir(resolvedPrompt)
	}

	outputFormat := "text"
	if of, _ := args["output_format"].(string); of != "" {
		outputFormat = of
	}

	cliArgs := []string{
		"-p",
		"--output-format", outputFormat,
		"--permission-mode", "bypassPermissions",
	}

	// When participating in a chat, inject the session ID so Claude can
	// resume the conversation from its own memory.
	if claudeSessionID != "" {
		cliArgs = append(cliArgs, "--session-id", claudeSessionID)
	}

	// Build the merged mcp_servers map: caller-provided entries plus an
	// auto-attached "validator" entry when `schema:` is set and the caller
	// didn't already define one. The validator is the running hally binary
	// invoked as `hally mcp-validator --schema <abs-path>`, which exposes a
	// `submit` tool whose input schema is the user-provided schema. The LLM
	// must call submit() and round-trip its JSON through it before answering;
	// validation errors come back inline so the LLM self-corrects.
	//
	// We shallow-copy the caller's map so the auto-attached validator entry
	// does not leak back into args["mcp_servers"] (an effect map can be
	// re-run after on_error: routing, and a stale validator entry would
	// point at a deleted tempfile on the second pass).
	callerServers, _ := args["mcp_servers"].(map[string]any)
	mcpServers := make(map[string]any, len(callerServers)+1)
	for k, v := range callerServers {
		mcpServers[k] = v
	}

	// Parse the optional `validator:` sub-block. When absent the
	// resulting validatorOptions is the zero value and the validator
	// runs schema-only (the v0 behaviour). Errors here are user-visible
	// (malformed YAML); surface as Result.Error so on_error: routes.
	vopts, vparseErr := parseValidatorOptions(args)
	if vparseErr != "" {
		return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: %s", vparseErr)}, nil
	}
	// validatorBlockPresent governs whether we run the abandonment-recovery
	// retry loop (claude --resume nudges) or the single-shot legacy path.
	// We key on the explicit presence of the YAML sub-block rather than on
	// any individual field so authors who write `validator: {}` opt into
	// the retry semantics with sensible defaults.
	_, validatorBlockPresent := args["validator"]

	// validatorOutputPath is set when we auto-attach the validator. It's
	// the path the validator subprocess writes the schema-validated payload
	// to on each successful submit. We read it back after `claude -p` exits
	// and bind the result as Result.Data["submitted"], so authors can
	// `bind: foo: submitted` and get the canonical, validated JSON instead
	// of relying on the LLM's final stdout text.
	//
	// validatorStateFilePath is set when the retry loop is active so the
	// validator's session counters (attempts/successful_submits/last_error)
	// survive each iteration's subprocess restart. We read this same file
	// between iterations to inspect the Outcome and decide whether to
	// re-engage with a nudge.
	var validatorOutputPath string
	var validatorStateFilePath string
	if schemaArg, _ := args["schema"].(string); strings.TrimSpace(schemaArg) != "" {
		if _, alreadyHasValidator := mcpServers["validator"]; !alreadyHasValidator {
			outFile, ofErr := os.CreateTemp("", "hally-validated-*.json")
			if ofErr != nil {
				return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: create validator output tempfile: %v", ofErr)}, nil
			}
			validatorOutputPath = outFile.Name()
			_ = outFile.Close()
			// Remove the empty tempfile so we can detect "validator never
			// captured anything" by os.Stat returning ErrNotExist after
			// claude exits — the validator will recreate it via atomic
			// rename on its first successful submit.
			_ = os.Remove(validatorOutputPath)
			defer os.Remove(validatorOutputPath)

			// In retry-loop mode, allocate a state-file path and pass it
			// to the validator subprocess so counters persist across
			// `claude --resume` restarts. Outside retry-loop mode we
			// don't bother — the legacy single-shot path doesn't need
			// cross-process state.
			if validatorBlockPresent {
				stFile, sfErr := os.CreateTemp("", "hally-validator-state-*.json")
				if sfErr != nil {
					return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: create validator state tempfile: %v", sfErr)}, nil
				}
				validatorStateFilePath = stFile.Name()
				_ = stFile.Close()
				_ = os.Remove(validatorStateFilePath)
				defer os.Remove(validatorStateFilePath)
				vopts.StateFilePath = validatorStateFilePath
			}

			validatorEntry, vErr := buildValidatorMCPServer(schemaArg, validatorOutputPath, vopts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		}
	}

	// Materialize mcp_servers (if any) into a temp config file.
	var mcpConfigPath string
	if len(mcpServers) > 0 {
		mcpConfig := map[string]any{"mcpServers": mcpServers}
		mcpBytes, mErr := json.Marshal(mcpConfig)
		if mErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: marshal mcp_servers: %v", mErr)}, nil
		}
		f, fErr := os.CreateTemp("", "hally-mcp-*.json")
		if fErr != nil {
			return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: create mcp config tempfile: %v", fErr)}, nil
		}
		if _, wErr := f.Write(mcpBytes); wErr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: write mcp config: %v", wErr)}, nil
		}
		_ = f.Close()
		mcpConfigPath = f.Name()
		defer os.Remove(mcpConfigPath)
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	// Pick the execution path:
	//   - validator: block present  → abandonment-recovery retry loop
	//                                  with `claude --resume <sid>` nudges
	//   - otherwise                  → single-shot legacy path (unchanged)
	if validatorBlockPresent {
		effectiveMaxRetries := vopts.MaxRetries
		if effectiveMaxRetries <= 0 {
			effectiveMaxRetries = validatorDefaultMaxRetries
		}
		return runWithValidatorRetryLoop(ctx, runValidatorLoopParams{
			Bin:                 bin,
			BaseCLIArgs:         cliArgs,
			Rendered:            rendered,
			WorkingDir:          workingDir,
			OutputFormat:        outputFormat,
			ValidatorOutputPath: validatorOutputPath,
			ValidatorStatePath:  validatorStateFilePath,
			MaxOuterIterations:  maxOuterIterations,
			ValidatorMaxRetries: effectiveMaxRetries,
		}), nil
	}

	cr, runErr := runClaudeOneShot(ctx, bin, cliArgs, rendered, workingDir)
	if runErr != nil {
		return Result{}, runErr
	}
	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.ask_with_mcp: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}, nil
	}

	res := Result{
		Data: map[string]any{
			"stdout":    cr.Stdout,
			"exit_code": cr.ExitCode,
			"ok":        cr.ExitCode == 0,
		},
	}

	if outputFormat == "json" && cr.ExitCode == 0 && cr.Stdout != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(cr.Stdout), &parsed); jErr == nil {
			res.Data["stdout_json"] = parsed
		} else {
			// Don't fail the handler — bind: { foo: stdout_json } will silently
			// not bind, and an explicit on_error: route can still fire if the
			// state machine treats absent-binding as a failure. The text stdout
			// remains available for diagnostics.
			res.Data["stdout_json_parse_error"] = jErr.Error()
		}
	}

	// If we auto-attached the validator, read back the canonical payload it
	// captured from the LLM's submit() call. This is the schema-validated
	// JSON, independent of whatever final prose claude wrote — bind to
	// `submitted` for the typed-output path.
	if validatorOutputPath != "" {
		if vBytes, vErr := os.ReadFile(validatorOutputPath); vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				res.Data["submitted"] = parsed
			} else {
				// The validator only writes payloads that already passed
				// schema validation, so a parse error here is a real bug.
				// Surface it through the handler error path so on_error:
				// can route.
				res.Error = fmt.Sprintf("host.oracle.ask_with_mcp: parse validator output: %v", jErr)
			}
		}
		// Note: file-not-found is the normal "LLM never made a successful
		// submit" case. We leave Result.Data["submitted"] unset; the
		// state machine handles its absence via guard / on_error.
	}

	// Preserve any earlier res.Error (e.g. validator parse-error) — only
	// fall back to the exit-code message when nothing more specific is set.
	if cr.ExitCode != 0 && res.Error == "" {
		res.Error = claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
	}
	return res, nil
}

// maxOuterIterations is the default cap on the outer claude-restart loop
// when a validator: block is present. The inner per-submit retries are
// capped separately by the validator's MaxRetries field. Combined cap is
// outer * inner = 3 * 5 = 15 submits in the worst case.
const maxOuterIterations = 3

// abandonmentNudgePrompt is the prompt sent on each `claude --resume`
// re-engagement after the LLM exited without a successful submit. It is
// rendered with the validator's `last_error` (if any) so the LLM sees the
// most recent rejection reason and corrects accordingly. Hard-coded for
// now; can be lifted into a YAML field on a future iteration if authors
// need per-room customisation.
const abandonmentNudgeTemplate = `Your previous turn ended without successfully calling submit.{{LAST_ERROR_BLOCK}}

You MUST call the validator's submit tool with a valid payload to continue.
Read the task again, identify what is needed, and call submit. Do not exit
the conversation without submitting.`

// runValidatorLoopParams bundles everything the retry loop needs. Pulling
// it into a struct keeps OracleAskWithMCPHandler's signature clean even as
// the loop grows new knobs (custom nudge, per-iteration logging, etc.).
type runValidatorLoopParams struct {
	Bin                 string
	BaseCLIArgs         []string // contains -p, --output-format, --permission-mode, --mcp-config <path>
	Rendered            string   // initial user prompt
	WorkingDir          string
	OutputFormat        string
	ValidatorOutputPath string
	ValidatorStatePath  string
	MaxOuterIterations  int
	// ValidatorMaxRetries mirrors validatorOptions.MaxRetries (or the
	// validator's default of 5 when unset). The host-side outcome
	// computation must agree with the in-validator one or we'd
	// misclassify "exhausted" as "abandoned" and waste an extra
	// re-engagement.
	ValidatorMaxRetries int
}

// runWithValidatorRetryLoop runs `claude -p` once per outer iteration,
// re-engaging via `claude --resume <session-id>` whenever the LLM exits
// without a successful submit. Between iterations it reads the validator
// state file to inspect Outcome and last_error.
//
// Outer-loop semantics:
//
//   iteration 0  : claude -p   --session-id <sid>
//   iteration N>0: claude      --resume     <sid>  (only when Outcome == Abandoned)
//
// Termination conditions (checked after each iteration):
//
//   Outcome == Success            → return success, bind submitted payload
//   Outcome == RetriesExhausted   → return error (last_error), on_error: fires
//   Outcome == Abandoned, n+1==N  → return error ("session abandoned"), on_error: fires
//   Outcome == Abandoned, n+1<N   → continue with --resume + nudge prompt
//
// The validator's in-memory counters reset between subprocess invocations,
// but the state-file path makes them persist so the validator can return
// the "MAX RETRIES EXHAUSTED" sentinel even if exhaustion straddles
// re-engagements (rare but possible — the LLM can submit many times within
// a single iteration, so iteration 1's submits accrue against iteration
// 0's attempts counter).
func runWithValidatorRetryLoop(ctx context.Context, p runValidatorLoopParams) Result {
	maxOuter := p.MaxOuterIterations
	if maxOuter <= 0 {
		maxOuter = maxOuterIterations
	}

	sessionID := newUUID()

	var lastIterStdout string
	var lastIterExit int
	var lastIterStderr string
	var lastInfraErr error
	for iter := 0; iter < maxOuter; iter++ {
		// Build the per-iteration argv. iter==0 starts a fresh session;
		// iter>0 resumes the same session so claude has the full prior
		// context (prompt, tool calls, validator rejections) when the
		// nudge arrives.
		var iterArgs []string
		var iterPrompt string
		if iter == 0 {
			iterArgs = append([]string{}, p.BaseCLIArgs...)
			iterArgs = append(iterArgs, "--session-id", sessionID)
			iterPrompt = p.Rendered
		} else {
			iterArgs = append([]string{}, p.BaseCLIArgs...)
			iterArgs = append(iterArgs, "--resume", sessionID)
			// Inspect the validator's recorded last_error so the nudge
			// can echo the most recent rejection reason.
			_, _, lastErr := readValidatorState(p.ValidatorStatePath)
			iterPrompt = renderNudge(lastErr)
		}

		cr, runErr := runClaudeOneShot(ctx, p.Bin, iterArgs, iterPrompt, p.WorkingDir)
		if runErr != nil {
			lastInfraErr = runErr
			break
		}
		if cr.Infra != nil {
			lastInfraErr = cr.Infra
			lastIterStderr = cr.Stderr
			break
		}
		lastIterStdout = cr.Stdout
		lastIterExit = cr.ExitCode
		lastIterStderr = cr.Stderr

		// Inspect the validator's outcome. If it has a successful submit
		// we're done. If it ran out of retries we're done (failure). If
		// the LLM abandoned without submitting, loop and re-engage —
		// unless we've used the outer budget.
		attempts, success, lastErr := readValidatorState(p.ValidatorStatePath)
		switch outcomeFromState(attempts, success, p.ValidatorMaxRetries) {
		case mcpOutcomeSuccess:
			return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, "")
		case mcpOutcomeRetriesExhausted:
			msg := lastErr
			if strings.TrimSpace(msg) == "" {
				msg = fmt.Sprintf("validator: max retries exhausted after %d attempts", attempts)
			}
			return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, msg)
		case mcpOutcomeAbandoned:
			// Try again unless we've spent the outer budget.
			continue
		}
	}

	// Outer budget spent (or infrastructure failure). Surface whatever we
	// know: prefer the validator's last_error, fall back to "abandoned",
	// fall back to infra error.
	if lastInfraErr != nil {
		msg := fmt.Sprintf("host.oracle.ask_with_mcp: claude exec failed: %v", lastInfraErr)
		if s := strings.TrimSpace(lastIterStderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}
	}
	attempts, _, lastErr := readValidatorState(p.ValidatorStatePath)
	msg := lastErr
	if strings.TrimSpace(msg) == "" {
		msg = fmt.Sprintf("validator: session abandoned without successful submit after %d outer iteration(s), %d attempt(s)", maxOuter, attempts)
	}
	return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, msg)
}

// assembleResult builds the standard handler return value from a final
// claude run. Mirrors the legacy single-shot path so authors see the
// same Data shape (stdout / exit_code / ok / submitted / stdout_json)
// regardless of which path executed.
//
// errMsg is the orchestrator-visible error: empty = success, non-empty
// goes to res.Error which the orchestrator copies into world.last_error
// and (when on_error: is declared on the host call) routes the journey.
func assembleResult(p runValidatorLoopParams, stdout string, exitCode int, stderr, errMsg string) Result {
	res := Result{
		Data: map[string]any{
			"stdout":    stdout,
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}
	if p.OutputFormat == "json" && exitCode == 0 && stdout != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(stdout), &parsed); jErr == nil {
			res.Data["stdout_json"] = parsed
		} else {
			res.Data["stdout_json_parse_error"] = jErr.Error()
		}
	}
	if p.ValidatorOutputPath != "" {
		if vBytes, vErr := os.ReadFile(p.ValidatorOutputPath); vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				res.Data["submitted"] = parsed
			} else {
				// The validator only writes schema-passed payloads; a
				// parse error here is a real bug.
				if errMsg == "" {
					errMsg = fmt.Sprintf("host.oracle.ask_with_mcp: parse validator output: %v", jErr)
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

// renderNudge interpolates the LLM-facing nudge prompt with the most
// recent rejection reason (if any). Hard-coded template for now; if/when
// a room needs a different tone the call site can plumb a string field
// through validatorOptions.
func renderNudge(lastError string) string {
	block := ""
	if strings.TrimSpace(lastError) != "" {
		block = "\n\nThe last submission attempt was rejected:\n" + lastError
	}
	return strings.Replace(abandonmentNudgeTemplate, "{{LAST_ERROR_BLOCK}}", block, 1)
}

// readValidatorState reads the validator's persisted counters. Returns
// zeroes when the file is missing (the LLM never called submit) or
// malformed (treated as "we don't know — assume abandoned"). Errors are
// silently swallowed because the validator may legitimately not have
// written anything yet on the very first iteration.
func readValidatorState(path string) (attempts, successfulSubmits int, lastError string) {
	if path == "" {
		return 0, 0, ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0, 0, ""
	}
	var st struct {
		Attempts          int    `json:"attempts"`
		SuccessfulSubmits int    `json:"successful_submits"`
		LastError         string `json:"last_error"`
	}
	if jErr := json.Unmarshal(data, &st); jErr != nil {
		return 0, 0, ""
	}
	return st.Attempts, st.SuccessfulSubmits, st.LastError
}

// outcomeFromState mirrors mcp.ValidatorServer.Outcome() at the host
// level — we reimplement here so the host package doesn't have to import
// the mcp package. The two implementations must stay in sync; the
// single-source-of-truth lives in mcp/validator.go.
type mcpOutcome int

const (
	mcpOutcomeUnknown mcpOutcome = iota
	mcpOutcomeSuccess
	mcpOutcomeRetriesExhausted
	mcpOutcomeAbandoned
)

func outcomeFromState(attempts, success, maxRetries int) mcpOutcome {
	if success >= 1 {
		return mcpOutcomeSuccess
	}
	if maxRetries > 0 && attempts >= maxRetries {
		return mcpOutcomeRetriesExhausted
	}
	return mcpOutcomeAbandoned
}

// validatorDefaultMaxRetries mirrors mcp.ValidatorConfig.MaxRetries's
// fallback. The host-side outcome computation must agree with the
// in-validator one or we'd misclassify exhaustion vs abandonment.
const validatorDefaultMaxRetries = 5
