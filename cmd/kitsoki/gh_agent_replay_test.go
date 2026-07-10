package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/githubapp"
)

// TestReplay_SignatureVerifiesAndParses proves a replayed payload (a) carries a
// signature the agent's own verifier accepts, and (b) parses back into the
// bug-report event the deck trigger expects.
func TestReplay_SignatureVerifiesAndParses(t *testing.T) {
	const secret = "s3cr3t"
	payload := buildIssuesWebhookPayload("o/r", 62, "opened", "web: bug", "body", []string{"bug"})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sig := signWebhook(secret, raw)

	if !githubapp.VerifyWebhookSignature(secret, raw, "sha256="+sig) {
		t.Fatal("replay signature must verify against the agent's verifier")
	}
	if githubapp.VerifyWebhookSignature("wrong", raw, "sha256="+sig) {
		t.Fatal("a wrong secret must not verify")
	}

	ev, ok, err := ghagent.ParseIssueOpenedBugReport(raw, "")
	if err != nil || !ok {
		t.Fatalf("replayed payload should parse as a bug report: ok=%v err=%v", ok, err)
	}
	if ev.Repo != "o/r" || ev.Number != "62" {
		t.Fatalf("parsed event wrong: %+v", ev)
	}
}

// TestReplay_PostWebhookDelivers proves postWebhook sets the GitHub headers and
// delivers the body to the endpoint.
func TestReplay_PostWebhookDelivers(t *testing.T) {
	var gotEvent, gotSig, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEvent = r.Header.Get("X-GitHub-Event")
		gotSig = r.Header.Get("X-Hub-Signature-256")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ignored\n"))
	}))
	defer srv.Close()

	body := []byte(`{"action":"opened"}`)
	status, resp, err := postWebhook(t.Context(), srv.URL, "issues", "abc", body, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted {
		t.Fatalf("status = %d", status)
	}
	if gotEvent != "issues" || gotSig != "sha256=abc" {
		t.Fatalf("headers wrong: event=%q sig=%q", gotEvent, gotSig)
	}
	if !strings.Contains(gotBody, "opened") {
		t.Fatalf("body not delivered: %q", gotBody)
	}
	if !strings.Contains(resp, "ignored") {
		t.Fatalf("response not returned: %q", resp)
	}
}
