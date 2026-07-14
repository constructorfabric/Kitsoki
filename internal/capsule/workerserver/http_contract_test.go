package workerserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/workerserver"
)

func TestWorkerRunRejectsMalformedAndUnsealedPrepared(t *testing.T) {
	server := newContractWorker(t, executor.Capabilities{
		ID:         "contract-worker",
		Placements: []string{"remote"},
		Isolation:  "supervised",
		Networks:   []string{"none"},
	})
	sealed := contractPrepared(t, "none", "supervised")
	unsealed := sealed
	unsealed.Envelope.Digest = ""
	unsealedBody, err := json.Marshal(map[string]any{"prepared": unsealed})
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		body []byte
		want string
	}{
		{name: "malformed JSON", body: []byte(`{"prepared":`), want: "invalid prepared execution"},
		{name: "unsealed envelope", body: unsealedBody, want: "must already be sealed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			raw := readAndClose(t, response)
			if response.StatusCode != http.StatusBadRequest || !bytes.Contains(raw, []byte(test.want)) {
				t.Fatalf("run = %d: %s", response.StatusCode, raw)
			}
		})
	}
}

func TestWorkerRunRejectsCapabilityPolicyMismatch(t *testing.T) {
	server := newContractWorker(t, executor.Capabilities{
		ID:         "contract-worker",
		Placements: []string{"remote"},
		Isolation:  "supervised",
		Networks:   []string{"none"},
	})
	prepared := contractPrepared(t, "live", "supervised")
	body, err := json.Marshal(map[string]any{"prepared": prepared})
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.Post(server.URL+"/v1/capsules/run", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw := readAndClose(t, response)
	if response.StatusCode != http.StatusPreconditionFailed || !bytes.Contains(raw, []byte("cannot satisfy network live")) {
		t.Fatalf("run = %d: %s", response.StatusCode, raw)
	}
}

func TestWorkerSourceHeadTreatsCorruptCacheAsMiss(t *testing.T) {
	root := t.TempDir()
	server := newContractWorkerAt(t, root, executor.Capabilities{
		ID:         "contract-worker",
		Placements: []string{"remote"},
		Isolation:  "supervised",
		Networks:   []string{"none"},
	})
	head := strings.Repeat("c", 40)
	dir := filepath.Join(root, "sources", head)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := workerserver.SourceMeta{
		Schema:       workerserver.SourceMetaSchema,
		Head:         head,
		BundleDigest: "sha256:bundle",
		Size:         128,
		StoredAt:     time.Now().UTC(),
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.bundle"), []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(http.MethodHead, server.URL+"/v1/capsules/sources/"+head, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("HEAD status = %d, want cache-miss 404", response.StatusCode)
	}
}

func TestWorkerSourceHeadTouchesValidCacheBeforeRunSubmission(t *testing.T) {
	root := t.TempDir()
	server := newContractWorkerAt(t, root, executor.Capabilities{ID: "contract-worker", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none"}})
	head := strings.Repeat("d", 40)
	dir := filepath.Join(root, "sources", head)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := []byte("valid-enough-for-cache-metadata")
	before := time.Now().UTC().Add(-48 * time.Hour)
	meta := workerserver.SourceMeta{Schema: workerserver.SourceMetaSchema, Head: head, BundleDigest: "sha256:bundle", Size: int64(len(bundle)), StoredAt: before}
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.bundle"), bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := http.Head(server.URL + "/v1/capsules/sources/" + head)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("HEAD status = %d", response.StatusCode)
	}
	updatedRaw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var updated workerserver.SourceMeta
	if err := json.Unmarshal(updatedRaw, &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.StoredAt.After(before) {
		t.Fatalf("source cache was not touched: before=%s after=%s", before, updated.StoredAt)
	}
}

func newContractWorker(t *testing.T, capabilities executor.Capabilities) *httptest.Server {
	t.Helper()
	return newContractWorkerAt(t, t.TempDir(), capabilities)
}

func newContractWorkerAt(t *testing.T, root string, capabilities executor.Capabilities) *httptest.Server {
	t.Helper()
	worker, err := workerserver.New(workerserver.Config{
		Root:         root,
		Capabilities: capabilities,
		Environment:  workerserver.EnvironmentVerifierFunc(func(context.Context, string, environment.Lock) error { return nil }),
		Runner: func(context.Context, string, executor.Prepared, string) (executor.Result, error) {
			t.Fatal("runner called during HTTP contract validation")
			return executor.Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(worker.Handler())
	t.Cleanup(server.Close)
	return server
}

func contractPrepared(t *testing.T, network, sandbox string) executor.Prepared {
	t.Helper()
	externalWrite := "deny"
	if network == "live" {
		externalWrite = "allow"
	}
	lock, err := environment.SealLock(environment.Lock{
		Schema:           environment.LockSchema,
		ID:               "contract",
		DefinitionDigest: "sha256:environment-definition",
		Network:          network,
		Sandbox:          sandbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := executor.Seal(executor.Envelope{
		JobID:            "contract-job",
		ProjectID:        "contract-project",
		DefinitionDigest: "sha256:capsule-definition",
		Instance:         control.Handle{ID: "contract-workspace", Generation: 1},
		SourceDigest:     strings.Repeat("a", 40),
		StoryPath:        "stories/ci/app.yaml",
		StoryDigest:      "sha256:story-closure",
		Environment:      lock,
		Policy: executor.Policy{
			Network:        network,
			MinimumSandbox: sandbox,
			ExternalWrite:  externalWrite,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return executor.Prepared{ID: "contract-execution", Envelope: envelope, Placement: "remote", Applied: envelope.Policy}
}
