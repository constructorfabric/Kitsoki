package ci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
)

const Schema = "capsule-ci/v1"
const VerdictSchema = "capsule-ci-verdict/v1"

type Config struct {
	Schema             string              `yaml:"schema" json:"schema"`
	ProjectProfile     string              `yaml:"project_profile,omitempty" json:"project_profile,omitempty"`
	DefaultEnvironment string              `yaml:"default_environment,omitempty" json:"default_environment,omitempty"`
	Pipelines          map[string]Pipeline `yaml:"pipelines" json:"pipelines"`
	Remotes            map[string]Remote   `yaml:"remotes,omitempty" json:"remotes,omitempty"`
	Receipt            ReceiptPolicy       `yaml:"receipt,omitempty" json:"receipt,omitempty"`
	Cleanup            CleanupPolicy       `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
}
type Pipeline struct {
	Story       string         `yaml:"story" json:"story"`
	Triggers    []string       `yaml:"triggers" json:"triggers"`
	Environment string         `yaml:"environment,omitempty" json:"environment,omitempty"`
	Executor    string         `yaml:"executor,omitempty" json:"executor,omitempty"`
	Mode        string         `yaml:"mode,omitempty" json:"mode,omitempty"`
	Required    bool           `yaml:"required,omitempty" json:"required,omitempty"`
	Permissions Permissions    `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Agents      Agents         `yaml:"agents,omitempty" json:"agents,omitempty"`
	Cleanup     CleanupPolicy  `yaml:"cleanup,omitempty" json:"cleanup,omitempty"`
	Result      ResultContract `yaml:"result" json:"result"`
}
type Permissions struct {
	Network       string `yaml:"network,omitempty" json:"network,omitempty"`
	ExternalWrite string `yaml:"external_write,omitempty" json:"external_write,omitempty"`
}
type Agents struct {
	Policy        string   `yaml:"policy,omitempty" json:"policy,omitempty"`
	Profiles      []string `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	MaxCostUSD    float64  `yaml:"max_cost_usd,omitempty" json:"max_cost_usd,omitempty"`
	OnUnavailable string   `yaml:"on_unavailable,omitempty" json:"on_unavailable,omitempty"`
}
type ResultContract struct {
	Schema    string   `yaml:"schema,omitempty" json:"schema,omitempty"`
	PassExits []string `yaml:"pass_exits,omitempty" json:"pass_exits,omitempty"`
	FailExits []string `yaml:"fail_exits,omitempty" json:"fail_exits,omitempty"`
	ParkExits []string `yaml:"park_exits,omitempty" json:"park_exits,omitempty"`
}
type Remote struct {
	Endpoint      string `yaml:"endpoint" json:"endpoint"`
	CredentialEnv string `yaml:"credential_env,omitempty" json:"credential_env,omitempty"`
	CAFile        string `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
}
type ReceiptPolicy struct {
	RequireSignature bool   `yaml:"require_signature,omitempty" json:"require_signature,omitempty"`
	Signer           string `yaml:"signer,omitempty" json:"signer,omitempty"`
}
type CleanupPolicy struct {
	KeepRuns            int   `yaml:"keep_runs,omitempty" json:"keep_runs,omitempty"`
	MaxReclaimableBytes int64 `yaml:"max_reclaimable_bytes,omitempty" json:"max_reclaimable_bytes,omitempty"`
	RequireHygieneCheck bool  `yaml:"require_hygiene_check,omitempty" json:"require_hygiene_check,omitempty"`
	IncludeCapsuleCache bool  `yaml:"include_capsule_cache,omitempty" json:"include_capsule_cache,omitempty"`
	IncludeGoBuildCache bool  `yaml:"include_go_build_cache,omitempty" json:"include_go_build_cache,omitempty"`
}
type Trigger struct {
	Kind              string   `json:"kind"`
	Provider          string   `json:"provider,omitempty"`
	EventID           string   `json:"event_id,omitempty"`
	Actor             string   `json:"actor,omitempty"`
	Ref               string   `json:"ref,omitempty"`
	BaseRef           string   `json:"base_ref,omitempty"`
	HeadSHA           string   `json:"head_sha,omitempty"`
	ChangedPaths      []string `json:"changed_paths,omitempty"`
	RequestedPipeline string   `json:"requested_pipeline,omitempty"`
}
type Check struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Outcome     string   `json:"outcome"`
	Evidence    []string `json:"evidence,omitempty"`
	DecisionRef string   `json:"decision_ref,omitempty"`
}
type Verdict struct {
	Schema            string  `json:"schema"`
	Pipeline          string  `json:"pipeline"`
	Outcome           string  `json:"outcome"`
	Summary           string  `json:"summary,omitempty"`
	Checks            []Check `json:"checks"`
	PromotionEligible bool    `json:"promotion_eligible"`
	SourceDigest      string  `json:"source_digest,omitempty"`
	StoryDigest       string  `json:"story_digest,omitempty"`
	EnvironmentDigest string  `json:"environment_digest,omitempty"`
	EnvelopeDigest    string  `json:"envelope_digest,omitempty"`
}

