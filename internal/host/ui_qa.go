package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UIQARunHandler is the explicit host boundary for the kitsoki-ui-qa
// external adapter. The shell gate and persona_qa completion adapter remain
// external because they own the browser/vision evidence protocol; stories
// invoke them through this typed capability instead of embedding subprocess
// and JSON-shaping logic in room YAML.
//
// The supported operation is gate. It runs qa.sh with the supplied artifact
// paths, then converts the newest verdict.json into the completion-state
// artifact when the external adapter can do so.
func UIQARunHandler(ctx context.Context, args map[string]any) (Result, error) {
	if op := stringValue(args, "op"); op != "gate" {
		return Result{Error: fmt.Sprintf("host.ui_qa.run: unknown op %q", op)}, nil
	}
	cwd := stringValue(args, "cwd")
	if cwd == "" {
		return Result{Error: "host.ui_qa.run: cwd is required"}, nil
	}
	for _, name := range []string{"video_path", "frames_dir", "feature_md_path", "scenarios_path"} {
		if stringValue(args, name) == "" {
			return Result{Error: fmt.Sprintf("host.ui_qa.run: %s is required", name)}, nil
		}
	}

	commandCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	qaArgs := []string{
		filepath.Join(".agents", "skills", "kitsoki-ui-qa", "scripts", "qa.sh"),
		stringValue(args, "video_path"),
		"--frames", stringValue(args, "frames_dir"),
		"--feature", stringValue(args, "feature_md_path"),
		"--scenarios", stringValue(args, "scenarios_path"),
		"--strict",
	}
	qa := exec.CommandContext(commandCtx, "bash", qaArgs...)
	qa.Dir = cwd
	qaOut, qaErr := qa.CombinedOutput()
	qaExitCode, err := commandExitCode(qaErr)
	if err != nil {
		return Result{}, fmt.Errorf("host.ui_qa.run: qa.sh: %w", err)
	}

	completionPath := ""
	var output strings.Builder
	output.Write(qaOut)
	if verdict, ok := newestVerdict(filepath.Join(cwd, ".artifacts", "ui-qa")); ok {
		candidate := filepath.Join(cwd, ".artifacts", "ui-qa", "completion-state.json")
		completion := exec.CommandContext(commandCtx, "python3", "-m", "tools.persona_qa", "--kind", "ui-qa", "--input", verdict, "--out", candidate)
		completion.Dir = cwd
		completionOut, completionErr := completion.CombinedOutput()
		output.Write(completionOut)
		if completionErr == nil {
			completionPath = candidate
		}
	}

	data := map[string]any{
		"stdout":                   output.String(),
		"exit_code":                qaExitCode,
		"ok":                       qaExitCode == 0,
		"qa_ok":                    qaExitCode == 0,
		"qa_exit_code":             qaExitCode,
		"qa_completion_state_path": completionPath,
	}
	if fail, _ := args["fail_on_error"].(bool); fail && qaExitCode != 0 {
		return Result{Data: data, Error: fmt.Sprintf("host.ui_qa.run: exit code %d", qaExitCode)}, nil
	}
	return Result{Data: data}, nil
}

func commandExitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

func newestVerdict(root string) (string, bool) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "verdict.json"))
	if err != nil || len(paths) == 0 {
		return "", false
	}
	sort.Slice(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr != nil || rightErr != nil {
			return paths[i] > paths[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return paths[0], true
}
