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
	"path/filepath"
	"strings"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
	"kitsoki/internal/render/sourcecolor"
)

// OracleAskHandler implements host.oracle.ask.
//
// Required args:
//   - prompt_path (string): path to a prompt template file. If relative, it
//     is resolved against the directory containing app.yaml (set by the loader
//     via KITSOKI_APP_DIR) or the process working directory as a fallback.
//
// Optional args:
//   - working_dir   (string): cwd passed to the claude subprocess (scopes
//     tool access). Defaults to the directory containing the prompt file.
//   - system_prompt (string): persona / system-prompt instruction. Threaded
//     to `claude --append-system-prompt`. When also passing `agent:`, the
//     inline value wins (override).
//   - agent         (string): name of an entry in AppDef.Agents (injected
//     via WithAgents); applies the agent's SystemPrompt as the system prompt
//     and forwards its Model (when set) as `claude --model`.
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

	rendered, err := render.Pongo(string(raw), expr.Env{Args: args})
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.ask: render prompt %q: %v", resolved, err)}, nil
	}
	// Strip source-color sentinels before piping to claude — see the
	// commentary in oracle_ask_with_mcp.go for the rationale.
	rendered = sourcecolor.Strip(rendered)

	bin, err := resolveOracleBin(ctx)
	if err != nil {
		return Result{Error: err.Error()}, nil
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
	// Per-call agent + inline system_prompt — applied via the same shared
	// helper as host.oracle.talk so apps see one consistent shape across
	// every oracle handler.
	agent, _ := resolveAgent(ctx, args)
	if sp := effectiveSystemPrompt(args, agent); strings.TrimSpace(sp) != "" {
		cliArgs = append(cliArgs, "--append-system-prompt", sp)
	}
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}

	cr, runErr := runClaudeOneShot(ctx, bin, cliArgs, rendered, workingDir)
	if runErr != nil {
		return Result{}, runErr
	}
	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.ask: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}, nil
	}

	res := Result{
		Data: map[string]any{
			// Wrap stdout with source-color sentinels so downstream
			// consumers (transcript, view templates) carry the
			// LLM-provenance label through to the final paint. The
			// sentinels are zero-width and survive pongo render,
			// hardwrap, and JSON serialization.
			"stdout":    sourcecolor.Wrap(cr.Stdout),
			"exit_code": cr.ExitCode,
			"ok":        cr.ExitCode == 0,
		},
	}
	if cr.ExitCode != 0 {
		res.Error = claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
	}
	return res, nil
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
