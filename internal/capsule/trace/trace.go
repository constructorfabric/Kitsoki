// Package trace defines the shared Capsule trace document and event schema.
package trace

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const DocumentSchema = "capsule-ci-trace/v1"

const (
	KindWorkspaceMaterializing = "capsule.workspace.materializing"
	KindWorkspaceReady         = "capsule.workspace.ready"
	KindWorkspaceFailed        = "capsule.workspace.failed"
	KindWorkspaceChanged       = "capsule.workspace.changed"
	KindWorkspaceCommitted     = "capsule.workspace.committed"
	KindWorkspaceIntegrated    = "capsule.workspace.integrated"
	KindWorkspaceClosed        = "capsule.workspace.closed"

	KindEnvironmentResolved = "capsule.environment.resolved"

	KindExecutorPrepared        = "capsule.executor.prepared"
	KindExecutorStarted         = "capsule.executor.started"
	KindExecutorFinished        = "capsule.executor.finished"
	KindExecutorFailed          = "capsule.executor.failed"
	KindExecutorCancelled       = "capsule.executor.cancelled"
	KindExecutorSourceUploading = "capsule.executor.source.uploading"
	KindExecutorSourceReady     = "capsule.executor.source.ready"

	KindWorkerRegistered            = "capsule.worker.registered"
	KindWorkerSourceMaterializing   = "capsule.worker.source.materializing"
	KindWorkerEnvironmentVerifying  = "capsule.worker.environment.verifying"
	KindWorkerEnvironmentVerified   = "capsule.worker.environment.verified"
	KindWorkerStoryStarted          = "capsule.worker.story.started"
	KindWorkerCompleted             = "capsule.worker.completed"
	KindWorkerFailed                = "capsule.worker.failed"
	KindWorkerCancellationRequested = "capsule.worker.cancellation_requested"
	KindWorkerCancelled             = "capsule.worker.cancelled"

	KindCIStarted = "capsule.ci.started"
	KindCIVerdict = "capsule.ci.verdict"

	KindSyncPlanned    = "capsule.sync.planned"
	KindSyncApplied    = "capsule.sync.applied"
	KindSyncStale      = "capsule.sync.stale"
	KindSyncConflicted = "capsule.sync.conflicted"
	KindSyncAborted    = "capsule.sync.aborted"
)

type Document struct {
	Schema string  `json:"schema"`
	Events []Event `json:"events"`
}

type Event struct {
	Kind              string         `json:"kind"`
	At                time.Time      `json:"at"`
	JobID             string         `json:"job_id,omitempty"`
	EnvelopeDigest    string         `json:"envelope_digest,omitempty"`
	Outcome           string         `json:"outcome,omitempty"`
	InstanceID        string         `json:"instance_id,omitempty"`
	Generation        uint64         `json:"generation,omitempty"`
	PlanDigest        string         `json:"plan_digest,omitempty"`
	Operation         string         `json:"operation,omitempty"`
	Class             string         `json:"class,omitempty"`
	TargetRef         string         `json:"target_ref,omitempty"`
	Candidate         string         `json:"candidate,omitempty"`
	OldTarget         string         `json:"old_target,omitempty"`
	NewTarget         string         `json:"new_target,omitempty"`
	ContinuationToken string         `json:"continuation_token,omitempty"`
	Error             string         `json:"error,omitempty"`
	Fields            map[string]any `json:"fields,omitempty"`
}

func NewDocument(events ...Event) Document {
	out := Document{Schema: DocumentSchema, Events: make([]Event, 0, len(events))}
	for _, event := range events {
		if event.At.IsZero() {
			event.At = time.Now().UTC()
		} else {
			event.At = event.At.UTC()
		}
		out.Events = append(out.Events, event)
	}
	return out
}

func MarshalDocument(doc Document) ([]byte, error) {
	if err := ValidateDocument(doc); err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}

