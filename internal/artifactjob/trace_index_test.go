package artifactjob

import (
	"context"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/app"
)

func TestReindexTraceReaderIndexesJournalArtifactEvents(t *testing.T) {
	ctx := context.Background()
	idx := NewMemoryStore()
	job := Job{
		ID:        "job-trace",
		SessionID: "session-trace",
		Story:     "stories/dev-story",
		Status:    StatusDone,
		CreatedAt: time.Date(2026, 7, 8, 1, 0, 0, 0, time.UTC),
	}
	trace := strings.NewReader(strings.Join([]string{
		`{"time":"2026-07-08T01:00:00Z","msg":"turn.start","session_id":"session-trace","turn":1}`,
		`{"ts":"2026-07-08T01:00:01Z","ev":"artifact.emitted","session_id":"session-trace","turn":1,"body":{"id":"proposal#1","kind":"html","mime":"text/html","label":"Proposal","path":"/tmp/proposal.html","producer":"host.artifacts_dir","size_bytes":123,"created_at":"2026-07-08T01:00:01Z"}}`,
		`{"time":"2026-07-08T01:00:02Z","msg":"turn.done","session_id":"session-trace","turn":3}`,
	}, "\n"))

	run, artifacts, err := ReindexTraceReader(ctx, idx, job, "/tmp/trace.jsonl", trace)
	if err != nil {
		t.Fatalf("ReindexTraceReader: %v", err)
	}
	if run.LastTurn != 3 || run.SessionID != app.SessionID("session-trace") || run.EndedAt == nil {
		t.Fatalf("run = %+v", run)
	}
	if len(artifacts) != 1 || artifacts[0].Handle != "proposal#1" || artifacts[0].Path != "/tmp/proposal.html" {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	resolved, err := idx.ResolveArtifact(ctx, job.ID, "proposal#1")
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if resolved.MIME != "text/html" || resolved.SizeBytes != 123 {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestRunURL(t *testing.T) {
	if got := RunURL("", "abc"); got != "/run/abc" {
		t.Fatalf("path-only URL = %q", got)
	}
	if got := RunURL("https://example.test/kitsoki/", "abc"); got != "https://example.test/kitsoki/run/abc" {
		t.Fatalf("hosted URL = %q", got)
	}
}
