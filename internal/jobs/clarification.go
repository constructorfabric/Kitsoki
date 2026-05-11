// Package jobs — clarification request support (§4.1, §4.3).
//
// A stalled job flips to awaiting_input, writes a typed clarification schema,
// and posts an action_required notification. When the user submits an answer,
// the answer is stored on the job row and the job can resume.
//
// Clarification collision = error: a handler attempting a second awaiting_input
// while one is pending errors out.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"kitsoki/internal/app"
)

// ClarificationSchema describes the fields required for a clarification answer.
type ClarificationSchema struct {
	// Fields maps field names to their type description.
	Fields map[string]string `json:"fields"`
	// Prompt is shown to the user when asking for clarification.
	Prompt string `json:"prompt"`
}

// RequestClarification transitions a job to awaiting_input and stores the
// clarification schema.
//
// Contract: only a job in status "running" may request clarification.
// Returns an error if the job has any other status (including done, failed, or
// already awaiting_input).  The SELECT and UPDATE are wrapped in a transaction
// to prevent a concurrent race where two callers could both pass the status
// check before either commits the UPDATE.
func (js *JobStore) RequestClarification(ctx context.Context, id JobID, schema ClarificationSchema) error {
	tx, err := js.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("jobs.RequestClarification: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Check current status inside the transaction.
	row := tx.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id)
	var status string
	if err := row.Scan(&status); err != nil {
		return fmt.Errorf("jobs.RequestClarification: %w", err)
	}
	if status != string(JobRunning) {
		return fmt.Errorf("jobs.RequestClarification: job %s is not running (status=%s); cannot request clarification", id, status)
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("jobs.RequestClarification: marshal schema: %w", err)
	}

	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `
		UPDATE jobs SET status=?, clarification_schema=?, clarification_answer=NULL, updated_at=?
		WHERE id=?`,
		string(JobAwaitingInput), string(schemaJSON), now, id); err != nil {
		return fmt.Errorf("jobs.RequestClarification: update: %w", err)
	}

	return tx.Commit()
}

// AnswerClarification stores the user's answer and returns the job to running status.
// The answer is any JSON-serialisable value.
func (js *JobStore) AnswerClarification(ctx context.Context, id JobID, answer any) error {
	// Verify job is awaiting_input.
	row := js.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id)
	var status string
	if err := row.Scan(&status); err != nil {
		return fmt.Errorf("jobs.AnswerClarification: %w", err)
	}
	if status != string(JobAwaitingInput) {
		return fmt.Errorf("jobs.AnswerClarification: job %s is not awaiting_input (status=%s)", id, status)
	}

	answerJSON, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("jobs.AnswerClarification: marshal answer: %w", err)
	}

	now := time.Now().UnixMilli()
	_, err = js.db.ExecContext(ctx, `
		UPDATE jobs SET status=?, clarification_answer=?, updated_at=?
		WHERE id=?`,
		string(JobRunning), string(answerJSON), now, id)
	return err
}

// GetClarificationSchema returns the clarification schema for a job that is
// awaiting_input. Returns nil if the job has no pending clarification.
func (js *JobStore) GetClarificationSchema(ctx context.Context, id JobID) (*ClarificationSchema, error) {
	row := js.db.QueryRowContext(ctx, `SELECT clarification_schema FROM jobs WHERE id=?`, id)
	var schemaJSON *string
	if err := row.Scan(&schemaJSON); err != nil {
		return nil, fmt.Errorf("jobs.GetClarificationSchema: %w", err)
	}
	if schemaJSON == nil || *schemaJSON == "" {
		return nil, nil
	}
	var schema ClarificationSchema
	if err := json.Unmarshal([]byte(*schemaJSON), &schema); err != nil {
		return nil, fmt.Errorf("jobs.GetClarificationSchema: unmarshal: %w", err)
	}
	return &schema, nil
}

// RequestClarificationAny is the host.ClarificationRequester-compatible
// overload that accepts schema as any (a ClarificationSchema value is
// expected; any other type is marshalled and unmarshalled into one).
// This allows host.JobContext.Store to reference *JobStore without a
// direct import of the jobs package from host.
//
// The method name matches the interface defined in internal/host/host.go:
//
//	type ClarificationRequester interface {
//	    RequestClarification(ctx, id string, schema any) error
//	    ...
//	}
func (js *JobStore) RequestClarificationAny(ctx context.Context, id JobID, schema any) error {
	var cs ClarificationSchema
	switch v := schema.(type) {
	case ClarificationSchema:
		cs = v
	case *ClarificationSchema:
		if v == nil {
			return fmt.Errorf("jobs.RequestClarificationAny: nil schema")
		}
		cs = *v
	default:
		// Round-trip via JSON for any other type.
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("jobs.RequestClarificationAny: marshal schema: %w", err)
		}
		if err := json.Unmarshal(b, &cs); err != nil {
			return fmt.Errorf("jobs.RequestClarificationAny: unmarshal schema: %w", err)
		}
	}
	return js.RequestClarification(ctx, id, cs)
}

// AnswerClarificationRaw returns the raw JSON-encoded clarification answer
// stored on the job row, or ("", nil) when no answer has been submitted yet.
// It satisfies the host.ClarificationRequester interface so that
// host.RequestClarification can poll for the answer without importing jobs.
func (js *JobStore) AnswerClarificationRaw(ctx context.Context, id JobID) (string, error) {
	row := js.db.QueryRowContext(ctx, `SELECT clarification_answer FROM jobs WHERE id=?`, id)
	var answer *string
	if err := row.Scan(&answer); err != nil {
		return "", fmt.Errorf("jobs.AnswerClarificationRaw: %w", err)
	}
	if answer == nil || *answer == "" {
		return "", nil
	}
	return *answer, nil
}

// PostClarificationNotification posts an action_required notification for a
// clarification request, teleporting back to the origin state/proposal.
func (js *JobStore) PostClarificationNotification(ctx context.Context, sessionID app.SessionID, j *Job, schema ClarificationSchema) error {
	n := &Notification{
		SessionID:          sessionID,
		Severity:           SeverityActionRequired,
		Title:              "Input required: " + j.Kind,
		Body:               schema.Prompt,
		TeleportState:      string(j.OriginState),
		TeleportJobID:      j.ID,
		TeleportProposalID: j.OriginProposalID,
		OriginKind:         "job",
		OriginRef:          "job:" + j.ID,
	}
	return js.InsertNotification(ctx, n)
}