func Load(project string) (Config, error) {
	path := filepath.Join(project, ".kitsoki", "ci.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("capsule ci: read %s: %w", path, err)
	}
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("capsule ci: parse %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("capsule ci: parse %s: multiple YAML documents are not allowed", path)
		}
		return Config{}, fmt.Errorf("capsule ci: parse %s: %w", path, err)
	}
	if err := Validate(project, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
func Validate(project string, cfg Config) error {
	if cfg.Schema != Schema {
		return fmt.Errorf("capsule ci: schema %q, want %q", cfg.Schema, Schema)
	}
	if len(cfg.Pipelines) == 0 {
		return fmt.Errorf("capsule ci: pipelines are required")
	}
	if cfg.Receipt.RequireSignature && strings.TrimSpace(cfg.Receipt.Signer) == "" {
		return fmt.Errorf("capsule ci: receipt signer is required when signatures are required")
	}
	if err := validateCleanupPolicy("cleanup", cfg.Cleanup); err != nil {
		return err
	}
	for name, remote := range cfg.Remotes {
		if strings.TrimSpace(name) == "" || isBuiltinExecutor(name) {
			return fmt.Errorf("capsule ci remote %q: invalid executor name", name)
		}
		u, err := url.Parse(remote.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("capsule ci remote %q: endpoint must be https", name)
		}
		if remote.CredentialEnv != "" && !validEnvName(remote.CredentialEnv) {
			return fmt.Errorf("capsule ci remote %q: invalid credential env", name)
		}
		if remote.CAFile != "" {
			clean := filepath.Clean(remote.CAFile)
			if filepath.IsAbs(remote.CAFile) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("capsule ci remote %q: ca_file must be project-relative", name)
			}
			if _, err := os.Stat(filepath.Join(project, clean)); err != nil {
				return fmt.Errorf("capsule ci remote %q: ca_file: %w", name, err)
			}
		}
	}
	for name, p := range cfg.Pipelines {
		if err := validateCleanupPolicy("pipeline "+name+" cleanup", p.Cleanup); err != nil {
			return err
		}
		if strings.TrimSpace(p.Story) == "" || filepath.IsAbs(p.Story) || strings.HasPrefix(filepath.Clean(p.Story), "..") {
			return fmt.Errorf("capsule ci pipeline %q: story must be project-relative", name)
		}
		if _, err := os.Stat(filepath.Join(project, p.Story)); err != nil {
			return fmt.Errorf("capsule ci pipeline %q: story: %w", name, err)
		}
		env := p.Environment
		if env == "" {
			env = cfg.DefaultEnvironment
		}
		if env == "" {
			return fmt.Errorf("capsule ci pipeline %q: environment is required", name)
		}
		if _, err := environment.Load(project, env); err != nil {
			return fmt.Errorf("capsule ci pipeline %q: %w", name, err)
		}
		if p.Mode != "" && p.Mode != "one-shot" && p.Mode != "staged" {
			return fmt.Errorf("capsule ci pipeline %q: invalid mode", name)
		}
		if p.Permissions.Network != "" && p.Permissions.Network != "none" && p.Permissions.Network != "replay" && p.Permissions.Network != "live" {
			return fmt.Errorf("capsule ci pipeline %q: invalid network", name)
		}
		if p.Agents.Policy == "allow" && (len(p.Agents.Profiles) == 0 || p.Agents.MaxCostUSD <= 0 || p.Agents.OnUnavailable == "") {
			return fmt.Errorf("capsule ci pipeline %q: allowed agents require profiles, budget, and fallback", name)
		}
		if p.Agents.Policy != "" && p.Agents.Policy != "allow" && p.Agents.Policy != "deny" {
			return fmt.Errorf("capsule ci pipeline %q: invalid agent policy %q", name, p.Agents.Policy)
		}
		if err := validateResultContract(p.Result); err != nil {
			return fmt.Errorf("capsule ci pipeline %q: %w", name, err)
		}
		if p.Executor != "" && !isBuiltinExecutor(p.Executor) {
			if _, ok := cfg.Remotes[p.Executor]; !ok {
				return fmt.Errorf("capsule ci pipeline %q: executor %q is not configured", name, p.Executor)
			}
		}
	}
	return nil
}
func ValidateVerdict(v Verdict, expected executor.Envelope, contract ResultContract) error {
	if err := validateResultContract(contract); err != nil {
		return err
	}
	if v.Schema != VerdictSchema {
		return fmt.Errorf("capsule ci: verdict schema %q", v.Schema)
	}
	if v.Pipeline == "" || v.Outcome == "" {
		return fmt.Errorf("capsule ci: verdict pipeline and outcome are required")
	}
	if pipeline, _ := expected.Trigger["requested_pipeline"].(string); pipeline != "" && v.Pipeline != pipeline {
		return fmt.Errorf("capsule ci: verdict pipeline %q does not match sealed pipeline %q", v.Pipeline, pipeline)
	}
	switch v.Outcome {
	case "passed", "failed", "needs_input", "cancelled", "infra_failed":
	default:
		return fmt.Errorf("capsule ci: invalid verdict outcome %q", v.Outcome)
	}
	seen := map[string]bool{}
	for _, check := range v.Checks {
		if check.ID == "" || seen[check.ID] {
			return fmt.Errorf("capsule ci: checks require unique ids")
		}
		seen[check.ID] = true
		if check.Outcome == "passed" && len(check.Evidence) == 0 && check.DecisionRef == "" {
			return fmt.Errorf("capsule ci: passed check %q has no evidence", check.ID)
		}
	}
	if v.SourceDigest != expected.SourceDigest || v.StoryDigest != expected.StoryDigest || v.EnvironmentDigest != expected.Environment.Digest || v.EnvelopeDigest != expected.Digest {
		return fmt.Errorf("capsule ci: verdict digest mismatch")
	}
	derived := v.Outcome == "passed"
	for _, c := range v.Checks {
		if c.Outcome != "passed" {
			derived = false
		}
	}
	if v.PromotionEligible != derived {
		return fmt.Errorf("capsule ci: promotion eligibility is derived, not caller-controlled")
	}
	if !contractAllowsOutcome(contract, v.Outcome) {
		return fmt.Errorf("capsule ci: verdict outcome %q is outside the pipeline result contract", v.Outcome)
	}
	return nil
}

