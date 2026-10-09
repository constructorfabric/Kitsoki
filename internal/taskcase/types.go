// Package taskcase defines the lane-neutral history task manifest used by the
// repo-history training loop.
package taskcase

import (
	"fmt"
	"strings"
)

const Kind = "history_task.v1"

type Lane string

const (
	LaneBugfix         Lane = "bugfix"
	LaneDesign         Lane = "design"
	LaneOnboarding     Lane = "onboarding"
	LaneImplementation Lane = "implementation"
	LaneDocs           Lane = "docs"
	LaneRouting        Lane = "routing"
	LaneAgentEval      Lane = "agent_eval"
)

type WeightKind string

const (
	WeightPrompt  WeightKind = "prompt"
	WeightSlot    WeightKind = "slot"
	WeightStar    WeightKind = "star"
	WeightGraph   WeightKind = "graph"
	WeightDecider WeightKind = "decider"
	WeightFixture WeightKind = "fixture"
	WeightEval    WeightKind = "eval"
	WeightSelect  WeightKind = "selection"
)

type OracleKind string

const (
	OracleRedGreen            OracleKind = "red_green"
	OracleFlowFixture         OracleKind = "flow_fixture"
	OracleAgentEval           OracleKind = "agent_eval"
	OracleArtifactComparator  OracleKind = "artifact_comparator"
	OracleStaticCheck         OracleKind = "static_check"
	OracleHumanReviewRequired OracleKind = "human_review_required"
)

type LivePolicy string

const (
	LiveNoCost           LivePolicy = "no_cost"
	LiveOperatorApproved LivePolicy = "operator_approved"
)

type Case struct {
	Kind             string           `yaml:"kind" json:"kind"`
	ID               string           `yaml:"id" json:"id"`
	Lane             Lane             `yaml:"lane" json:"lane"`
	Source           Source           `yaml:"source" json:"source"`
	Story            Story            `yaml:"story" json:"story"`
	TrainableSurface TrainableSurface `yaml:"trainable_surface" json:"trainable_surface"`
	Input            Input            `yaml:"input" json:"input"`
	Oracle           Oracle           `yaml:"oracle" json:"oracle"`
	CostPolicy       CostPolicy       `yaml:"cost_policy" json:"cost_policy"`
	Artifacts        Artifacts        `yaml:"artifacts" json:"artifacts"`

	Path string `yaml:"-" json:"-"`
}

type Source struct {
	CorpusRef   string `yaml:"corpus_ref" json:"corpus_ref"`
	Repo        string `yaml:"repo" json:"repo"`
	BaselineRef string `yaml:"baseline_ref,omitempty" json:"baseline_ref,omitempty"`
	SuccessRef  string `yaml:"success_ref,omitempty" json:"success_ref,omitempty"`
}

type Story struct {
	App        string `yaml:"app" json:"app"`
	Entrypoint string `yaml:"entrypoint" json:"entrypoint"`
}

type TrainableSurface struct {
	WeightKind WeightKind `yaml:"weight_kind" json:"weight_kind"`
	TargetRefs []string   `yaml:"target_refs,omitempty" json:"target_refs,omitempty"`
}

type Input struct {
	PromptOrTicket string `yaml:"prompt_or_ticket" json:"prompt_or_ticket"`
}

type Oracle struct {
	Kind          OracleKind    `yaml:"kind" json:"kind"`
	Command       string        `yaml:"command,omitempty" json:"command,omitempty"`
	Comparator    string        `yaml:"comparator,omitempty" json:"comparator,omitempty"`
	AcceptanceBar AcceptanceBar `yaml:"acceptance_bar,omitempty" json:"acceptance_bar,omitempty"`
}

type AcceptanceBar struct {
	MinPassRate float64 `yaml:"min_pass_rate,omitempty" json:"min_pass_rate,omitempty"`
}

type CostPolicy struct {
	LivePolicy LivePolicy `yaml:"live_policy" json:"live_policy"`
	Ladder     string     `yaml:"ladder,omitempty" json:"ladder,omitempty"`
}