func ValidateDocument(doc Document) error {
	if doc.Schema != DocumentSchema {
		return fmt.Errorf("capsule trace: unsupported schema %q", doc.Schema)
	}
	for i, event := range doc.Events {
		if err := ValidateEvent(event); err != nil {
			return fmt.Errorf("capsule trace: event %d: %w", i, err)
		}
	}
	return nil
}

func ValidateEvent(event Event) error {
	if strings.TrimSpace(event.Kind) == "" {
		return fmt.Errorf("kind is required")
	}
	if err := validateProviderSafeValue("error", event.Error); err != nil {
		return err
	}
	if err := validateProviderSafeFields(event.Fields); err != nil {
		return err
	}
	switch event.Kind {
	case KindCIStarted, KindCIVerdict, KindExecutorPrepared, KindExecutorStarted, KindExecutorFinished, KindExecutorFailed, KindExecutorCancelled,
		KindExecutorSourceUploading, KindExecutorSourceReady,
		KindWorkerRegistered, KindWorkerSourceMaterializing, KindWorkerEnvironmentVerifying, KindWorkerEnvironmentVerified,
		KindWorkerStoryStarted, KindWorkerCompleted, KindWorkerFailed, KindWorkerCancellationRequested, KindWorkerCancelled:
		if event.JobID == "" || event.EnvelopeDigest == "" {
			return fmt.Errorf("%s requires job_id and envelope_digest", event.Kind)
		}
	case KindSyncPlanned, KindSyncApplied, KindSyncStale, KindSyncConflicted, KindSyncAborted:
		if event.PlanDigest == "" || event.Operation == "" || event.TargetRef == "" {
			return fmt.Errorf("%s requires plan_digest, operation, and target_ref", event.Kind)
		}
	case KindWorkspaceMaterializing, KindWorkspaceReady, KindWorkspaceFailed, KindWorkspaceChanged, KindWorkspaceCommitted, KindWorkspaceIntegrated, KindWorkspaceClosed:
		if event.InstanceID == "" {
			return fmt.Errorf("%s requires instance_id", event.Kind)
		}
	case KindEnvironmentResolved:
		// Environment resolution may be recorded before a job exists; Fields carry
		// definition and lock digests.
	default:
		return fmt.Errorf("unknown kind %q", event.Kind)
	}
	return nil
}

func validateProviderSafeFields(fields map[string]any) error {
	for key, value := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized == "" {
			return fmt.Errorf("field key is required")
		}
		if providerUnsafeKey(normalized) {
			return fmt.Errorf("field %q is not provider-safe", key)
		}
		if err := validateProviderSafeValue(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateProviderSafeValue(path string, value any) error {
	switch v := value.(type) {
	case nil, bool, float64, int, int64, uint64, json.Number:
		return nil
	case string:
		if providerUnsafeString(v) {
			return fmt.Errorf("field %q contains provider-unsafe value", path)
		}
	case []any:
		for i, item := range v {
			if err := validateProviderSafeValue(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case []string:
		for i, item := range v {
			if err := validateProviderSafeValue(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			normalized := strings.ToLower(strings.TrimSpace(key))
			if normalized == "" {
				return fmt.Errorf("field key is required")
			}
			if providerUnsafeKey(normalized) {
				return fmt.Errorf("field %q is not provider-safe", key)
			}
			if err := validateProviderSafeValue(path+"."+key, item); err != nil {
				return err
			}
		}
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("field %q contains unsupported value %T", path, value)
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return fmt.Errorf("field %q contains unsupported value %T", path, value)
		}
		return validateProviderSafeValue(path, decoded)
	}
	return nil
}

func providerUnsafeKey(key string) bool {
	for _, marker := range []string{"secret", "token", "password", "credential", "private_key", "api_key"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func providerUnsafeString(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		return true
	}
	for _, token := range strings.Fields(trimmed) {
		token = strings.Trim(token, `"'()[]{}:;,`)
		if filepath.IsAbs(token) || strings.HasPrefix(token, "~"+string(filepath.Separator)) {
			return true
		}
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"secret=", "token=", "password=", "credential=", "api_key=", "private_key="} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
