// Package jobs_test — end-to-end clarification round-trip test.
//
// Tests the full path:
//  1. Submit a job whose handler calls host.RequestClarification.
//  2. Assert a JobAwaitingInput event reaches the session subscriber.
//  3. Assert GetClarificationSchema returns the schema.
//  4. Call AnswerClarification to resume the job.
//  5. Assert the handler returns the answer and the job reaches JobDone.
package jobs_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// TestClarificationRoundTrip exercises the full clarification lifecycle under
// the scheduler:
//  1. A handler calls host.RequestClarification and blocks waiting for an answer.
//  2. The scheduler fans out JobAwaitingInput to the session subscriber.
//  3. The test inspects the DB schema, answers the clarification, and verifies
//     the handler resumes and the job reaches JobDone with the answer echoed.
func TestClarificationRoundTrip(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	sched := jobs.NewScheduler(js)
	sessID := app.SessionID("sess-clar-roundtrip")

	// Subscribe to the session before submitting so we see every event.
	sessCh, ack, unsub := sched.SubscribeSession(sessID)
	defer unsub()

	// Handler: calls host.RequestClarification, then echoes the answer as result.
	var jobID string
	schema := jobs.ClarificationSchema{
		Prompt: "what color?",
		Fields: map[string]string{"answer": "string"},
	}

	id, submitErr := sched.Submit(context.Background(), jobs.JobSpec{
		SessionID: sessID,
		Kind:      "host.test.clarify",
		Handler: func(ctx context.Context, args map[string]any) (host.Result, error) {
			rawAnswer, cErr := host.RequestClarification(ctx, schema)
			if cErr != nil {
				return host.Result{Error: cErr.Error()}, nil
			}
			return host.Result{Data: map[string]any{"answer_raw": rawAnswer}}, nil
		},
	})
	if submitErr != nil {
		t.Fatalf("Submit: %v", submitErr)
	}
	jobID = id

	// Wait for JobAwaitingInput event on the session channel.
	deadline := time.After(3 * time.Second)
	var awaitingEv jobs.JobEvent
	for {
		select {
		case ev := <-sessCh:
			ack()
			if ev.JobID == jobID && ev.Status == jobs.JobAwaitingInput {
				awaitingEv = ev
				goto gotAwaiting
			}
			// Ignore other events (e.g. from a different test's jobs).
		case <-deadline:
			t.Fatal("timeout waiting for JobAwaitingInput event")
		}
	}
gotAwaiting:
	if awaitingEv.JobID != jobID {
		t.Fatalf("wrong job ID in awaiting event: %s", awaitingEv.JobID)
	}

	// Assert the clarification schema is stored in the DB.
	gotSchema, err := js.GetClarificationSchema(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetClarificationSchema: %v", err)
	}
	if gotSchema == nil {
		t.Fatal("expected schema, got nil")
	}
	if gotSchema.Prompt != "what color?" {
		t.Fatalf("expected prompt 'what color?', got %q", gotSchema.Prompt)
	}

	// Answer the clarification: job should flip back to running and unblock the handler.
	if err := js.AnswerClarification(context.Background(), jobID, "blue"); err != nil {
		t.Fatalf("AnswerClarification: %v", err)
	}

	// Wait for JobDone on the session channel.
	deadline = time.After(3 * time.Second)
	var doneEv jobs.JobEvent
	for {
		select {
		case ev := <-sessCh:
			ack()
			if ev.JobID == jobID && (ev.Status == jobs.JobDone || ev.Status == jobs.JobFailed) {
				doneEv = ev
				goto gotDone
			}
		case <-deadline:
			t.Fatal("timeout waiting for JobDone event")
		}
	}
gotDone:
	if doneEv.Status != jobs.JobDone {
		t.Fatalf("expected JobDone, got %s (error=%s)", doneEv.Status, doneEv.Error)
	}
	if doneEv.Result == nil {
		t.Fatal("expected result, got nil")
	}

	// The handler echoed the answer as answer_raw (raw JSON).
	answerRaw, _ := doneEv.Result.Data["answer_raw"].(string)
	if answerRaw == "" {
		t.Fatalf("expected answer_raw in result, got %v", doneEv.Result.Data)
	}
	// The raw JSON should decode to "blue".
	var decoded string
	if err := json.Unmarshal([]byte(answerRaw), &decoded); err != nil {
		t.Fatalf("decode answer_raw %q: %v", answerRaw, err)
	}
	if decoded != "blue" {
		t.Fatalf("expected answer 'blue', got %q", decoded)
	}
}
