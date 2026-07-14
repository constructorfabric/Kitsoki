// Package workerserver implements the authenticated, durable HTTP edge for a
// Capsule remote executor. The worker never trusts controller-local workspace
// paths: it materializes a content-addressed git bundle, verifies the sealed
// commit and complete story closure, then invokes an injected runner.
package workerserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/storydigest"
)

const (
	RunRecordSchema  = "capsule-worker-run/v1"
	SourceMetaSchema = "capsule-worker-source/v1"
)

type Runner func(context.Context, string, executor.Prepared, string) (executor.Result, error)

type EnvironmentVerifier interface {
	Verify(context.Context, string, environment.Lock) error
}

type EnvironmentVerifierFunc func(context.Context, string, environment.Lock) error

func (f EnvironmentVerifierFunc) Verify(ctx context.Context, root string, lock environment.Lock) error {
	return f(ctx, root, lock)
}

type Config struct {
	Root           string
	Token          string
	RequireAuth    bool
	Capabilities   executor.Capabilities
	MaxBundleBytes int64
	Runner         Runner
	Environment    EnvironmentVerifier
	Now            func() time.Time
}

type Server struct {
	cfg    Config
	mu     sync.Mutex
	active map[string]context.CancelFunc
}

type SourceMeta struct {
	Schema       string    `json:"schema"`
	Head         string    `json:"head"`
	BundleDigest string    `json:"bundle_digest"`
	Size         int64     `json:"size"`
	StoredAt     time.Time `json:"stored_at"`
}

type RunRecord struct {
	Schema         string                             `json:"schema"`
	ExecutionID    string                             `json:"execution_id"`
	EnvelopeDigest string                             `json:"envelope_digest"`
	SourceDigest   string                             `json:"source_digest"`
	StoryDigest    string                             `json:"story_digest"`
	RequestID      string                             `json:"request_id,omitempty"`
	Status         string                             `json:"status"`
	Stage          string                             `json:"stage"`
	StartedAt      time.Time                          `json:"started_at"`
	UpdatedAt      time.Time                          `json:"updated_at"`
	TerminalAt     time.Time                          `json:"terminal_at,omitempty"`
	Error          string                             `json:"error,omitempty"`
	Events         []executor.Event                   `json:"events,omitempty"`
	Result         executor.Result                    `json:"result,omitempty"`
	Agent          *executor.AgentDiagnostics         `json:"agent,omitempty"`
	Cleanup        *executor.WorkerCleanupDiagnostics `json:"cleanup,omitempty"`
}

func New(cfg Config) (*Server, error) {
	root, err := filepath.Abs(cfg.Root)
	if err != nil || strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("capsule worker: durable root is required")
	}
	if cfg.RequireAuth && strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("capsule worker: authentication token is required")
	}
	if cfg.Runner == nil {
		return nil, fmt.Errorf("capsule worker: runner is required")
	}
	if cfg.Environment == nil {
		return nil, fmt.Errorf("capsule worker: environment verifier is required")
	}
	if cfg.MaxBundleBytes <= 0 {
		cfg.MaxBundleBytes = executor.DefaultMaxBundleSize
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Capabilities.ID == "" {
		// A bare worker process has no portable network confinement. Operators
		// may advertise none/replay only when the enclosing container, VM, or
		// network policy actually enforces it.
		cfg.Capabilities = executor.Capabilities{ID: "capsule-http-worker", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"live"}, Cancellable: true}
	}
	cfg.Root = root
	for _, dir := range []string{"sources", "runs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			return nil, fmt.Errorf("capsule worker: create %s: %w", dir, err)
		}
	}
	return &Server{cfg: cfg, active: map[string]context.CancelFunc{}}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/capsules/capabilities", s.capabilities)
	mux.HandleFunc("HEAD /v1/capsules/sources/{head}", s.sourceHead)
	mux.HandleFunc("PUT /v1/capsules/sources/{head}", s.sourcePut)
	mux.HandleFunc("POST /v1/capsules/validate", s.validate)
	mux.HandleFunc("POST /v1/capsules/run", s.run)
	mux.HandleFunc("GET /v1/capsules/executions/{id}", s.executionGet)
	mux.HandleFunc("DELETE /v1/capsules/executions/{id}", s.executionCancel)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Kitsoki-Request-ID"))
		if requestID == "" {
			requestID = fmt.Sprintf("worker-%d", s.cfg.Now().UnixNano())
		}
		w.Header().Set("X-Kitsoki-Request-ID", requestID)
		if s.cfg.RequireAuth || s.cfg.Token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if len(got) != len(s.cfg.Token) || subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
				writeError(w, http.StatusUnauthorized, "authentication failed", requestID)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) capabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": s.cfg.Capabilities})
}

