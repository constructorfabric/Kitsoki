package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// PolicySchemaVersion versions effective policy hashing and evaluation rules.
const PolicySchemaVersion = "v1"

// EvidenceRequirement identifies evidence required before strict close. A zero
// MaxAgeSeconds accepts any recorded evidence of that kind; positive values
// require evidence captured within that age at close time.
type EvidenceRequirement struct {
	Kind          string `json:"kind"`
	MaxAgeSeconds int64  `json:"max_age_seconds,omitempty"`
}

// PolicyProfile determines what an objective may authorize. Strict profiles
// require an open objective for mutation and fresh evidence for close.
type PolicyProfile struct {
	SchemaVersion        string                `json:"schema_version,omitempty"`
	Name                 string                `json:"name"`
	Strict               bool                  `json:"strict"`
	RequiredEvidence     []EvidenceRequirement `json:"required_evidence,omitempty"`
	AllowUnlimitedBudget bool                  `json:"allow_unlimited_budget,omitempty"`
}

// EffectiveHash is stable across equivalent policy declarations. It is copied
// into evidence and receipts so an evaluator can detect stale policy evidence.
func (p PolicyProfile) EffectiveHash() string {
	p = p.normalized()
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (p PolicyProfile) normalized() PolicyProfile {
	if p.SchemaVersion == "" {
		p.SchemaVersion = PolicySchemaVersion
	}
	p.RequiredEvidence = append([]EvidenceRequirement(nil), p.RequiredEvidence...)
	sort.Slice(p.RequiredEvidence, func(i, j int) bool {
		if p.RequiredEvidence[i].Kind == p.RequiredEvidence[j].Kind {
			return p.RequiredEvidence[i].MaxAgeSeconds < p.RequiredEvidence[j].MaxAgeSeconds
		}
		return p.RequiredEvidence[i].Kind < p.RequiredEvidence[j].Kind
	})
	return p
}

func (p PolicyProfile) clone() PolicyProfile { return p.normalized() }

// Evidence is an observed result used for a future close decision. Evidence is
// additive: a retry records another item rather than overwriting prior proof.
type Evidence struct {
	SchemaVersion string    `json:"schema_version"`
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Summary       string    `json:"summary"`
	CapturedAt    time.Time `json:"captured_at"`
	PolicyHash    string    `json:"policy_hash"`
}

// RecordEvidenceInput is the typed contract for attaching observed proof.
type RecordEvidenceInput struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
}

