package ci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	capsuletrace "kitsoki/internal/capsule/trace"
)

type launcher func(context.Context, executor.Prepared) (Verdict, error)

func (f launcher) Launch(ctx context.Context, p executor.Prepared) (Verdict, error) { return f(ctx, p) }

type fixedJobStore struct {
	id    artifactjob.JobID
	inner *artifactjob.MemoryStore
}

// fixtureBuiltinExecutors models a test-owned contained host so parity tests
// can exercise one sealed none-network envelope across fake placements. The
// production HostProvider intentionally advertises only live network.
func fixtureBuiltinExecutors() BuiltinExecutors {
	builtins := NewBuiltinExecutors()
	builtins.Host.(*executor.HostProvider).Cap.Networks = []string{"none"}
	return builtins
}

func newFixedJobStore(id string) fixedJobStore {
	return fixedJobStore{id: artifactjob.JobID(id), inner: artifactjob.NewMemoryStore()}
}

func (s fixedJobStore) Register(ctx context.Context, req artifactjob.RegisterRequest) (artifactjob.Job, error) {
	req.ID = s.id
	return s.inner.Register(ctx, req)
}
func (s fixedJobStore) BindRun(ctx context.Context, id artifactjob.JobID, sessionID string, runURL string, tracePath string) (artifactjob.Job, error) {
	return s.inner.BindRun(ctx, id, sessionID, runURL, tracePath)
}
func (s fixedJobStore) Update(ctx context.Context, id artifactjob.JobID, update artifactjob.Update) (artifactjob.Job, error) {
	return s.inner.Update(ctx, id, update)
}
func (s fixedJobStore) Attach(ctx context.Context, id artifactjob.JobID, sessionID string) (artifactjob.Job, error) {
	return s.inner.Attach(ctx, id, sessionID)
}
func (s fixedJobStore) Get(ctx context.Context, id artifactjob.JobID) (artifactjob.Job, error) {
	return s.inner.Get(ctx, id)
}
func (s fixedJobStore) List(ctx context.Context, filter artifactjob.ListFilter) ([]artifactjob.Job, error) {
	return s.inner.List(ctx, filter)
}
func (s fixedJobStore) Archive(ctx context.Context, id artifactjob.JobID) (artifactjob.Job, error) {
	return s.inner.Archive(ctx, id)
}
func (s fixedJobStore) SweepInterrupted(ctx context.Context, sessionID string) (int64, error) {
	return s.inner.SweepInterrupted(ctx, sessionID)
}

func TestServiceRunsTypedStoryVerdictWithFakeExecutor(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Provider: executor.NewFakeProvider("fake"), Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
		return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
	})}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != artifactjob.StatusDone || !result.Verdict.PromotionEligible {
		t.Fatalf("result %#v", result)
	}
}

func TestServiceDerivesPromotionEligibilityAtUntrustedStoryBoundary(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Provider: executor.NewFakeProvider("fake"), Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
		// A story JSON object omits this bool and therefore decodes it as false.
		return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
	})}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verdict.PromotionEligible {
		t.Fatalf("runtime did not derive promotion eligibility: %#v", result.Verdict)
	}
}

func TestValidateVerdictRejectsDigestMismatchAndCallerControlledPromotion(t *testing.T) {
	lock, err := environment.SealLock(environment.Lock{Schema: environment.LockSchema, ID: "ci", DefinitionDigest: "sha256:env-def", Network: "none", Sandbox: "supervised"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := executor.Seal(executor.Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "story", StoryDigest: "sha256:story", Environment: lock, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	valid := Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: envelope.SourceDigest, StoryDigest: envelope.StoryDigest, EnvironmentDigest: envelope.Environment.Digest, EnvelopeDigest: envelope.Digest}
	if err := ValidateVerdict(valid, envelope, ResultContract{}); err != nil {
		t.Fatal(err)
	}
	mismatch := valid
	mismatch.SourceDigest = "sha256:other"
	if err := ValidateVerdict(mismatch, envelope, ResultContract{}); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch, got %v", err)
	}
	controlled := valid
	controlled.Checks[0].Outcome = "failed"
	if err := ValidateVerdict(controlled, envelope, ResultContract{}); err == nil || !strings.Contains(err.Error(), "derived") {
		t.Fatalf("expected derived promotion eligibility error, got %v", err)
	}
}

func TestValidateVerdictBindsPipelineAndDeclaredOutcomeContract(t *testing.T) {
	lock, err := environment.SealLock(environment.Lock{Schema: environment.LockSchema, ID: "ci", DefinitionDigest: "sha256:env-def", Network: "none", Sandbox: "supervised"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := executor.Seal(executor.Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "story", StoryDigest: "sha256:story", Environment: lock, Trigger: map[string]any{"requested_pipeline": "change"}, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	contract := ResultContract{Schema: VerdictSchema, PassExits: []string{"passed"}, FailExits: []string{"failed"}, ParkExits: []string{"needs_input"}}
	valid := Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: envelope.SourceDigest, StoryDigest: envelope.StoryDigest, EnvironmentDigest: envelope.Environment.Digest, EnvelopeDigest: envelope.Digest}
	if err := ValidateVerdict(valid, envelope, contract); err != nil {
		t.Fatal(err)
	}
	wrongPipeline := valid
	wrongPipeline.Pipeline = "other"
	if err := ValidateVerdict(wrongPipeline, envelope, contract); err == nil || !strings.Contains(err.Error(), "sealed pipeline") {
		t.Fatalf("expected pipeline binding rejection, got %v", err)
	}
	wrongOutcome := valid
	wrongOutcome.Outcome = "infra_failed"
	wrongOutcome.PromotionEligible = false
	if err := ValidateVerdict(wrongOutcome, envelope, contract); err == nil || !strings.Contains(err.Error(), "result contract") {
		t.Fatalf("expected contract outcome rejection, got %v", err)
	}
}

func TestLoadRejectsUnknownCapsuleCIFields(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	path := filepath.Join(root, ".kitsoki", "ci.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    permisssions:\n      network: none\n")...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "permisssions") {
		t.Fatalf("expected unknown-field rejection, got %v", err)
	}
}

func TestPlanBindsGitHubTriggerToExactWorkspaceSourceAndPipeline(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	ciPath := filepath.Join(root, ".kitsoki", "ci.yaml")
	raw, err := os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "triggers: [local]", "triggers: [local, pull_request]", 1))
	if err := os.WriteFile(ciPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{ProjectRoot: root, Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}}
	req := RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "0123456789012345678901234567890123456789", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "pull_request", Provider: "github", HeadSHA: "0123456789012345678901234567890123456789", RequestedPipeline: "change"}}
	_, envelope, err := service.Plan(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := envelope.Trigger["provider"]; got != "github" {
		t.Fatalf("sealed trigger provider = %#v", got)
	}
	req.Trigger.HeadSHA = "ffffffffffffffffffffffffffffffffffffffff"
	if _, _, err := service.Plan(context.Background(), req); err == nil || !strings.Contains(err.Error(), "does not match workspace source") {
		t.Fatalf("expected source mismatch, got %v", err)
	}
	req.Trigger.HeadSHA = req.SourceDigest
	req.Trigger.RequestedPipeline = "nightly"
	if _, _, err := service.Plan(context.Background(), req); err == nil || !strings.Contains(err.Error(), "trigger requested pipeline") {
		t.Fatalf("expected pipeline mismatch, got %v", err)
	}
}

