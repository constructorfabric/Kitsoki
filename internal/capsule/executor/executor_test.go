package executor

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
)

func TestFakeProviderSealsEnvelopeAndRejectsUnsupportedNetwork(t *testing.T) {
	env := testEnvironmentLock(t)
	base := Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "story", StoryDigest: "sha256:story", Environment: env, Policy: Policy{Network: "none", MinimumSandbox: "supervised", ExternalWrite: "deny"}}
	provider := NewFakeProvider("remote")
	prepared, err := provider.Prepare(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := provider.Prepare(context.Background(), base)
	if err != nil || again.ID != prepared.ID {
		t.Fatalf("prepare not idempotent %#v %v", again, err)
	}
	got, err := provider.Run(context.Background(), prepared, func(context.Context, Prepared) (Result, error) { return Result{Artifacts: []string{"b", "a"}}, nil }, nil)
	if err != nil || got.ExecutionID != prepared.ID || got.Artifacts[0] != "a" {
		t.Fatalf("run %#v %v", got, err)
	}
	base.Policy.Network = "live"
	if _, err := provider.Prepare(context.Background(), base); err == nil {
		t.Fatal("unsupported network accepted")
	}
}

func TestSealRejectsIncompleteOrInconsistentEnvelope(t *testing.T) {
	base := Envelope{
		JobID:            "job",
		ProjectID:        "project",
		DefinitionDigest: "sha256:def",
		Instance:         control.Handle{ID: "workspace", Generation: 1},
		SourceDigest:     "sha256:source",
		StoryPath:        "stories/ci/app.yaml",
		StoryDigest:      "sha256:story",
		Environment:      testEnvironmentLock(t),
		Policy:           Policy{Network: "none", MinimumSandbox: "supervised", ExternalWrite: "deny", Agents: AgentPolicy{Policy: "deny"}},
	}
	tests := []struct {
		name string
		want string
		edit func(*Envelope)
	}{
		{name: "definition", want: "definition digest", edit: func(e *Envelope) { e.DefinitionDigest = "" }},
		{name: "workspace id", want: "workspace id and generation", edit: func(e *Envelope) { e.Instance.ID = "" }},
		{name: "workspace generation", want: "workspace id and generation", edit: func(e *Envelope) { e.Instance.Generation = 0 }},
		{name: "source", want: "source digest", edit: func(e *Envelope) { e.SourceDigest = "" }},
		{name: "story path", want: "story path", edit: func(e *Envelope) { e.StoryPath = "" }},
		{name: "story escape", want: "project-relative", edit: func(e *Envelope) { e.StoryPath = "../app.yaml" }},
		{name: "story normalized", want: "normalized", edit: func(e *Envelope) { e.StoryPath = "stories/ci/../ci/app.yaml" }},
		{name: "story digest", want: "story digest", edit: func(e *Envelope) { e.StoryDigest = "" }},
		{name: "environment tamper", want: "environment lock", edit: func(e *Envelope) { e.Environment.Network = "live" }},
		{name: "network policy", want: "network policy", edit: func(e *Envelope) { e.Policy.Network = "sometimes" }},
		{name: "sandbox mismatch", want: "does not match environment lock", edit: func(e *Envelope) { e.Policy.MinimumSandbox = "container" }},
		{name: "external write", want: "external-write policy", edit: func(e *Envelope) { e.Policy.ExternalWrite = "maybe" }},
		{name: "denied agent fields", want: "denied agents", edit: func(e *Envelope) { e.Policy.Agents.MaxCostUSD = 1 }},
		{name: "allowed agent budget", want: "finite positive budget", edit: func(e *Envelope) {
			e.Policy.Agents = AgentPolicy{Policy: "allow", Profiles: []string{"reviewer"}, OnUnavailable: "needs_input"}
		}},
		{name: "agent fallback", want: "agent-unavailable fallback", edit: func(e *Envelope) {
			e.Policy.Agents = AgentPolicy{Policy: "allow", Profiles: []string{"reviewer"}, MaxCostUSD: 1, OnUnavailable: "continue"}
		}},
		{name: "agent duplicate", want: "unique normalized names", edit: func(e *Envelope) {
			e.Policy.Agents = AgentPolicy{Policy: "allow", Profiles: []string{"reviewer", "reviewer"}, MaxCostUSD: 1, OnUnavailable: "needs_input"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := base
			test.edit(&envelope)
			_, err := Seal(envelope)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Seal() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSealRejectsMutationOfExistingDigest(t *testing.T) {
	envelope, err := Seal(Envelope{
		JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "workspace", Generation: 1}, SourceDigest: "sha256:source",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: testEnvironmentLock(t),
		Policy: Policy{Network: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope.SourceDigest = "sha256:other"
	if _, err := Seal(envelope); err == nil || !strings.Contains(err.Error(), "envelope digest mismatch") {
		t.Fatalf("expected stale digest rejection, got %v", err)
	}
}

func TestSealRejectsNetworkDriftAndUnenforceableLiveExternalDeny(t *testing.T) {
	lock := testEnvironmentLock(t)
	base := Envelope{
		JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "workspace", Generation: 1}, SourceDigest: "sha256:source",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: lock,
		Policy: Policy{Network: "replay"},
	}
	if _, err := Seal(base); err == nil || !strings.Contains(err.Error(), "does not match environment") {
		t.Fatalf("expected network drift rejection, got %v", err)
	}
	lock.Network = "live"
	lock, err := environment.SealLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	base.Environment = lock
	base.Policy = Policy{Network: "live", ExternalWrite: "deny"}
	if _, err := Seal(base); err == nil || !strings.Contains(err.Error(), "cannot be enforced") {
		t.Fatalf("expected live external-write rejection, got %v", err)
	}
	base.Policy.ExternalWrite = "allow"
	if _, err := Seal(base); err != nil {
		t.Fatalf("expected explicit live compatibility policy: %v", err)
	}
}

func TestValidatePreparedRequiresPlacementAndExactAppliedPolicy(t *testing.T) {
	envelope, err := Seal(Envelope{
		JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "workspace", Generation: 1}, SourceDigest: "sha256:source",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: testEnvironmentLock(t),
		Policy: Policy{Network: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := Prepared{ID: "execution", Placement: "remote", Envelope: envelope, Applied: envelope.Policy}
	if _, err := ValidatePrepared(valid); err != nil {
		t.Fatalf("ValidatePrepared() matching policy: %v", err)
	}
	missingPlacement := valid
	missingPlacement.Placement = ""
	if _, err := ValidatePrepared(missingPlacement); err == nil || !strings.Contains(err.Error(), "placement") {
		t.Fatalf("expected placement rejection, got %v", err)
	}
	mismatch := valid
	mismatch.Applied.ExternalWrite = "allow"
	if _, err := ValidatePrepared(mismatch); err == nil || !strings.Contains(err.Error(), "applied policy") {
		t.Fatalf("expected applied-policy rejection, got %v", err)
	}
}

func testEnvironmentLock(t *testing.T) environment.Lock {
	t.Helper()
	return testEnvironmentLockWithSandbox(t, "supervised")
}

func testEnvironmentLockWithSandbox(t *testing.T, sandbox string) environment.Lock {
	t.Helper()
	lock, err := environment.SealLock(environment.Lock{
		Schema: environment.LockSchema, ID: "ci", DefinitionDigest: "sha256:environment-definition",
		Network: "none", Sandbox: sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lock
}