// PolicyDecision is returned for every authorization attempt. A denied decision
// is evidence too: it lets callers return an actionable machine-readable reason.
type PolicyDecision struct {
	Allowed    bool   `json:"allowed"`
	Code       string `json:"code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	PolicyHash string `json:"policy_hash"`
}

const (
	ErrPolicyDenied       = "POLICY_DENIED"
	ErrObjectiveRequired  = "OBJECTIVE_REQUIRED"
	ErrEvidenceRequired   = "EVIDENCE_REQUIRED"
	ErrEvidenceStale      = "EVIDENCE_STALE"
	ErrPolicyBudgetClosed = "BUDGET_EXHAUSTED"
)

var ErrCloseEvidence = errors.New("objective close lacks required fresh evidence")

// PolicyViolationError preserves the machine-readable close decision for MCP
// callers while still allowing errors.Is(err, ErrCloseEvidence) in Go callers.
type PolicyViolationError struct{ Decision PolicyDecision }

func (e PolicyViolationError) Error() string { return e.Decision.Reason }
func (e PolicyViolationError) Unwrap() error { return ErrCloseEvidence }

// ObjectiveService owns objective lifecycle and policy evaluation. The Store
// and Clock are injected so replay tests are deterministic and no live agent or
// provider is ever necessary.
type ObjectiveService struct {
	store ObjectiveStore
	now   func() time.Time
	mu    sync.Mutex
}

// NewObjectiveService builds the lifecycle service over an injected store.
func NewObjectiveService(store ObjectiveStore, now func() time.Time) (*ObjectiveService, error) {
	if store == nil {
		return nil, errors.New("objective store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &ObjectiveService{store: store, now: now}, nil
}

func (s *ObjectiveService) Open(ctx context.Context, input OpenObjectiveInput) (Objective, Receipt, error) {
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Goal) == "" || len(input.Acceptance) == 0 {
		return Objective{}, Receipt{}, errors.New("objective id, goal, and acceptance are required")
	}
	if input.Budget.MaxMutations < 0 {
		return Objective{}, Receipt{}, errors.New("max_mutations cannot be negative")
	}
	policy := input.Policy.normalized()
	if policy.Name == "" {
		return Objective{}, Receipt{}, errors.New("policy name is required")
	}
	now := s.now().UTC()
	objective := Objective{
		SchemaVersion: ObjectiveSchemaVersion, ID: input.ID, Goal: input.Goal,
		Acceptance: append([]string(nil), input.Acceptance...), Constraints: append([]string(nil), input.Constraints...),
		Budget: input.Budget, Policy: policy, Status: ObjectiveOpen, Revision: 1, OpenedAt: now, UpdatedAt: now,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.Create(ctx, objective); err != nil {
		return Objective{}, Receipt{}, err
	}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptObjectiveOpened, map[string]string{"goal": objective.Goal})
	if err != nil {
		return Objective{}, Receipt{}, err
	}
	return objective, receipt, nil
}

func (s *ObjectiveService) Get(ctx context.Context, id string) (Objective, error) {
	return s.store.Get(ctx, id)
}

// Update changes an open objective and emits a new receipt; it never rewrites
// prior receipts or changes the objective identity.
func (s *ObjectiveService) Update(ctx context.Context, id string, input UpdateObjectiveInput) (Objective, Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objective, err := s.store.Get(ctx, id)
	if err != nil {
		return Objective{}, Receipt{}, err
	}
	if objective.Status != ObjectiveOpen {
		return Objective{}, Receipt{}, ErrObjectiveClosed
	}
	if input.Goal != "" {
		objective.Goal = input.Goal
	}
	if input.Acceptance != nil {
		objective.Acceptance = append([]string(nil), input.Acceptance...)
	}
	if input.Constraints != nil {
		objective.Constraints = append([]string(nil), input.Constraints...)
	}
	if input.Budget != nil {
		if input.Budget.MaxMutations < objective.Budget.MutationsUsed {
			return Objective{}, Receipt{}, errors.New("budget cannot be below mutations already used")
		}
		objective.Budget = *input.Budget
	}
	objective.Revision++
	objective.UpdatedAt = s.now().UTC()
	if err := s.store.Save(ctx, objective); err != nil {
		return Objective{}, Receipt{}, err
	}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptObjectiveUpdated, nil)
	return objective, receipt, err
}

// Reopen explicitly starts a new authority epoch after close. It does not erase
// receipts, evidence, or budget consumption from the earlier epoch.
func (s *ObjectiveService) Reopen(ctx context.Context, id string) (Objective, Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objective, err := s.store.Get(ctx, id)
	if err != nil {
		return Objective{}, Receipt{}, err
	}
	if objective.Status == ObjectiveOpen {
		return Objective{}, Receipt{}, ErrObjectiveOpen
	}
	objective.Status = ObjectiveOpen
	objective.ClosedAt = nil
	objective.Revision++
	objective.UpdatedAt = s.now().UTC()
	if err := s.store.Save(ctx, objective); err != nil {
		return Objective{}, Receipt{}, err
	}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptObjectiveReopened, nil)
	return objective, receipt, err
}

// RecordEvidence attaches policy-hashed evidence without changing an objective
// status. It is intentionally valid after close for audit corrections, while a
// subsequent close after Reopen still evaluates freshness at that later time.
func (s *ObjectiveService) RecordEvidence(ctx context.Context, objectiveID string, input RecordEvidenceInput) (Evidence, Receipt, error) {
	if strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Summary) == "" {
		return Evidence{}, Receipt{}, errors.New("evidence id, kind, and summary are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	objective, err := s.store.Get(ctx, objectiveID)
	if err != nil {
		return Evidence{}, Receipt{}, err
	}
	evidence := Evidence{SchemaVersion: PolicySchemaVersion, ID: input.ID, Kind: input.Kind, Summary: input.Summary, CapturedAt: s.now().UTC(), PolicyHash: objective.Policy.EffectiveHash()}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptEvidenceRecorded, evidence)
	return evidence, receipt, err
}

// AuthorizeMutation checks the strict open-objective policy and consumes one
// configured mutation allowance only after authorization succeeds.
func (s *ObjectiveService) AuthorizeMutation(ctx context.Context, id string) (PolicyDecision, Objective, Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objective, err := s.store.Get(ctx, id)
	if err != nil {
		return PolicyDecision{Allowed: false, Code: ErrObjectiveRequired, Reason: err.Error()}, Objective{}, Receipt{}, err
	}
	decision := evaluateMutation(objective)
	if !decision.Allowed {
		_, _ = s.appendReceipt(ctx, objective, ReceiptMutationDenied, decision)
		return decision, objective, Receipt{}, nil
	}
	if objective.Budget.MaxMutations > 0 {
		objective.Budget.MutationsUsed++
	}
	objective.Revision++
	objective.UpdatedAt = s.now().UTC()
	if err := s.store.Save(ctx, objective); err != nil {
		return PolicyDecision{}, Objective{}, Receipt{}, err
	}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptMutationAuthorized, decision)
	return decision, objective, receipt, err
}

// Close performs the strict evidence freshness check before changing status.
func (s *ObjectiveService) Close(ctx context.Context, id string) (Objective, Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objective, err := s.store.Get(ctx, id)
	if err != nil {
		return Objective{}, Receipt{}, err
	}
	if objective.Status != ObjectiveOpen {
		return Objective{}, Receipt{}, ErrObjectiveClosed
	}
	receipts, err := s.store.ListReceipts(ctx, id)
	if err != nil {
		return Objective{}, Receipt{}, err
	}
	if decision := evaluateClose(objective, receipts, s.now().UTC()); !decision.Allowed {
		_, _ = s.appendReceipt(ctx, objective, ReceiptCloseDenied, decision)
		return Objective{}, Receipt{}, PolicyViolationError{Decision: decision}
	}
	now := s.now().UTC()
	objective.Status = ObjectiveClosed
	objective.ClosedAt = &now
	objective.Revision++
	objective.UpdatedAt = now
	if err := s.store.Save(ctx, objective); err != nil {
		return Objective{}, Receipt{}, err
	}
	receipt, err := s.appendReceipt(ctx, objective, ReceiptObjectiveClosed, nil)
	return objective, receipt, err
}

func evaluateMutation(objective Objective) PolicyDecision {
	decision := PolicyDecision{Allowed: true, PolicyHash: objective.Policy.EffectiveHash()}
	if objective.Policy.Strict && objective.Status != ObjectiveOpen {
		return PolicyDecision{Code: ErrObjectiveRequired, Reason: "strict policy requires an open objective", PolicyHash: decision.PolicyHash}
	}
	if objective.Budget.MaxMutations > 0 && objective.Budget.MutationsUsed >= objective.Budget.MaxMutations {
		return PolicyDecision{Code: ErrPolicyBudgetClosed, Reason: "objective mutation budget is exhausted", PolicyHash: decision.PolicyHash}
	}
	return decision
}

func evaluateClose(objective Objective, receipts []Receipt, now time.Time) PolicyDecision {
	decision := PolicyDecision{Allowed: true, PolicyHash: objective.Policy.EffectiveHash()}
	if !objective.Policy.Strict {
		return decision
	}
	for _, requirement := range objective.Policy.RequiredEvidence {
		fresh := false
		seen := false
		for _, receipt := range receipts {
			if receipt.Type != ReceiptEvidenceRecorded || receipt.Evidence == nil || receipt.Evidence.Kind != requirement.Kind {
				continue
			}
			evidence := *receipt.Evidence
			seen = true
			if evidence.PolicyHash != decision.PolicyHash {
				continue
			}
			if requirement.MaxAgeSeconds == 0 || !evidence.CapturedAt.Add(time.Duration(requirement.MaxAgeSeconds)*time.Second).Before(now) {
				fresh = true
				break
			}
		}
		if fresh {
			continue
		}
		if seen {
			return PolicyDecision{Code: ErrEvidenceStale, Reason: "required evidence is stale or was recorded under another policy", PolicyHash: decision.PolicyHash}
		}
		return PolicyDecision{Code: ErrEvidenceRequired, Reason: "required evidence is absent: " + requirement.Kind, PolicyHash: decision.PolicyHash}
	}
	return decision
}