// NormalizeVerdict makes promotion eligibility a runtime-derived field. Story
// YAML/JSON decodes an omitted bool as false, so validation alone cannot tell
// omission from a caller-supplied false. Every untrusted story boundary calls
// this before the strict validator; persisted receipts still validate the
// normalized value and therefore cannot forge promotion through a true value.
func NormalizeVerdict(v Verdict) Verdict {
	v.PromotionEligible = verdictDerivedPromotion(v)
	return v
}

func validateResultContract(contract ResultContract) error {
	if contract.Schema != "" && contract.Schema != VerdictSchema {
		return fmt.Errorf("capsule ci: result schema %q, want %q", contract.Schema, VerdictSchema)
	}
	seen := map[string]string{}
	for _, group := range []struct {
		name     string
		outcomes []string
	}{
		{name: "pass_exits", outcomes: contract.PassExits},
		{name: "fail_exits", outcomes: contract.FailExits},
		{name: "park_exits", outcomes: contract.ParkExits},
	} {
		for _, outcome := range group.outcomes {
			if outcome != "passed" && outcome != "failed" && outcome != "needs_input" && outcome != "cancelled" && outcome != "infra_failed" {
				return fmt.Errorf("capsule ci: result %s contains invalid outcome %q", group.name, outcome)
			}
			if prior, duplicate := seen[outcome]; duplicate {
				return fmt.Errorf("capsule ci: result outcome %q appears in both %s and %s", outcome, prior, group.name)
			}
			seen[outcome] = group.name
		}
	}
	return nil
}

