package agenteval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Report struct {
	Kind          string            `json:"kind"`
	Eval          string            `json:"eval"`
	App           string            `json:"app"`
	Call          string            `json:"call"`
	GeneratedAt   time.Time         `json:"generated_at"`
	DatasetHash   string            `json:"dataset_hash"`
	PromptHash    string            `json:"prompt_hash,omitempty"`
	SchemaHash    string            `json:"schema_hash,omitempty"`
	ToolboxHash   string            `json:"toolbox_hash,omitempty"`
	AdherenceBar  AdherenceBar      `json:"adherence_bar"`
	Candidates    []CandidateResult `json:"candidates"`
	Decision      *Decision         `json:"decision,omitempty"`
	FailureSample []FailureSample   `json:"failure_samples,omitempty"`

	Path string `json:"-"`
}

type CandidateResult struct {
	Profile         string  `json:"profile"`
	Backend         string  `json:"backend,omitempty"`
	Provider        string  `json:"provider,omitempty"`
	Model           string  `json:"model"`
	Effort          string  `json:"effort,omitempty"`
	Pass            bool    `json:"pass"`
	SchemaValidRate float64 `json:"schema_valid_rate"`
	ComparatorRate  float64 `json:"comparator_pass_rate"`
	ContractRate    float64 `json:"contract_conformance_rate"`
	P50LatencyMS    int     `json:"p50_latency_ms,omitempty"`
	P95LatencyMS    int     `json:"p95_latency_ms,omitempty"`
	AvgCostUSD      float64 `json:"avg_cost_usd,omitempty"`
	P95CostUSD      float64 `json:"p95_cost_usd,omitempty"`
	RetryRate       float64 `json:"retry_rate,omitempty"`
	FallbackRate    float64 `json:"fallback_rate,omitempty"`
	ExamplesRun     int     `json:"examples_run"`
}

type Decision struct {
	Strategy        string `json:"strategy"`
	SelectedProfile string `json:"selected_profile,omitempty"`
	SelectedModel   string `json:"selected_model,omitempty"`
	SelectedEffort  string `json:"selected_effort,omitempty"`
	Evidence        string `json:"evidence,omitempty"`
	RejectedSummary string `json:"rejected_summary,omitempty"`
	PinnedBy        string `json:"pinned_by,omitempty"`
	PinnedAt        string `json:"pinned_at,omitempty"`
	FallbackProfile string `json:"fallback_profile,omitempty"`
}

type FailureSample struct {
	Example string         `json:"example"`
	Profile string         `json:"profile"`
	Model   string         `json:"model"`
	Reason  string         `json:"reason"`
	Expect  map[string]any `json:"expect,omitempty"`
	Actual  map[string]any `json:"actual,omitempty"`
}

func LoadReport(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report Report
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	report.Path = abs
	return &report, nil
}

func LoadReportsForDataset(ds *Dataset) ([]*Report, error) {
	dir := filepath.Join(filepath.Dir(ds.Path), "reports", ds.Call)
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	out := make([]*Report, 0, len(matches))
	for _, path := range matches {
		report, err := LoadReport(path)
		if err != nil {
			return nil, fmt.Errorf("load report %s: %w", path, err)
		}
		out = append(out, report)
	}
	return out, nil
}

func ReportStatus(report *Report, call CallSite) string {
	if report == nil {
		return "unmeasured"
	}
	stale := report.DatasetHash != "" && call.DatasetHash != "" && report.DatasetHash != call.DatasetHash
	stale = stale || (report.PromptHash != "" && call.PromptHash != "" && report.PromptHash != call.PromptHash)
	stale = stale || (report.SchemaHash != "" && call.SchemaHash != "" && report.SchemaHash != call.SchemaHash)
	stale = stale || (report.ToolboxHash != "" && call.ToolboxHash != "" && report.ToolboxHash != call.ToolboxHash)
	if stale {
		return "stale"
	}
	for _, c := range report.Candidates {
		if c.Pass {
			return "passing"
		}
	}
	return "failing"
}

func LatestReport(reports []*Report) *Report {
	if len(reports) == 0 {
		return nil
	}
	for _, report := range reports {
		if filepath.Base(report.Path) == "latest.json" {
			return report
		}
	}
	latest := reports[0]
	for _, report := range reports[1:] {
		if report.GeneratedAt.After(latest.GeneratedAt) {
			latest = report
		}
	}
	return latest
}
