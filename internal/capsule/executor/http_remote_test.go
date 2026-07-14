package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/control"
)

func TestHTTPRemoteWorkerSendsSealedEnvelopeWithoutCredentialAndReturnsResult(t *testing.T) {
	var sawRun, sawValidate bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/capsules/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": Capabilities{ID: "remote", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none"}, Cancellable: true}})
		case "/v1/capsules/validate":
			sawValidate = true
			w.WriteHeader(http.StatusNoContent)
		case "/v1/capsules/run":
			sawRun = true
			if got := r.Header.Get("X-Kitsoki-Request-ID"); !strings.HasPrefix(got, "req-") {
				t.Errorf("request id %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer token" {
				t.Errorf("authorization %q", got)
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "token") {
				t.Fatal("credential leaked into remote payload")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": Result{ExitCode: 0, VerdictArtifact: "artifact:verdict", VerdictJSON: []byte(`{"schema":"capsule-ci-verdict/v1"}`), Artifacts: []string{"b", "a"}}, "run": ExecutionStatus{ExecutionID: "run/1", Status: "completed", Stage: "terminal"}})
		case "/v1/capsules/executions/run/1":
			status := "running"
			if r.Method == http.MethodDelete {
				status = "cancelling"
			} else if r.Method != http.MethodGet {
				t.Errorf("method %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"run": ExecutionStatus{ExecutionID: "run/1", Status: status, Stage: "story"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	worker := HTTPRemoteWorker{Endpoint: "https://worker.invalid", Client: rewriteClient(t, server), Credential: func(context.Context) (string, error) { return "token", nil }}
	capabilities, err := worker.Describe(context.Background())
	if err != nil || capabilities.ID != "remote" {
		t.Fatalf("capabilities %#v: %v", capabilities, err)
	}
	prepared := testHTTPPrepared(t, "run/1")
	if err := worker.AcceptPrepared(context.Background(), prepared); err != nil || !sawValidate {
		t.Fatalf("worker-side no-spend validation: %v saw=%t", err, sawValidate)
	}
	result, err := worker.Run(context.Background(), prepared, func(context.Context, Prepared) (Result, error) {
		t.Fatal("remote worker must not run the local task")
		return Result{}, nil
	}, nil)
	if err != nil || !sawRun || result.ExecutionID != "run/1" || len(result.VerdictJSON) == 0 || result.Artifacts[0] != "a" {
		t.Fatalf("result %#v: %v", result, err)
	}
	if err := worker.Cancel(context.Background(), "run/1"); err != nil {
		t.Fatal(err)
	}
	status, err := worker.Status(context.Background(), "run/1")
	if err != nil || status.Schema != ExecutionStatusSchema || status.Status != "running" {
		t.Fatalf("status %#v: %v", status, err)
	}
}

func TestHTTPRemoteWorkerRejectsHTTP200UnsuccessfulCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"result": Result{ExitCode: 1, VerdictJSON: []byte(`{"schema":"capsule-ci-verdict/v1"}`)}, "run": ExecutionStatus{ExecutionID: "run-unsuccessful", Status: "completed", Stage: "terminal"}})
	}))
	defer server.Close()
	worker := HTTPRemoteWorker{Endpoint: "https://worker.invalid", Client: rewriteClient(t, server)}
	_, err := worker.Run(context.Background(), testHTTPPrepared(t, "run-unsuccessful"), nil, nil)
	var executionErr ExecutionError
	if !errors.As(err, &executionErr) || !strings.Contains(err.Error(), "unsuccessful completion") {
		t.Fatalf("expected rejected successful HTTP response, got %T %v", err, err)
	}
}

func TestHTTPRemoteWorkerImportsWorkerFailureTimeline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "environment mismatch",
			"run": ExecutionStatus{
				ExecutionID: "run-failed",
				Status:      "failed",
				Stage:       "verify_environment",
				Error:       "environment mismatch",
				Events:      []Event{{Kind: "capsule.worker.environment.verifying", ExecutionID: "run-failed", Outcome: "running"}, {Kind: "capsule.worker.failed", ExecutionID: "run-failed", Outcome: "failed", Error: "environment mismatch"}},
			},
		})
	}))
	defer server.Close()
	worker := HTTPRemoteWorker{Endpoint: "https://worker.invalid", Client: rewriteClient(t, server)}
	prepared := testHTTPPrepared(t, "run-failed")
	var events []Event
	_, err := worker.Run(context.Background(), prepared, nil, EventSinkFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}))
	var executionErr ExecutionError
	if !errors.As(err, &executionErr) || executionErr.Execution.Stage != "verify_environment" {
		t.Fatalf("expected typed execution error, got %T %v", err, err)
	}
	if len(events) != 4 || events[1].Kind != "capsule.worker.environment.verifying" || events[2].Kind != "capsule.worker.failed" || events[3].Kind != "capsule.executor.failed" {
		t.Fatalf("events %#v", events)
	}
	if events[3].Fields["worker_stage"] != "verify_environment" || events[3].Fields["error_kind"] != "execution" {
		t.Fatalf("terminal fields %#v", events[3].Fields)
	}
}

