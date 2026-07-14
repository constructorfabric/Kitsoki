package studio

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ObjectiveSchemaVersion versions persisted objective records. Keep this
// explicit: receipts are long-lived evidence and must be readable after the
// operating policy evolves.
const ObjectiveSchemaVersion = "v1"

// ObjectiveStatus records whether an objective may authorize mutations.
type ObjectiveStatus string

const (
	ObjectiveOpen   ObjectiveStatus = "open"
	ObjectiveClosed ObjectiveStatus = "closed"
)

// Budget is the mutation allowance for an objective. Zero MaxMutations means
// no mutation budget was set; a positive value is consumed by AuthorizeMutation.
type Budget struct {
	MaxMutations  int `json:"max_mutations,omitempty"`
	MutationsUsed int `json:"mutations_used,omitempty"`
}

// Objective is the versioned authority record shared by future workspace,
// CodeAct, and gate tools. It deliberately contains declarative acceptance and
// constraints rather than agent prose so policy is machine-checkable.
type Objective struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Goal          string          `json:"goal"`
	Acceptance    []string        `json:"acceptance"`
	Constraints   []string        `json:"constraints,omitempty"`
	Budget        Budget          `json:"budget"`
	Policy        PolicyProfile   `json:"policy"`
	Status        ObjectiveStatus `json:"status"`
	Revision      int             `json:"revision"`
	OpenedAt      time.Time       `json:"opened_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	ClosedAt      *time.Time      `json:"closed_at,omitempty"`
}

// OpenObjectiveInput is the typed contract for opening an objective.
type OpenObjectiveInput struct {
	ID          string        `json:"id"`
	Goal        string        `json:"goal"`
	Acceptance  []string      `json:"acceptance"`
	Constraints []string      `json:"constraints,omitempty"`
	Budget      Budget        `json:"budget,omitempty"`
	Policy      PolicyProfile `json:"policy"`
}

// UpdateObjectiveInput changes the goal contract without silently reopening a
// closed objective. Reopen is intentionally a distinct audited operation.
type UpdateObjectiveInput struct {
	Goal        string   `json:"goal,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
	Budget      *Budget  `json:"budget,omitempty"`
}

var (
	ErrObjectiveNotFound = errors.New("objective not found")
	ErrObjectiveClosed   = errors.New("objective is closed")
	ErrObjectiveOpen     = errors.New("objective is already open")
	ErrBudgetExhausted   = errors.New("objective mutation budget exhausted")
)

// ObjectiveStore is the persistence seam. Production callers may persist to a
// session/trace store; tests and embedded users can supply MemoryObjectiveStore.
// Returned values must be independent snapshots so callers cannot mutate state
// without emitting a receipt through ObjectiveService.
type ObjectiveStore interface {
	Create(context.Context, Objective) error
	Get(context.Context, string) (Objective, error)
	Save(context.Context, Objective) error
	AppendReceipt(context.Context, Receipt) error
	ListReceipts(context.Context, string) ([]Receipt, error)
}

// MemoryObjectiveStore is a deterministic, concurrency-safe store suitable for
// replay tests and dependency-injected local use. It never calls a provider.
type MemoryObjectiveStore struct {
	mu         sync.RWMutex
	objectives map[string]Objective
	receipts   map[string][]Receipt
}

// NewMemoryObjectiveStore builds an empty injected store.
func NewMemoryObjectiveStore() *MemoryObjectiveStore {
	return &MemoryObjectiveStore{
		objectives: make(map[string]Objective),
		receipts:   make(map[string][]Receipt),
	}
}

func (s *MemoryObjectiveStore) Create(_ context.Context, objective Objective) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectives[objective.ID]; ok {
		return fmt.Errorf("objective %q already exists", objective.ID)
	}
	s.objectives[objective.ID] = cloneObjective(objective)
	return nil
}

func (s *MemoryObjectiveStore) Get(_ context.Context, id string) (Objective, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	objective, ok := s.objectives[id]
	if !ok {
		return Objective{}, fmt.Errorf("%w: %s", ErrObjectiveNotFound, id)
	}
	return cloneObjective(objective), nil
}

func (s *MemoryObjectiveStore) Save(_ context.Context, objective Objective) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectives[objective.ID]; !ok {
		return fmt.Errorf("%w: %s", ErrObjectiveNotFound, objective.ID)
	}
	s.objectives[objective.ID] = cloneObjective(objective)
	return nil
}

func (s *MemoryObjectiveStore) AppendReceipt(_ context.Context, receipt Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectives[receipt.ObjectiveID]; !ok {
		return fmt.Errorf("%w: %s", ErrObjectiveNotFound, receipt.ObjectiveID)
	}
	s.receipts[receipt.ObjectiveID] = append(s.receipts[receipt.ObjectiveID], cloneReceipt(receipt))
	return nil
}

func (s *MemoryObjectiveStore) ListReceipts(_ context.Context, id string) ([]Receipt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.objectives[id]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrObjectiveNotFound, id)
	}
	receipts := s.receipts[id]
	out := make([]Receipt, len(receipts))
	for i := range receipts {
		out[i] = cloneReceipt(receipts[i])
	}
	return out, nil
}

func cloneObjective(in Objective) Objective {
	out := in
	out.Acceptance = append([]string(nil), in.Acceptance...)
	out.Constraints = append([]string(nil), in.Constraints...)
	out.Policy = in.Policy.clone()
	if in.ClosedAt != nil {
		closedAt := *in.ClosedAt
		out.ClosedAt = &closedAt
	}
	return out
}
