package studio

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// objectivePolicyFixture is deliberately in-memory and clock-controlled: these
// are replay/no-LLM tests of the authority contract, not model tests.
type objectivePolicyFixture struct {
	now     time.Time
	store   *MemoryObjectiveStore
	service *ObjectiveService
}

func newObjectivePolicyFixture(t *testing.T) *objectivePolicyFixture {
	t.Helper()
	fixture := &objectivePolicyFixture{
		now:   time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC),
		store: NewMemoryObjectiveStore(),
	}
	service, err := NewObjectiveService(fixture.store, func() time.Time { return fixture.now })
	require.NoError(t, err)
	fixture.service = service
	return fixture
}

func (f *objectivePolicyFixture) open(t *testing.T, maxMutations int, requirement EvidenceRequirement) Objective {
	t.Helper()
	requirements := []EvidenceRequirement(nil)
	if requirement.Kind != "" {
		requirements = append(requirements, requirement)
	}
	objective, receipt, err := f.service.Open(context.Background(), OpenObjectiveInput{
		ID: "deliver-agent-os", Goal: "implement the operating system", Acceptance: []string{"focused gate passes"},
		Constraints: []string{"no live LLM"}, Budget: Budget{MaxMutations: maxMutations},
		Policy: PolicyProfile{Name: "strict", Strict: true, RequiredEvidence: requirements},
	})
	require.NoError(t, err)
	assert.Equal(t, ReceiptObjectiveOpened, receipt.Type)
	return objective
}

func TestObjectiveLifecycle_EmitsAdditiveReceiptsWithoutLLM(t *testing.T) {
	ctx := context.Background()
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 1, EvidenceRequirement{Kind: "gate", MaxAgeSeconds: 300})
	assert.Equal(t, ObjectiveOpen, objective.Status)
	assert.Equal(t, ObjectiveSchemaVersion, objective.SchemaVersion)
	assert.NotEmpty(t, objective.Policy.EffectiveHash())

	objective, updateReceipt, err := fixture.service.Update(ctx, objective.ID, UpdateObjectiveInput{Goal: "implement the complete operating system"})
	require.NoError(t, err)
	assert.Equal(t, ReceiptObjectiveUpdated, updateReceipt.Type)
	assert.Equal(t, 2, objective.Revision)

	decision, objective, authorizationReceipt, err := fixture.service.AuthorizeMutation(ctx, objective.ID)
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, 1, objective.Budget.MutationsUsed)
	assert.Equal(t, ReceiptMutationAuthorized, authorizationReceipt.Type)

	decision, _, _, err = fixture.service.AuthorizeMutation(ctx, objective.ID)
	require.NoError(t, err, "a policy denial is data, not a transport failure")
	assert.False(t, decision.Allowed)
	assert.Equal(t, ErrPolicyBudgetClosed, decision.Code)

	_, _, err = fixture.service.Close(ctx, objective.ID)
	require.ErrorIs(t, err, ErrCloseEvidence)

	evidence, evidenceReceipt, err := fixture.service.RecordEvidence(ctx, objective.ID, RecordEvidenceInput{ID: "gate-1", Kind: "gate", Summary: "go test passed"})
	require.NoError(t, err)
	assert.Equal(t, objective.Policy.EffectiveHash(), evidence.PolicyHash)
	assert.Equal(t, ReceiptEvidenceRecorded, evidenceReceipt.Type)

	closed, closeReceipt, err := fixture.service.Close(ctx, objective.ID)
	require.NoError(t, err)
	assert.Equal(t, ObjectiveClosed, closed.Status)
	assert.Equal(t, ReceiptObjectiveClosed, closeReceipt.Type)

	reopened, reopenReceipt, err := fixture.service.Reopen(ctx, objective.ID)
	require.NoError(t, err)
	assert.Equal(t, ObjectiveOpen, reopened.Status)
	assert.Nil(t, reopened.ClosedAt)
	assert.Equal(t, ReceiptObjectiveReopened, reopenReceipt.Type)

	receipts, err := fixture.store.ListReceipts(ctx, objective.ID)
	require.NoError(t, err)
	require.Len(t, receipts, 8)
	for index, receipt := range receipts {
		assert.Equal(t, index+1, receipt.Sequence)
		assert.Equal(t, ReceiptSchemaVersion, receipt.SchemaVersion)
		assert.Equal(t, objective.ID, receipt.ObjectiveID)
	}
	assert.Equal(t, ReceiptMutationDenied, receipts[3].Type)
	assert.Equal(t, ReceiptCloseDenied, receipts[4].Type)
}

