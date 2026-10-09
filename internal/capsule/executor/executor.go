package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
)

const EnvelopeSchema = "capsule-execution-envelope/v1"
const ExecutionStatusSchema = "capsule-execution-status/v1"

type Capabilities struct {
	ID              string   `json:"id"`
	Placements      []string `json:"placements"`
	Isolation       string   `json:"isolation"`
	Networks        []string `json:"networks"`
	EnvironmentRefs []string `json:"environment_refs,omitempty"`
	Cancellable     bool     `json:"cancellable"`
}
type Policy struct {
	Network        string      `json:"network"`
	MinimumSandbox string      `json:"minimum_sandbox"`
	ExternalWrite  string      `json:"external_write"`
	Agents         AgentPolicy `json:"agents"`
}
type AgentPolicy struct {
	Policy        string   `json:"policy"`
	Profiles      []string `json:"profiles,omitempty"`
	MaxCostUSD    float64  `json:"max_cost_usd,omitempty"`
	OnUnavailable string   `json:"on_unavailable,omitempty"`
}
type Envelope struct {
	Schema           string           `json:"schema"`
	JobID            string           `json:"job_id"`
	ProjectID        string           `json:"project_id"`
	DefinitionDigest string           `json:"definition_digest"`
	Instance         control.Handle   `json:"instance"`
	SourceDigest     string           `json:"source_digest"`
	StoryPath        string           `json:"story_path"`
	StoryDigest      string           `json:"story_digest"`
	Environment      environment.Lock `json:"environment"`
	Trigger          map[string]any   `json:"trigger"`
	Policy           Policy           `json:"policy"`
	Digest           string           `json:"digest"`
}
type Event struct {
	Kind           string         `json:"kind"`
	At             time.Time      `json:"at"`
	EnvelopeDigest string         `json:"envelope_digest"`
	ExecutionID    string         `json:"execution_id,omitempty"`
	Outcome        string         `json:"outcome,omitempty"`
	Error          string         `json:"error,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
}
type EventSink interface {
	Emit(context.Context, Event) error
}
type EventSinkFunc func(context.Context, Event) error

func (f EventSinkFunc) Emit(ctx context.Context, e Event) error { return f(ctx, e) }

type Prepared struct {
	ID        string   `json:"id"`
	Envelope  Envelope `json:"envelope"`
	Placement string   `json:"placement"`
	Applied   Policy   `json:"applied_policy"`
}
type Result struct {
	ExecutionID     string            `json:"execution_id"`
	ExitCode        int               `json:"exit_code"`
	VerdictArtifact string            `json:"verdict_artifact,omitempty"`
	VerdictJSON     []byte            `json:"verdict_json,omitempty"`
	Artifacts       []string          `json:"artifacts,omitempty"`
	Provider        map[string]string `json:"provider,omitempty"`
}

// ExecutionStatus is the provider-neutral observation returned by a durable
// executor after submission. A cancellation request is intentionally
// non-terminal until the worker reports cancelled.
type ExecutionStatus struct {
	Schema         string                    `json:"schema"`
	ExecutionID    string                    `json:"execution_id"`
	EnvelopeDigest string                    `json:"envelope_digest,omitempty"`
	RequestID      string                    `json:"request_id,omitempty"`
	Status         string                    `json:"status"`
	Stage          string                    `json:"stage,omitempty"`
	StartedAt      time.Time                 `json:"started_at,omitempty"`
	UpdatedAt      time.Time                 `json:"updated_at,omitempty"`
	TerminalAt     time.Time                 `json:"terminal_at,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Events         []Event                   `json:"events,omitempty"`
	Result         Result                    `json:"result,omitempty"`
	Agent          *AgentDiagnostics         `json:"agent,omitempty"`
	Cleanup        *WorkerCleanupDiagnostics `json:"cleanup,omitempty"`
}

func (s ExecutionStatus) Terminal() bool {
	return s.Status == "completed" || s.Status == "failed" || s.Status == "cancelled"
}

