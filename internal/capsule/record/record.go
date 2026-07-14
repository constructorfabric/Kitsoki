package record

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/receipt"
	capsuletrace "kitsoki/internal/capsule/trace"
)

type Stored struct {
	Receipt      receipt.Receipt      `json:"receipt"`
	Verification receipt.Verification `json:"verification"`
	TracePath    string               `json:"trace_path"`
	ReceiptPath  string               `json:"receipt_path"`
}
type PersistOptions struct {
	Signer receipt.Signer
}

// FileRunObserver is the durable filesystem observer shared by CLI front doors
// and available to the scoped MCP service. Every checkpoint rewrites the compact
// run projection and trace atomically enough for another process to diagnose a
// running or interrupted job before a terminal receipt exists.
type FileRunObserver struct{ ProjectRoot string }

func (o FileRunObserver) Observe(_ context.Context, observation ci.RunObservation) error {
	result := observation.Result
	writeErr := (ci.FileRunStore{ProjectRoot: o.ProjectRoot}).Write(ci.RunRecord{JobID: string(result.Job.ID), Result: result, DiagnosticError: observation.DiagnosticError})
	tracePath, traceErr := PersistTrace(o.ProjectRoot, result)
	if traceErr != nil && writeErr != nil {
		return fmt.Errorf("capsule record: checkpoint trace: %v; run record: %w", traceErr, writeErr)
	}
	if traceErr != nil {
		return fmt.Errorf("capsule record: checkpoint trace: %w", traceErr)
	}
	if writeErr != nil {
		return fmt.Errorf("capsule record: checkpoint run record (trace %s): %w", tracePath, writeErr)
	}
	return nil
}

func Persist(project string, result ci.RunResult) (Stored, error) {
	return PersistWithOptions(project, result, PersistOptions{})
}

func PersistTrace(project string, result ci.RunResult) (string, error) {
	path, _, err := writeTrace(project, result)
	return path, err
}

func PersistWithOptions(project string, result ci.RunResult, opts PersistOptions) (Stored, error) {
	if result.Job.ID == "" {
		return Stored{}, fmt.Errorf("capsule record: job id is required")
	}
	tracePath, raw, err := writeTrace(project, result)
	if err != nil {
		return Stored{}, err
	}
	sum := sha256.Sum256(raw)
	built, verification, err := receipt.Build(receipt.BuildInput{Job: result.Job, Envelope: result.Envelope, Verdict: result.Verdict, Artifacts: []receipt.Artifact{{Handle: result.Execution.VerdictArtifact, Kind: "verdict"}}, TraceDigest: "sha256:" + hex.EncodeToString(sum[:])})
	if err != nil {
		return Stored{}, err
	}
	built, err = signByPolicy(project, built, opts.Signer)
	if err != nil {
		return Stored{}, err
	}
	policy, err := receiptPolicy(project)
	if err != nil {
		return Stored{}, err
	}
	verification = receipt.Verify(built, opts.Signer, policy.RequireSignature)
	if verification.Status != "valid" {
		return Stored{}, fmt.Errorf("capsule record: receipt %s", verification.Status)
	}
	receiptPath := filepath.Join(project, ".capsules", "ci", string(result.Job.ID)+".receipt.json")
	encoded, err := json.MarshalIndent(built, "", "  ")
	if err != nil {
		return Stored{}, err
	}
	if err := writeAtomic(receiptPath, append(encoded, '\n'), 0o600); err != nil {
		return Stored{}, err
	}
	return Stored{Receipt: built, Verification: verification, TracePath: tracePath, ReceiptPath: receiptPath}, nil
}

func writeTrace(project string, result ci.RunResult) (string, []byte, error) {
	if result.Job.ID == "" {
		return "", nil, fmt.Errorf("capsule record: job id is required")
	}
	dir := filepath.Join(project, ".capsules", "ci")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	trace := runTrace(result)
	raw, err := capsuletrace.MarshalDocument(trace)
	if err != nil {
		return "", nil, err
	}
	tracePath := filepath.Join(dir, string(result.Job.ID)+".trace.json")
	if err := writeAtomic(tracePath, append(raw, '\n'), 0o600); err != nil {
		return "", nil, err
	}
	return tracePath, raw, nil
}