func (s *Server) sourceHead(w http.ResponseWriter, r *http.Request) {
	head, err := cleanObjectID(r.PathValue("head"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	// Serialize cache-use acknowledgement with source deletion. Cleanup
	// revalidates StoredAt under the same lock, so a controller HEAD hit cannot
	// race an aged source removal before its following POST /run.
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, _, ok := s.validSource(head)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	meta.StoredAt = s.cfg.Now().UTC()
	if err := writeJSONFile(s.sourceMetaPath(head), meta); err != nil {
		writeError(w, http.StatusInternalServerError, "touch source metadata", requestID(r))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) sourcePut(w http.ResponseWriter, r *http.Request) {
	head, err := cleanObjectID(r.PathValue("head"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	bundleDigest := strings.TrimSpace(r.Header.Get("X-Kitsoki-Bundle-Digest"))
	if bundleDigest == "" {
		writeError(w, http.StatusBadRequest, "bundle digest header is required", requestID(r))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBundleBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "source bundle exceeds worker limit", requestID(r))
		return
	}
	bundle := executor.SourceBundle{Schema: executor.SourceBundleSchema, Format: executor.SourceBundleFormat, Head: head, Digest: bundleDigest, Size: int64(len(data)), Data: data}
	if err := executor.ValidateSourceBundle(bundle, s.cfg.MaxBundleBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	if err := s.storeSource(r.Context(), bundle); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	writeJSON(w, http.StatusCreated, SourceMeta{Schema: SourceMetaSchema, Head: head, BundleDigest: bundle.Digest, Size: bundle.Size, StoredAt: s.cfg.Now()})
}

// validate accepts the same prepared envelope as /run but deliberately does
// not require a source cache entry, create a run record, or invoke a story.
// It is the worker-side half of Capsule CI doctor.
func (s *Server) validate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request struct {
		Prepared executor.Prepared `json:"prepared"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid prepared execution", requestID(r))
		return
	}
	prepared, err := validatePrepared(request.Prepared)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	if err := executor.ValidateCapabilities(s.cfg.Capabilities, prepared.Envelope.Policy); err != nil {
		writeError(w, http.StatusPreconditionFailed, err.Error(), requestID(r))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) storeSource(ctx context.Context, bundle executor.SourceBundle) error {
	dir := s.sourceDir(bundle.Head)
	if raw, err := os.ReadFile(filepath.Join(dir, "meta.json")); err == nil {
		var existing SourceMeta
		if json.Unmarshal(raw, &existing) == nil && existing.Head == bundle.Head && existing.BundleDigest == bundle.Digest {
			return nil
		}
	}
	tmp, err := os.MkdirTemp(filepath.Join(s.cfg.Root, "sources"), ".incoming-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bundlePath := filepath.Join(tmp, "source.bundle")
	if err := os.WriteFile(bundlePath, bundle.Data, 0o600); err != nil {
		return err
	}
	verifyPath := filepath.Join(tmp, "verify")
	if err := cloneBundle(ctx, bundlePath, verifyPath, bundle.Head); err != nil {
		return fmt.Errorf("capsule worker: verify source bundle: %w", err)
	}
	if err := os.RemoveAll(verifyPath); err != nil {
		return err
	}
	meta := SourceMeta{Schema: SourceMetaSchema, Head: bundle.Head, BundleDigest: bundle.Digest, Size: bundle.Size, StoredAt: s.cfg.Now()}
	if err := writeJSONFile(filepath.Join(tmp, "meta.json"), meta); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request struct {
		Prepared executor.Prepared `json:"prepared"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid prepared execution", requestID(r))
		return
	}
	prepared, err := validatePrepared(request.Prepared)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	if err := executor.ValidateCapabilities(s.cfg.Capabilities, prepared.Envelope.Policy); err != nil {
		writeError(w, http.StatusPreconditionFailed, err.Error(), requestID(r))
		return
	}
	if terminal, ok := s.readTerminal(prepared.ID, prepared.Envelope.Digest); ok {
		terminal = s.projectRun(terminal)
		status := http.StatusOK
		if terminal.Status == "failed" {
			status = http.StatusUnprocessableEntity
		}
		if terminal.Status == "cancelled" {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": terminal.Error, "result": terminal.Result, "run": terminal})
		return
	}
	runCtx, cancel := context.WithCancel(r.Context())
	if !s.activate(prepared.ID, cancel) {
		cancel()
		writeError(w, http.StatusConflict, "execution is already running", requestID(r))
		return
	}
	defer func() {
		cancel()
		s.deactivate(prepared.ID)
	}()
	record, result, runErr := s.execute(runCtx, prepared, requestID(r))
	record = s.projectRun(record)
	if runErr != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(runErr, context.Canceled) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": record.Error, "request_id": requestID(r), "run": record})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result, "run": record})
}

func (s *Server) execute(ctx context.Context, prepared executor.Prepared, reqID string) (RunRecord, executor.Result, error) {
	now := s.cfg.Now()
	record := RunRecord{Schema: RunRecordSchema, ExecutionID: prepared.ID, EnvelopeDigest: prepared.Envelope.Digest, SourceDigest: prepared.Envelope.SourceDigest, StoryDigest: prepared.Envelope.StoryDigest, RequestID: reqID, Status: "running", Stage: "registered", StartedAt: now, UpdatedAt: now}
	s.appendEvent(&record, prepared, "capsule.worker.registered", "running", "")
	if err := s.writeRun(record); err != nil {
		persistErr := fmt.Errorf("capsule worker: persist registered checkpoint: %w", err)
		record.Error = boundedError(persistErr)
		return record, executor.Result{}, persistErr
	}
	runDir := s.runDir(prepared.ID)
	workspace := filepath.Join(runDir, "workspace")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return s.fail(record, prepared, "materialize", err)
	}
	record.Stage = "materializing_source"
	record.UpdatedAt = s.cfg.Now()
	s.appendEvent(&record, prepared, "capsule.worker.source.materializing", "running", "")
	if err := s.writeRun(record); err != nil {
		return s.fail(record, prepared, "materialize", fmt.Errorf("capsule worker: persist materializing-source checkpoint: %w", err))
	}
	if err := cloneBundle(ctx, s.sourceBundlePath(prepared.Envelope.SourceDigest), workspace, prepared.Envelope.SourceDigest); err != nil {
		return s.fail(record, prepared, "materialize", err)
	}
	storyPath, err := confinedStoryPath(workspace, prepared.Envelope.StoryPath)
	if err != nil {
		return s.fail(record, prepared, "verify_story", err)
	}
	digest, err := storydigest.Compute(workspace, storyPath)
	if err != nil {
		return s.fail(record, prepared, "verify_story", err)
	}
	if digest.Digest != prepared.Envelope.StoryDigest {
		return s.fail(record, prepared, "verify_story", fmt.Errorf("capsule worker: story closure digest mismatch: got %s, want %s", digest.Digest, prepared.Envelope.StoryDigest))
	}
	record.Stage = "verifying_environment"
	record.UpdatedAt = s.cfg.Now()
	s.appendEvent(&record, prepared, "capsule.worker.environment.verifying", "running", "")
	if err := s.writeRun(record); err != nil {
		return s.fail(record, prepared, "verify_environment", err)
	}
	if err := s.cfg.Environment.Verify(ctx, workspace, prepared.Envelope.Environment); err != nil {
		return s.fail(record, prepared, "verify_environment", err)
	}
	record.Stage = "environment_verified"
	record.UpdatedAt = s.cfg.Now()
	s.appendEvent(&record, prepared, "capsule.worker.environment.verified", "passed", "")
	if err := s.writeRun(record); err != nil {
		return s.fail(record, prepared, "verify_environment", err)
	}
	record.Stage = "running_story"
	record.UpdatedAt = s.cfg.Now()
	s.appendEvent(&record, prepared, "capsule.worker.story.started", "running", "")
	if err := s.writeRun(record); err != nil {
		return s.fail(record, prepared, "running_story", fmt.Errorf("capsule worker: persist running-story checkpoint: %w", err))
	}
	tracePath := filepath.Join(runDir, "story-trace.jsonl")
	result, err := s.cfg.Runner(ctx, workspace, prepared, tracePath)
	if err != nil {
		return s.fail(record, prepared, "running_story", err)
	}
	if result.ExitCode != 0 {
		return s.fail(record, prepared, "running_story", fmt.Errorf("capsule worker: runner reported non-zero exit code %d", result.ExitCode))
	}
	result.ExecutionID = prepared.ID
	if result.Provider == nil {
		result.Provider = map[string]string{}
	}
	result.Provider["worker_run"] = prepared.ID
	result.Artifacts = append(result.Artifacts, "worker:executions/"+prepared.ID+"/run", "worker:executions/"+prepared.ID+"/story-trace")
	record.Status = "completed"
	record.Stage = "terminal"
	record.Result = result
	record.TerminalAt = s.cfg.Now()
	record.UpdatedAt = record.TerminalAt
	s.appendEvent(&record, prepared, "capsule.worker.completed", "passed", "")
	if err := s.writeRun(record); err != nil {
		return s.fail(record, prepared, "persist_terminal", fmt.Errorf("capsule worker: persist completed checkpoint: %w", err))
	}
	return record, result, nil
}

func (s *Server) fail(record RunRecord, prepared executor.Prepared, stage string, err error) (RunRecord, executor.Result, error) {
	if current, readErr := s.readRun(record.ExecutionID); readErr == nil && current.EnvelopeDigest == record.EnvelopeDigest && len(current.Events) > len(record.Events) {
		record.Events = current.Events
	}
	record.Status = "failed"
	eventKind := "capsule.worker.failed"
	if errors.Is(err, context.Canceled) {
		record.Status = "cancelled"
		record.Stage = "terminal"
		eventKind = "capsule.worker.cancelled"
	} else {
		record.Stage = stage
	}
	record.Error = boundedError(err)
	record.TerminalAt = s.cfg.Now()
	record.UpdatedAt = record.TerminalAt
	s.appendEvent(&record, prepared, eventKind, record.Status, record.Error)
	if persistErr := s.writeRun(record); persistErr != nil {
		combined := errors.Join(err, fmt.Errorf("capsule worker: persist terminal %s checkpoint: %w", record.Status, persistErr))
		record.Error = boundedError(combined)
		return record, executor.Result{}, combined
	}
	return record, executor.Result{}, err
}

func (s *Server) executionGet(w http.ResponseWriter, r *http.Request) {
	id, err := cleanID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	record, err := s.readRun(id)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "execution not found", requestID(r))
			return
		}
		writeError(w, http.StatusInternalServerError, "read execution", requestID(r))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": s.projectRun(record)})
}

func (s *Server) executionCancel(w http.ResponseWriter, r *http.Request) {
	id, err := cleanID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), requestID(r))
		return
	}
	s.mu.Lock()
	cancel := s.active[id]
	s.mu.Unlock()
	if cancel == nil {
		if record, err := s.readRun(id); err == nil {
			if record.Status == "completed" || record.Status == "failed" || record.Status == "cancelled" {
				writeJSON(w, http.StatusOK, map[string]any{"run": s.projectRun(record)})
				return
			}
			writeError(w, http.StatusConflict, "execution has durable non-terminal state but no active worker", requestID(r))
			return
		}
		writeError(w, http.StatusNotFound, "active execution not found", requestID(r))
		return
	}
	record, err := s.readRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read active execution", requestID(r))
		return
	}
	record.Status = "cancelling"
	record.Stage = "cancellation_requested"
	record.UpdatedAt = s.cfg.Now()
	record.Events = append(record.Events, executor.Event{Kind: "capsule.worker.cancellation_requested", At: record.UpdatedAt, EnvelopeDigest: record.EnvelopeDigest, ExecutionID: id, Outcome: "running", Fields: map[string]any{"stage": record.Stage}})
	if err := s.writeRun(record); err != nil {
		writeError(w, http.StatusInternalServerError, "persist cancellation request", requestID(r))
		return
	}
	cancel()
	writeJSON(w, http.StatusAccepted, map[string]any{"run": s.projectRun(record)})
}