// ExecutionError preserves the worker's durable terminal record when the
// execution did not complete successfully. Callers can diagnose the exact
// worker stage/timeline without scraping a transport error body.
type ExecutionError struct {
	Execution ExecutionStatus
	Cause     error
}

func (e ExecutionError) Error() string {
	message := e.Execution.Error
	if message == "" {
		message = "remote execution " + e.Execution.Status
	}
	if e.Execution.Stage != "" {
		return fmt.Sprintf("capsule executor: execution %s failed at %s: %s", e.Execution.ExecutionID, e.Execution.Stage, message)
	}
	return fmt.Sprintf("capsule executor: execution %s failed: %s", e.Execution.ExecutionID, message)
}

func (e ExecutionError) Unwrap() error { return e.Cause }

// ExecutionController is an optional durable-provider extension used by
// status/cancel front doors. Provider.Cancel remains for compatibility with
// one-shot adapters; callers that need truthful two-phase cancellation require
// this interface.
type ExecutionController interface {
	Status(context.Context, string) (ExecutionStatus, error)
	RequestCancel(context.Context, string) (ExecutionStatus, error)
}

type Task func(context.Context, Prepared) (Result, error)
type Provider interface {
	Describe(context.Context) (Capabilities, error)
	Prepare(context.Context, Envelope) (Prepared, error)
	Run(context.Context, Prepared, Task, EventSink) (Result, error)
	Cancel(context.Context, string) error
}

func Seal(e Envelope) (Envelope, error) {
	if e.Schema == "" {
		e.Schema = EnvelopeSchema
	}
	if e.Schema != EnvelopeSchema {
		return Envelope{}, fmt.Errorf("capsule executor: schema %q", e.Schema)
	}
	if strings.TrimSpace(e.JobID) == "" || strings.TrimSpace(e.ProjectID) == "" {
		return Envelope{}, fmt.Errorf("capsule executor: job and project are required")
	}
	if strings.TrimSpace(e.DefinitionDigest) == "" {
		return Envelope{}, fmt.Errorf("capsule executor: capsule definition digest is required")
	}
	if strings.TrimSpace(e.Instance.ID) == "" || e.Instance.Generation == 0 {
		return Envelope{}, fmt.Errorf("capsule executor: workspace id and generation are required")
	}
	if strings.TrimSpace(e.SourceDigest) == "" {
		return Envelope{}, fmt.Errorf("capsule executor: source digest is required")
	}
	storyPath := filepath.Clean(strings.TrimSpace(e.StoryPath))
	if storyPath == "." || filepath.IsAbs(storyPath) || storyPath == ".." || strings.HasPrefix(storyPath, ".."+string(filepath.Separator)) {
		return Envelope{}, fmt.Errorf("capsule executor: story path must be project-relative")
	}
	if storyPath != e.StoryPath {
		return Envelope{}, fmt.Errorf("capsule executor: story path must be normalized")
	}
	if strings.TrimSpace(e.StoryDigest) == "" {
		return Envelope{}, fmt.Errorf("capsule executor: story digest is required")
	}
	if err := environment.ValidateLock(e.Environment); err != nil {
		return Envelope{}, fmt.Errorf("capsule executor: environment lock: %w", err)
	}
	policy, err := normalizeAndValidatePolicy(e.Policy, e.Environment)
	if err != nil {
		return Envelope{}, err
	}
	e.Policy = policy
	providedDigest := e.Digest
	e.Digest = ""
	raw, err := json.Marshal(e)
	if err != nil {
		return Envelope{}, err
	}
	sum := sha256.Sum256(raw)
	e.Digest = "sha256:" + hex.EncodeToString(sum[:])
	if providedDigest != "" && providedDigest != e.Digest {
		return Envelope{}, fmt.Errorf("capsule executor: envelope digest mismatch")
	}
	return e, nil
}

