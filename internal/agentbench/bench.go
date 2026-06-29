package agentbench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	goyaml "github.com/goccy/go-yaml"
)

const Kind = "agent_bench/v1"

type Manifest struct {
	Version string `json:"version" yaml:"version"`
	Cases   []Case `json:"cases" yaml:"cases"`
	Path    string `json:"-" yaml:"-"`
}

type Case struct {
	ID           string       `json:"id" yaml:"id"`
	Description  string       `json:"description,omitempty" yaml:"description,omitempty"`
	Trace        string       `json:"trace,omitempty" yaml:"trace,omitempty"`
	Run          RunSpec      `json:"run,omitempty" yaml:"run,omitempty"`
	Budgets      Budgets      `json:"budgets,omitempty" yaml:"budgets,omitempty"`
	Expectations Expectations `json:"expectations,omitempty" yaml:"expectations,omitempty"`
}

type RunSpec struct {
	Command []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Env     map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Workdir string            `json:"workdir,omitempty" yaml:"workdir,omitempty"`
	Timeout string            `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type Budgets struct {
	MaxWallSeconds    float64 `json:"max_wall_seconds,omitempty" yaml:"max_wall_seconds,omitempty"`
	MaxToolCalls      int     `json:"max_tool_calls,omitempty" yaml:"max_tool_calls,omitempty"`
	MaxReadCalls      int     `json:"max_read_calls,omitempty" yaml:"max_read_calls,omitempty"`
	MaxFilesRead      int     `json:"max_files_read,omitempty" yaml:"max_files_read,omitempty"`
	MaxInputTokens    int64   `json:"max_input_tokens,omitempty" yaml:"max_input_tokens,omitempty"`
	MaxOutputTokens   int64   `json:"max_output_tokens,omitempty" yaml:"max_output_tokens,omitempty"`
	MaxTotalTokens    int64   `json:"max_total_tokens,omitempty" yaml:"max_total_tokens,omitempty"`
	MaxCostUSD        float64 `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
	MaxThinkingEvents int     `json:"max_thinking_events,omitempty" yaml:"max_thinking_events,omitempty"`
}

type Expectations struct {
	RequireSubmit   bool     `json:"require_submit,omitempty" yaml:"require_submit,omitempty"`
	FinalState      string   `json:"final_state,omitempty" yaml:"final_state,omitempty"`
	ForbiddenTools  []string `json:"forbidden_tools,omitempty" yaml:"forbidden_tools,omitempty"`
	RequireArtifact string   `json:"require_artifact,omitempty" yaml:"require_artifact,omitempty"`
}

type Report struct {
	CaseID   string   `json:"case_id"`
	Trace    string   `json:"trace"`
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures,omitempty"`
	Metrics  Metrics  `json:"metrics"`
}

type Metrics struct {
	Events                   int            `json:"events"`
	AgentStreamEvents        int            `json:"agent_stream_events"`
	AgentCallsStarted        int            `json:"agent_calls_started,omitempty"`
	AgentCallsFinished       int            `json:"agent_calls_finished,omitempty"`
	AgentCallsErrored        int            `json:"agent_calls_errored,omitempty"`
	AgentCallsInFlight       int            `json:"agent_calls_in_flight,omitempty"`
	WallSeconds              float64        `json:"wall_seconds,omitempty"`
	ToolCallsTotal           int            `json:"tool_calls_total"`
	ToolCallsByName          map[string]int `json:"tool_calls_by_name,omitempty"`
	ReadCalls                int            `json:"read_calls,omitempty"`
	FilesRead                []string       `json:"files_read,omitempty"`
	ForbiddenToolCalls       map[string]int `json:"forbidden_tool_calls,omitempty"`
	ThinkingEvents           int            `json:"thinking_events,omitempty"`
	InputTokens              int64          `json:"input_tokens,omitempty"`
	OutputTokens             int64          `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int64          `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int64          `json:"cache_read_input_tokens,omitempty"`
	TotalTokens              int64          `json:"total_tokens,omitempty"`
	CostUSD                  float64        `json:"cost_usd,omitempty"`
	FinalState               string         `json:"final_state,omitempty"`
	Submitted                bool           `json:"submitted"`
}

