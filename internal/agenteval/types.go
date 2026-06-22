// Package agenteval loads and scores story-local agent task evals.
//
// The package is intentionally free of live provider calls. It gives CLI,
// cassette lint, and future TUI/web surfaces a shared deterministic substrate:
// eval dataset parsing, call-site resolution, report loading, hashing, and
// comparator scoring.
package agenteval

import (
	"encoding/json"
	"fmt"
)

// Dataset is the YAML shape stored under stories/<name>/evals/*.yaml.
type Dataset struct {
	Kind       string          `yaml:"kind" json:"kind"`
	App        string          `yaml:"app" json:"app"`
	Call       string          `yaml:"call" json:"call"`
	Agent      string          `yaml:"agent,omitempty" json:"agent,omitempty"`
	Task       Task            `yaml:"task" json:"task"`
	Matrix     Matrix          `yaml:"matrix" json:"matrix"`
	Comparator ComparatorSpec  `yaml:"comparator" json:"comparator"`
	Examples   []Example       `yaml:"examples" json:"examples"`
	Selection  SelectionPolicy `yaml:"selection,omitempty" json:"selection,omitempty"`

	Path    string `yaml:"-" json:"-"`
	AppPath string `yaml:"-" json:"-"`
}

type Task struct {
	Goal        string       `yaml:"goal" json:"goal"`
	Boundedness Boundedness  `yaml:"boundedness" json:"boundedness"`
	Adherence   AdherenceBar `yaml:"adherence_bar" json:"adherence_bar"`
}

type Boundedness struct {
	MaxTurns        int    `yaml:"max_turns" json:"max_turns"`
	ToolPolicy      string `yaml:"tool_policy" json:"tool_policy"`
	PreparedContext bool   `yaml:"prepared_context" json:"prepared_context"`
}

type AdherenceBar struct {
	MinPassRate     float64 `yaml:"min_pass_rate" json:"min_pass_rate"`
	MaxP95LatencyMS int     `yaml:"max_p95_latency_ms,omitempty" json:"max_p95_latency_ms,omitempty"`
	MaxAvgCostUSD   float64 `yaml:"max_avg_cost_usd,omitempty" json:"max_avg_cost_usd,omitempty"`
}

type Matrix struct {
	Profiles []string            `yaml:"profiles" json:"profiles"`
	Models   map[string][]string `yaml:"models,omitempty" json:"models,omitempty"`
	Effort   []string            `yaml:"effort,omitempty" json:"effort,omitempty"`
	Repeat   int                 `yaml:"repeat,omitempty" json:"repeat,omitempty"`
}

// ComparatorSpec accepts either a scalar comparator name or a mapping:
//
//	comparator: enum
//	comparator: { kind: enum, field: intent }
type ComparatorSpec struct {
	Kind  string `json:"kind"`
	Field string `json:"field,omitempty"`
}

func (c *ComparatorSpec) UnmarshalYAML(unmarshal func(any) error) error {
	var scalar string
	if err := unmarshal(&scalar); err == nil {
		c.Kind = scalar
		return nil
	}
	var object struct {
		Kind  string `yaml:"kind"`
		Field string `yaml:"field"`
	}
	if err := unmarshal(&object); err != nil {
		return err
	}
	c.Kind = object.Kind
	c.Field = object.Field
	return nil
}

type Example struct {
	Name   string         `yaml:"name" json:"name"`
	Args   map[string]any `yaml:"args,omitempty" json:"args,omitempty"`
	Expect map[string]any `yaml:"expect" json:"expect"`
	Actual map[string]any `yaml:"actual,omitempty" json:"actual,omitempty"`
}

type SelectionPolicy struct {
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	Pinned   Pin    `yaml:"pinned,omitempty" json:"pinned,omitempty"`
}

type Pin struct {
	Profile         string `yaml:"profile,omitempty" json:"profile,omitempty"`
	Model           string `yaml:"model,omitempty" json:"model,omitempty"`
	Effort          string `yaml:"effort,omitempty" json:"effort,omitempty"`
	Evidence        string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	FallbackProfile string `yaml:"fallback_profile,omitempty" json:"fallback_profile,omitempty"`
}

type CallSite struct {
	Call         string `json:"call"`
	Handler      string `json:"handler"`
	Agent        string `json:"agent,omitempty"`
	PromptPath   string `json:"prompt_path,omitempty"`
	SchemaPath   string `json:"schema_path,omitempty"`
	SchemaName   string `json:"schema_name,omitempty"`
	EffectIndex  int    `json:"effect_index"`
	StatePath    string `json:"state_path"`
	DatasetHash  string `json:"dataset_hash,omitempty"`
	PromptHash   string `json:"prompt_hash,omitempty"`
	SchemaHash   string `json:"schema_hash,omitempty"`
	ToolboxHash  string `json:"toolbox_hash,omitempty"`
	SelectionRef string `json:"selection_ref,omitempty"`
}

type ValidationResult struct {
	Dataset *Dataset
	Call    CallSite
	Errors  []string
	Warns   []string
}

func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

type CompareResult struct {
	Pass   bool   `json:"pass"`
	Reason string `json:"reason,omitempty"`
}

func toRaw(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return b, nil
}
