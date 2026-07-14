package ghagent_test

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/ghagent"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
)

// mockReactionTicket is a host.Handler stub bound as the ticket seam. It
// answers comment_reactions with a fixed reaction list — no network, no
// cassette file needed (WS-C C4 gh-agent surface).
type mockReactionTicket struct {
	reactions []map[string]any
	err       string
	lastArgs  map[string]any
}

func (m *mockReactionTicket) handler(_ context.Context, args map[string]any) (host.Result, error) {
	m.lastArgs = args
	if m.err != "" {
		return host.Result{Error: m.err}, nil
	}
	hasThumbsdown, hasThumbsup := false, false
	for _, r := range m.reactions {
		switch r["content"] {
		case "-1":
			hasThumbsdown = true
		case "+1":
			hasThumbsup = true
		}
	}
	reactions := make([]any, len(m.reactions))
	for i, r := range m.reactions {
		reactions[i] = r
	}
	return host.Result{Data: map[string]any{
		"ok":             true,
		"reactions":      reactions,
		"has_thumbsdown": hasThumbsdown,
		"has_thumbsup":   hasThumbsup,
	}}, nil
}

func routingFeedbackEntriesFor(t *testing.T, jr journal.Reader, sid app.SessionID) []journal.RoutingFeedbackEvent {
	t.Helper()
	seq, stop := jr.ReplayTyped(sid)
	defer func() {
		if err := stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}
	}()
	var out []journal.RoutingFeedbackEvent
	for e := range seq {
		if e.Kind != journal.KindRoutingFeedback {
			continue
		}
		var body journal.RoutingFeedbackEvent
		if err := json.Unmarshal(e.Body, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		out = append(out, body)
	}
	return out
}

func TestReadAckReactionVerdict_Thumbsdown(t *testing.T) {
	ticket := &mockReactionTicket{reactions: []map[string]any{{"content": "-1"}}}
	verdict, ok, err := ghagent.ReadAckReactionVerdict(context.Background(), ticket.handler, "o/r", "1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok || verdict != "down" {
		t.Fatalf("verdict=%q ok=%v, want down/true", verdict, ok)
	}
	if ticket.lastArgs["op"] != "comment_reactions" {
		t.Fatalf("op = %v", ticket.lastArgs["op"])
	}
}

func TestReadAckReactionVerdict_NoneYet(t *testing.T) {
	ticket := &mockReactionTicket{}
	verdict, ok, err := ghagent.ReadAckReactionVerdict(context.Background(), ticket.handler, "o/r", "1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok || verdict != "" {
		t.Fatalf("verdict=%q ok=%v, want none", verdict, ok)
	}
}

// RecordAckReactionFeedback journals a 👎 reaction on the ack comment through
// the SAME journal event the TUI's /route and web's thumbs control write.
func TestRecordAckReactionFeedback_Thumbsdown(t *testing.T) {
	ticket := &mockReactionTicket{reactions: []map[string]any{{"content": "-1"}}}
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	jr := journal.NewMemReader(store)

	job := jobs.GHJob{
		JobID:        "job-1",
		Repo:         "o/r",
		ObjectNumber: "42",
		Story:        "stories/bugfix",
		CommentID:    "1",
	}

	recorded, err := ghagent.RecordAckReactionFeedback(context.Background(), ticket.handler, w, job, "the login page crashes")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !recorded {
		t.Fatalf("expected recorded=true")
	}

	entries := routingFeedbackEntriesFor(t, jr, app.SessionID(job.JobID))
	if len(entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(entries))
	}
	want := journal.RoutingFeedbackEvent{
		Phrase:  "the login page crashes",
		State:   "o/r#42",
		Intent:  "stories/bugfix",
		Tier:    "label-router",
		Verdict: "down",
	}
	if entries[0] != want {
		t.Fatalf("entry = %+v, want %+v", entries[0], want)
	}
}

// No reaction yet is not an error and journals nothing — a polling caller can
// simply re-check later.
func TestRecordAckReactionFeedback_NoReactionYet(t *testing.T) {
	ticket := &mockReactionTicket{}
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	jr := journal.NewMemReader(store)

	job := jobs.GHJob{JobID: "job-2", Repo: "o/r", ObjectNumber: "7", Story: "stories/dev-story", CommentID: "9"}
	recorded, err := ghagent.RecordAckReactionFeedback(context.Background(), ticket.handler, w, job, "add dark mode")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if recorded {
		t.Fatalf("expected recorded=false")
	}
	entries := routingFeedbackEntriesFor(t, jr, app.SessionID(job.JobID))
	if len(entries) != 0 {
		t.Fatalf("entries: got %d, want 0", len(entries))
	}
}

func TestRecordAckReactionFeedback_RequiresCommentID(t *testing.T) {
	ticket := &mockReactionTicket{}
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)

	job := jobs.GHJob{JobID: "job-3", Repo: "o/r", ObjectNumber: "1", Story: "stories/bugfix"} // no CommentID
	_, err := ghagent.RecordAckReactionFeedback(context.Background(), ticket.handler, w, job, "phrase")
	if err == nil {
		t.Fatalf("expected error for missing comment_id")
	}
}