func validatePrepared(prepared executor.Prepared) (executor.Prepared, error) {
	validated, err := executor.ValidatePrepared(prepared)
	if err != nil {
		return executor.Prepared{}, fmt.Errorf("capsule worker: invalid prepared execution: %w", err)
	}
	prepared = validated
	if _, err := cleanID(prepared.ID); err != nil {
		return executor.Prepared{}, err
	}
	if _, err := cleanObjectID(prepared.Envelope.SourceDigest); err != nil {
		return executor.Prepared{}, err
	}
	return prepared, nil
}

func cloneBundle(ctx context.Context, bundlePath, dest, head string) error {
	if _, err := os.Stat(bundlePath); err != nil {
		return fmt.Errorf("source %s is not materialized", head)
	}
	_ = os.RemoveAll(dest)
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", bundlePath, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone bundle: %w: %s", err, strings.TrimSpace(string(out)))
	}
	cmd = exec.CommandContext(ctx, "git", "-C", dest, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git read materialized HEAD: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != head {
		return fmt.Errorf("materialized HEAD %s does not match sealed source %s", strings.TrimSpace(string(out)), head)
	}
	return nil
}

func confinedStoryPath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || rel == "" || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", fmt.Errorf("capsule worker: story path must be project-relative")
	}
	path := filepath.Join(root, filepath.Clean(rel))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("capsule worker: resolve story: %w", err)
	}
	base, _ := filepath.EvalSymlinks(root)
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("capsule worker: story path escapes source")
	}
	return resolved, nil
}

