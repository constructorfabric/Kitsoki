package agentbench

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MarkdownReport renders a compact, reviewable score report. It is intentionally
// deterministic so it can be committed as evidence or diffed across tuning runs.
func MarkdownReport(report Report) string {
	var b strings.Builder
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	fmt.Fprintf(&b, "# Agent Bench: %s\n\n", report.CaseID)
	fmt.Fprintf(&b, "- Status: %s\n", status)
	fmt.Fprintf(&b, "- Trace: `%s`\n", report.Trace)
	fmt.Fprintf(&b, "- Final state: `%s`\n", emptyAs(report.Metrics.FinalState, "(none)"))
	fmt.Fprintf(&b, "- Submitted: %t\n\n", report.Metrics.Submitted)

	b.WriteString("## Metrics\n\n")
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&b, "| Events | %d |\n", report.Metrics.Events)
	fmt.Fprintf(&b, "| Agent stream events | %d |\n", report.Metrics.AgentStreamEvents)
	fmt.Fprintf(&b, "| Wall seconds | %.3f |\n", report.Metrics.WallSeconds)
	fmt.Fprintf(&b, "| Input tokens | %d |\n", report.Metrics.InputTokens)
	fmt.Fprintf(&b, "| Output tokens | %d |\n", report.Metrics.OutputTokens)
	fmt.Fprintf(&b, "| Total tokens | %d |\n", report.Metrics.TotalTokens)
	fmt.Fprintf(&b, "| Cost USD | %.6f |\n", report.Metrics.CostUSD)
	fmt.Fprintf(&b, "| Tool calls | %d |\n", report.Metrics.ToolCallsTotal)
	fmt.Fprintf(&b, "| Read calls | %d |\n", report.Metrics.ReadCalls)
	fmt.Fprintf(&b, "| Files read | %d |\n", len(report.Metrics.FilesRead))
	fmt.Fprintf(&b, "| Thinking events | %d |\n", report.Metrics.ThinkingEvents)
	fmt.Fprintf(&b, "| Agent calls started | %d |\n", report.Metrics.AgentCallsStarted)
	fmt.Fprintf(&b, "| Agent calls finished | %d |\n", report.Metrics.AgentCallsFinished)
	fmt.Fprintf(&b, "| Agent calls errored | %d |\n", report.Metrics.AgentCallsErrored)
	fmt.Fprintf(&b, "| Agent calls in flight | %d |\n\n", report.Metrics.AgentCallsInFlight)

	if len(report.Failures) > 0 {
		b.WriteString("## Failures\n\n")
		for _, failure := range report.Failures {
			fmt.Fprintf(&b, "- %s\n", failure)
		}
		b.WriteString("\n")
	}
	if len(report.Metrics.ToolCallsByName) > 0 {
		b.WriteString("## Tool Calls\n\n")
		for _, name := range sortedKeys(report.Metrics.ToolCallsByName) {
			fmt.Fprintf(&b, "- `%s`: %d\n", name, report.Metrics.ToolCallsByName[name])
		}
		b.WriteString("\n")
	}
	if len(report.Metrics.FilesRead) > 0 {
		b.WriteString("## Files Read\n\n")
		for _, path := range report.Metrics.FilesRead {
			fmt.Fprintf(&b, "- `%s`\n", path)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func SlideyDeckJSON(report Report) ([]byte, error) {
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	failures := report.Failures
	if len(failures) == 0 {
		failures = []string{"No score failures."}
	}
	deck := map[string]any{
		"title": fmt.Sprintf("Agent Bench: %s", report.CaseID),
		"meta": map[string]any{
			"generator": "kitsoki agent-bench",
			"kind":      "agent-bench-report",
			"case_id":   report.CaseID,
			"passed":    report.Passed,
		},
		"slides": []map[string]any{
			{
				"title": fmt.Sprintf("%s %s", status, report.CaseID),
				"blocks": []map[string]any{
					{"type": "text", "text": fmt.Sprintf("Trace: %s", report.Trace)},
					{"type": "text", "text": fmt.Sprintf("Final state: %s | submit: %t", emptyAs(report.Metrics.FinalState, "(none)"), report.Metrics.Submitted)},
				},
			},
			{
				"title": "Budget Signals",
				"blocks": []map[string]any{
					{"type": "text", "text": fmt.Sprintf("Cost $%.6f | tokens %d in / %d out / %d total", report.Metrics.CostUSD, report.Metrics.InputTokens, report.Metrics.OutputTokens, report.Metrics.TotalTokens)},
					{"type": "text", "text": fmt.Sprintf("Tools %d | reads %d | files %d | wall %.3fs", report.Metrics.ToolCallsTotal, report.Metrics.ReadCalls, len(report.Metrics.FilesRead), report.Metrics.WallSeconds)},
					{"type": "text", "text": fmt.Sprintf("Agent calls started %d | finished %d | errored %d | in flight %d", report.Metrics.AgentCallsStarted, report.Metrics.AgentCallsFinished, report.Metrics.AgentCallsErrored, report.Metrics.AgentCallsInFlight)},
				},
			},
			{
				"title": "Findings",
				"blocks": []map[string]any{
					{"type": "list", "items": failures},
				},
			},
		},
	}
	return json.MarshalIndent(deck, "", "  ")
}

func emptyAs(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
