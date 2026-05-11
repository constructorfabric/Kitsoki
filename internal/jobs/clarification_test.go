package jobs_test

import (
	"context"
	"testing"

	"kitsoki/internal/jobs"
)

func TestClarification_RequestAndAnswer(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	// Insert a running job.
	j := makeTestJob("job-clar-1", jobs.JobRunning)
	if err := js.UpsertJob(context.Background(), j); err != nil {
		t.Fatalf("UpsertJob: %v", err)
	}

	// Request clarification.
	schema := jobs.ClarificationSchema{
		Fields: map[string]string{"branch": "string"},
		Prompt: "Which branch should I use?",
	}
	if err := js.RequestClarification(context.Background(), j.ID, schema); err != nil {
		t.Fatalf("RequestClarification: %v", err)
	}

	// Check schema is retrievable.
	got, err := js.GetClarificationSchema(context.Background(), j.ID)
	if err != nil {
		t.Fatalf("GetClarificationSchema: %v", err)
	}
	if got == nil {
		t.Fatal("expected schema, got nil")
	}
	if got.Prompt != "Which branch should I use?" {
		t.Fatalf("expected prompt, got %q", got.Prompt)
	}

	// Cannot request clarification again while pending.
	if err := js.RequestClarification(context.Background(), j.ID, schema); err == nil {
		t.Fatal("expected error on second RequestClarification")
	}

	// Answer the clarification.
	if err := js.AnswerClarification(context.Background(), j.ID, map[string]any{"branch": "main"}); err != nil {
		t.Fatalf("AnswerClarification: %v", err)
	}

	// Job should be running again.
	listed, err := js.ListJobsByStatus(context.Background(), j.SessionID, jobs.JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected job back to running, got %d", len(listed))
	}
}

func TestClarification_AnswerWrongStatus(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	j := makeTestJob("job-clar-2", jobs.JobRunning)
	if err := js.UpsertJob(context.Background(), j); err != nil {
		t.Fatalf("UpsertJob: %v", err)
	}

	// Try to answer without requesting first.
	if err := js.AnswerClarification(context.Background(), j.ID, "yes"); err == nil {
		t.Fatal("expected error when answering a job not awaiting_input")
	}
}

// TestRequestClarification_RejectsDoneJob verifies P1-2: calling
// RequestClarification on a job that is already done (or failed) must return
// an error.  Before the fix, only awaiting_input was rejected; any other
// non-running status (done, failed, cancelled) was silently accepted.
func TestRequestClarification_RejectsDoneJob(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	for _, status := range []jobs.JobStatus{jobs.JobDone, jobs.JobFailed, jobs.JobCancelled, jobs.JobAwaitingInput} {
		t.Run(string(status), func(t *testing.T) {
			j := makeTestJob("job-clar-reject-"+string(status), status)
			if err := js.UpsertJob(context.Background(), j); err != nil {
				t.Fatalf("UpsertJob: %v", err)
			}

			schema := jobs.ClarificationSchema{
				Fields: map[string]string{"q": "string"},
				Prompt: "test",
			}
			err := js.RequestClarification(context.Background(), j.ID, schema)
			if err == nil {
				t.Fatalf("expected error when requesting clarification on %s job", status)
			}
		})
	}
}
