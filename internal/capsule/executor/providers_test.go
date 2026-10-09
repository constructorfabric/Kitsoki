package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
)

func TestHostAndFakeRemoteProduceNormalizedParity(t *testing.T) {
	lock := testEnvironmentLock(t)
	lock.Network = "live"
	lock, err := environment.SealLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: lock, Policy: Policy{Network: "live", ExternalWrite: "allow"}})
	if err != nil {
		t.Fatal(err)
	}
	task := func(context.Context, Prepared) (Result, error) {
		return Result{ExitCode: 0, VerdictArtifact: "artifact:verdict", Artifacts: []string{"artifact:b", "artifact:a"}}, nil
	}
	host := NewHostProvider()
	worker := NewFakeRemoteWorker()
	worker.Cap.Networks = []string{"live"}
	remote := NewRemoteProvider(worker)
	hp, err := host.Prepare(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	rp, err := remote.Prepare(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	hr, err := host.Run(context.Background(), hp, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	rr, err := remote.Run(context.Background(), rp, task, nil)
	if err != nil {
		t.Fatal(err)
	}
	hr.ExecutionID = ""
	rr.ExecutionID = ""
	if !reflect.DeepEqual(hr, rr) {
		t.Fatalf("host=%#v remote=%#v", hr, rr)
	}
	if err := remote.Cancel(context.Background(), rp.ID); err != nil {
		t.Fatal(err)
	}
}

func TestContainerProviderAdaptsCompletionState(t *testing.T) {
	envelope, err := Seal(Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: testEnvironmentLock(t), Policy: Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	provider := NewContainerProvider(NewFakeContainerBackend())
	prepared, err := provider.Prepare(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Run(context.Background(), prepared, func(context.Context, Prepared) (Result, error) {
		return Result{ExitCode: 0, VerdictArtifact: "artifact:verdict"}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider["completion_state_schema"] != CompletionStateSchema || result.Provider["completion_state_outcome"] != "passed" {
		t.Fatalf("missing completion-state provider facts: %#v", result)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0] != "completion-state:"+prepared.ID {
		t.Fatalf("missing completion-state artifact: %#v", result.Artifacts)
	}
	if err := provider.Cancel(context.Background(), prepared.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProviderPrepareEnforcesMinimumSandboxWithoutDoctor(t *testing.T) {
	envelope, err := Seal(Envelope{
		JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story",
		Environment: testEnvironmentLockWithSandbox(t, "container"), Policy: Policy{Network: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	container := NewFakeContainerBackend()
	providers := []struct {
		name     string
		provider Provider
		wantErr  bool
	}{
		{name: "host", provider: NewHostProvider(), wantErr: true},
		{name: "fake", provider: NewFakeProvider("fake"), wantErr: true},
		{name: "remote", provider: NewRemoteProvider(NewFakeRemoteWorker()), wantErr: true},
		{name: "container", provider: NewContainerProvider(container)},
	}
	for _, test := range providers {
		t.Run(test.name, func(t *testing.T) {
			if host, ok := test.provider.(*HostProvider); ok {
				host.Cap.Networks = []string{"none"} // isolate the sandbox assertion.
			}
			_, err := test.provider.Prepare(context.Background(), envelope)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "below required sandbox") {
					t.Fatalf("Prepare() error = %v, want sandbox rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
		})
	}
}

func TestDockerBackendBuildsArenaStyleContainerRun(t *testing.T) {
	workspace := t.TempDir()
	lock := testEnvironmentLock(t)
	lock.ImageDigest = "alpine@sha256:123"
	lock, err := environment.SealLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source", StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: lock, Policy: Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	var argv []string
	backend := &DockerBackend{
		Context: "local-vm",
		WorkspacePath: func(context.Context, Prepared) (string, error) {
			return workspace, nil
		},
		Runner: DockerRunnerFunc(func(_ context.Context, got []string) (ContainerRunOutput, error) {
			argv = append([]string(nil), got...)
			results := dockerMountSource(t, got, "/results")
			raw, _ := json.Marshal(dockerResultFile{
				Result:          Result{ExitCode: 0, VerdictArtifact: "artifact:verdict"},
				CompletionState: CompletionState{Schema: CompletionStateSchema, Outcome: "passed", Artifacts: []string{"artifact:completion-state"}},
			})
			if err := os.WriteFile(filepath.Join(results, "result.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(results, "story-trace.jsonl"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return ContainerRunOutput{ExitCode: 0, Stdout: "ok\n"}, nil
		}),
	}
	provider := NewContainerProvider(backend)
	prepared, err := provider.Prepare(context.Background(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Run(context.Background(), prepared, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(argv[:4], []string{"docker", "--context", "local-vm", "run"}) {
		t.Fatalf("docker argv %#v", argv)
	}
	if got := dockerMountSource(t, argv, "/workspace"); got != workspace {
		t.Fatalf("workspace mount %q, want %q in %#v", got, workspace, argv)
	}
	if !slices.Contains(argv, "alpine@sha256:123") {
		t.Fatalf("image missing from argv %#v", argv)
	}
	if !containsArgPair(argv, "--network", "none") {
		t.Fatalf("network:none was not enforced in %#v", argv)
	}
	if !containsArgPair(argv, "--trace", "/results/story-trace.jsonl") {
		t.Fatalf("worker trace output missing from %#v", argv)
	}
	if result.Provider["completion_state_schema"] != CompletionStateSchema || result.Provider["completion_state_outcome"] != "passed" {
		t.Fatalf("completion-state provider facts %#v", result.Provider)
	}
	if !slices.Contains(result.Artifacts, "artifact:completion-state") {
		t.Fatalf("completion-state artifact missing: %#v", result.Artifacts)
	}
	if !slices.Contains(result.Artifacts, "container:story-trace") {
		t.Fatalf("story trace artifact missing: %#v", result.Artifacts)
	}
}

func TestDockerBackendAdvertisesReplayOnlyWhenEnforced(t *testing.T) {
	backend := NewDockerBackend(func(context.Context, Prepared) (string, error) { return t.TempDir(), nil })
	capabilities, err := backend.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(capabilities.Networks, []string{"none"}) {
		t.Fatalf("default Docker networks = %v", capabilities.Networks)
	}
	backend.ReplayNetwork = "capsule-replay"
	capabilities, err = backend.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(capabilities.Networks, []string{"none", "replay"}) {
		t.Fatalf("configured Docker networks = %v", capabilities.Networks)
	}
}

func containsArgPair(argv []string, key, value string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == key && argv[i+1] == value {
			return true
		}
	}
	return false
}

func dockerMountSource(t *testing.T, argv []string, dest string) string {
	t.Helper()
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] != "-v" {
			continue
		}
		src, dst, ok := strings.Cut(argv[i+1], ":")
		if ok && dst == dest {
			return src
		}
	}
	t.Fatalf("mount %s not found in %#v", dest, argv)
	return ""
}