func TestServiceSelectsTheDeclaredPipelineExecutor(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    executor: remote-fake\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Executors: fixtureBuiltinExecutors(), Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
		return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
	})}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.ExecutionID == "" || result.Execution.ExecutionID[:7] != "remote-" {
		t.Fatalf("selected execution %#v", result.Execution)
	}
}

func TestServiceRunFailureReturnsFailedJobAndExecutorEvents(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Executors: fixtureBuiltinExecutors(), Launcher: launcher(func(context.Context, executor.Prepared) (Verdict, error) {
		return Verdict{}, io.ErrUnexpectedEOF
	})}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err == nil {
		t.Fatal("expected run error")
	}
	if result.Job.Status != artifactjob.StatusFailed {
		t.Fatalf("status=%s result=%#v", result.Job.Status, result)
	}
	if len(result.Events) != 2 || result.Events[0].Kind != "capsule.executor.started" || result.Events[1].Kind != "capsule.executor.failed" {
		t.Fatalf("events %#v", result.Events)
	}
}

func TestServiceProjectsCancelledExecutionAsCancelledTerminal(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	service := Service{
		ProjectRoot: root,
		Jobs:        artifactjob.NewMemoryStore(),
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Provider:    executor.NewFakeProvider("fake"),
		Launcher:    launcher(func(context.Context, executor.Prepared) (Verdict, error) { return Verdict{}, context.Canceled }),
	}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if result.Job.Status != artifactjob.StatusCancelled || result.Stage != RunStageFinished || !result.Terminal || result.Verdict.Outcome != "cancelled" || result.Verdict.EnvelopeDigest != result.Envelope.Digest {
		t.Fatalf("cancelled result %#v", result)
	}
}

func TestServicePreservesRegisteredRunAndCheckpointsPrepareFailure(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	provider := &failureInjectionProvider{prepareErr: io.ErrUnexpectedEOF}
	var stages []string
	service := Service{
		ProjectRoot: root,
		Jobs:        newFixedJobStore("job-prepare-failure"),
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Provider:    provider,
		Observer: RunObserverFunc(func(_ context.Context, observation RunObservation) error {
			stages = append(stages, observation.Result.Stage)
			return nil
		}),
	}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err == nil || !strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("expected injected prepare failure, got %v", err)
	}
	if result.Job.ID != "job-prepare-failure" || result.Job.Status != artifactjob.StatusFailed || result.Envelope.JobID != "job-prepare-failure" || result.Envelope.Digest == "" {
		t.Fatalf("registered result was lost: %#v", result)
	}
	if result.Stage != RunStageFailed || !result.Terminal || provider.ran {
		t.Fatalf("lifecycle result=%#v provider=%#v", result, provider)
	}
	wantStages := []string{RunStageRequested, RunStagePreparing, RunStageFailed}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("stages=%v want=%v", stages, wantStages)
	}
}