func TestPolicy_StrictRejectsMissingOrStaleCloseEvidence(t *testing.T) {
	ctx := context.Background()
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{Kind: "gate", MaxAgeSeconds: 10})

	_, _, err := fixture.service.Close(ctx, objective.ID)
	var missing PolicyViolationError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, ErrEvidenceRequired, missing.Decision.Code)

	_, _, err = fixture.service.RecordEvidence(ctx, objective.ID, RecordEvidenceInput{ID: "old-gate", Kind: "gate", Summary: "old gate"})
	require.NoError(t, err)
	fixture.now = fixture.now.Add(11 * time.Second)
	_, _, err = fixture.service.Close(ctx, objective.ID)
	var stale PolicyViolationError
	require.ErrorAs(t, err, &stale)
	assert.Equal(t, ErrEvidenceStale, stale.Decision.Code)

	_, _, err = fixture.service.RecordEvidence(ctx, objective.ID, RecordEvidenceInput{ID: "fresh-gate", Kind: "gate", Summary: "fresh gate"})
	require.NoError(t, err)
	closed, _, err := fixture.service.Close(ctx, objective.ID)
	require.NoError(t, err)
	assert.Equal(t, ObjectiveClosed, closed.Status)
}

func TestPolicy_StrictMutationRequiresAnOpenObjective(t *testing.T) {
	ctx := context.Background()
	fixture := newObjectivePolicyFixture(t)
	decision, _, _, err := fixture.service.AuthorizeMutation(ctx, "missing")
	require.ErrorIs(t, err, ErrObjectiveNotFound)
	assert.False(t, decision.Allowed)
	assert.Equal(t, ErrObjectiveRequired, decision.Code)

	objective := fixture.open(t, 0, EvidenceRequirement{})
	closed, _, err := fixture.service.Close(ctx, objective.ID)
	require.NoError(t, err)
	assert.Equal(t, ObjectiveClosed, closed.Status)
	decision, _, _, err = fixture.service.AuthorizeMutation(ctx, objective.ID)
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, ErrObjectiveRequired, decision.Code)
}

func TestReceiptStoreSnapshotsAreAdditive(t *testing.T) {
	ctx := context.Background()
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{})
	first, err := fixture.store.ListReceipts(ctx, objective.ID)
	require.NoError(t, err)
	first[0].ObjectiveID = "mutated-copy"
	second, err := fixture.store.ListReceipts(ctx, objective.ID)
	require.NoError(t, err)
	assert.Equal(t, objective.ID, second[0].ObjectiveID, "receipt reads are snapshots, never mutable store state")

	_, _, err = fixture.service.RecordEvidence(ctx, objective.ID, RecordEvidenceInput{ID: "inspect", Kind: "inspection", Summary: "observed"})
	require.NoError(t, err)
	third, err := fixture.store.ListReceipts(ctx, objective.ID)
	require.NoError(t, err)
	assert.Len(t, third, 2)
	assert.Equal(t, ReceiptObjectiveOpened, third[0].Type)
	assert.Equal(t, ReceiptEvidenceRecorded, third[1].Type)
}

func TestObjectivePolicyTools_RegisterWithoutChangingStudioDefault(t *testing.T) {
	ctx := context.Background()
	fixture := newObjectivePolicyFixture(t)
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "objective-contract-test", Version: "v1"}, nil)
	RegisterObjectivePolicyTools(server, fixture.service)

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, name := range []string{"objective.open", "objective.update", "objective.reopen", "evidence.record", "policy.authorize_mutation", "objective.close", "receipt.list"} {
		assert.Truef(t, names[name], "%s must be supplied by the unregistered helper", name)
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "objective.open", Arguments: map[string]any{
		"id": "tool-objective", "goal": "verify typed contract", "acceptance": []string{"tool call succeeds"},
		"policy": map[string]any{"name": "strict", "strict": true},
	}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var decoded ObjectiveResult
	require.NoError(t, json.Unmarshal([]byte(textResult(result)), &decoded))
	assert.True(t, decoded.OK)
	assert.Equal(t, ReceiptObjectiveOpened, decoded.Receipt.Type)
}

func TestObjectivePolicyTools_AreNotRegisteredByDefaultServer(t *testing.T) {
	ctx := context.Background()
	server := NewServer(NewStudioSession(nil))
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	_, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range tools.Tools {
		assert.NotEqual(t, "objective.open", tool.Name, "final integration owns public registration")
		assert.NotEqual(t, "policy.authorize_mutation", tool.Name, "final integration owns public registration")
	}
}

func TestPolicy_EffectiveHashIsOrderIndependent(t *testing.T) {
	first := PolicyProfile{Name: "strict", Strict: true, RequiredEvidence: []EvidenceRequirement{
		{Kind: "gate", MaxAgeSeconds: 60}, {Kind: "inspection", MaxAgeSeconds: 10},
	}}
	second := PolicyProfile{Name: "strict", Strict: true, RequiredEvidence: []EvidenceRequirement{
		{Kind: "inspection", MaxAgeSeconds: 10}, {Kind: "gate", MaxAgeSeconds: 60},
	}}
	assert.Equal(t, first.EffectiveHash(), second.EffectiveHash())
}

func textResult(result *mcpsdk.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	text, _ := result.Content[0].(*mcpsdk.TextContent)
	if text == nil {
		return ""
	}
	return text.Text
}

func TestObjectiveServiceRejectsNilStore(t *testing.T) {
	_, err := NewObjectiveService(nil, nil)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrObjectiveNotFound))
}
