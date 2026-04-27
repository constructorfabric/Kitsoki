// Package host — host.oracle.ask handler for one-shot Claude prompt-file calls.
//
// host.oracle.ask reads a prompt template file from disk, substitutes
// {{ args.X }} placeholders against the handler's invocation args, and pipes
// the rendered text to `claude -p`. The binary's stdout is returned verbatim.
// Each invocation is independent — no session_id is tracked.
//
// This is the primitive behind patterns like:
//   - propose: natural-language intent  → drafted shell command
//   - refine:  current draft + feedback → updated shell command
//   - repair:  failed cmd + error       → corrected shell command
//
// For conversational, session-preserving calls see host.oracle.talk (oracle.go).
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hally/internal/expr"
)

// OracleAskHandler implements host.oracle.ask.
//
// Required args:
//   - prompt_path (string): path to a prompt template file. If relative, it
//     is resolved against the directory containing app.yaml (set by the loader
//     via HALLY_APP_DIR) or the process working directory as a fallback.
//
// Optional args:
//   - working_dir (string): cwd passed to the claude subprocess (scopes
//     tool access). Defaults to the directory containing the prompt file.
//
// All other keys in args are treated as template variables and are
// available to the prompt template as {{ args.X }}.
//
// Returns Result.Data with:
//   - stdout    (string): claude's final text reply, stripped of a trailing newline
//   - exit_code (int):    claude's exit code (0 on success)
//   - ok        (bool):   true iff exit_code == 0
//
// If the claude binary is unavailable, the prompt file cannot be read, or
// the template fails to render, the handler returns Result{Error: ...}
// rather than a Go error so flow tests stay deterministic and the state
// machine can surface the failure via on_error:.
func OracleAskHandler(ctx context.Context, args map[string]any) (Result, error) {
	promptPath, _ := args["prompt_path"].(string)
	if strings.TrimSpace(promptPath) == "" {
		return Result{Error: "host.oracle.ask: prompt_path argument is required"}, nil
	}

	resolved := resolvePromptPath(promptPath)
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask: read prompt %q: %v", resolved, err)}, nil
	}

	rendered, err := expr.Render(string(raw), expr.Env{Args: args})
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask: render prompt %q: %v", resolved, err)}, nil
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

	cliArgs := []string{
		"-p",
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
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
			// Infrastructure failure (e.g. binary vanished mid-run).
			stderrText := strings.TrimSpace(stderr.String())
			msg := fmt.Sprintf("host.oracle.ask: claude exec failed: %v", runErr)
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

// AppDirEnv is the env var loaders set to the directory containing app.yaml,
// so handlers can resolve relative paths (prompt files, scripts) deterministically.
const AppDirEnv = "HALLY_APP_DIR"

// resolvePromptPath expands a prompt_path arg to an absolute path.
// Relative paths are resolved against HALLY_APP_DIR when set, otherwise
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
