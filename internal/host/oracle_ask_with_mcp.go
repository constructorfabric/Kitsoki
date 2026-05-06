// Package host — host.oracle.ask_with_mcp handler for one-shot Claude calls
// that need MCP servers attached (typed JSON via wiggum-style schema validators
// being the primary use-case).
//
// This is host.oracle.ask plus an mcp_servers: arg that is materialized into a
// temporary --mcp-config file and passed to `claude -p`. The bug-fix room
// uses this for every LLM-driven phase (proposal §5.2, §7.1).
package host

import (
	"context"
	"encoding/json"
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
	case int64:
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
//
// Returns Result.Data with:
//   - stdout      (string): claude's text reply
//   - stdout_json (any):    parsed JSON when output_format=="json" and parse succeeds
//   - exit_code   (int):    claude's exit code
//   - ok          (bool):   exit_code == 0
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

	bin, err := resolveOracleBin()
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	workingDir, _ := args["working_dir"].(string)
	if workingDir == "" {
		workingDir = filepath.Dir(resolved)
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

	// validatorOutputPath is set when we auto-attach the validator. It's
	// the path the validator subprocess writes the schema-validated payload
	// to on each successful submit. We read it back after `claude -p` exits
	// and bind the result as Result.Data["submitted"], so authors can
	// `bind: foo: submitted` and get the canonical, validated JSON instead
	// of relying on the LLM's final stdout text.
	var validatorOutputPath string
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