func TestServiceDoesNotExecuteWhenRunningCheckpointCannotPersist(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	provider := &failureInjectionProvider{}
	service := Service{
		ProjectRoot: root,
		Jobs:        newFixedJobStore("job-observer-failure"),
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Provider:    provider,
		Observer: RunObserverFunc(func(_ context.Context, observation RunObservation) error {
			if observation.Result.Stage == RunStageRunning {
				return io.ErrClosedPipe
			}
			return nil
		}),
	}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err == nil || !strings.Contains(err.Error(), "persist running checkpoint") {
		t.Fatalf("expected checkpoint error, got %v", err)
	}
	if provider.ran {
		t.Fatal("provider ran after the pre-execution checkpoint failed")
	}
	if result.Job.ID != "job-observer-failure" || result.Job.Status != artifactjob.StatusFailed || result.Stage != RunStageFailed || !result.Terminal {
		t.Fatalf("result %#v", result)
	}
}

type failureInjectionProvider struct {
	prepareErr error
	ran        bool
	cap        executor.Capabilities
}

func (p *failureInjectionProvider) Describe(context.Context) (executor.Capabilities, error) {
	if p.cap.ID != "" {
		return p.cap, nil
	}
	return executor.Capabilities{ID: "injected", Placements: []string{"fake"}, Isolation: "supervised", Networks: []string{"none"}}, nil
}

func (p *failureInjectionProvider) Prepare(_ context.Context, envelope executor.Envelope) (executor.Prepared, error) {
	if p.prepareErr != nil {
		return executor.Prepared{}, p.prepareErr
	}
	return executor.Prepared{ID: "injected-prepared", Envelope: envelope, Placement: "fake", Applied: envelope.Policy}, nil
}

func (p *failureInjectionProvider) Run(context.Context, executor.Prepared, executor.Task, executor.EventSink) (executor.Result, error) {
	p.ran = true
	return executor.Result{}, nil
}

func (*failureInjectionProvider) Cancel(context.Context, string) error { return nil }

func TestServiceSelectsInjectedContainerExecutor(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    executor: container\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	builtins := NewBuiltinExecutors()
	builtins.Container = executor.NewContainerProvider(executor.NewFakeContainerBackend())
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Executors: builtins, Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
		return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
	})}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Execution.ExecutionID == "" || result.Execution.ExecutionID[:10] != "container-" {
		t.Fatalf("selected execution %#v", result.Execution)
	}
	if result.Execution.Provider["completion_state_outcome"] != "passed" {
		t.Fatalf("container provider facts %#v", result.Execution.Provider)
	}
}

func TestServiceHostAndFakeRemoteProduceEquivalentStoryRun(t *testing.T) {
	hostRoot := filepath.Join(t.TempDir(), "project")
	requireFiles(t, hostRoot)
	remoteRoot := filepath.Join(t.TempDir(), "project")
	requireFiles(t, remoteRoot)
	raw, err := os.ReadFile(filepath.Join(remoteRoot, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    executor: remote-fake\n")...)
	if err := os.WriteFile(filepath.Join(remoteRoot, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	launch := launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
		return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Summary: "equivalent", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
	})
	run := func(root string) RunResult {
		t.Helper()
		service := Service{ProjectRoot: root, Jobs: newFixedJobStore("job-equivalence"), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Executors: fixtureBuiltinExecutors(), Launcher: launch}
		result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	host := normalizeEquivalentRun(run(hostRoot))
	remote := normalizeEquivalentRun(run(remoteRoot))
	if string(host.Job.Status) != string(artifactjob.StatusDone) || string(remote.Job.Status) != string(artifactjob.StatusDone) {
		t.Fatalf("statuses host=%s remote=%s", host.Job.Status, remote.Job.Status)
	}
	if string(host.Verdict.Outcome) != "passed" || !host.Verdict.PromotionEligible {
		t.Fatalf("host verdict %#v", host.Verdict)
	}
	if string(remote.Verdict.Outcome) != "passed" || !remote.Verdict.PromotionEligible {
		t.Fatalf("remote verdict %#v", remote.Verdict)
	}
	if !reflect.DeepEqual(host.Verdict, remote.Verdict) || !reflect.DeepEqual(host.Execution, remote.Execution) || host.Envelope.Digest != remote.Envelope.Digest {
		t.Fatalf("host=%#v\nremote=%#v", host, remote)
	}
}

func TestServiceAddsRequiredHygieneCheckToVerdict(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\ncleanup:\n  keep_runs: 10\n  require_hygiene_check: true\n  max_reclaimable_bytes: 100\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{
		ProjectRoot: root,
		Jobs:        artifactjob.NewMemoryStore(),
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Executors:   fixtureBuiltinExecutors(),
		Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
			return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
		}),
		Hygiene: HygienePlannerFunc(func(_ context.Context, policy CleanupPolicy) (HygieneReport, error) {
			if policy.KeepRuns != 10 || policy.MaxReclaimableBytes != 100 {
				t.Fatalf("policy %#v", policy)
			}
			return HygieneReport{Schema: "capsule-hygiene-plan/v1", Candidates: 2, TotalBytes: 50, EvidenceRef: "artifact:hygiene-plan"}, nil
		}),
	}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verdict.PromotionEligible {
		t.Fatalf("verdict %#v", result.Verdict)
	}
	got := checkByID(result.Verdict.Checks, "capsule-hygiene")
	if got == nil || got.Outcome != "passed" || len(got.Evidence) != 1 || got.Evidence[0] != "artifact:hygiene-plan" {
		t.Fatalf("hygiene check %#v", got)
	}
}

func TestServiceHygieneDebtBlocksPromotion(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\ncleanup:\n  require_hygiene_check: true\n  max_reclaimable_bytes: 100\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{
		ProjectRoot: root,
		Jobs:        artifactjob.NewMemoryStore(),
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Executors:   fixtureBuiltinExecutors(),
		Launcher: launcher(func(_ context.Context, p executor.Prepared) (Verdict, error) {
			return Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: p.Envelope.SourceDigest, StoryDigest: p.Envelope.StoryDigest, EnvironmentDigest: p.Envelope.Environment.Digest, EnvelopeDigest: p.Envelope.Digest}, nil
		}),
		Hygiene: HygienePlannerFunc(func(context.Context, CleanupPolicy) (HygieneReport, error) {
			return HygieneReport{Schema: "capsule-hygiene-plan/v1", Candidates: 3, TotalBytes: 101, EvidenceRef: "artifact:hygiene-plan"}, nil
		}),
	}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Outcome != "failed" || result.Verdict.PromotionEligible {
		t.Fatalf("verdict %#v", result.Verdict)
	}
	got := checkByID(result.Verdict.Checks, "capsule-hygiene")
	if got == nil || got.Outcome != "failed" {
		t.Fatalf("hygiene check %#v", got)
	}
}

