package studio

import (
	"context"
	"time"
)

// ReceiptSchemaVersion versions immutable lifecycle event envelopes.
const ReceiptSchemaVersion = "v1"

// ReceiptType names additive objective lifecycle events.
type ReceiptType string

const (
	ReceiptObjectiveOpened    ReceiptType = "objective.opened"
	ReceiptObjectiveUpdated   ReceiptType = "objective.updated"
	ReceiptObjectiveReopened  ReceiptType = "objective.reopened"
	ReceiptEvidenceRecorded   ReceiptType = "evidence.recorded"
	ReceiptMutationAuthorized ReceiptType = "policy.mutation_authorized"
	ReceiptMutationDenied     ReceiptType = "policy.mutation_denied"
	ReceiptObjectiveClosed    ReceiptType = "objective.closed"
	ReceiptCloseDenied        ReceiptType = "policy.close_denied"
)

// Receipt is an append-only event envelope. Typed payload fields keep evidence
// durable across JSON-backed stores; no caller may replace older receipts.
type Receipt struct {
	SchemaVersion     string            `json:"schema_version"`
	Sequence          int               `json:"sequence"`
	Type              ReceiptType       `json:"type"`
	ObjectiveID       string            `json:"objective_id"`
	ObjectiveRevision int               `json:"objective_revision"`
	PolicyHash        string            `json:"policy_hash"`
	RecordedAt        time.Time         `json:"recorded_at"`
	Evidence          *Evidence         `json:"evidence,omitempty"`
	Decision          *PolicyDecision   `json:"decision,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
}

func (s *ObjectiveService) appendReceipt(ctx context.Context, objective Objective, kind ReceiptType, data any) (Receipt, error) {
	existing, err := s.store.ListReceipts(ctx, objective.ID)
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		SchemaVersion: ReceiptSchemaVersion, Sequence: len(existing) + 1, Type: kind,
		ObjectiveID: objective.ID, ObjectiveRevision: objective.Revision,
		PolicyHash: objective.Policy.EffectiveHash(), RecordedAt: s.now().UTC(),
	}
	switch value := data.(type) {
	case Evidence:
		copy := value
		receipt.Evidence = &copy
	case PolicyDecision:
		copy := value
		receipt.Decision = &copy
	case map[string]string:
		receipt.Attributes = make(map[string]string, len(value))
		for key, item := range value {
			receipt.Attributes[key] = item
		}
	}
	if err := s.store.AppendReceipt(ctx, receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func cloneReceipt(in Receipt) Receipt {
	out := in
	if in.Evidence != nil {
		evidence := *in.Evidence
		out.Evidence = &evidence
	}
	if in.Decision != nil {
		decision := *in.Decision
		out.Decision = &decision
	}
	if in.Attributes != nil {
		out.Attributes = make(map[string]string, len(in.Attributes))
		for key, item := range in.Attributes {
			out.Attributes[key] = item
		}
	}
	return out
}
