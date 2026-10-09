package studio

import (
	"context"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterObjectivePolicyTools registers the typed objective/policy contracts
// on an MCP server. NewServer intentionally does not call this helper yet: the
// final integration slice owns public profile selection and compatibility.
func RegisterObjectivePolicyTools(server *mcpsdk.Server, service *ObjectiveService) {
	if server == nil || service == nil {
		panic("objective policy tools require a server and service")
	}
	h := objectiveToolHandlers{service: service}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "objective.open", Description: "Open a versioned objective with goal, acceptance, constraints, budget, and effective policy. Returns the objective plus an additive receipt."}, h.open)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "objective.get", Description: "Read one versioned objective and its current authority state. Read-only."}, h.get)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "objective.update", Description: "Update an open objective's declarative contract or budget. Returns an additive receipt; it never silently reopens a closed objective."}, h.update)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "objective.reopen", Description: "Explicitly reopen a closed objective as a new audited authority epoch. Returns an additive receipt."}, h.reopen)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "evidence.record", Description: "Attach policy-hashed observed evidence to an objective. Evidence is additive and is later checked for close freshness."}, h.recordEvidence)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "policy.authorize_mutation", Description: "Check and consume one objective mutation allowance. Strict policy rejects missing, closed, or budget-exhausted authority and returns a typed decision."}, h.authorizeMutation)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "objective.close", Description: "Close an open objective only when its strict policy has all required fresh evidence. Returns an additive close receipt."}, h.close)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "receipt.list", Description: "List immutable, additive lifecycle receipts for an objective. Read-only."}, h.listReceipts)
}

type objectiveToolHandlers struct{ service *ObjectiveService }

type ObjectiveIDArgs struct {
	ObjectiveID string `json:"objective_id"`
}

type ObjectiveUpdateArgs struct {
	ObjectiveID string               `json:"objective_id"`
	Update      UpdateObjectiveInput `json:"update"`
}

type ObjectiveEvidenceArgs struct {
	ObjectiveID string              `json:"objective_id"`
	Evidence    RecordEvidenceInput `json:"evidence"`
}

type ObjectiveResult struct {
	OK        bool      `json:"ok"`
	Objective Objective `json:"objective"`
	Receipt   Receipt   `json:"receipt,omitempty"`
}

type EvidenceResult struct {
	OK       bool     `json:"ok"`
	Evidence Evidence `json:"evidence"`
	Receipt  Receipt  `json:"receipt"`
}

type MutationAuthorizationResult struct {
	OK        bool           `json:"ok"`
	Decision  PolicyDecision `json:"decision"`
	Objective Objective      `json:"objective,omitempty"`
	Receipt   Receipt        `json:"receipt,omitempty"`
}

type ReceiptListResult struct {
	OK       bool      `json:"ok"`
	Receipts []Receipt `json:"receipts"`
}

func (h objectiveToolHandlers) open(ctx context.Context, _ *mcpsdk.CallToolRequest, args OpenObjectiveInput) (*mcpsdk.CallToolResult, any, error) {
	objective, receipt, err := h.service.Open(ctx, args)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ObjectiveResult{OK: true, Objective: objective, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) get(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveIDArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	objective, err := h.service.Get(ctx, args.ObjectiveID)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ObjectiveResult{OK: true, Objective: objective}, nil
}

func (h objectiveToolHandlers) update(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveUpdateArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	objective, receipt, err := h.service.Update(ctx, args.ObjectiveID, args.Update)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ObjectiveResult{OK: true, Objective: objective, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) reopen(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveIDArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	objective, receipt, err := h.service.Reopen(ctx, args.ObjectiveID)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ObjectiveResult{OK: true, Objective: objective, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) recordEvidence(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveEvidenceArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	evidence, receipt, err := h.service.RecordEvidence(ctx, args.ObjectiveID, args.Evidence)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, EvidenceResult{OK: true, Evidence: evidence, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) authorizeMutation(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveIDArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	decision, objective, receipt, err := h.service.AuthorizeMutation(ctx, args.ObjectiveID)
	if err != nil && !errors.Is(err, ErrObjectiveNotFound) {
		return objectivePolicyToolError(err), nil, nil
	}
	if !decision.Allowed {
		return buildToolError(decision.Code, decision.Reason), nil, nil
	}
	return nil, MutationAuthorizationResult{OK: true, Decision: decision, Objective: objective, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) close(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveIDArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	objective, receipt, err := h.service.Close(ctx, args.ObjectiveID)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ObjectiveResult{OK: true, Objective: objective, Receipt: receipt}, nil
}

func (h objectiveToolHandlers) listReceipts(ctx context.Context, _ *mcpsdk.CallToolRequest, args ObjectiveIDArgs) (*mcpsdk.CallToolResult, any, error) {
	if args.ObjectiveID == "" {
		return buildToolError(ErrBadRequest, "objective_id is required"), nil, nil
	}
	receipts, err := h.service.store.ListReceipts(ctx, args.ObjectiveID)
	if err != nil {
		return objectivePolicyToolError(err), nil, nil
	}
	return nil, ReceiptListResult{OK: true, Receipts: receipts}, nil
}

func objectivePolicyToolError(err error) *mcpsdk.CallToolResult {
	var violation PolicyViolationError
	if errors.As(err, &violation) {
		return buildToolError(violation.Decision.Code, violation.Decision.Reason)
	}
	switch {
	case errors.Is(err, ErrObjectiveNotFound):
		return buildToolError(ErrObjectiveRequired, err.Error())
	case errors.Is(err, ErrObjectiveClosed):
		return buildToolError(ErrPolicyDenied, err.Error())
	case errors.Is(err, ErrObjectiveOpen):
		return buildToolError(ErrBadRequest, err.Error())
	case errors.Is(err, ErrBudgetExhausted):
		return buildToolError(ErrPolicyBudgetClosed, err.Error())
	default:
		return buildToolError(ErrBadRequest, fmt.Sprintf("objective policy: %v", err))
	}
}