func TestPipelineCleanupPolicyOverridesGlobalRetention(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw := []byte("schema: capsule-ci/v1\ndefault_environment: ci\ncleanup:\n  keep_runs: 20\n  require_hygiene_check: true\n  max_reclaimable_bytes: 100\npipelines:\n  change:\n    story: .kitsoki/stories/ci/app.yaml\n    triggers: [local]\n    cleanup:\n      keep_runs: 5\n      max_reclaimable_bytes: 10\n    result:\n      schema: capsule-ci-verdict/v1\n")
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}}
	pipeline, _, err := service.Plan(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if pipeline.Cleanup.KeepRuns != 5 || pipeline.Cleanup.MaxReclaimableBytes != 10 || !pipeline.Cleanup.RequireHygieneCheck {
		t.Fatalf("cleanup policy %#v", pipeline.Cleanup)
	}
}

func normalizeEquivalentRun(result RunResult) RunResult {
	result.Execution.ExecutionID = ""
	result.Job.ID = ""
	result.Job.CreatedAt = result.Job.CreatedAt.UTC()
	result.Job.UpdatedAt = result.Job.CreatedAt
	return result
}

func checkByID(checks []Check, id string) *Check {
	for i := range checks {
		if checks[i].ID == id {
			return &checks[i]
		}
	}
	return nil
}