func runTrace(result ci.RunResult) capsuletrace.Document {
	jobID := string(result.Job.ID)
	envelope := result.Envelope
	events := make([]capsuletrace.Event, 0, 6+len(result.Events))
	if envelope.Instance.ID != "" {
		events = append(events, capsuletrace.Event{
			Kind:       capsuletrace.KindWorkspaceReady,
			At:         result.StartedAt,
			InstanceID: envelope.Instance.ID,
			Generation: envelope.Instance.Generation,
			Fields: map[string]any{
				"definition_digest": envelope.DefinitionDigest,
				"source_digest":     envelope.SourceDigest,
			},
		})
	}
	if envelope.Environment.Digest != "" {
		events = append(events, capsuletrace.Event{
			Kind:           capsuletrace.KindEnvironmentResolved,
			At:             result.StartedAt,
			JobID:          jobID,
			EnvelopeDigest: envelope.Digest,
			Fields: map[string]any{
				"environment_id":             envelope.Environment.ID,
				"environment_digest":         envelope.Environment.Digest,
				"environment_definition":     envelope.Environment.DefinitionDigest,
				"environment_network":        envelope.Environment.Network,
				"environment_sandbox":        envelope.Environment.Sandbox,
				"environment_cache_keys":     envelope.Environment.CacheKeys,
				"environment_private_inputs": envelope.Environment.SecretRequired,
			},
		})
	}
	if result.Execution.ExecutionID != "" && jobID != "" && envelope.Digest != "" {
		events = append(events, capsuletrace.Event{
			Kind:           capsuletrace.KindExecutorPrepared,
			At:             result.UpdatedAt,
			JobID:          jobID,
			EnvelopeDigest: envelope.Digest,
			Fields: map[string]any{
				"execution_id":     result.Execution.ExecutionID,
				"network":          envelope.Policy.Network,
				"minimum_sandbox":  envelope.Policy.MinimumSandbox,
				"external_write":   envelope.Policy.ExternalWrite,
				"source_digest":    envelope.SourceDigest,
				"story_digest":     envelope.StoryDigest,
				"verdict_artifact": result.Execution.VerdictArtifact,
			},
		})
	}
	if jobID != "" && envelope.Digest != "" {
		events = append(events, capsuletrace.Event{Kind: capsuletrace.KindCIStarted, At: result.StartedAt, JobID: jobID, EnvelopeDigest: envelope.Digest, Fields: map[string]any{"stage": result.Stage, "terminal": result.Terminal}})
	}
	for _, event := range result.Events {
		if converted, ok := executorTraceEvent(jobID, event); ok {
			events = append(events, converted)
		}
	}
	if result.Verdict.Outcome != "" && jobID != "" && envelope.Digest != "" {
		events = append(events, capsuletrace.Event{Kind: capsuletrace.KindCIVerdict, At: result.UpdatedAt, JobID: jobID, EnvelopeDigest: envelope.Digest, Outcome: result.Verdict.Outcome})
	}
	return capsuletrace.NewDocument(events...)
}

func executorTraceEvent(jobID string, event executor.Event) (capsuletrace.Event, bool) {
	switch event.Kind {
	case capsuletrace.KindExecutorStarted, capsuletrace.KindExecutorFinished, capsuletrace.KindExecutorFailed, capsuletrace.KindExecutorCancelled,
		capsuletrace.KindExecutorSourceUploading, capsuletrace.KindExecutorSourceReady,
		capsuletrace.KindWorkerRegistered, capsuletrace.KindWorkerSourceMaterializing, capsuletrace.KindWorkerEnvironmentVerifying, capsuletrace.KindWorkerEnvironmentVerified,
		capsuletrace.KindWorkerStoryStarted, capsuletrace.KindWorkerCompleted, capsuletrace.KindWorkerFailed, capsuletrace.KindWorkerCancellationRequested, capsuletrace.KindWorkerCancelled:
		return capsuletrace.Event{Kind: event.Kind, At: event.At, JobID: jobID, EnvelopeDigest: event.EnvelopeDigest, Outcome: event.Outcome, Error: providerSafeExecutorError(event), Fields: executorTraceFields(event)}, true
	default:
		return capsuletrace.Event{}, false
	}
}

func executorTraceFields(event executor.Event) map[string]any {
	fields := map[string]any{}
	if event.ExecutionID != "" {
		fields["execution_id"] = event.ExecutionID
	}
	allowed := map[string]bool{
		"transport": true, "remote_host": true, "request_id": true, "method": true, "path": true, "status": true, "duration_ms": true,
		"error_kind": true, "message": true, "completion_state_outcome": true, "exit_code": true, "stage": true,
		"worker_request_id": true, "worker_status": true, "worker_stage": true,
		"source_digest": true, "bundle_digest": true, "bundle_bytes": true, "source_cache": true,
	}
	for key, value := range event.Fields {
		if allowed[key] && providerSafeExecutorField(value) {
			fields[key] = value
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func providerSafeExecutorField(value any) bool {
	text, ok := value.(string)
	if !ok {
		return true
	}
	if filepath.IsAbs(strings.TrimSpace(text)) {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{"secret=", "token=", "password=", "credential=", "api_key=", "private_key="} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func providerSafeExecutorError(event executor.Event) string {
	if event.Error == "" {
		return ""
	}
	// Executor event details are projected into the allowlisted fields above.
	// Keep the trace error itself stable and provider-safe; the full local error
	// remains in the run record's DiagnosticError for on-host diagnosis.
	if event.Kind == capsuletrace.KindExecutorCancelled || event.Kind == capsuletrace.KindWorkerCancelled {
		return "executor cancelled; see local run diagnostic"
	}
	return "executor failed; see local run diagnostic"
}

func writeAtomic(path string, raw []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
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
	return os.Rename(tmpPath, path)
}

func signByPolicy(project string, r receipt.Receipt, signer receipt.Signer) (receipt.Receipt, error) {
	policy, err := receiptPolicy(project)
	if err != nil {
		return r, err
	}
	if !policy.RequireSignature {
		return r, nil
	}
	if signer == nil {
		return r, fmt.Errorf("capsule record: receipt signature required by project policy")
	}
	if policy.Signer != "" && signer.Name() != policy.Signer {
		return r, fmt.Errorf("capsule record: signer %q does not satisfy project policy %q", signer.Name(), policy.Signer)
	}
	return receipt.Sign(r, signer)
}

func receiptPolicy(project string) (ci.ReceiptPolicy, error) {
	cfg, err := ci.Load(project)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ci.ReceiptPolicy{}, nil
		}
		return ci.ReceiptPolicy{}, err
	}
	return cfg.Receipt, nil
}