// ValidatePrepared verifies the immutable parts of a provider-prepared
// execution before a worker accepts it. Provider-specific identifiers may add
// stricter syntax checks at their persistence boundary.
func ValidatePrepared(prepared Prepared) (Prepared, error) {
	if strings.TrimSpace(prepared.ID) == "" {
		return Prepared{}, fmt.Errorf("capsule executor: prepared execution id is required")
	}
	if strings.TrimSpace(prepared.Placement) == "" {
		return Prepared{}, fmt.Errorf("capsule executor: prepared placement is required")
	}
	if prepared.Envelope.Digest == "" {
		return Prepared{}, fmt.Errorf("capsule executor: prepared envelope must already be sealed")
	}
	sealed, err := Seal(prepared.Envelope)
	if err != nil {
		return Prepared{}, err
	}
	if !reflect.DeepEqual(prepared.Applied, sealed.Policy) {
		return Prepared{}, fmt.Errorf("capsule executor: applied policy does not match sealed policy")
	}
	prepared.Envelope = sealed
	return prepared, nil
}

// ValidateCapabilities enforces the sealed network and minimum-isolation
// policy at the scheduling boundary. Doctor calls this for preflight, but each
// provider and worker must call it too because preflight is not an authority
// boundary and may be skipped by API clients.
func ValidateCapabilities(capabilities Capabilities, policy Policy) error {
	if strings.TrimSpace(capabilities.ID) == "" || len(capabilities.Placements) == 0 {
		return fmt.Errorf("capsule executor: capabilities require an id and placement")
	}
	for _, placement := range capabilities.Placements {
		if placement == "" || placement != strings.TrimSpace(placement) {
			return fmt.Errorf("capsule executor: capabilities contain an invalid placement")
		}
	}
	for _, network := range capabilities.Networks {
		if network != "none" && network != "replay" && network != "live" {
			return fmt.Errorf("capsule executor: capabilities contain invalid network %q", network)
		}
	}
	if !supports(capabilities.Networks, policy.Network) {
		return fmt.Errorf("capsule executor: provider %s cannot satisfy network %s", capabilities.ID, policy.Network)
	}
	rank := map[string]int{"none": 0, "supervised": 1, "process": 1, "container": 2, "vm": 3, "hermetic": 4}
	got := strings.ToLower(strings.TrimSpace(capabilities.Isolation))
	want := strings.ToLower(strings.TrimSpace(policy.MinimumSandbox))
	gotRank, gotOK := rank[got]
	wantRank, wantOK := rank[want]
	if !gotOK {
		return fmt.Errorf("capsule executor: provider %s advertises unknown isolation %q", capabilities.ID, capabilities.Isolation)
	}
	if !wantOK {
		return fmt.Errorf("capsule executor: invalid minimum sandbox %q", policy.MinimumSandbox)
	}
	if gotRank < wantRank {
		return fmt.Errorf("capsule executor: provider %s isolation %s is below required sandbox %s", capabilities.ID, capabilities.Isolation, policy.MinimumSandbox)
	}
	return nil
}

