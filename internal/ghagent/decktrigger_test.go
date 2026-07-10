package ghagent

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"kitsoki/internal/host"
)

func issuesPayload(action, repo string, number int, title, body string, isPR bool) []byte {
	m := map[string]any{
		"action":     action,
		"repository": map[string]any{"full_name": repo},
		"issue": map[string]any{
			"number":   number,
			"title":    title,
			"html_url": "https://github.com/" + repo + "/issues/" + strconv.Itoa(number),
			"body":     body,
		},
	}
	if isPR {
		m["issue"].(map[string]any)["pull_request"] = map[string]any{}
	}
	b, _ := json.Marshal(m)
	return b
}

func TestParseIssueOpenedBugReport(t *testing.T) {
	body := "A bug.\n\n```kitsoki\nfiled_by: alice\n```"
	ev, ok, err := ParseIssueOpenedBugReport(issuesPayload("opened", "o/r", 7, "web: broken", body, false), "")
	if err != nil || !ok {
		t.Fatalf("expected ok, got ok=%v err=%v", ok, err)
	}
	if ev.Repo != "o/r" || ev.Number != "7" || ev.Title != "web: broken" {
		t.Fatalf("bad event: %+v", ev)
	}
	if ev.FiledBy != "alice" {
		t.Errorf("filed_by not parsed: %q", ev.FiledBy)
	}

	// Wrong action, PR, and missing number are all not-applicable (ok=false).
	for _, tc := range []struct {
		name              string
		action            string
		isPR              bool
		number            int
	}{
		{"edited", "edited", false, 7},
		{"closed", "closed", false, 7},
		{"pull request", "opened", true, 7},
		{"no number", "opened", false, 0},
	} {
		_, ok, err := ParseIssueOpenedBugReport(issuesPayload(tc.action, "o/r", tc.number, "t", "b", tc.isPR), "")
		if err != nil || ok {
			t.Errorf("%s: expected not-applicable, got ok=%v err=%v", tc.name, ok, err)
		}
	}

	// labeled + reopened are deck-worthy.
	for _, action := range []string{"labeled", "reopened"} {
		if _, ok, _ := ParseIssueOpenedBugReport(issuesPayload(action, "o/r", 7, "t", "b", false), ""); !ok {
			t.Errorf("action %q should be deck-worthy", action)
		}
	}
}

func TestDeckTrigger_Handle_DecksWhenEvidencePresent(t *testing.T) {
	evStore, _ := NewEvidenceStore(t.TempDir())
	deckStore, _ := NewDeckStore(t.TempDir())
	// Seed the agent's evidence for issue #7.
	if err := evStore.Save(DeckID("o/r", "7"), []byte(`[{"type":2}]`), []byte(`{"log":{"entries":[]}}`)); err != nil {
		t.Fatal(err)
	}

	var commentID string
	ticket := func(_ context.Context, args map[string]any) (host.Result, error) {
		if args["op"] == "comment" {
			commentID, _ = args["id"].(string)
		}
		return host.Result{Data: map[string]any{"url": "x"}}, nil
	}
	tr := &DeckTrigger{
		Evidence:      evStore,
		Store:         deckStore,
		Renderer:      fakeRenderer{},
		Ticket:        ticket,
		PublicBaseURL: "https://agent",
	}

	url, handled, err := tr.Handle(context.Background(), issuesPayload("opened", "o/r", 7, "web: x", "b", false))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !handled {
		t.Fatal("expected handled=true with evidence present")
	}
	if url != "https://agent/decks/o-r-7" {
		t.Fatalf("deck url = %q", url)
	}
	if commentID != "7" {
		t.Errorf("comment posted to issue %q", commentID)
	}
	if !deckStore.Exists(DeckID("o/r", "7")) {
		t.Error("deck not hosted")
	}

	// Idempotent: a redelivery does nothing (deck already exists).
	_, handled2, err := tr.Handle(context.Background(), issuesPayload("opened", "o/r", 7, "web: x", "b", false))
	if err != nil || handled2 {
		t.Fatalf("redelivery should be a no-op, got handled=%v err=%v", handled2, err)
	}
}

func TestDeckTrigger_Handle_SkipsWithoutEvidence(t *testing.T) {
	evStore, _ := NewEvidenceStore(t.TempDir())
	deckStore, _ := NewDeckStore(t.TempDir())
	tr := &DeckTrigger{Evidence: evStore, Store: deckStore, Renderer: fakeRenderer{}, PublicBaseURL: "https://a"}

	// No evidence seeded → not a deckable bug report; skip quietly.
	_, handled, err := tr.Handle(context.Background(), issuesPayload("opened", "o/r", 9, "t", "b", false))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if handled {
		t.Fatal("must not handle an issue with no on-agent evidence")
	}
}
