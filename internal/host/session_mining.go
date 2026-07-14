package host

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// SessionMiningRunHandler is the explicit external boundary for the
// session-mining adapter scripts. The scripts remain Python because they are
// the established transcript/report ecosystem; callers select a named
// operation and pass only its argv through this host capability.
//
// Args:
//   - op (string, required): one of the allow-listed adapter names below
//   - tools_dir (string, required): tools/session-mining directory
//   - cwd (string, optional): child working directory, default tools_dir
//   - args ([]any, required): arguments after the selected script
//   - fail_on_error (bool, optional): return Result.Error on non-zero exit
func SessionMiningRunHandler(ctx context.Context, args map[string]any) (Result, error) {
	toolsDir := stringValue(args, "tools_dir")
	if toolsDir == "" {
		return Result{Error: "host.session_mining.run: tools_dir is required"}, nil
	}
	absToolsDir, err := filepath.Abs(toolsDir)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.session_mining.run: tools_dir: %v", err)}, nil
	}
	toolsDir = absToolsDir
	op := stringValue(args, "op")
	script, ok := sessionMiningScripts[op]
	if !ok {
		return Result{Error: fmt.Sprintf("host.session_mining.run: unknown op %q", op)}, nil
	}
	rawArgs, present := args["args"]
	if !present || rawArgs == nil {
		return Result{Error: "host.session_mining.run: args are required"}, nil
	}
	argv, err := coerceArgs(rawArgs)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.session_mining.run: %v", err)}, nil
	}

	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "python3", append([]string{filepath.Join(toolsDir, script)}, argv...)...)
	cmd.Dir = toolsDir
	if cwd := stringValue(args, "cwd"); cwd != "" {
		cmd.Dir = cwd
	}
	out, runErr := cmd.CombinedOutput()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("host.session_mining.run: exec: %w", runErr)
		}
	}
	data := map[string]any{
		"ok":        exitCode == 0,
		"exit_code": exitCode,
		"stdout":    string(out),
	}
	if parsed := lastJSONObject(string(out)); parsed != nil {
		data["stdout_json"] = parsed
	}
	if exitCode != 0 {
		if fail, _ := args["fail_on_error"].(bool); fail {
			return Result{Data: data, Error: fmt.Sprintf("host.session_mining.run: exit code %d", exitCode)}, nil
		}
	}
	return Result{Data: data}, nil
}

var sessionMiningScripts = map[string]string{
	"prep":              "prep.py",
	"codex_prep":        "codex_prep.py",
	"outcomes":          "outcomes.py",
	"codex_outcomes":    "codex_outcomes.py",
	"eval_pilot_report": "eval_pilot_report.py",
	"ground":            "ground.py",
	"tag_score":         "tag_score.py",
	"emit":              "emit.py",
}