func normalizeAndValidatePolicy(policy Policy, lock environment.Lock) (Policy, error) {
	if policy.Network == "" {
		policy.Network = "none"
	}
	if policy.MinimumSandbox == "" {
		policy.MinimumSandbox = lock.Sandbox
	}
	if policy.ExternalWrite == "" {
		policy.ExternalWrite = "deny"
	}
	if policy.Agents.Policy == "" {
		policy.Agents.Policy = "deny"
	}
	if policy.Network != "none" && policy.Network != "replay" && policy.Network != "live" {
		return Policy{}, fmt.Errorf("capsule executor: invalid network policy %q", policy.Network)
	}
	if policy.Network != lock.Network {
		return Policy{}, fmt.Errorf("capsule executor: network policy %q does not match environment lock %q", policy.Network, lock.Network)
	}
	if policy.MinimumSandbox != lock.Sandbox {
		return Policy{}, fmt.Errorf("capsule executor: minimum sandbox %q does not match environment lock %q", policy.MinimumSandbox, lock.Sandbox)
	}
	if policy.ExternalWrite != "deny" && policy.ExternalWrite != "allow" {
		return Policy{}, fmt.Errorf("capsule executor: invalid external-write policy %q", policy.ExternalWrite)
	}
	// V1 can prove external-write denial only when egress is denied or routed
	// through a supervised replay boundary. A live network plus "deny" would be
	// a requested posture presented as an applied guarantee.
	if policy.Network == "live" && policy.ExternalWrite == "deny" {
		return Policy{}, fmt.Errorf("capsule executor: external-write deny cannot be enforced with live network")
	}
	switch policy.Agents.Policy {
	case "deny":
		if len(policy.Agents.Profiles) != 0 || policy.Agents.MaxCostUSD != 0 || policy.Agents.OnUnavailable != "" {
			return Policy{}, fmt.Errorf("capsule executor: denied agents cannot carry profiles, budget, or fallback")
		}
	case "allow":
		if len(policy.Agents.Profiles) == 0 || policy.Agents.MaxCostUSD <= 0 || math.IsNaN(policy.Agents.MaxCostUSD) || math.IsInf(policy.Agents.MaxCostUSD, 0) {
			return Policy{}, fmt.Errorf("capsule executor: allowed agents require profiles and a finite positive budget")
		}
		if policy.Agents.OnUnavailable != "needs_input" && policy.Agents.OnUnavailable != "infra_failed" && policy.Agents.OnUnavailable != "failed" {
			return Policy{}, fmt.Errorf("capsule executor: invalid agent-unavailable fallback %q", policy.Agents.OnUnavailable)
		}
		seen := map[string]bool{}
		for _, profile := range policy.Agents.Profiles {
			if profile == "" || profile != strings.TrimSpace(profile) || seen[profile] {
				return Policy{}, fmt.Errorf("capsule executor: agent profiles must be unique normalized names")
			}
			seen[profile] = true
		}
	default:
		return Policy{}, fmt.Errorf("capsule executor: invalid agent policy %q", policy.Agents.Policy)
	}
	return policy, nil
}

// FakeProvider proves local/remote parity without a network, container, or
// model. It records the exact immutable envelope supplied to Prepare.
type FakeProvider struct {
	Cap       Capabilities
	Placement string
	mu        sync.Mutex
	prepared  map[string]Prepared
	Cancelled map[string]bool
}

func NewFakeProvider(id string) *FakeProvider {
	return &FakeProvider{Cap: Capabilities{ID: id, Placements: []string{"fake"}, Isolation: "supervised", Networks: []string{"none", "replay"}, Cancellable: true}, Placement: "fake", prepared: map[string]Prepared{}, Cancelled: map[string]bool{}}
}
func (p *FakeProvider) Describe(context.Context) (Capabilities, error) { return p.Cap, nil }
func (p *FakeProvider) Prepare(ctx context.Context, e Envelope) (Prepared, error) {
	sealed, err := Seal(e)
	if err != nil {
		return Prepared{}, err
	}
	if err := ValidateCapabilities(p.Cap, sealed.Policy); err != nil {
		return Prepared{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.prepared[sealed.Digest]; existing.ID != "" {
		return existing, nil
	}
	out := Prepared{ID: "fake-" + sealed.Digest[len(sealed.Digest)-12:], Envelope: sealed, Placement: p.Placement, Applied: sealed.Policy}
	p.prepared[sealed.Digest] = out
	return out, nil
}
func (p *FakeProvider) Run(ctx context.Context, prepared Prepared, task Task, sink EventSink) (Result, error) {
	if sink != nil {
		_ = sink.Emit(ctx, Event{Kind: "capsule.executor.started", At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: "running"})
	}
	if task == nil {
		return Result{}, errors.New("capsule executor: task is required")
	}
	result, err := task(ctx, prepared)
	result.ExecutionID = prepared.ID
	sort.Strings(result.Artifacts)
	if sink != nil {
		kind := "capsule.executor.finished"
		if err != nil {
			kind = "capsule.executor.failed"
		}
		outcome := "passed"
		errorText := ""
		if err != nil {
			outcome = "failed"
			errorText = err.Error()
		}
		_ = sink.Emit(ctx, Event{Kind: kind, At: time.Now().UTC(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: outcome, Error: errorText})
	}
	return result, err
}
func (p *FakeProvider) Cancel(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Cancelled[id] = true
	return nil
}
func supports(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

var _ = time.Now // kept available for provider implementations that report attempt timing.