func contractAllowsOutcome(contract ResultContract, outcome string) bool {
	if len(contract.PassExits) == 0 && len(contract.FailExits) == 0 && len(contract.ParkExits) == 0 {
		return true
	}
	switch outcome {
	case "passed":
		return contains(contract.PassExits, outcome)
	case "needs_input":
		return contains(contract.ParkExits, outcome)
	default:
		return contains(contract.FailExits, outcome)
	}
}

// Launcher is a story adapter. Production adapters start the selected Kitsoki
// story and return its terminal typed artifact; tests use a no-LLM fake.
type Launcher interface {
	Launch(context.Context, executor.Prepared) (Verdict, error)
}

// ExecutorSelector chooses a named checked-in pipeline placement. The selector
// is injected at the front door so stories cannot swap providers or acquire
// remote authority while running.
type ExecutorSelector interface {
	Select(context.Context, string) (executor.Provider, error)
}

type ExecutorSelectorFunc func(context.Context, string) (executor.Provider, error)

func (f ExecutorSelectorFunc) Select(ctx context.Context, name string) (executor.Provider, error) {
	return f(ctx, name)
}

type Service struct {
	ProjectRoot string
	Jobs        artifactjob.Store
	Env         environment.Resolver
	Provider    executor.Provider
	Executors   ExecutorSelector
	Launcher    Launcher
	Hygiene     HygienePlanner
	Observer    RunObserver
	Now         func() time.Time
}

const (
	RunStageRequested  = "requested"
	RunStagePreparing  = "preparing"
	RunStageRunning    = "running"
	RunStageCollecting = "collecting"
	RunStageFinished   = "finished"
	RunStageFailed     = "failed"
)

// RunObservation is a durable lifecycle checkpoint emitted before executor
// side effects, after preparation, after every executor event, and at terminal
// completion. The observer is injected so CLI and MCP front doors can share the
// same persistence without coupling the CI service to a filesystem layout.
type RunObservation struct {
	Result          RunResult `json:"result"`
	DiagnosticError string    `json:"diagnostic_error,omitempty"`
}

type RunObserver interface {
	Observe(context.Context, RunObservation) error
}

type RunObserverFunc func(context.Context, RunObservation) error

func (f RunObserverFunc) Observe(ctx context.Context, observation RunObservation) error {
	return f(ctx, observation)
}

type HygienePlanner interface {
	PlanHygiene(context.Context, CleanupPolicy) (HygieneReport, error)
}
type HygieneReport struct {
	Schema            string `json:"schema,omitempty"`
	Candidates        int    `json:"candidates"`
	TotalBytes        int64  `json:"total_bytes"`
	EvidenceRef       string `json:"evidence_ref,omitempty"`
	DiskKnown         bool   `json:"disk_known,omitempty"`
	DiskCapacityBytes int64  `json:"disk_capacity_bytes,omitempty"`
	DiskFreeBytes     int64  `json:"disk_free_bytes,omitempty"`
	DiskMinimumBytes  int64  `json:"disk_minimum_bytes,omitempty"`
	DiskBelowMinimum  bool   `json:"disk_below_minimum,omitempty"`
}
type HygienePlannerFunc func(context.Context, CleanupPolicy) (HygieneReport, error)

func (f HygienePlannerFunc) PlanHygiene(ctx context.Context, policy CleanupPolicy) (HygieneReport, error) {
	return f(ctx, policy)
}

