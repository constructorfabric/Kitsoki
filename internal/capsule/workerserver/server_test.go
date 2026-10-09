package workerserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/storydigest"
	"kitsoki/internal/capsule/storylauncher"
	"kitsoki/internal/capsule/workerserver"
	"kitsoki/internal/capsuletest"
)

func TestAuthenticatedWorkerMaterializesAndVerifiesPortableSource(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := filepath.ToSlash(filepath.Join(".kitsoki", "stories", "ci", "app.yaml"))
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add no-LLM CI story")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	closure, err := storydigest.Compute(project, storyRel)
	if err != nil {
		t.Fatal(err)
	}
	envLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, workspace string, prepared executor.Prepared, _ string) (executor.Result, error) {
		if prepared.ID == "remote-nonzero" {
			return executor.Result{ExitCode: 7, VerdictArtifact: "verdict:failed"}, nil
		}
		verdict, err := (storylauncher.Launcher{StoryPath: filepath.Join(workspace, prepared.Envelope.StoryPath)}).Launch(ctx, prepared)
		if err != nil {
			return executor.Result{}, err
		}
		raw, err := json.Marshal(verdict)
		return executor.Result{ExitCode: 0, VerdictArtifact: "verdict:worker-test", VerdictJSON: raw}, err
	}
	worker, err := workerserver.New(workerserver.Config{Root: t.TempDir(), Token: "test-token", RequireAuth: true, Capabilities: isolatedTestCapabilities(), Runner: runner, Environment: environment.Verifier{}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(worker.Handler())
	t.Cleanup(server.Close)
	client := server.Client()

	unauthorized, err := client.Get(server.URL + "/v1/capsules/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}

	upload, err := http.NewRequest(http.MethodPut, server.URL+"/v1/capsules/sources/"+head, bytes.NewReader(bundle.Data))
	if err != nil {
		t.Fatal(err)
	}
	upload.Header.Set("Authorization", "Bearer test-token")
	upload.Header.Set("X-Kitsoki-Bundle-Digest", bundle.Digest)
	response, err := client.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	readAndClose(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d", response.StatusCode)
	}

	envelope, err := executor.Seal(executor.Envelope{
		JobID:            "job-remote-e2e",
		ProjectID:        "worker-test",
		DefinitionDigest: "sha256:def",
		Instance:         control.Handle{ID: "workspace", Generation: 1},
		SourceDigest:     head,
		StoryPath:        storyRel,
		StoryDigest:      closure.Digest,
		Environment:      envLock,
		Trigger:          map[string]any{"kind": "local", "requested_pipeline": "change"},
		Policy:           executor.Policy{Network: "none", ExternalWrite: "deny"},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := executor.Prepared{ID: "remote-e2e", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	body, _ := json.Marshal(map[string]any{"prepared": prepared})
	runRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/capsules/run", bytes.NewReader(body))
	runRequest.Header.Set("Authorization", "Bearer test-token")
	runRequest.Header.Set("Content-Type", "application/json")
	runResponse, err := client.Do(runRequest)
	if err != nil {
		t.Fatal(err)
	}
	raw := readAndClose(t, runResponse)
	if runResponse.StatusCode != http.StatusOK {
		t.Fatalf("run status = %d: %s", runResponse.StatusCode, raw)
	}
	var completed struct {
		Result executor.Result        `json:"result"`
		Run    workerserver.RunRecord `json:"run"`
	}
	if err := json.Unmarshal(raw, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Run.Status != "completed" || completed.Run.Stage != "terminal" || len(completed.Run.Events) < 4 {
		t.Fatalf("run = %+v", completed.Run)
	}
	if completed.Result.ExecutionID != prepared.ID || len(completed.Result.VerdictJSON) == 0 {
		t.Fatalf("result = %+v", completed.Result)
	}

	nonzero := prepared
	nonzero.ID = "remote-nonzero"
	nonzeroBody, _ := json.Marshal(map[string]any{"prepared": nonzero})
	nonzeroRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/capsules/run", bytes.NewReader(nonzeroBody))
	nonzeroRequest.Header.Set("Authorization", "Bearer test-token")
	nonzeroRequest.Header.Set("Content-Type", "application/json")
	nonzeroResponse, err := client.Do(nonzeroRequest)
	if err != nil {
		t.Fatal(err)
	}
	nonzeroRaw := readAndClose(t, nonzeroResponse)
	if nonzeroResponse.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(nonzeroRaw, []byte("non-zero exit code 7")) || !bytes.Contains(nonzeroRaw, []byte(`"status":"failed"`)) {
		t.Fatalf("nonzero run = %d: %s", nonzeroResponse.StatusCode, nonzeroRaw)
	}

	statusRequest, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/capsules/executions/"+prepared.ID, nil)
	statusRequest.Header.Set("Authorization", "Bearer test-token")
	statusResponse, err := client.Do(statusRequest)
	if err != nil {
		t.Fatal(err)
	}
	statusRaw := readAndClose(t, statusResponse)
	if statusResponse.StatusCode != http.StatusOK || !bytes.Contains(statusRaw, []byte(`"status":"completed"`)) {
		t.Fatalf("status = %d: %s", statusResponse.StatusCode, statusRaw)
	}
}

func TestWorkerRejectsStoryClosureMismatchBeforeRunner(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := "story/app.yaml"
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add story")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	envLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	worker, err := workerserver.New(workerserver.Config{Root: t.TempDir(), Capabilities: isolatedTestCapabilities(), Environment: environment.Verifier{}, Runner: func(context.Context, string, executor.Prepared, string) (executor.Result, error) {
		called = true
		return executor.Result{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	t.Cleanup(server.Close)
	upload, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/capsules/sources/"+head, bytes.NewReader(bundle.Data))
	upload.Header.Set("X-Kitsoki-Bundle-Digest", bundle.Digest)
	response, err := http.DefaultClient.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	readAndClose(t, response)

	envelope, _ := executor.Seal(executor.Envelope{JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: head, StoryPath: storyRel, StoryDigest: "sha256:not-the-story", Environment: envLock, Policy: executor.Policy{Network: "none"}})
	prepared := executor.Prepared{ID: "digest-mismatch", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	body, _ := json.Marshal(map[string]any{"prepared": prepared})
	run, _ := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
	raw := readAndClose(t, run)
	if run.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("story closure digest mismatch")) {
		t.Fatalf("run = %d: %s", run.StatusCode, raw)
	}
	if called {
		t.Fatal("runner was called before story digest verification")
	}
}

func TestWorkerRejectsEnvironmentLockFromDifferentSourceState(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := "story/app.yaml"
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	controllerLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(project, envRel), "schema: capsule-environment/v1\nid: ci\nnetwork: none\nsandbox: supervised\nbootstrap:\n  command: changed-on-worker-source\n")
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add drifted environment")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	closure, err := storydigest.Compute(project, storyRel)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	worker, err := workerserver.New(workerserver.Config{
		Root:         t.TempDir(),
		Capabilities: isolatedTestCapabilities(),
		Environment:  environment.Verifier{},
		Runner: func(context.Context, string, executor.Prepared, string) (executor.Result, error) {
			called = true
			return executor.Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	t.Cleanup(server.Close)
	upload, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/capsules/sources/"+head, bytes.NewReader(bundle.Data))
	upload.Header.Set("X-Kitsoki-Bundle-Digest", bundle.Digest)
	response, err := http.DefaultClient.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	readAndClose(t, response)

	envelope, err := executor.Seal(executor.Envelope{JobID: "job-env-mismatch", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: head, StoryPath: storyRel, StoryDigest: closure.Digest, Environment: controllerLock, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := executor.Prepared{ID: "env-mismatch", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	body, _ := json.Marshal(map[string]any{"prepared": prepared})
	run, _ := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
	raw := readAndClose(t, run)
	if run.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("worker lock mismatch")) || !bytes.Contains(raw, []byte(`"stage":"verify_environment"`)) {
		t.Fatalf("run = %d: %s", run.StatusCode, raw)
	}
	if called {
		t.Fatal("runner was called before environment verification")
	}
}

func TestWorkerReportsTerminalCheckpointPersistenceFailure(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := "story/app.yaml"
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add persistence fixture")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	closure, err := storydigest.Compute(project, storyRel)
	if err != nil {
		t.Fatal(err)
	}
	envLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := workerserver.New(workerserver.Config{
		Root:         t.TempDir(),
		Capabilities: isolatedTestCapabilities(),
		Environment:  environment.Verifier{},
		Runner: func(_ context.Context, _ string, _ executor.Prepared, tracePath string) (executor.Result, error) {
			runDir := filepath.Dir(tracePath)
			if err := os.RemoveAll(runDir); err != nil {
				return executor.Result{}, err
			}
			if err := os.WriteFile(runDir, []byte("blocks run directory recreation"), 0o600); err != nil {
				return executor.Result{}, err
			}
			return executor.Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	t.Cleanup(server.Close)
	upload, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/capsules/sources/"+head, bytes.NewReader(bundle.Data))
	upload.Header.Set("X-Kitsoki-Bundle-Digest", bundle.Digest)
	response, err := http.DefaultClient.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	readAndClose(t, response)
	envelope, err := executor.Seal(executor.Envelope{JobID: "job-persist", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: head, StoryPath: storyRel, StoryDigest: closure.Digest, Environment: envLock, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := executor.Prepared{ID: "persist-failure", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	body, _ := json.Marshal(map[string]any{"prepared": prepared})
	run, err := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw := readAndClose(t, run)
	if run.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(raw, []byte("persist completed checkpoint")) || !bytes.Contains(raw, []byte("persist terminal failed checkpoint")) {
		t.Fatalf("run = %d: %s", run.StatusCode, raw)
	}
}

func TestHTTPControllerUploadsSourceOnceAndReceivesWorkerTimeline(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := "story/app.yaml"
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add portable story")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	closure, err := storydigest.Compute(project, storyRel)
	if err != nil {
		t.Fatal(err)
	}
	envLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, workspace string, prepared executor.Prepared, _ string) (executor.Result, error) {
		verdict, err := (storylauncher.Launcher{StoryPath: filepath.Join(workspace, prepared.Envelope.StoryPath)}).Launch(ctx, prepared)
		raw, _ := json.Marshal(verdict)
		return executor.Result{VerdictJSON: raw, VerdictArtifact: "verdict:http-e2e"}, err
	}
	serverImpl, err := workerserver.New(workerserver.Config{Root: t.TempDir(), Token: "controller-token", RequireAuth: true, Capabilities: isolatedTestCapabilities(), Runner: runner, Environment: environment.Verifier{}})
	if err != nil {
		t.Fatal(err)
	}
	puts := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/capsules/sources/") {
			puts++
		}
		serverImpl.Handler().ServeHTTP(w, r)
	})
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	envelope, err := executor.Seal(executor.Envelope{JobID: "job-controller-e2e", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: head, StoryPath: storyRel, StoryDigest: closure.Digest, Environment: envLock, Trigger: map[string]any{"requested_pipeline": "change"}, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := executor.Prepared{ID: "controller-e2e", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	controller := executor.HTTPRemoteWorker{
		Endpoint: server.URL,
		Client:   server.Client(),
		Credential: func(context.Context) (string, error) {
			return "controller-token", nil
		},
		Source: executor.SourceBundlerFunc(func(context.Context, executor.Envelope) (executor.SourceBundle, error) {
			return bundle, nil
		}),
	}
	for attempt := 0; attempt < 2; attempt++ {
		var events []executor.Event
		result, err := controller.Run(context.Background(), prepared, nil, executor.EventSinkFunc(func(_ context.Context, event executor.Event) error {
			events = append(events, event)
			return nil
		}))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt+1, err)
		}
		if result.ExecutionID != prepared.ID || len(result.VerdictJSON) == 0 {
			t.Fatalf("attempt %d result = %+v", attempt+1, result)
		}
		if !hasEvent(events, "capsule.executor.source.ready") || !hasEvent(events, "capsule.worker.story.started") || !hasEvent(events, "capsule.executor.finished") {
			t.Fatalf("attempt %d events = %#v", attempt+1, events)
		}
	}
	if puts != 1 {
		t.Fatalf("source PUT count = %d, want one upload plus one cache hit", puts)
	}
}

func TestWorkerCancellationIsRequestedThenDurablyTerminalAndIdempotent(t *testing.T) {
	project := capsuletest.Open(t, "clean-repo")
	storyRel := "story/app.yaml"
	write(t, filepath.Join(project, storyRel), passingStory)
	envRel := writeEnvironment(t, project)
	git(t, project, "add", storyRel, envRel)
	git(t, project, "-c", "user.name=Capsule Test", "-c", "user.email=capsule@example.invalid", "commit", "-m", "Add cancellable story")
	head := strings.TrimSpace(git(t, project, "rev-parse", "HEAD"))
	closure, err := storydigest.Compute(project, storyRel)
	if err != nil {
		t.Fatal(err)
	}
	envLock, err := (environment.Resolver{ProjectRoot: project}).Resolve(context.Background(), "ci")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := executor.GitBundle(context.Background(), project, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var calls atomic.Int32
	worker, err := workerserver.New(workerserver.Config{
		Root:         t.TempDir(),
		Capabilities: isolatedTestCapabilities(),
		Environment:  environment.Verifier{},
		Runner: func(ctx context.Context, _ string, _ executor.Prepared, _ string) (executor.Result, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-ctx.Done()
			return executor.Result{}, ctx.Err()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	t.Cleanup(server.Close)
	upload, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/capsules/sources/"+head, bytes.NewReader(bundle.Data))
	upload.Header.Set("X-Kitsoki-Bundle-Digest", bundle.Digest)
	response, err := http.DefaultClient.Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	readAndClose(t, response)
	envelope, err := executor.Seal(executor.Envelope{JobID: "job-cancel", ProjectID: "project", DefinitionDigest: "sha256:def", Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: head, StoryPath: storyRel, StoryDigest: closure.Digest, Environment: envLock, Policy: executor.Policy{Network: "none"}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := executor.Prepared{ID: "cancel-e2e", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
	body, _ := json.Marshal(map[string]any{"prepared": prepared})
	postDone := make(chan *http.Response, 1)
	go func() {
		resp, _ := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
		postDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	cancelRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/v1/capsules/executions/"+prepared.ID, nil)
	cancelResponse, err := http.DefaultClient.Do(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	cancelRaw := readAndClose(t, cancelResponse)
	if cancelResponse.StatusCode != http.StatusAccepted || !bytes.Contains(cancelRaw, []byte(`"status":"cancelling"`)) {
		t.Fatalf("cancel = %d: %s", cancelResponse.StatusCode, cancelRaw)
	}
	postResponse := <-postDone
	postRaw := readAndClose(t, postResponse)
	if postResponse.StatusCode != http.StatusConflict || !bytes.Contains(postRaw, []byte(`"status":"cancelled"`)) || !bytes.Contains(postRaw, []byte("capsule.worker.cancelled")) {
		t.Fatalf("post = %d: %s", postResponse.StatusCode, postRaw)
	}
	statusResponse, err := http.Get(server.URL + "/v1/capsules/executions/" + prepared.ID)
	if err != nil {
		t.Fatal(err)
	}
	statusRaw := readAndClose(t, statusResponse)
	if statusResponse.StatusCode != http.StatusOK || !bytes.Contains(statusRaw, []byte(`"status":"cancelled"`)) {
		t.Fatalf("status = %d: %s", statusResponse.StatusCode, statusRaw)
	}
	retry, err := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	retryRaw := readAndClose(t, retry)
	if retry.StatusCode != http.StatusConflict || calls.Load() != 1 || !bytes.Contains(retryRaw, []byte(`"status":"cancelled"`)) {
		t.Fatalf("retry = %d calls=%d: %s", retry.StatusCode, calls.Load(), retryRaw)
	}
}

func hasEvent(events []executor.Event, kind string) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeEnvironment(t *testing.T, project string) string {
	t.Helper()
	rel := filepath.ToSlash(filepath.Join(".kitsoki", "environments", "ci.yaml"))
	write(t, filepath.Join(project, rel), "schema: capsule-environment/v1\nid: ci\nnetwork: none\nsandbox: supervised\n")
	return rel
}

// isolatedTestCapabilities models a worker enclosed by a test-owned network
// boundary. Production workers must opt into this claim only when their VM or
// container actually enforces it.
func isolatedTestCapabilities() executor.Capabilities {
	return executor.Capabilities{ID: "capsule-http-worker", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none", "replay"}, Cancellable: true}
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", root}, args...)
	out, err := exec.Command("git", argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func readAndClose(t *testing.T, response *http.Response) []byte {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

const passingStory = `app:
  id: remote-worker-test
  version: 0.1.0
  title: Remote worker test
  author: Test
  license: CC0
world:
  ci_job_id: { type: string, default: "" }
  ci_pipeline: { type: string, default: "" }
  ci_trigger: { type: object, default: {} }
  ci_source: { type: object, default: {} }
  ci_workspace: { type: object, default: {} }
  ci_environment: { type: object, default: {} }
  ci_policy: { type: object, default: {} }
  ci_verdict: { type: object, default: {} }
intents:
  run: { description: run, examples: [run], priority: 1 }
root: idle
states:
  idle:
    view: [{ prose: ready }]
    on:
      run:
        - target: done
          effects:
            - set:
                ci_verdict:
                  schema: capsule-ci-verdict/v1
                  pipeline: "{{ world.ci_pipeline }}"
                  outcome: passed
                  summary: no-LLM remote proof
                  checks:
                    - id: deterministic
                      kind: deterministic
                      outcome: passed
                      evidence: [worker:test]
                  promotion_eligible: true
                  source_digest: "{{ world.ci_source.digest }}"
                  story_digest: "{{ world.ci_trigger.story_digest }}"
                  environment_digest: "{{ world.ci_environment.digest }}"
                  envelope_digest: "{{ world.ci_trigger.envelope_digest }}"
  done:
    terminal: true
    view: [{ prose: passed }]
`
