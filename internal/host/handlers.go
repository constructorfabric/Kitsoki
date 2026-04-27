// Package host — built-in handler implementations.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// WorkspaceManagerGetHandler implements host.workspace_manager.get.
// It shells out to the workspace-manager CLI binary and parses JSON output.
// Args:
//   - workspace_id (string, optional): if set, fetch that workspace; else fetch current
//
// Returns Result.Data with the parsed JSON from the CLI.
func WorkspaceManagerGetHandler(ctx context.Context, args map[string]any) (Result, error) {
	// Build the command: workspace-manager get [--id <id>]
	cmdArgs := []string{"get"}
	if id, ok := args["workspace_id"].(string); ok && id != "" {
		cmdArgs = append(cmdArgs, "--id", id)
	}

	out, err := exec.CommandContext(ctx, "workspace-manager", cmdArgs...).Output()
	if err != nil {
		// Check if it's an exit error with stderr
		if exitErr, ok := err.(*exec.ExitError); ok {
			return Result{Error: strings.TrimSpace(string(exitErr.Stderr))}, nil
		}
		// Binary not found or infra failure
		return Result{}, fmt.Errorf("host.workspace_manager.get: exec: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		return Result{}, fmt.Errorf("host.workspace_manager.get: parse JSON: %w", err)
	}

	return Result{Data: data}, nil
}

// RunHandler implements host.run — executes an arbitrary shell command via bash.
// Args:
//   - cmd (string, required): the shell command to run
//   - cwd (string, optional): working directory
//
// Returns Result.Data with:
//   - stdout (string): combined stdout
//   - exit_code (int): exit code
//   - ok (bool): true if exit code == 0
func RunHandler(ctx context.Context, args map[string]any) (Result, error) {
	cmd, ok := args["cmd"].(string)
	if !ok || cmd == "" {
		return Result{Error: "host.run: cmd argument is required"}, nil
	}

	bashCmd := exec.CommandContext(ctx, "bash", "-c", cmd)

	if cwd, ok := args["cwd"].(string); ok && cwd != "" {
		bashCmd.Dir = cwd
	}

	out, err := bashCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("host.run: exec: %w", err)
		}
	}

	return Result{
		Data: map[string]any{
			"stdout":    string(out),
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}, nil
}

// RegisterBuiltins registers all built-in host handlers into the registry.
// Call at process startup before any app is loaded.
func RegisterBuiltins(r *Registry) {
	r.Register("host.workspace_manager.get", WorkspaceManagerGetHandler)
	r.Register("host.run", RunHandler)
	r.Register("host.oracle.ask", OracleAskHandler)
	r.Register("host.oracle.talk", OracleTalkHandler)
}