type RunRequest struct {
	Pipeline         string
	Workspace        control.Handle
	DefinitionDigest string
	SourceDigest     string
	StoryDigest      string
	Trigger          Trigger
}
type RunResult struct {
	Job       artifactjob.Job   `json:"job"`
	Envelope  executor.Envelope `json:"envelope"`
	Verdict   Verdict           `json:"verdict"`
	Execution executor.Result   `json:"execution"`
	Events    []executor.Event  `json:"events,omitempty"`
	Pipeline  string            `json:"pipeline,omitempty"`
	Executor  string            `json:"executor,omitempty"`
	Stage     string            `json:"stage,omitempty"`
	StartedAt time.Time         `json:"started_at,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Terminal  bool              `json:"terminal,omitempty"`
}

func (s Service) Plan(ctx context.Context, req RunRequest) (Pipeline, executor.Envelope, error) {
	root, err := filepath.Abs(s.ProjectRoot)
	if err != nil {
		return Pipeline{}, executor.Envelope{}, err
	}
	s.ProjectRoot = root
	cfg, err := Load(s.ProjectRoot)
	if err != nil {
		return Pipeline{}, executor.Envelope{}, err
	}
	p, ok := cfg.Pipelines[req.Pipeline]
	if !ok {
		return Pipeline{}, executor.Envelope{}, fmt.Errorf("capsule ci: pipeline %q not found", req.Pipeline)
	}
	p.Cleanup = mergeCleanupPolicy(cfg.Cleanup, p.Cleanup)
	if req.Trigger.Kind == "" {
		req.Trigger.Kind = "local"
	}
	if req.Trigger.RequestedPipeline == "" {
		req.Trigger.RequestedPipeline = req.Pipeline
	}
	if req.Trigger.RequestedPipeline != req.Pipeline {
		return Pipeline{}, executor.Envelope{}, fmt.Errorf("capsule ci: trigger requested pipeline %q, run selected %q", req.Trigger.RequestedPipeline, req.Pipeline)
	}
	if !contains(p.Triggers, req.Trigger.Kind) {
		return Pipeline{}, executor.Envelope{}, fmt.Errorf("capsule ci: trigger %q not allowed", req.Trigger.Kind)
	}
	if req.Trigger.Provider == "github" {
		if req.Trigger.Kind != "pull_request" {
			return Pipeline{}, executor.Envelope{}, fmt.Errorf("capsule ci: unsupported GitHub trigger kind %q", req.Trigger.Kind)
		}
		if req.Trigger.HeadSHA != req.SourceDigest {
			return Pipeline{}, executor.Envelope{}, fmt.Errorf("capsule ci: GitHub head sha %q does not match workspace source %q", req.Trigger.HeadSHA, req.SourceDigest)
		}
	}
	envID := p.Environment
	if envID == "" {
		envID = cfg.DefaultEnvironment
	}
	s.Env.ProjectRoot = s.ProjectRoot
	lock, err := s.Env.Resolve(ctx, envID)
	if err != nil {
		return Pipeline{}, executor.Envelope{}, err
	}
	e, err := executor.Seal(executor.Envelope{JobID: "pending", ProjectID: filepath.Base(s.ProjectRoot), DefinitionDigest: req.DefinitionDigest, Instance: req.Workspace, SourceDigest: req.SourceDigest, StoryPath: p.Story, StoryDigest: req.StoryDigest, Environment: lock, Trigger: triggerMap(req.Trigger), Policy: executor.Policy{Network: defaultNetwork(p.Permissions.Network), MinimumSandbox: lock.Sandbox, ExternalWrite: defaultExternal(p.Permissions.ExternalWrite), Agents: executor.AgentPolicy{Policy: defaultAgentPolicy(p.Agents.Policy), Profiles: append([]string(nil), p.Agents.Profiles...), MaxCostUSD: p.Agents.MaxCostUSD, OnUnavailable: p.Agents.OnUnavailable}}})
	return p, e, err
}
func (s Service) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if s.Jobs == nil || (s.Provider == nil && s.Executors == nil) {
		return RunResult{}, fmt.Errorf("capsule ci: jobs and provider are required")
	}
	p, envelope, err := s.Plan(ctx, req)
	if err != nil {
		return RunResult{}, err
	}
	job, err := s.Jobs.Register(ctx, artifactjob.RegisterRequest{AppID: "capsule-ci", Story: envelope.StoryPath, Origin: artifactjob.Origin{Kind: req.Trigger.Kind, Ref: req.Trigger.Ref}, WorkspaceInstanceID: artifactjob.InstanceID(req.Workspace.ID), Owner: "capsule-ci"})
	if err != nil {
		return RunResult{}, err
	}
	result := RunResult{Job: job, Envelope: envelope, Pipeline: req.Pipeline, Executor: p.Executor, StartedAt: job.CreatedAt}
	if result.StartedAt.IsZero() {
		result.StartedAt = s.now()
	}
	envelope.JobID = string(job.ID)
	// Plan seals the provisional "pending" job identity. Clear that digest
	// before deliberately changing the identity; Seal rejects stale digests so
	// providers cannot accidentally accept a mutated envelope as a fresh one.
	envelope.Digest = ""
	sealed, err := executor.Seal(envelope)
	if err != nil {
		result.Envelope = envelope
		return s.failRegistered(ctx, result, err)
	}
	envelope = sealed
	result.Envelope = envelope
	if result, err = s.observe(ctx, result, RunStageRequested, false, nil); err != nil {
		return s.failWithoutObservation(ctx, result, fmt.Errorf("capsule ci: persist requested checkpoint: %w", err))
	}
	provider := s.Provider
	if s.Executors != nil {
		provider, err = s.Executors.Select(ctx, p.Executor)
		if err != nil {
			return s.failRegistered(ctx, result, err)
		}
	}
	if provider == nil {
		return s.failRegistered(ctx, result, fmt.Errorf("capsule ci: no provider for executor %q", p.Executor))
	}
	result, err = s.observe(ctx, result, RunStagePreparing, false, nil)
	if err != nil {
		return s.failWithoutObservation(ctx, result, fmt.Errorf("capsule ci: persist preparing checkpoint: %w", err))
	}
	prepared, err := provider.Prepare(ctx, envelope)
	if err != nil {
		return s.failRegistered(ctx, result, err)
	}
	var verdict Verdict
	var events []executor.Event
	result.Execution.ExecutionID = prepared.ID
	result, err = s.observe(ctx, result, RunStageRunning, false, nil)
	if err != nil {
		return s.failWithoutObservation(ctx, result, fmt.Errorf("capsule ci: persist running checkpoint: %w", err))
	}
	var eventMu sync.Mutex
	var observerErr error
	sink := executor.EventSinkFunc(func(_ context.Context, event executor.Event) error {
		if event.At.IsZero() {
			event.At = s.now()
		}
		eventMu.Lock()
		defer eventMu.Unlock()
		events = append(events, event)
		result.Events = append([]executor.Event(nil), events...)
		checkpoint := result
		if _, persistErr := s.observe(ctx, checkpoint, RunStageRunning, false, nil); persistErr != nil {
			if observerErr == nil {
				observerErr = fmt.Errorf("capsule ci: persist executor event: %w", persistErr)
			}
		}
		return observerErr
	})
	execution, runErr := provider.Run(ctx, prepared, func(ctx context.Context, prepared executor.Prepared) (executor.Result, error) {
		if s.Launcher == nil {
			return executor.Result{}, fmt.Errorf("capsule ci: no local launcher is configured")
		}
		v, e := s.Launcher.Launch(ctx, prepared)
		if e != nil {
			return executor.Result{}, e
		}
		v = NormalizeVerdict(v)
		if e = ValidateVerdict(v, prepared.Envelope, p.Result); e != nil {
			return executor.Result{}, e
		}
		raw, e := json.Marshal(v)
		if e != nil {
			return executor.Result{}, e
		}
		verdict = v
		return executor.Result{VerdictArtifact: "verdict:" + hashVerdict(v), VerdictJSON: raw}, nil
	}, sink)
	eventMu.Lock()
	result.Events = append([]executor.Event(nil), events...)
	eventMu.Unlock()
	result.Execution = execution
	if result.Execution.ExecutionID == "" {
		result.Execution.ExecutionID = prepared.ID
	}
	eventMu.Lock()
	persistErr := observerErr
	eventMu.Unlock()
	if runErr == nil && persistErr != nil {
		runErr = persistErr
	}
	if runErr == nil && len(execution.VerdictJSON) > 0 {
		if err := json.Unmarshal(execution.VerdictJSON, &verdict); err != nil {
			runErr = fmt.Errorf("capsule ci: parse executor verdict: %w", err)
		}
		verdict = NormalizeVerdict(verdict)
	}
	if runErr == nil {
		verdict, runErr = s.applyHygienePolicy(ctx, p, verdict)
		if runErr == nil {
			raw, e := json.Marshal(verdict)
			if e != nil {
				runErr = e
			} else {
				execution.VerdictJSON = raw
				execution.VerdictArtifact = "verdict:" + hashVerdict(verdict)
			}
		}
	}
	if runErr == nil {
		if err := ValidateVerdict(verdict, prepared.Envelope, p.Result); err != nil {
			runErr = err
		}
	}
	status := artifactjob.StatusDone
	if verdict.Outcome == "infra_failed" {
		status = artifactjob.StatusFailed
	}
	if verdict.Outcome == "needs_input" {
		status = artifactjob.StatusAwaitingInput
	}
	result.Verdict = verdict
	if runErr != nil {
		return s.failRegistered(ctx, result, runErr)
	}
	summary := verdict.Summary
	updated, updateErr := s.Jobs.Update(ctx, job.ID, artifactjob.Update{Status: &status, Summary: &summary, TerminalArtifactHandle: &execution.VerdictArtifact})
	if updateErr != nil {
		return s.failRegistered(ctx, result, fmt.Errorf("capsule ci: update terminal job: %w", updateErr))
	}
	result.Job = updated
	result, observeErr := s.observe(ctx, result, RunStageFinished, true, nil)
	if observeErr != nil {
		return result, fmt.Errorf("capsule ci: persist terminal checkpoint: %w", observeErr)
	}
	return result, nil
}

func (s Service) failRegistered(ctx context.Context, result RunResult, runErr error) (RunResult, error) {
	status := artifactjob.StatusFailed
	stage := RunStageFailed
	if errors.Is(runErr, context.Canceled) {
		status = artifactjob.StatusCancelled
		stage = RunStageFinished
		result.Verdict = cancelledVerdict(result, runErr)
	}
	summary := runErr.Error()
	result.Job.Status = status
	if result.Job.ID != "" {
		if updated, err := s.Jobs.Update(ctx, result.Job.ID, artifactjob.Update{Status: &status, Summary: &summary}); err == nil {
			result.Job = updated
		} else {
			runErr = fmt.Errorf("%w (also failed to update job: %v)", runErr, err)
		}
	}
	observed, observeErr := s.observe(ctx, result, stage, true, runErr)
	if observeErr != nil {
		return observed, fmt.Errorf("%w (also failed to persist terminal checkpoint: %v)", runErr, observeErr)
	}
	return observed, runErr
}

func (s Service) failWithoutObservation(ctx context.Context, result RunResult, runErr error) (RunResult, error) {
	status := artifactjob.StatusFailed
	stage := RunStageFailed
	if errors.Is(runErr, context.Canceled) {
		status = artifactjob.StatusCancelled
		stage = RunStageFinished
		result.Verdict = cancelledVerdict(result, runErr)
	}
	summary := runErr.Error()
	result.Job.Status = status
	if result.Job.ID != "" {
		if updated, err := s.Jobs.Update(ctx, result.Job.ID, artifactjob.Update{Status: &status, Summary: &summary}); err == nil {
			result.Job = updated
		}
	}
	result.Stage, result.Terminal, result.UpdatedAt = stage, true, s.now()
	return result, runErr
}

func cancelledVerdict(result RunResult, cause error) Verdict {
	summary := "Capsule CI execution was cancelled."
	if cause != nil && cause.Error() != "" {
		summary = cause.Error()
	}
	return Verdict{
		Schema:            VerdictSchema,
		Pipeline:          result.Pipeline,
		Outcome:           "cancelled",
		Summary:           summary,
		Checks:            []Check{},
		PromotionEligible: false,
		SourceDigest:      result.Envelope.SourceDigest,
		StoryDigest:       result.Envelope.StoryDigest,
		EnvironmentDigest: result.Envelope.Environment.Digest,
		EnvelopeDigest:    result.Envelope.Digest,
	}
}

func (s Service) observe(ctx context.Context, result RunResult, stage string, terminal bool, runErr error) (RunResult, error) {
	result.Stage = stage
	result.Terminal = terminal
	result.UpdatedAt = s.now()
	if result.StartedAt.IsZero() {
		result.StartedAt = result.UpdatedAt
	}
	if s.Observer == nil {
		return result, nil
	}
	diagnostic := ""
	if runErr != nil {
		diagnostic = runErr.Error()
	}
	return result, s.Observer.Observe(ctx, RunObservation{Result: result, DiagnosticError: diagnostic})
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func contains(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}

func defaultAgentPolicy(value string) string {
	if value == "" {
		return "deny"
	}
	return value
}
func defaultNetwork(v string) string {
	if v == "" {
		return "none"
	}
	return v
}
func defaultExternal(v string) string {
	if v == "" {
		return "deny"
	}
	return v
}
func triggerMap(t Trigger) map[string]any {
	raw, _ := json.Marshal(t)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
func hashVerdict(v Verdict) string {
	v.PromotionEligible = false
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func (s Service) applyHygienePolicy(ctx context.Context, p Pipeline, v Verdict) (Verdict, error) {
	policy := p.Cleanup
	if !policy.RequireHygieneCheck && policy.MaxReclaimableBytes <= 0 {
		return v, nil
	}
	if s.Hygiene == nil {
		return v, fmt.Errorf("capsule ci: hygiene check required but no hygiene planner is configured")
	}
	report, err := s.Hygiene.PlanHygiene(ctx, policy)
	if err != nil {
		return v, err
	}
	outcome := "passed"
	summary := fmt.Sprintf("hygiene within policy: %d reclaimable byte(s) across %d candidate(s)", report.TotalBytes, report.Candidates)
	if policy.MaxReclaimableBytes > 0 && report.TotalBytes > policy.MaxReclaimableBytes {
		outcome = "failed"
		summary = fmt.Sprintf("hygiene exceeds policy: %d reclaimable byte(s) across %d candidate(s), max %d", report.TotalBytes, report.Candidates, policy.MaxReclaimableBytes)
	}
	evidence := []string{}
	if report.EvidenceRef != "" {
		evidence = append(evidence, report.EvidenceRef)
	}
	v.Checks = replaceCheck(v.Checks, Check{ID: "capsule-hygiene", Kind: "hygiene", Outcome: outcome, Evidence: evidence, DecisionRef: summary})
	v.PromotionEligible = verdictDerivedPromotion(v)
	if outcome != "passed" && v.Outcome == "passed" {
		v.Outcome = "failed"
		if v.Summary == "" {
			v.Summary = summary
		}
	}
	return v, nil
}
func replaceCheck(checks []Check, check Check) []Check {
	out := make([]Check, 0, len(checks)+1)
	replaced := false
	for _, existing := range checks {
		if existing.ID == check.ID {
			out = append(out, check)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, check)
	}
	return out
}
func verdictDerivedPromotion(v Verdict) bool {
	if v.Outcome != "passed" {
		return false
	}
	for _, c := range v.Checks {
		if c.Outcome != "passed" {
			return false
		}
	}
	return true
}
func mergeCleanupPolicy(base, override CleanupPolicy) CleanupPolicy {
	out := base
	if override.KeepRuns != 0 {
		out.KeepRuns = override.KeepRuns
	}
	if override.MaxReclaimableBytes != 0 {
		out.MaxReclaimableBytes = override.MaxReclaimableBytes
	}
	if override.RequireHygieneCheck {
		out.RequireHygieneCheck = true
	}
	if override.IncludeCapsuleCache {
		out.IncludeCapsuleCache = true
	}
	if override.IncludeGoBuildCache {
		out.IncludeGoBuildCache = true
	}
	return out
}
func validateCleanupPolicy(label string, policy CleanupPolicy) error {
	if policy.KeepRuns < 0 {
		return fmt.Errorf("capsule ci %s: keep_runs must be >= 0", label)
	}
	if policy.MaxReclaimableBytes < 0 {
		return fmt.Errorf("capsule ci %s: max_reclaimable_bytes must be >= 0", label)
	}
	return nil
}
func isBuiltinExecutor(name string) bool {
	switch name {
	case "", "host", "local", "remote-fake", "container", "container-fake":
		return true
	default:
		return false
	}
}
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || ('A' <= r && r <= 'Z') || (i > 0 && '0' <= r && r <= '9') {
			continue
		}
		return false
	}
	return true
}

var _ = sort.Strings
