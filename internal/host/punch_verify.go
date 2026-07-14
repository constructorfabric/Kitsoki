package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// PunchVerifyHandler runs the deterministic checks declared by one punch-list
// item. The story calls this through ctx.host from punch_verify.star; the
// policy and process boundary stay in Go, while the story owns the orchestration
// and typed output contract.
func PunchVerifyHandler(ctx context.Context, args map[string]any) (Result, error) {
	statePath, _ := args["state_path"].(string)
	itemID, _ := args["item_id"].(string)
	state := map[string]any{}
	if statePath != "" {
		raw, err := os.ReadFile(statePath)
		if err != nil {
			return Result{}, fmt.Errorf("host.punch.verify: read state: %w", err)
		}
		if err := json.Unmarshal(raw, &state); err != nil {
			return Result{}, fmt.Errorf("host.punch.verify: parse state: %w", err)
		}
	}

	var item map[string]any
	if items, ok := state["items"].([]any); ok {
		for _, raw := range items {
			candidate, ok := raw.(map[string]any)
			if ok && stringValue(candidate, "id") == itemID {
				item = candidate
				break
			}
		}
	}
	if item == nil {
		return punchVerifyResult("failed", fmt.Sprintf("item not found: %s", itemID), nil), nil
	}

	checks := []any{}
	if verify, ok := item["verify"].([]any); ok {
		for _, raw := range verify {
			check, ok := raw.(map[string]any)
			if !ok {
				checks = append(checks, failedCheck("", "invalid verifier entry"))
				continue
			}
			checks = append(checks, runPunchCheck(ctx, check))
		}
	}
	if gate, _ := item["gate_command"].(string); strings.TrimSpace(gate) != "" {
		checks = append(checks, runPunchCommand(ctx, gate, nil))
	}
	if len(checks) == 0 {
		return punchVerifyResult("partial", "no deterministic verifier declared", checks), nil
	}

	failed := 0
	for _, raw := range checks {
		if check, ok := raw.(map[string]any); ok {
			if good, _ := check["ok"].(bool); !good {
				failed++
			}
		}
	}
	status := "passed"
	if failed > 0 {
		status = "failed"
	}
	return punchVerifyResult(status, fmt.Sprintf("%d/%d deterministic checks passed", len(checks)-failed, len(checks)), checks), nil
}

func runPunchCheck(ctx context.Context, check map[string]any) map[string]any {
	kind, _ := check["kind"].(string)
	switch kind {
	case "story_validate":
		story, _ := check["story"].(string)
		return runPunchCommand(ctx, "go run ./cmd/kitsoki render "+story+" -o -", []any{"go", "run", "./cmd/kitsoki", "render", story, "-o", "-"})
	case "story_test":
		story, _ := check["story"].(string)
		argv := []any{"go", "run", "./cmd/kitsoki", "test", "flows", story}
		if flows, _ := check["flows"].(string); flows != "" {
			argv = append(argv, "--flows", flows)
		}
		return runPunchCommand(ctx, strings.Join(anyStrings(argv), " "), argv)
	case "command":
		command, _ := check["cmd"].(string)
		if isLLMSpendingCommand(command) {
			return failedCheck(command, "blocked: verifier appears to invoke LLM/live command")
		}
		return runPunchCommand(ctx, command, nil)
	default:
		return failedCheck(kind, fmt.Sprintf("unsupported verify kind: %s", kind))
	}
}

func runPunchCommand(ctx context.Context, display string, argv []any) map[string]any {
	args := map[string]any{"cmd": display, "timeout": float64(120)}
	if argv != nil {
		args["cmd"] = argv[0]
		args["args"] = argv[1:]
	}
	res, err := RunHandler(ctx, args)
	if err != nil {
		return failedCheck(display, err.Error())
	}
	check := map[string]any{
		"cmd":       display,
		"exit_code": int64(0),
		"output":    "",
		"ok":        false,
	}
	for key, value := range res.Data {
		switch key {
		case "stdout":
			check["output"] = tailString(fmt.Sprint(value), 4000)
		case "exit_code", "ok":
			check[key] = value
		}
	}
	if res.Error != "" {
		check["output"] = res.Error
	}
	return check
}

func punchVerifyResult(status, summary string, checks []any) Result {
	return Result{Data: map[string]any{"verify_result": map[string]any{
		"status": status, "summary": summary, "checks": checks,
	}}}
}

func failedCheck(command, output string) map[string]any {
	return map[string]any{"cmd": command, "exit_code": int64(2), "output": output, "ok": false}
}

func stringValue(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func anyStrings(values []any) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = fmt.Sprint(value)
	}
	return out
}

func tailString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}

var llmSpendingCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bclaude\b`),
	regexp.MustCompile(`(?i)\bcodex\b`),
	regexp.MustCompile(`(?i)\bopenai\b`),
	regexp.MustCompile(`(?i)\banthropic\b`),
	regexp.MustCompile(`(?i)\bkitsoki\s+tour\b`),
	regexp.MustCompile(`(?i)\bharness:?\s*live\b`),
	regexp.MustCompile(`(?i)\B--harness\s+live\b`),
}

func isLLMSpendingCommand(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	for _, pattern := range llmSpendingCommandPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}