func TestHTTPRemoteWorkerErrorIncludesBoundedRemoteDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Kitsoki-Request-ID", "worker-request-1")
		http.Error(w, "worker failed token=super-secret because setup missing", http.StatusBadGateway)
	}))
	defer server.Close()
	worker := HTTPRemoteWorker{Endpoint: "https://worker.invalid", Client: rewriteClient(t, server)}
	prepared := testHTTPPrepared(t, "run/1")
	var events []Event
	_, err := worker.Run(context.Background(), prepared, nil, EventSinkFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}))
	if err == nil {
		t.Fatal("expected remote error")
	}
	msg := err.Error()
	for _, want := range []string{"kind=status", "host=worker.invalid", "status=502 Bad Gateway", "request_id=worker-request-1", "body=worker failed token=<redacted>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error missing %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "super-secret") {
		t.Fatalf("error leaked secret body: %s", msg)
	}
	if len(events) != 2 || events[0].Kind != "capsule.executor.started" || events[1].Kind != "capsule.executor.failed" {
		t.Fatalf("events %#v", events)
	}
	failed := events[1]
	if failed.At.IsZero() || failed.Outcome != "failed" || failed.Error != "remote executor request failed" {
		t.Fatalf("failed event %#v", failed)
	}
	for _, key := range []string{"method", "path", "status", "request_id", "duration_ms", "error_kind", "message"} {
		if _, ok := failed.Fields[key]; !ok {
			t.Fatalf("failed event missing %s: %#v", key, failed.Fields)
		}
	}
	if failed.Fields["path"] != "v1/capsules/run" || failed.Fields["request_id"] != "worker-request-1" || failed.Fields["error_kind"] != "status" {
		t.Fatalf("structured fields %#v", failed.Fields)
	}
	if strings.Contains(fmt.Sprint(failed.Fields), "super-secret") {
		t.Fatalf("event fields leaked secret: %#v", failed.Fields)
	}
}

func TestHTTPRemoteWorkerEnforcesOverallTimeoutWithInjectedTransport(t *testing.T) {
	worker := HTTPRemoteWorker{
		Endpoint: "https://worker.invalid",
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
		Timeouts: HTTPTimeouts{Connect: time.Second, ResponseHeader: time.Second, Overall: 20 * time.Millisecond},
	}
	prepared := testHTTPPrepared(t, "run-timeout")
	started := time.Now()
	var events []Event
	_, err := worker.Run(context.Background(), prepared, nil, EventSinkFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}))
	if err == nil || !strings.Contains(err.Error(), "kind=transport") {
		t.Fatalf("expected structured transport timeout, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout was not bounded: %s", elapsed)
	}
	if len(events) != 2 || events[1].Fields["error_kind"] != "transport" || events[1].At.IsZero() {
		t.Fatalf("events %#v", events)
	}
	defaults := (HTTPRemoteWorker{}).EffectiveTimeouts()
	if defaults.Connect <= 0 || defaults.ResponseHeader <= 0 || defaults.Overall <= 0 {
		t.Fatalf("unbounded defaults %#v", defaults)
	}
}

func rewriteClient(t *testing.T, server *httptest.Server) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme = "http"
		request.URL.Host = strings.TrimPrefix(server.URL, "http://")
		return transport.RoundTrip(request)
	})}
}

func testHTTPPrepared(t *testing.T, id string) Prepared {
	t.Helper()
	envelope, err := Seal(Envelope{
		JobID: "job", ProjectID: "project", DefinitionDigest: "sha256:def",
		Instance: control.Handle{ID: "w", Generation: 1}, SourceDigest: "sha256:source",
		StoryPath: "stories/ci/app.yaml", StoryDigest: "sha256:story", Environment: testEnvironmentLock(t),
		Policy: Policy{Network: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Prepared{ID: id, Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
