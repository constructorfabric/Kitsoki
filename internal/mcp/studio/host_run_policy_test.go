package studio

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostRunPolicy_StrictCannotBeWeakenedByCallerMode(t *testing.T) {
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{})
	policy, err := NewHostRunPolicy(HostRunPolicyConfig{Profile: HostRunProfileStrict, Objectives: fixture.service, Now: func() time.Time { return fixture.now }})
	require.NoError(t, err)

	for _, request := range []HostRunPolicyRequest{
		{ObjectiveID: objective.ID, Mode: HostRunModeException, Reason: "please allow it"},
		{ObjectiveID: objective.ID, Mode: HostRunModeLegacy},
		{ObjectiveID: objective.ID},
	} {
		decision, err := AuthorizeHostRun(context.Background(), policy, request)
		require.NoError(t, err)
		assert.False(t, decision.Allowed)
		assert.Equal(t, HostRunProfileStrict, decision.EffectiveProfile)
		assert.Equal(t, ErrHostRunRejected, decision.Code)
	}

	decision, err := AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{ObjectiveID: objective.ID, Mode: HostRunModeGate})
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, HostRunModeGate, decision.EffectiveMode)
	assert.Equal(t, objective.Policy.EffectiveHash(), decision.PolicyHash)
}

func TestHostRunPolicy_EscapeExceptionRequiresReasonAndRecordsReceipt(t *testing.T) {
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{})
	policy, err := NewHostRunPolicy(HostRunPolicyConfig{Profile: HostRunProfileEscape, Objectives: fixture.service, Now: func() time.Time { return fixture.now }})
	require.NoError(t, err)

	decision, err := AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{ObjectiveID: objective.ID, Mode: HostRunModeException})
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, ErrHostRunRejected, decision.Code)

	decision, err = AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{ObjectiveID: objective.ID, WorkspaceID: "workspace-1", Mode: HostRunModeException, Reason: "package manager has no typed gate"})
	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.NotNil(t, decision.Evidence)
	require.NotNil(t, decision.Receipt)
	assert.Equal(t, "host_run_exception", decision.Evidence.Kind)
	assert.Equal(t, ReceiptEvidenceRecorded, decision.Receipt.Type)
}

func TestHostRunPolicy_LegacyCompatibilityAndExplicitLegacyAreDistinct(t *testing.T) {
	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{})
	policy, err := NewHostRunPolicy(HostRunPolicyConfig{Profile: HostRunProfileLegacy, Objectives: fixture.service, Now: func() time.Time { return fixture.now }})
	require.NoError(t, err)

	compatibility, err := AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{})
	require.NoError(t, err)
	assert.True(t, compatibility.Allowed)
	assert.Equal(t, HostRunModeLegacy, compatibility.EffectiveMode)
	assert.Nil(t, compatibility.Receipt, "compatibility path has no objective to attach evidence to")

	explicit, err := AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{ObjectiveID: objective.ID, Mode: HostRunModeLegacy, Reason: "old client compatibility"})
	require.NoError(t, err)
	require.True(t, explicit.Allowed)
	require.NotNil(t, explicit.Receipt)
	assert.Equal(t, "host_run_legacy", explicit.Evidence.Kind)
}

func TestHostRunPolicy_IsInjectableAndNilPreservesExistingHandlerCompatibility(t *testing.T) {
	denied := HostRunPolicyFunc(func(_ context.Context, request HostRunPolicyRequest) (HostRunPolicyDecision, error) {
		return HostRunPolicyDecision{Allowed: false, Code: "TEST_DENIED", Reason: request.ObjectiveID}, nil
	})
	decision, err := AuthorizeHostRun(context.Background(), denied, HostRunPolicyRequest{ObjectiveID: "objective-1", Mode: HostRunModeGate})
	require.NoError(t, err)
	assert.False(t, decision.Allowed)
	assert.Equal(t, "TEST_DENIED", decision.Code)

	decision, err = AuthorizeHostRun(context.Background(), nil, HostRunPolicyRequest{})
	require.NoError(t, err)
	assert.True(t, decision.Allowed)
	assert.Equal(t, HostRunProfileLegacy, decision.EffectiveProfile)
}

func TestHostRunPolicy_ConfigurationAndInfrastructureFailuresAreHonest(t *testing.T) {
	_, err := NewHostRunPolicy(HostRunPolicyConfig{Profile: HostRunProfileStrict})
	require.Error(t, err)
	_, err = NewHostRunPolicy(HostRunPolicyConfig{Profile: "unrecognised"})
	require.Error(t, err)

	fixture := newObjectivePolicyFixture(t)
	objective := fixture.open(t, 0, EvidenceRequirement{})
	failing := &failingObjectiveStore{ObjectiveStore: fixture.store, failAppend: errors.New("receipt store unavailable")}
	service, err := NewObjectiveService(failing, func() time.Time { return fixture.now })
	require.NoError(t, err)
	policy, err := NewHostRunPolicy(HostRunPolicyConfig{Profile: HostRunProfileEscape, Objectives: service, Now: func() time.Time { return fixture.now }})
	require.NoError(t, err)
	_, err = AuthorizeHostRun(context.Background(), policy, HostRunPolicyRequest{ObjectiveID: objective.ID, Mode: HostRunModeException, Reason: "need command"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record host.run authorization")
}

type failingObjectiveStore struct {
	ObjectiveStore
	failAppend error
}

func (s *failingObjectiveStore) AppendReceipt(ctx context.Context, receipt Receipt) error {
	if s.failAppend != nil {
		return s.failAppend
	}
	return s.ObjectiveStore.AppendReceipt(ctx, receipt)
}
