package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BakeoffRunHandler is the explicit boundary for the external, offline bakeoff
// report tools. The story invokes it through Starlark instead of embedding a
// Python command in room YAML. The benchmark harness remains external because
// its report/deck schema is shared with the matrix tooling and is not a story
// state-machine concern.
func BakeoffRunHandler(ctx context.Context, args map[string]any) (Result, error) {
	op := stringValue(args, "op")
	cwd := stringValue(args, "cwd")
	if cwd == "" {
		cwd = stringValue(args, "harness_dir")
	}
	if cwd == "" {
		return Result{Error: "host.bakeoff.run: cwd is required"}, nil
	}
	var script string
	var argv []string
	executable := "python3"
	switch op {
	case "bench":
		script = "bench.py"
		var err error
		argv, err = coerceArgs(args["args"])
		if err != nil {
			return Result{Error: fmt.Sprintf("host.bakeoff.run: %v", err)}, nil
		}
		argv = append([]string{script}, argv...)
	case "prepare_handoffs":
		executable = "./prepare_handoffs.sh"
		var err error
		argv, err = coerceArgs(args["args"])
		if err != nil {
			return Result{Error: fmt.Sprintf("host.bakeoff.run: %v", err)}, nil
		}
	case "aggregate":
		script = "aggregate.py"
		argv = []string{script, "--generated-at", stringValue(args, "generated_at")}
	case "report":
		script = "../session-mining/eval_pilot_report.py"
		argv = []string{script, "--summary", "results/summary.json", "--markdown", "results/report.md", "--deck", "results/deck.html"}
	default:
		return Result{Error: fmt.Sprintf("host.bakeoff.run: unknown op %q", op)}, nil
	}
	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, argv...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	data := map[string]any{"ok": err == nil, "exit_code": 0, "stdout": string(out)}
	if err != nil {
		data["exit_code"] = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			data["exit_code"] = exitErr.ExitCode()
		}
		data["error"] = strings.TrimSpace(err.Error())
	}
	if parsed := lastJSONObject(string(out)); parsed != nil {
		data["stdout_json"] = parsed
	}
	return Result{Data: data}, nil
}

func lastJSONObject(output string) map[string]any {
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		value := map[string]any{}
		if json.Unmarshal([]byte(line), &value) == nil {
			return value
		}
	}
	return nil
}