type Artifacts struct {
	Root string `yaml:"root" json:"root"`
}

type ValidationResult struct {
	Case     *Case    `json:"case,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

func Validate(c *Case) ValidationResult {
	result := ValidationResult{Case: c}
	if c == nil {
		result.Errors = append(result.Errors, "case is nil")
		return result
	}
	required(&result, c.Kind, "kind")
	if c.Kind != "" && c.Kind != Kind {
		result.Errors = append(result.Errors, fmt.Sprintf("kind must be %s", Kind))
	}
	required(&result, c.ID, "id")
	if strings.ContainsAny(c.ID, " \t\n") {
		result.Errors = append(result.Errors, "id must be stable and whitespace-free")
	}
	if !validLane(c.Lane) {
		result.Errors = append(result.Errors, fmt.Sprintf("unsupported lane %q", c.Lane))
	}
	required(&result, c.Source.CorpusRef, "source.corpus_ref")
	required(&result, c.Source.Repo, "source.repo")
	required(&result, c.Story.App, "story.app")
	required(&result, c.Story.Entrypoint, "story.entrypoint")
	if !validWeightKind(c.TrainableSurface.WeightKind) {
		result.Errors = append(result.Errors, fmt.Sprintf("unsupported trainable_surface.weight_kind %q", c.TrainableSurface.WeightKind))
	}
	required(&result, c.Input.PromptOrTicket, "input.prompt_or_ticket")
	if !validOracleKind(c.Oracle.Kind) {
		result.Errors = append(result.Errors, fmt.Sprintf("unsupported oracle.kind %q", c.Oracle.Kind))
	}
	if c.Oracle.Kind != OracleHumanReviewRequired && c.Oracle.Command == "" && c.Oracle.Comparator == "" {
		result.Errors = append(result.Errors, "oracle requires command or comparator unless kind is human_review_required")
	}
	if c.Oracle.Kind == OracleRedGreen {
		required(&result, c.Source.BaselineRef, "source.baseline_ref")
		required(&result, c.Source.SuccessRef, "source.success_ref")
		required(&result, c.Oracle.Command, "oracle.command")
	}
	if c.Oracle.Kind == OracleHumanReviewRequired {
		result.Warnings = append(result.Warnings, "human_review_required is a queue state only; it is not armed for autonomous training")
	}
	if !validLivePolicy(c.CostPolicy.LivePolicy) {
		result.Errors = append(result.Errors, fmt.Sprintf("unsupported cost_policy.live_policy %q", c.CostPolicy.LivePolicy))
	}
	required(&result, c.Artifacts.Root, "artifacts.root")
	if c.Artifacts.Root != "" && !strings.HasPrefix(c.Artifacts.Root, ".artifacts/") {
		result.Errors = append(result.Errors, "artifacts.root must live under .artifacts/")
	}
	return result
}

func required(result *ValidationResult, value, field string) {
	if strings.TrimSpace(value) == "" {
		result.Errors = append(result.Errors, field+" is required")
	}
}

func validLane(v Lane) bool {
	switch v {
	case LaneBugfix, LaneDesign, LaneOnboarding, LaneImplementation, LaneDocs, LaneRouting, LaneAgentEval:
		return true
	default:
		return false
	}
}

func validWeightKind(v WeightKind) bool {
	switch v {
	case WeightPrompt, WeightSlot, WeightStar, WeightGraph, WeightDecider, WeightFixture, WeightEval, WeightSelect:
		return true
	default:
		return false
	}
}

func validOracleKind(v OracleKind) bool {
	switch v {
	case OracleRedGreen, OracleFlowFixture, OracleAgentEval, OracleArtifactComparator, OracleStaticCheck, OracleHumanReviewRequired:
		return true
	default:
		return false
	}
}

func validLivePolicy(v LivePolicy) bool {
	switch v {
	case LiveNoCost, LiveOperatorApproved:
		return true
	default:
		return false
	}
}