func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := goyaml.UnmarshalWithOptions(b, &m, goyaml.Strict()); err != nil {
		return Manifest{}, err
	}
	m.Path = path
	if m.Version != Kind {
		return Manifest{}, fmt.Errorf("unsupported agent bench version %q", m.Version)
	}
	seen := map[string]bool{}
	for i, c := range m.Cases {
		if c.ID == "" {
			return Manifest{}, fmt.Errorf("case %d missing id", i)
		}
		if seen[c.ID] {
			return Manifest{}, fmt.Errorf("duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
	}
	return m, nil
}

func (m Manifest) Case(id string) (Case, error) {
	if id == "" {
		if len(m.Cases) == 1 {
			return m.Cases[0], nil
		}
		return Case{}, fmt.Errorf("--case is required when manifest has %d cases", len(m.Cases))
	}
	for _, c := range m.Cases {
		if c.ID == id {
			return c, nil
		}
	}
	return Case{}, fmt.Errorf("case %q not found", id)
}

func ScoreManifestCase(manifestPath, caseID, traceOverride string) (Report, error) {
	m, err := LoadManifest(manifestPath)
	if err != nil {
		return Report{}, err
	}
	c, err := m.Case(caseID)
	if err != nil {
		return Report{}, err
	}
	trace, err := resolveCaseTrace(manifestPath, c, traceOverride)
	if err != nil {
		return Report{}, err
	}
	return ScoreTrace(trace, c)
}

func resolveCaseTrace(manifestPath string, c Case, traceOverride string) (string, error) {
	trace := traceOverride
	traceFromManifest := false
	if trace == "" {
		trace = c.Trace
		traceFromManifest = true
	}
	if trace == "" {
		return "", fmt.Errorf("case %q has no trace; pass --trace or set trace in manifest", c.ID)
	}
	if traceFromManifest && !filepath.IsAbs(trace) {
		trace = filepath.Join(filepath.Dir(manifestPath), trace)
	}
	return trace, nil
}

func ScoreTrace(tracePath string, c Case) (Report, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()

	metrics := Metrics{
		ToolCallsByName:    map[string]int{},
		ForbiddenToolCalls: map[string]int{},
	}
	fileSet := map[string]bool{}
	forbidden := map[string]bool{}
	for _, tool := range c.Expectations.ForbiddenTools {
		forbidden[tool] = true
	}

	var first, last time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			return Report{}, fmt.Errorf("%s:%d: %w", tracePath, line, err)
		}
		metrics.Events++
		if ev.StatePath != "" {
			metrics.FinalState = ev.StatePath
		}
		if !ev.TS.IsZero() {
			if first.IsZero() {
				first = ev.TS
			}
			last = ev.TS
		}
		if ev.Kind == "agent.stream" {
			metrics.AgentStreamEvents++
			if isThinkingEvent(ev.Payload) {
				metrics.ThinkingEvents++
			}
			for _, call := range extractToolCalls(ev.Payload) {
				metrics.ToolCallsTotal++
				metrics.ToolCallsByName[call.Name]++
				if forbidden[call.Name] {
					metrics.ForbiddenToolCalls[call.Name]++
				}
				if strings.EqualFold(call.Name, "Read") {
					metrics.ReadCalls++
					if path := call.ReadPath(); path != "" && !fileSet[path] {
						fileSet[path] = true
						metrics.FilesRead = append(metrics.FilesRead, path)
					}
				}
				if isSubmitTool(call.Name) {
					metrics.Submitted = true
				}
			}
			accumulateUsage(&metrics, ev.Payload)
		}
		switch ev.Kind {
		case "agent.call.start":
			metrics.AgentCallsStarted++
		case "agent.returned", "agent.call.returned", "agent.call.complete", "agent.call.end", "agent.task.complete":
			metrics.AgentCallsFinished++
			accumulateUsage(&metrics, ev.Payload)
		case "agent.error", "agent.call.error":
			metrics.AgentCallsErrored++
			accumulateUsage(&metrics, ev.Payload)
		}
		if payloadHasSubmit(ev.Payload) {
			metrics.Submitted = true
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	if !first.IsZero() && !last.IsZero() && last.After(first) {
		metrics.WallSeconds = math.Round(last.Sub(first).Seconds()*1000) / 1000
	}
	if metrics.TotalTokens == 0 {
		metrics.TotalTokens = metrics.InputTokens + metrics.OutputTokens
	}
	sort.Strings(metrics.FilesRead)
	if len(metrics.ForbiddenToolCalls) == 0 {
		metrics.ForbiddenToolCalls = nil
	}
	if len(metrics.ToolCallsByName) == 0 {
		metrics.ToolCallsByName = nil
	}
	terminalCalls := metrics.AgentCallsFinished + metrics.AgentCallsErrored
	if metrics.AgentCallsStarted > terminalCalls {
		metrics.AgentCallsInFlight = metrics.AgentCallsStarted - terminalCalls
	}

	failures := scoreFailures(c, metrics, tracePath)
	return Report{
		CaseID:   c.ID,
		Trace:    tracePath,
		Passed:   len(failures) == 0,
		Failures: failures,
		Metrics:  metrics,
	}, nil
}

type traceEvent struct {
	TS        time.Time      `json:"ts"`
	Kind      string         `json:"kind"`
	StatePath string         `json:"state_path"`
	Payload   map[string]any `json:"payload"`
}

type toolCall struct {
	Name    string
	Preview string
	Input   any
}

func extractToolCalls(payload map[string]any) []toolCall {
	var out []toolCall
	if raw, ok := payload["tools"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name := stringValue(m, "name", "tool"); name != "" {
				out = append(out, toolCall{Name: name, Preview: stringValue(m, "preview", "text"), Input: m["input"]})
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	if name := stringValue(payload, "tool", "name"); name != "" {
		out = append(out, toolCall{Name: name, Preview: stringValue(payload, "preview", "text"), Input: payload["input"]})
	}
	return out
}

func (c toolCall) ReadPath() string {
	if m, ok := c.Input.(map[string]any); ok {
		for _, key := range []string{"file_path", "path"} {
			if s, ok := m[key].(string); ok {
				return s
			}
		}
	}
	fields := strings.Fields(c.Preview)
	for _, f := range fields {
		if strings.Contains(f, "/") || strings.Contains(f, ".") {
			return strings.Trim(f, "`'\"")
		}
	}
	return ""
}

func isThinkingEvent(payload map[string]any) bool {
	if stringValue(payload, "thinking") != "" {
		return true
	}
	subtype := stringValue(payload, "subtype", "type")
	return strings.Contains(subtype, "thinking")
}

func isSubmitTool(name string) bool {
	lower := strings.ToLower(name)
	return lower == "submit" || strings.HasSuffix(lower, "__submit") || strings.Contains(lower, "validator__submit")
}

func payloadHasSubmit(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "submitted" {
				if b, ok := val.(bool); !ok || b {
					return true
				}
			}
			if payloadHasSubmit(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if payloadHasSubmit(val) {
				return true
			}
		}
	}
	return false
}

func accumulateUsage(metrics *Metrics, payload map[string]any) {
	metrics.InputTokens = maxInt64(metrics.InputTokens, int64Value(payload, "input_tokens"))
	metrics.OutputTokens = maxInt64(metrics.OutputTokens, int64Value(payload, "output_tokens"))
	metrics.CacheCreationInputTokens = maxInt64(metrics.CacheCreationInputTokens, int64Value(payload, "cache_creation_input_tokens"))
	metrics.CacheReadInputTokens = maxInt64(metrics.CacheReadInputTokens, int64Value(payload, "cache_read_input_tokens"))
	metrics.TotalTokens = maxInt64(metrics.TotalTokens, int64Value(payload, "total_tokens"))
	metrics.CostUSD = math.Max(metrics.CostUSD, floatValue(payload, "total_cost_usd", "cost_usd"))
	if meta, ok := payload["meta"].(map[string]any); ok {
		metrics.CostUSD = math.Max(metrics.CostUSD, floatValue(meta, "total_cost_usd", "cost_usd"))
		if usage, ok := meta["usage"].(map[string]any); ok {
			metrics.InputTokens = maxInt64(metrics.InputTokens, int64Value(usage, "input_tokens"))
			metrics.OutputTokens = maxInt64(metrics.OutputTokens, int64Value(usage, "output_tokens"))
			metrics.CacheCreationInputTokens = maxInt64(metrics.CacheCreationInputTokens, int64Value(usage, "cache_creation_input_tokens"))
			metrics.CacheReadInputTokens = maxInt64(metrics.CacheReadInputTokens, int64Value(usage, "cache_read_input_tokens"))
			metrics.TotalTokens = maxInt64(metrics.TotalTokens, int64Value(usage, "total_tokens"))
		}
	}
}

func scoreFailures(c Case, m Metrics, tracePath string) []string {
	var failures []string
	addMaxFloat := func(label string, actual, max float64) {
		if max > 0 && actual > max {
			failures = append(failures, fmt.Sprintf("%s %.3f exceeds max %.3f", label, actual, max))
		}
	}
	addMaxInt := func(label string, actual, max int) {
		if max > 0 && actual > max {
			failures = append(failures, fmt.Sprintf("%s %d exceeds max %d", label, actual, max))
		}
	}
	addMaxInt64 := func(label string, actual, max int64) {
		if max > 0 && actual > max {
			failures = append(failures, fmt.Sprintf("%s %d exceeds max %d", label, actual, max))
		}
	}
	addMaxFloat("wall_seconds", m.WallSeconds, c.Budgets.MaxWallSeconds)
	addMaxInt("tool_calls_total", m.ToolCallsTotal, c.Budgets.MaxToolCalls)
	addMaxInt("read_calls", m.ReadCalls, c.Budgets.MaxReadCalls)
	addMaxInt("files_read", len(m.FilesRead), c.Budgets.MaxFilesRead)
	addMaxInt("thinking_events", m.ThinkingEvents, c.Budgets.MaxThinkingEvents)
	addMaxInt64("input_tokens", m.InputTokens, c.Budgets.MaxInputTokens)
	addMaxInt64("output_tokens", m.OutputTokens, c.Budgets.MaxOutputTokens)
	addMaxInt64("total_tokens", m.TotalTokens, c.Budgets.MaxTotalTokens)
	addMaxFloat("cost_usd", m.CostUSD, c.Budgets.MaxCostUSD)
	for _, tool := range sortedKeys(m.ForbiddenToolCalls) {
		failures = append(failures, fmt.Sprintf("forbidden tool %q called %d time(s)", tool, m.ForbiddenToolCalls[tool]))
	}
	if m.AgentCallsInFlight > 0 {
		failures = append(failures, fmt.Sprintf("agent_calls_in_flight %d: trace has start event(s) without returned/error terminal event", m.AgentCallsInFlight))
	}
	if c.Expectations.RequireSubmit && !m.Submitted {
		failures = append(failures, "required submit was not observed")
	}
	if c.Expectations.FinalState != "" && m.FinalState != c.Expectations.FinalState {
		failures = append(failures, fmt.Sprintf("final_state %q does not match expected %q", m.FinalState, c.Expectations.FinalState))
	}
	if c.Expectations.RequireArtifact != "" {
		path := c.Expectations.RequireArtifact
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(tracePath), path)
		}
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				failures = append(failures, fmt.Sprintf("required artifact %q does not exist", c.Expectations.RequireArtifact))
			} else {
				failures = append(failures, fmt.Sprintf("required artifact %q cannot be checked: %v", c.Expectations.RequireArtifact, err))
			}
		}
	}
	return failures
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func int64Value(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case int:
			return int64(v)
		case json.Number:
			n, _ := v.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	return 0
}

func floatValue(m map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch v := m[key].(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		case json.Number:
			n, _ := v.Float64()
			return n
		case string:
			n, _ := strconv.ParseFloat(v, 64)
			return n
		}
	}
	return 0
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
