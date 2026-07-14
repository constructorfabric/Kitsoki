package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// HostRunProfile is server configuration, not a tool argument. This is the
// crucial boundary: a caller may request a mode but cannot weaken the profile
// that decides whether it is effective.
type HostRunProfile string

const (
	HostRunProfileLegacy HostRunProfile = "legacy"
	HostRunProfileStrict HostRunProfile = "strict"
	HostRunProfileEscape HostRunProfile = "escape"
)

type HostRunMode string

const (
	HostRunModeGate      HostRunMode = "gate"
	HostRunModeException HostRunMode = "exception"
	HostRunModeLegacy    HostRunMode = "legacy"
)

type HostRunPolicyRequest struct {
	ObjectiveID string      `json:"objective_id,omitempty"`
	WorkspaceID string      `json:"workspace_id,omitempty"`
	Mode        HostRunMode `json:"mode,omitempty"`
	Reason      string      `json:"reason,omitempty"`
}

type HostRunPolicyDecision struct {
	Allowed          bool           `json:"allowed"`
	Code             string         `json:"code,omitempty"`
	Reason           string         `json:"reason,omitempty"`
	EffectiveProfile HostRunProfile `json:"effective_profile"`
	EffectiveMode    HostRunMode    `json:"effective_mode,omitempty"`
	PolicyHash       string         `json:"policy_hash,omitempty"`
	Evidence         *Evidence      `json:"evidence,omitempty"`
	Receipt          *Receipt       `json:"receipt,omitempty"`
}

const (
	ErrHostRunModeRequired = "HOST_RUN_MODE_REQUIRED"
	ErrHostRunRejected     = "HOST_RUN_REJECTED"
)

// HostRunPolicy is the injected authorization seam a future host.run wrapper
// uses before delegating to its existing execution handler.
type HostRunPolicy interface {
	Authorize(context.Context, HostRunPolicyRequest) (HostRunPolicyDecision, error)
}

// HostRunPolicyFunc makes tests and alternate server embeddings inject an
// explicit decision without mutating global state.
type HostRunPolicyFunc func(context.Context, HostRunPolicyRequest) (HostRunPolicyDecision, error)

func (f HostRunPolicyFunc) Authorize(ctx context.Context, request HostRunPolicyRequest) (HostRunPolicyDecision, error) {
	return f(ctx, request)
}

type HostRunPolicyConfig struct {
	Profile    HostRunProfile
	Objectives *ObjectiveService
	Now        func() time.Time
}

type objectiveHostRunPolicy struct {
	profile    HostRunProfile
	objectives *ObjectiveService
	now        func() time.Time
}

func NewHostRunPolicy(config HostRunPolicyConfig) (HostRunPolicy, error) {
	if config.Profile == "" {
		config.Profile = HostRunProfileLegacy
	}
	if config.Profile != HostRunProfileLegacy && config.Profile != HostRunProfileStrict && config.Profile != HostRunProfileEscape {
		return nil, fmt.Errorf("unknown host.run profile %q", config.Profile)
	}
	if config.Profile != HostRunProfileLegacy && config.Objectives == nil {
		return nil, errors.New("strict and escape host.run profiles require objective service")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &objectiveHostRunPolicy{profile: config.Profile, objectives: config.Objectives, now: config.Now}, nil
}

// AuthorizeHostRun keeps policy invocation explicit at the legacy-handler
// boundary. A nil policy preserves today's host.run behavior during the staged
// migration; strict/escape servers must pass a configured policy explicitly.
func AuthorizeHostRun(ctx context.Context, policy HostRunPolicy, request HostRunPolicyRequest) (HostRunPolicyDecision, error) {
	if policy == nil {
		return HostRunPolicyDecision{Allowed: true, EffectiveProfile: HostRunProfileLegacy, EffectiveMode: HostRunModeLegacy, Reason: "legacy compatibility: no host.run policy configured"}, nil
	}
	return policy.Authorize(ctx, request)
}

func (p *objectiveHostRunPolicy) Authorize(ctx context.Context, request HostRunPolicyRequest) (HostRunPolicyDecision, error) {
	mode := request.Mode
	if mode == "" && p.profile == HostRunProfileLegacy {
		mode = HostRunModeLegacy
	}
	decision := HostRunPolicyDecision{EffectiveProfile: p.profile, EffectiveMode: mode}
	switch p.profile {
	case HostRunProfileStrict:
		if mode != HostRunModeGate {
			return rejectHostRun(decision, ErrHostRunRejected, "strict profile excludes non-gate host.run"), nil
		}
	case HostRunProfileEscape:
		if mode != HostRunModeException && mode != HostRunModeGate {
			return rejectHostRun(decision, ErrHostRunModeRequired, "escape profile requires mode gate or exception"), nil
		}
	case HostRunProfileLegacy:
		if mode != HostRunModeLegacy && mode != HostRunModeGate && mode != HostRunModeException {
			return rejectHostRun(decision, ErrHostRunModeRequired, "legacy profile requires mode legacy, gate, or exception"), nil
		}
	}
	if mode == HostRunModeException && strings.TrimSpace(request.Reason) == "" {
		return rejectHostRun(decision, ErrHostRunRejected, "host.run exception requires a recorded reason"), nil
	}
	if request.ObjectiveID == "" {
		if p.profile == HostRunProfileLegacy && mode == HostRunModeLegacy {
			decision.Allowed = true
			decision.Reason = "legacy compatibility use without objective receipt"
			return decision, nil
		}
		return rejectHostRun(decision, ErrObjectiveRequired, "host.run mode requires an open objective"), nil
	}
	if p.objectives == nil {
		return rejectHostRun(decision, ErrObjectiveRequired, "host.run profile has no objective service"), nil
	}
	objective, err := p.objectives.Get(ctx, request.ObjectiveID)
	if err != nil {
		return rejectHostRun(decision, ErrObjectiveRequired, err.Error()), nil
	}
	if objective.Status != ObjectiveOpen {
		return rejectHostRun(decision, ErrObjectiveRequired, "host.run objective is not open"), nil
	}
	decision.Allowed, decision.PolicyHash = true, objective.Policy.EffectiveHash()
	if mode != HostRunModeException && !(p.profile == HostRunProfileLegacy && mode == HostRunModeLegacy) {
		return decision, nil
	}
	// Exceptions and explicit legacy use receive an additive objective receipt.
	// The evidence describes authorization, not a successful validation, so its
	// kind cannot satisfy the normal required kind "gate" by accident.
	kind := "host_run_exception"
	if mode == HostRunModeLegacy {
		kind = "host_run_legacy"
	}
	evidence, receipt, err := p.objectives.RecordEvidence(ctx, request.ObjectiveID, RecordEvidenceInput{
		ID: fmt.Sprintf("%s:%d", kind, p.now().UTC().UnixNano()), Kind: kind,
		Summary: fmt.Sprintf("host.run %s authorized: %s", mode, strings.TrimSpace(request.Reason)),
	})
	if err != nil {
		return HostRunPolicyDecision{}, fmt.Errorf("record host.run authorization: %w", err)
	}
	decision.Evidence, decision.Receipt = &evidence, &receipt
	return decision, nil
}

func rejectHostRun(decision HostRunPolicyDecision, code, reason string) HostRunPolicyDecision {
	decision.Allowed, decision.Code, decision.Reason = false, code, reason
	return decision
}
