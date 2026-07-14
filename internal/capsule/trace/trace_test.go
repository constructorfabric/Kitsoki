package trace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateDocumentRequiresKnownSchemaAndEventFacts(t *testing.T) {
	doc := NewDocument(
		Event{Kind: KindCIStarted, JobID: "job", EnvelopeDigest: "sha256:env"},
		Event{Kind: KindSyncPlanned, PlanDigest: "sha256:plan", Operation: "publish", TargetRef: "main"},
		Event{Kind: KindWorkspaceReady, InstanceID: "workspace", Generation: 2},
	)
	if err := ValidateDocument(doc); err != nil {
		t.Fatal(err)
	}
	doc.Schema = "other"
	if err := ValidateDocument(doc); err == nil {
		t.Fatal("unsupported schema accepted")
	}
}

func TestValidateEventRejectsIncompleteKnownFacts(t *testing.T) {
	cases := []Event{
		{Kind: KindCIStarted, JobID: "job"},
		{Kind: KindSyncApplied, PlanDigest: "sha256:plan", Operation: "promote"},
		{Kind: KindWorkspaceCommitted},
		{Kind: "capsule.unknown"},
	}
	for _, tc := range cases {
		if err := ValidateEvent(tc); err == nil {
			t.Fatalf("event accepted: %#v", tc)
		}
	}
}

func TestValidateEventRejectsProviderUnsafeFields(t *testing.T) {
	cases := []Event{
		{Kind: KindEnvironmentResolved, Fields: map[string]any{"secret_ref": "KITSOKI_TOKEN"}},
		{Kind: KindEnvironmentResolved, Fields: map[string]any{"log": "/Users/brad/code/Kitsoki/.env"}},
		{Kind: KindEnvironmentResolved, Fields: map[string]any{"nested": map[string]any{"api_key": "redacted"}}},
		{Kind: KindEnvironmentResolved, Fields: map[string]any{"args": []any{"ok", "token=abc123"}}},
		{Kind: KindWorkspaceFailed, InstanceID: "workspace", Error: "open /tmp/secret: permission denied"},
	}
	for _, tc := range cases {
		if err := ValidateEvent(tc); err == nil {
			t.Fatalf("provider-unsafe event accepted: %#v", tc)
		}
	}
}

func TestMarshalDocumentGoldenByteStableAndOldReaderCompatible(t *testing.T) {
	fixed := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	doc := Document{Schema: DocumentSchema, Events: []Event{
		{Kind: KindWorkspaceReady, At: fixed, InstanceID: "workspace-1", Generation: 7, Fields: map[string]any{"workspace_path": ".capsules/workspaces/workspace-1"}},
		{Kind: KindEnvironmentResolved, At: fixed.Add(time.Second), Fields: map[string]any{"environment_digest": "sha256:env", "lock_path": ".kitsoki/environments/ci.lock.json"}},
		{Kind: KindCIStarted, At: fixed.Add(2 * time.Second), JobID: "job-1", EnvelopeDigest: "sha256:envelope"},
		{Kind: KindCIVerdict, At: fixed.Add(3 * time.Second), JobID: "job-1", EnvelopeDigest: "sha256:envelope", Outcome: "passed"},
	}}
	raw, err := MarshalDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{"schema":"capsule-ci-trace/v1","events":[{"kind":"capsule.workspace.ready","at":"2026-07-10T01:02:03Z","instance_id":"workspace-1","generation":7,"fields":{"workspace_path":".capsules/workspaces/workspace-1"}},{"kind":"capsule.environment.resolved","at":"2026-07-10T01:02:04Z","fields":{"environment_digest":"sha256:env","lock_path":".kitsoki/environments/ci.lock.json"}},{"kind":"capsule.ci.started","at":"2026-07-10T01:02:05Z","job_id":"job-1","envelope_digest":"sha256:envelope"},{"kind":"capsule.ci.verdict","at":"2026-07-10T01:02:06Z","job_id":"job-1","envelope_digest":"sha256:envelope","outcome":"passed"}]}`
	if string(raw) != golden {
		t.Fatalf("golden trace changed\nwant: %s\n got: %s", golden, raw)
	}
	if raw2, err := MarshalDocument(doc); err != nil || string(raw2) != golden {
		t.Fatalf("trace is not byte-stable: %v %s", err, raw2)
	}
	var oldReader struct {
		Schema string `json:"schema"`
		Events []struct {
			Kind           string `json:"kind"`
			JobID          string `json:"job_id,omitempty"`
			EnvelopeDigest string `json:"envelope_digest,omitempty"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &oldReader); err != nil {
		t.Fatal(err)
	}
	if oldReader.Schema != DocumentSchema || len(oldReader.Events) != 4 || oldReader.Events[2].JobID != "job-1" {
		t.Fatalf("old reader projection %#v", oldReader)
	}
	if strings.Contains(string(raw), "/Users/") || strings.Contains(strings.ToLower(string(raw)), "token") {
		t.Fatalf("golden trace leaked provider-unsafe content: %s", raw)
	}
}