func (s *Server) activate(id string, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[id] != nil {
		return false
	}
	s.active[id] = cancel
	return true
}

func (s *Server) deactivate(id string) {
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
}

func (s *Server) appendEvent(record *RunRecord, prepared executor.Prepared, kind, outcome, errorText string) {
	record.Events = append(record.Events, executor.Event{Kind: kind, At: s.cfg.Now(), EnvelopeDigest: prepared.Envelope.Digest, ExecutionID: prepared.ID, Outcome: outcome, Error: errorText, Fields: map[string]any{"stage": record.Stage}})
}

func (s *Server) writeRun(record RunRecord) error {
	return writeJSONFile(s.runRecordPath(record.ExecutionID), record)
}

func (s *Server) readRun(id string) (RunRecord, error) {
	raw, err := os.ReadFile(s.runRecordPath(id))
	if err != nil {
		return RunRecord{}, err
	}
	var record RunRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return RunRecord{}, err
	}
	if record.Schema != RunRecordSchema || record.ExecutionID != id {
		return RunRecord{}, fmt.Errorf("capsule worker: invalid run record")
	}
	return record, nil
}

func (s *Server) readTerminal(id, envelopeDigest string) (RunRecord, bool) {
	record, err := s.readRun(id)
	if err != nil || record.EnvelopeDigest != envelopeDigest {
		return RunRecord{}, false
	}
	return record, record.Status == "completed" || record.Status == "failed" || record.Status == "cancelled"
}

