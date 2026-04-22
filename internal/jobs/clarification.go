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
	"encoding/json"
	"fmt"
	"time"

	"hally/internal/app"
)

// ClarificationSchema describes the fields required for a clarification answer.
type ClarificationSchema struct {
	// Fields maps field names to their type description.
	Fields map[string]string `json:"fields"`
	// Prompt is shown to the user when asking for clarification.
	Prompt string `json:"prompt"`
}

// RequestClarification transitions a job to awaiting_input and stores the
// clarification schema. Returns an error if the job is already awaiting_input.
func (js *JobStore) RequestClarification(ctx context.Context, id JobID, schema ClarificationSchema) error {
	// Check current status.
	row := js.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, id)
	var status string
	if err := row.Scan(&status); err != nil {
		return fmt.Errorf("jobs.RequestClarification: %w", err)
	}
	if status == string(JobAwaitingInput) {
		return fmt.Errorf("jobs.RequestClarification: job %s already awaiting_input (clarification collision)", id)
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("jobs.RequestClarification: marshal schema: %w", err)
	}

	now := time.Now().UnixMilli()
	_, err = js.db.ExecContext(ctx, `
		UPDATE jobs SET status=?, clarification_schema=?, clarification_answer=NULL, updated_at=?
		WHERE id=?`,
		string(JobAwaitingInput), string(schemaJSON), now, id)
	return err
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
