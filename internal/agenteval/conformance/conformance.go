// Package conformance checks recorded agent traces against the contract stamped
// on agent.call.start events.
package conformance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"kitsoki/internal/effect"
	"kitsoki/internal/store"
)

type Report struct {
	Calls      int         `json:"calls"`
	ToolUses   int         `json:"tool_uses"`
	Violations []Violation `json:"violations,omitempty"`
}

func (r Report) OK() bool { return len(r.Violations) == 0 }

type Violation struct {
	EventIndex int    `json:"event_index"`
	CallID     string `json:"call_id"`
	Tool       string `json:"tool,omitempty"`
	Reason     string `json:"reason"`
}

type contract struct {
	Verb         string   `json:"verb"`
	Agent        string   `json:"agent,omitempty"`
	Toolbox      string   `json:"toolbox,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	DeniedTools  []string `json:"denied_tools,omitempty"`
	Effect       string   `json:"effect,omitempty"`
}

// CheckFile loads an event JSONL trace and checks every recorded agent tool use
// against its matching agent.call.start contract.
func CheckFile(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	return CheckJSONL(f)
}

func CheckJSONL(r io.Reader) (Report, error) {
	events, err := readJSONL(r)
	if err != nil {
		return Report{}, err
	}
	return CheckEvents(events), nil
}

func readJSONL(r io.Reader) ([]store.Event, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var events []store.Event
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev store.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("line %d: decode event: %w", lineNo, err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read trace JSONL: %w", err)
	}
	return events, nil
}

func CheckEvents(events []store.Event) Report {
	report := Report{}
	contracts := map[string]contract{}
	for i, ev := range events {
		switch ev.Kind {
		case store.AgentCalled:
			report.Calls++
			if ev.CallID == "" {
				report.Violations = append(report.Violations, Violation{
					EventIndex: i,
					Reason:     "agent.call.start missing call_id",
				})
				continue
			}
			c, err := parseContract(ev.Payload)
			if err != nil {
				report.Violations = append(report.Violations, Violation{
					EventIndex: i,
					CallID:     ev.CallID,
					Reason:     err.Error(),
				})
				continue
			}
			contracts[ev.CallID] = c
		case store.AgentStreamEvent, store.LLMToolCall:
			if ev.CallID == "" {
				continue
			}
			c, ok := contracts[ev.CallID]
			if !ok {
				report.Violations = append(report.Violations, Violation{
					EventIndex: i,
					CallID:     ev.CallID,
					Reason:     "tool use has no matching agent.call.start contract",
				})
				continue
			}
			for _, tool := range toolsFromPayload(ev.Payload) {
				report.ToolUses++
				report.Violations = append(report.Violations, checkToolUse(i, ev.CallID, c, tool)...)
			}
		}
	}
	return report
}

func parseContract(raw json.RawMessage) (contract, error) {
	var c contract
	if len(raw) == 0 {
		return c, fmt.Errorf("agent.call.start missing payload")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("decode agent.call.start payload: %w", err)
	}
	if c.Effect != "" && !effect.Effect(c.Effect).Valid() {
		return c, fmt.Errorf("invalid declared effect %q", c.Effect)
	}
	return c, nil
}

func checkToolUse(eventIndex int, callID string, c contract, tool string) []Violation {
	var violations []Violation
	if tool == "" {
		return nil
	}
	if containsTool(c.DeniedTools, tool) {
		violations = append(violations, Violation{
			EventIndex: eventIndex,
			CallID:     callID,
			Tool:       tool,
			Reason:     fmt.Sprintf("tool %q is denied by contract", tool),
		})
	}
	if len(c.AllowedTools) > 0 && !containsTool(c.AllowedTools, tool) {
		violations = append(violations, Violation{
			EventIndex: eventIndex,
			CallID:     callID,
			Tool:       tool,
			Reason:     fmt.Sprintf("tool %q is outside allowed_tools", tool),
		})
	}
	if c.Effect != "" {
		actual := effect.ToolClass(tool)
		declared := effect.Effect(c.Effect)
		if !actual.LessEqual(declared) {
			violations = append(violations, Violation{
				EventIndex: eventIndex,
				CallID:     callID,
				Tool:       tool,
				Reason:     fmt.Sprintf("tool %q has %s effect, exceeding declared %s effect", tool, actual, declared),
			})
		}
	}
	return violations
}

func toolsFromPayload(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		Tool  string `json:"tool"`
		Name  string `json:"name"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	var out []string
	if payload.Tool != "" {
		out = append(out, payload.Tool)
	}
	if payload.Name != "" {
		out = append(out, payload.Name)
	}
	for _, tool := range payload.Tools {
		if tool.Name != "" {
			out = append(out, tool.Name)
		}
	}
	return dedupe(out)
}

func containsTool(tools []string, want string) bool {
	want = canonicalTool(want)
	for _, tool := range tools {
		if canonicalTool(tool) == want {
			return true
		}
	}
	return false
}

func canonicalTool(tool string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tool), "host."))
}

func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
