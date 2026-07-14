package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ProductJourneyRunHandler is the explicit host boundary for the external
// product-journey harness. The harness remains Python because it owns the
// browser/TUI/VS Code capture adapters and their evidence protocol; stories
// must reach it through this typed host capability rather than spelling a
// Python subprocess themselves.
//
// Args:
//   - script (string, optional): one of the supported product-journey
//     capture adapters; defaults to tools/product-journey/run.py
//   - args ([]any, required): arguments after the selected script
//   - cwd (string, optional): harness working directory, default "."
//   - fail_on_error (bool, optional): return Result.Error on non-zero exit
//
// The result mirrors host.run's structured process envelope, including
// stdout_json from the last non-empty stdout line.
func ProductJourneyRunHandler(ctx context.Context, args map[string]any) (Result, error) {
	rawArgs, present := args["args"]
	if !present || rawArgs == nil {
		return Result{Error: "host.product_journey.run: args are required"}, nil
	}
	argv, err := coerceArgs(rawArgs)
	if err != nil || len(argv) == 0 {
		if err != nil {
			return Result{Error: fmt.Sprintf("host.product_journey.run: %v", err)}, nil
		}
		return Result{Error: "host.product_journey.run: args are required"}, nil
	}

	script := "tools/product-journey/run.py"
	if requested, _ := args["script"].(string); strings.TrimSpace(requested) != "" {
		switch requested {
		case "tools/product-journey/run.py", "tools/arena/scripts/run_scenario_qa_legs_parallel.py":
			script = requested
		default:
			return Result{Error: fmt.Sprintf("host.product_journey.run: unsupported script %q", requested)}, nil
		}
	}
	command := append([]string{script}, argv...)
	cmd := exec.CommandContext(ctx, "python3", command...)
	if cwd, _ := args["cwd"].(string); strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	out, runErr := cmd.CombinedOutput()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("host.product_journey.run: exec: %w", runErr)
		}
	}

	data := map[string]any{
		"stdout":    string(out),
		"exit_code": exitCode,
		"ok":         exitCode == 0,
	}
	if value, ok := lastJSONLine(string(out)); ok {
		data["stdout_json"] = value
	} else if looksLikeJSON(strings.TrimSpace(string(out))) {
		data["stdout_json_parse_error"] = "last output line was not valid JSON"
	}
	if exitCode != 0 {
		if fail, _ := args["fail_on_error"].(bool); fail {
			return Result{Data: data, Error: fmt.Sprintf("host.product_journey.run: exit code %d", exitCode)}, nil
		}
	}
	return Result{Data: data}, nil
}

func lastJSONLine(stdout string) (any, bool) {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			return value, true
		}
		return nil, false
	}
	return nil, false
}