func TestServiceAcceptsTypedVerdictFromRemoteWorkerWithoutLocalLauncher(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    executor: remote\nremotes:\n  remote:\n    endpoint: https://worker.invalid\n    credential_env: KITSOKI_TEST_REMOTE_TOKEN\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITSOKI_TEST_REMOTE_TOKEN", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capsules/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": executor.Capabilities{ID: "remote", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none"}, Cancellable: true}})
		case "/v1/capsules/validate":
			w.WriteHeader(http.StatusNoContent)
		case "/v1/capsules/run":
			if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
				t.Errorf("authorization %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "secret-token") {
				t.Fatal("credential leaked into remote payload")
			}
			var req struct {
				Prepared executor.Prepared `json:"prepared"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			verdict := Verdict{Schema: VerdictSchema, Pipeline: "change", Outcome: "passed", Checks: []Check{{ID: "test", Kind: "deterministic", Outcome: "passed", Evidence: []string{"artifact:test"}}}, PromotionEligible: true, SourceDigest: req.Prepared.Envelope.SourceDigest, StoryDigest: req.Prepared.Envelope.StoryDigest, EnvironmentDigest: req.Prepared.Envelope.Environment.Digest, EnvelopeDigest: req.Prepared.Envelope.Digest}
			raw, _ := json.Marshal(verdict)
			_ = json.NewEncoder(w).Encode(map[string]any{"result": executor.Result{VerdictArtifact: "artifact:verdict", VerdictJSON: raw}, "run": executor.ExecutionStatus{ExecutionID: req.Prepared.ID, Status: "completed", Stage: "terminal"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	service := Service{ProjectRoot: root, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })}, Executors: ConfiguredExecutors{Builtins: NewBuiltinExecutors(), Remotes: cfg.Remotes, Client: ciRewriteClient(t, server)}}
	result, err := service.Run(context.Background(), RunRequest{Pipeline: "change", Workspace: control.Handle{ID: "w", Generation: 1}, DefinitionDigest: "sha256:def", SourceDigest: "sha256:source", StoryDigest: "sha256:story", Trigger: Trigger{Kind: "local"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Outcome != "passed" || result.Execution.ExecutionID == "" {
		t.Fatalf("remote result %#v", result)
	}
}

func TestValidateRejectsUndeclaredRemoteExecutor(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    executor: remote\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "executor \"remote\" is not configured") {
		t.Fatalf("expected undeclared executor error, got %v", err)
	}
}

func TestValidateRequiresReceiptSignerWhenSignaturesAreRequired(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\nreceipt:\n  require_signature: true\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "receipt signer is required") {
		t.Fatalf("expected signer policy error, got %v", err)
	}
}

func TestValidateAllowedAgentsRequireBudgetAndFallback(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki", "ci.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n    agents:\n      policy: allow\n      profiles: [local]\n")...)
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "ci.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "allowed agents require profiles, budget, and fallback") {
		t.Fatalf("expected budget/fallback policy error, got %v", err)
	}
}

func ciRewriteClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: ciRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(request)
	})}
}

type ciRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ciRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
func requireFiles(t *testing.T, root string) {
	t.Helper()
	for path, raw := range map[string]string{".kitsoki/environments/ci.yaml": "schema: capsule-environment/v1\nid: ci\nsource:\n  host_probe: true\ntoolchains:\n  go: '1.25'\n", ".kitsoki/ci.yaml": "schema: capsule-ci/v1\ndefault_environment: ci\npipelines:\n  change:\n    story: .kitsoki/stories/ci/app.yaml\n    triggers: [local]\n    result:\n      schema: capsule-ci-verdict/v1\n", ".kitsoki/stories/ci/app.yaml": "app:\n  id: ci\nrooms:\n  idle:\n    view: ok\n"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileRunStorePersistsCompletedRun(t *testing.T) {
	store := FileRunStore{ProjectRoot: t.TempDir()}
	want := RunRecord{JobID: "job-1", Result: RunResult{Job: artifactjob.Job{ID: "job-1"}, Verdict: Verdict{Schema: VerdictSchema, Outcome: "passed"}}}
	if err := store.Write(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("job-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Verdict.Outcome != "passed" {
		t.Fatalf("record %#v", got)
	}
}

func TestFileRunStoreListSkipsReceiptSidecars(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	if err := store.Write(RunRecord{JobID: "job", Result: RunResult{Job: artifactjob.Job{ID: "job"}}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.WriteFile(filepath.Join(dir, "job.receipt.json"), []byte(`{"job_id":"job"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].JobID != "job" {
		t.Fatalf("records %#v", got)
	}
}

func TestFileRunStoreIndexProjectsReceiptAndDigestSummary(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	job := artifactjob.Job{ID: "job", Status: artifactjob.StatusDone, Story: "stories/capsule-ci/app.yaml", WorkspaceInstanceID: "workspace-1"}
	result := RunResult{
		Job: job,
		Envelope: executor.Envelope{
			Digest:       "sha256:envelope",
			SourceDigest: "sha256:source",
			StoryDigest:  "sha256:story",
			Environment:  environment.Lock{Digest: "sha256:env"},
		},
		Verdict: Verdict{Pipeline: "change", Outcome: "passed", PromotionEligible: true},
	}
	if err := store.Write(RunRecord{JobID: "job", Result: result, ReceiptID: "sha256:receipt", ReceiptVerification: "valid"}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.WriteFile(filepath.Join(dir, "job.receipt.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "job.trace.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := store.Index()
	if err != nil {
		t.Fatal(err)
	}
	if index.Schema != RunIndexSchema || len(index.Runs) != 1 {
		t.Fatalf("index %#v", index)
	}
	run := index.Runs[0]
	if run.ReceiptID != "sha256:receipt" || run.ReceiptVerification != "valid" || !run.PromotionEligible {
		t.Fatalf("receipt projection %#v", run)
	}
	if run.SourceDigest != "sha256:source" || run.StoryDigest != "sha256:story" || run.EnvironmentDigest != "sha256:env" || run.EnvelopeDigest != "sha256:envelope" {
		t.Fatalf("digest projection %#v", run)
	}
	if run.TracePath != ".capsules/ci/job.trace.json" || run.ReceiptPath != ".capsules/ci/job.receipt.json" {
		t.Fatalf("sidecar projection %#v", run)
	}
}

func TestFileRunStoreProviderSummaryUsesRunIndexEvidence(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	writeRun := func(id, outcome, receiptStatus string, eligible bool) {
		t.Helper()
		result := RunResult{
			Job:      artifactjob.Job{ID: artifactjob.JobID(id), Status: artifactjob.StatusDone},
			Envelope: executor.Envelope{Digest: "sha256:env-" + id, SourceDigest: "sha256:source-" + id, StoryDigest: "sha256:story-" + id, Environment: environment.Lock{Digest: "sha256:environment-" + id}},
			Verdict:  Verdict{Pipeline: "change", Outcome: outcome, PromotionEligible: eligible},
		}
		if err := store.Write(RunRecord{JobID: id, Result: result, ReceiptID: "sha256:receipt-" + id, ReceiptVerification: receiptStatus}); err != nil {
			t.Fatal(err)
		}
	}
	writeRun("b", "failed", "invalid", false)
	writeRun("a", "passed", "valid", true)
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.WriteFile(filepath.Join(dir, "a.trace.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := store.ProviderSummary(1)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Schema != ProviderSummarySchema || summary.Total != 2 || summary.Passed != 1 || summary.Failed != 1 || summary.PromotionEligible != 1 {
		t.Fatalf("summary %#v", summary)
	}
	if len(summary.Latest) != 1 || summary.Latest[0].JobID != "b" {
		t.Fatalf("latest %#v", summary.Latest)
	}
	if !strings.Contains(summary.Markdown, "Capsule CI: 2 run(s)") || !strings.Contains(summary.Markdown, "| b | change | failed | invalid |") {
		t.Fatalf("markdown %s", summary.Markdown)
	}
	for _, field := range summary.ProviderSafeFields {
		if strings.Contains(field, "secret") {
			t.Fatalf("unsafe provider field %q", field)
		}
	}
}

func TestFileRunStoreDiagnoseSummarizesFailureTrace(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	result := RunResult{
		Job: artifactjob.Job{ID: "job-fail", Status: artifactjob.StatusFailed},
		Envelope: executor.Envelope{
			Digest:       "sha256:envelope",
			SourceDigest: "sha256:source",
			StoryDigest:  "sha256:story",
			Environment:  environment.Lock{Digest: "sha256:environment"},
		},
		Verdict: Verdict{Pipeline: "change", Outcome: "failed"},
	}
	if err := store.Write(RunRecord{JobID: "job-fail", Result: result, DiagnosticError: "remote worker returned 502"}); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(root, ".capsules", "ci", "job-fail.trace.json")
	raw := []byte(`{"schema":"capsule-ci-trace/v1","events":[
{"kind":"capsule.executor.started","at":"2026-07-11T00:00:00Z","job_id":"job-fail","envelope_digest":"sha256:envelope","fields":{"execution_id":"remote-1","transport":"https","remote_host":"worker.example"}},
{"kind":"capsule.executor.failed","at":"2026-07-11T00:00:01Z","job_id":"job-fail","envelope_digest":"sha256:envelope","fields":{"execution_id":"remote-1","status":502,"error_kind":"http_status","message":"bad gateway","token":"must-not-leak"}},
{"kind":"capsule.ci.verdict","at":"2026-07-11T00:00:02Z","job_id":"job-fail","envelope_digest":"sha256:envelope","outcome":"failed"}
]}`)
	if err := os.WriteFile(tracePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	diagnosis, err := store.Diagnose("job-fail")
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.Schema != RunDiagnosisSchema || diagnosis.FailureKind != "executor_failed" || diagnosis.FailureSummary != "http_status" || diagnosis.TerminalError != "remote worker returned 502" {
		t.Fatalf("diagnosis %#v", diagnosis)
	}
	if diagnosis.ExecutorEventCount != 2 || diagnosis.LastExecutorEvent == nil || diagnosis.LastExecutorEvent.Kind != "capsule.executor.failed" {
		t.Fatalf("executor summary %#v", diagnosis)
	}
	if _, ok := diagnosis.LastExecutorEvent.Fields["token"]; ok {
		t.Fatalf("unsafe field leaked: %#v", diagnosis.LastExecutorEvent.Fields)
	}
	if len(diagnosis.Artifacts) != 1 || diagnosis.Artifacts[0].Path != ".capsules/ci/job-fail.trace.json" {
		t.Fatalf("artifacts %#v", diagnosis.Artifacts)
	}
	if len(diagnosis.NextCommands) == 0 || !strings.Contains(strings.Join(diagnosis.NextCommands, "\n"), "capsule ci status --job job-fail") {
		t.Fatalf("next commands %#v", diagnosis.NextCommands)
	}
}

func TestFileRunStoreDiagnoseLatestDetectsStallAndOpenExecutorSpan(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	old := time.Now().UTC().Add(-time.Hour)
	if err := store.Write(RunRecord{JobID: "job-old", Result: RunResult{Job: artifactjob.Job{ID: "job-old", Status: artifactjob.StatusDone}, UpdatedAt: old.Add(-time.Hour), Terminal: true}}); err != nil {
		t.Fatal(err)
	}
	result := RunResult{Job: artifactjob.Job{ID: "job-running", Status: artifactjob.StatusRunning, WorkspaceInstanceID: "workspace-1"}, Pipeline: "change", Stage: RunStageRunning, StartedAt: old.Add(-time.Minute), UpdatedAt: old, Terminal: false, Envelope: executor.Envelope{Digest: "sha256:envelope"}, Execution: executor.Result{ExecutionID: "remote-1"}}
	if err := store.Write(RunRecord{JobID: "job-running", Result: result}); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(root, ".capsules", "ci", "job-running.trace.json")
	raw := []byte(fmt.Sprintf(`{"schema":"capsule-ci-trace/v1","events":[{"kind":"capsule.executor.started","at":%q,"job_id":"job-running","envelope_digest":"sha256:envelope","outcome":"running","fields":{"execution_id":"remote-1","request_id":"req-1"}}]}`, old.Format(time.RFC3339Nano)))
	if err := os.WriteFile(tracePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	diagnosis, err := store.DiagnoseLatest(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.Run.JobID != "job-running" || !diagnosis.Stalled || !diagnosis.ExecutorSpanOpen || diagnosis.LastExecutorEvent == nil {
		t.Fatalf("diagnosis %#v", diagnosis)
	}
	if diagnosis.LastActivityAt.IsZero() || !strings.Contains(diagnosis.StallReason, "no durable activity") {
		t.Fatalf("stall details %#v", diagnosis)
	}
}

func TestFileRunStoreDiagnosisConsumesProviderSafeWorkerAgentBreadcrumbs(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	lastActivity := now.Add(-5 * time.Minute)
	result := RunResult{Job: artifactjob.Job{ID: "job-agent-stall", Status: artifactjob.StatusRunning}, Pipeline: "change", Stage: RunStageRunning, StartedAt: lastActivity.Add(-time.Minute), UpdatedAt: lastActivity, Execution: executor.Result{ExecutionID: "remote-agent-1"}}
	if err := store.Write(RunRecord{JobID: "job-agent-stall", Result: result}); err != nil {
		t.Fatal(err)
	}
	callRef := "sha256:" + strings.Repeat("a", 24)
	status := executor.ExecutionStatus{
		Schema:      executor.ExecutionStatusSchema,
		ExecutionID: "remote-agent-1",
		Status:      "running",
		Stage:       "running_story",
		UpdatedAt:   lastActivity,
		Agent: &executor.AgentDiagnostics{
			Schema:         executor.AgentDiagnosticsSchema,
			TraceState:     "ok",
			ObservedAt:     now,
			LastActivityAt: lastActivity,
			StallHint:      "process_no_output",
			ActiveCalls:    []executor.AgentCallStatus{{CallRef: callRef, Verb: "task", Backend: "claude", Phase: "process_no_output", StartedAt: lastActivity.Add(-time.Minute), LastActivityAt: lastActivity}},
			Breadcrumbs:    []executor.AgentBreadcrumb{{At: lastActivity, Kind: "process_no_output", CallRef: callRef, Backend: "claude", Severity: "warn", DurationMS: 240000}},
		},
		Cleanup: &executor.WorkerCleanupDiagnostics{Schema: executor.WorkerCleanupDiagnosticsSchema, Outcome: "completed", CompletedAt: now.Add(-time.Hour), RunsRemoved: 2, SourcesRemoved: 1, ReclaimedBytes: 4096},
	}
	if _, err := store.RecordExecutorStatus("job-agent-stall", status); err != nil {
		t.Fatal(err)
	}
	diagnosis, err := store.DiagnoseAt("job-agent-stall", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !diagnosis.Stalled || diagnosis.Agent == nil || diagnosis.Agent.StallHint != "process_no_output" || len(diagnosis.Agent.Breadcrumbs) != 1 {
		t.Fatalf("diagnosis = %#v", diagnosis)
	}
	if !diagnosis.LastActivityAt.Equal(lastActivity) || !strings.Contains(diagnosis.StallReason, "agent process_no_output") {
		t.Fatalf("stall details = %#v", diagnosis)
	}
	if diagnosis.WorkerCleanup == nil || diagnosis.WorkerCleanup.ReclaimedBytes != 4096 {
		t.Fatalf("cleanup diagnostics = %#v", diagnosis.WorkerCleanup)
	}
}

func TestFileRunStoreDiagnoseFlagsTerminalUnclosedExecutorSpan(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	updated := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	result := RunResult{Job: artifactjob.Job{ID: "job-terminal", Status: artifactjob.StatusFailed}, Pipeline: "change", Stage: RunStageFailed, UpdatedAt: updated, Terminal: true, Envelope: executor.Envelope{Digest: "sha256:envelope"}, Execution: executor.Result{ExecutionID: "remote-1"}}
	if err := store.Write(RunRecord{JobID: "job-terminal", Result: result}); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(root, ".capsules", "ci", "job-terminal.trace.json")
	raw := []byte(`{"schema":"capsule-ci-trace/v1","events":[{"kind":"capsule.executor.started","at":"2026-07-11T00:59:00Z","job_id":"job-terminal","envelope_digest":"sha256:envelope","fields":{"execution_id":"remote-1"}}]}`)
	if err := os.WriteFile(tracePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	diagnosis, err := store.DiagnoseAt("job-terminal", updated, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.FailureKind != "executor_span_unclosed" || !diagnosis.ExecutorSpanOpen || diagnosis.Stalled {
		t.Fatalf("diagnosis %#v", diagnosis)
	}
}

func TestFileRunStoreDiagnoseShowsWorkerSourceTimelineAndClosesCancelledSpan(t *testing.T) {
	root := t.TempDir()
	store := FileRunStore{ProjectRoot: root}
	updated := time.Date(2026, 7, 11, 6, 0, 0, 0, time.UTC)
	result := RunResult{Job: artifactjob.Job{ID: "job-cancelled", Status: artifactjob.StatusCancelled}, Pipeline: "change", Stage: RunStageFinished, UpdatedAt: updated, Terminal: true, Envelope: executor.Envelope{Digest: "sha256:envelope"}, Execution: executor.Result{ExecutionID: "remote-1"}, Verdict: Verdict{Outcome: "cancelled"}}
	if err := store.Write(RunRecord{JobID: "job-cancelled", Result: result}); err != nil {
		t.Fatal(err)
	}
	doc := capsuletrace.NewDocument(
		capsuletrace.Event{Kind: capsuletrace.KindExecutorStarted, At: updated.Add(-4 * time.Second), JobID: "job-cancelled", EnvelopeDigest: "sha256:envelope", Fields: map[string]any{"execution_id": "remote-1"}},
		capsuletrace.Event{Kind: capsuletrace.KindExecutorSourceReady, At: updated.Add(-3 * time.Second), JobID: "job-cancelled", EnvelopeDigest: "sha256:envelope", Fields: map[string]any{"execution_id": "remote-1", "source_cache": "hit", "bundle_digest": "sha256:bundle"}},
		capsuletrace.Event{Kind: capsuletrace.KindWorkerStoryStarted, At: updated.Add(-2 * time.Second), JobID: "job-cancelled", EnvelopeDigest: "sha256:envelope", Fields: map[string]any{"execution_id": "remote-1", "worker_stage": "running_story"}},
		capsuletrace.Event{Kind: capsuletrace.KindExecutorCancelled, At: updated, JobID: "job-cancelled", EnvelopeDigest: "sha256:envelope", Outcome: "cancelled", Fields: map[string]any{"execution_id": "remote-1", "worker_status": "cancelled"}},
	)
	raw, err := capsuletrace.MarshalDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".capsules", "ci", "job-cancelled.trace.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	diagnosis, err := store.DiagnoseAt("job-cancelled", updated.Add(time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if diagnosis.ExecutorSpanOpen || len(diagnosis.Timeline) != 4 {
		t.Fatalf("diagnosis %#v", diagnosis)
	}
	if diagnosis.Timeline[1].Fields["source_cache"] != "hit" || diagnosis.Timeline[2].Fields["worker_stage"] != "running_story" {
		t.Fatalf("timeline %#v", diagnosis.Timeline)
	}
}

func TestFileRunStoreIndexEmptyRunsSlice(t *testing.T) {
	index, err := (FileRunStore{ProjectRoot: t.TempDir()}).Index()
	if err != nil {
		t.Fatal(err)
	}
	if index.Runs == nil || len(index.Runs) != 0 {
		t.Fatalf("runs %#v", index.Runs)
	}
}

func TestFileRunStoreCancelParksOrRunningJob(t *testing.T) {
	store := FileRunStore{ProjectRoot: t.TempDir()}
	if err := store.Write(RunRecord{JobID: "job-cancel", Result: RunResult{Job: artifactjob.Job{ID: "job-cancel", Status: artifactjob.StatusAwaitingInput}, Verdict: Verdict{Outcome: "needs_input"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Cancel("job-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Job.Status != artifactjob.StatusCancelled || got.Result.Verdict.Outcome != "cancelled" {
		t.Fatalf("cancel %#v", got)
	}
}

func TestFileRunStoreRejectsTraversalJobIDs(t *testing.T) {
	store := FileRunStore{ProjectRoot: t.TempDir()}
	for _, id := range []string{"../outside", "nested/job", "", strings.Repeat("a", 129)} {
		if err := store.Write(RunRecord{JobID: id}); err == nil {
			t.Fatalf("Write accepted %q", id)
		}
		if _, err := store.Get(id); err == nil {
			t.Fatalf("Get accepted %q", id)
		}
		if _, err := store.Cancel(id); err == nil {
			t.Fatalf("Cancel accepted %q", id)
		}
		if _, err := store.RecordExecutorStatus(id, executor.ExecutionStatus{}); err == nil {
			t.Fatalf("RecordExecutorStatus accepted %q", id)
		}
	}
}

func TestFileRunStoreProjectsTwoPhaseRemoteCancellationTruthfully(t *testing.T) {
	store := FileRunStore{ProjectRoot: t.TempDir()}
	result := RunResult{Job: artifactjob.Job{ID: "job-remote", Status: artifactjob.StatusRunning}, Pipeline: "change", Stage: RunStageRunning, Execution: executor.Result{ExecutionID: "remote-1"}, Envelope: executor.Envelope{SourceDigest: "source", StoryDigest: "story", Environment: environment.Lock{Digest: "environment"}, Digest: "envelope"}}
	if err := store.Write(RunRecord{JobID: "job-remote", Result: result}); err != nil {
		t.Fatal(err)
	}
	requested, err := store.RecordExecutorStatus("job-remote", executor.ExecutionStatus{Schema: executor.ExecutionStatusSchema, ExecutionID: "remote-1", Status: "cancelling", Stage: "cancellation_requested"})
	if err != nil {
		t.Fatal(err)
	}
	if requested.Result.Job.Status != artifactjob.StatusInterrupted || requested.Result.Terminal || requested.Result.Verdict.Outcome == "cancelled" {
		t.Fatalf("cancellation request was projected as terminal: %#v", requested)
	}
	terminal, err := store.RecordExecutorStatus("job-remote", executor.ExecutionStatus{Schema: executor.ExecutionStatusSchema, ExecutionID: "remote-1", Status: "cancelled", Stage: "terminal"})
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Result.Job.Status != artifactjob.StatusCancelled || !terminal.Result.Terminal || terminal.Result.Verdict.Outcome != "cancelled" || terminal.Result.Verdict.PromotionEligible {
		t.Fatalf("terminal cancellation %#v", terminal)
	}
}

func TestFileRunStoreDoesNotPromoteRemoteCompletedExecutionBeforeCollection(t *testing.T) {
	store := FileRunStore{ProjectRoot: t.TempDir()}
	result := RunResult{Job: artifactjob.Job{ID: "job-remote", Status: artifactjob.StatusRunning}, Pipeline: "change", Stage: RunStageRunning, Execution: executor.Result{ExecutionID: "remote-1"}}
	if err := store.Write(RunRecord{JobID: "job-remote", Result: result}); err != nil {
		t.Fatal(err)
	}
	record, err := store.RecordExecutorStatus("job-remote", executor.ExecutionStatus{Schema: executor.ExecutionStatusSchema, ExecutionID: "remote-1", Status: "completed", Stage: "terminal", Result: executor.Result{ExecutionID: "remote-1", VerdictJSON: []byte(`{"outcome":"passed"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if record.Result.Job.Status != artifactjob.StatusInterrupted || record.Result.Stage != RunStageCollecting || record.Result.Terminal || record.Result.Verdict.PromotionEligible {
		t.Fatalf("uncollected execution was promoted: %#v", record)
	}
}
