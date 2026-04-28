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
	"os/exec"
	"path/filepath"
	"strings"

	"hally/internal/expr"
)

// hallyBinaryEnv overrides the path to the hally binary used to spawn the
// auto-attached validator. Set in tests; in production callers may also
// set it if `hally` is not the running binary's name.
const hallyBinaryEnv = "HALLY_BIN"

// buildValidatorMCPServer constructs an mcp_servers entry that runs
// `hally mcp-validator --schema <abs-path> [--output <path>]`. The schema
// path is resolved against HALLY_APP_DIR if relative, mirroring
// resolvePromptPath. When outputPath is non-empty the validator will
// write each successful submit's payload to that file (atomic, last-call
// wins) so the parent can recover the canonical JSON.
func buildValidatorMCPServer(schemaPath, outputPath string) (map[string]any, error) {
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
//
// All other keys in args are template variables ({{ args.X }}).
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

	rendered, err := expr.Render(string(raw), expr.Env{Args: args})
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask_with_mcp: render prompt %q: %v", resolved, err)}, nil
	}

	bin := os.Getenv(OracleBinEnv)
	if bin == "" {
		path, lookErr := exec.LookPath("claude")
		if lookErr != nil {
			return Result{Error: ErrOracleUnavailable.Error()}, nil
		}
		bin = path
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
	mcpServers, _ := args["mcp_servers"].(map[string]any)
	if mcpServers == nil {
		mcpServers = map[string]any{}
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
			defer func() {
				if validatorOutputPath != "" {
					_ = os.Remove(validatorOutputPath)
				}
			}()

			validatorEntry, vErr := buildValidatorMCPServer(schemaArg, validatorOutputPath)
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

	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Stdin = strings.NewReader(rendered)
	cmd.Dir = workingDir

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			stderrText := strings.TrimSpace(stderr.String())
			msg := fmt.Sprintf("host.oracle.ask_with_mcp: claude exec failed: %v", runErr)
			if stderrText != "" {
				msg = fmt.Sprintf("%s\nstderr: %s", msg, stderrText)
			}
			return Result{Error: msg}, nil
		}
	}

	out := strings.TrimRight(stdout.String(), "\n")
	res := Result{
		Data: map[string]any{
			"stdout":    out,
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}

	if outputFormat == "json" && exitCode == 0 && out != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(out), &parsed); jErr == nil {
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
				if res.Error == "" {
					res.Error = fmt.Sprintf("host.oracle.ask_with_mcp: parse validator output: %v", jErr)
				}
			}
		}
		// Note: file-not-found is the normal "LLM never made a successful
		// submit" case. We leave Result.Data["submitted"] unset; the
		// state machine handles its absence via guard / on_error.
	}

	if exitCode != 0 {
		stderrText := strings.TrimSpace(stderr.String())
		if stderrText != "" {
			res.Error = stderrText
		} else if out != "" {
			res.Error = out
		} else {
			res.Error = fmt.Sprintf("claude exited with code %d", exitCode)
		}
	}
	return res, nil
}