func (s *Server) sourceDir(head string) string { return filepath.Join(s.cfg.Root, "sources", head) }
func (s *Server) sourceBundlePath(head string) string {
	return filepath.Join(s.sourceDir(head), "source.bundle")
}
func (s *Server) sourceMetaPath(head string) string {
	return filepath.Join(s.sourceDir(head), "meta.json")
}
func (s *Server) runDir(id string) string        { return filepath.Join(s.cfg.Root, "runs", id) }
func (s *Server) runRecordPath(id string) string { return filepath.Join(s.runDir(id), "run.json") }

func cleanObjectID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return "", fmt.Errorf("capsule worker: invalid source digest")
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", fmt.Errorf("capsule worker: invalid source digest")
		}
	}
	return value, nil
}

func cleanID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return "", fmt.Errorf("capsule worker: invalid execution id")
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "", fmt.Errorf("capsule worker: invalid execution id")
		}
	}
	return value, nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message, reqID string) {
	writeJSON(w, status, map[string]any{"error": message, "request_id": reqID})
}

func requestID(r *http.Request) string { return r.Header.Get("X-Kitsoki-Request-ID") }

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.Join(strings.Fields(err.Error()), " ")
	if len(value) > 1000 {
		value = value[:1000] + "…"
	}
	return value
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
