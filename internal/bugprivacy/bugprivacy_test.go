package bugprivacy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/runstatus/harscrub"
)

func TestCheckSubstitutesHighEntropyAndFilesFollowUp(t *testing.T) {
	root := t.TempDir()
	secret := "mF9xQ2rT8vLp0AqZ7nByC4dEuGhJkM3sW6yI"
	safe, result, err := Check(context.Background(), nil, Report{
		Surface: "cli",
		Target:  "kitsoki",
		Title:   "leaks opaque token",
		Body:    "the request body included " + secret,
	}, harscrub.ScrubOptions{}, root, "tester")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if strings.Contains(safe.Body, secret) {
		t.Fatalf("safe report leaked high-entropy value: %s", safe.Body)
	}
	if result.Status != "passed_with_substitutions" {
		t.Fatalf("status = %q", result.Status)
	}
	if result.FollowUpPath == "" {
		t.Fatal("expected depersonalized follow-up path")
	}
	data, err := os.ReadFile(filepath.Join(root, result.FollowUpPath))
	if err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	body := string(data)
	if strings.Contains(body, secret) {
		t.Fatalf("follow-up leaked raw secret: %s", body)
	}
	if !strings.Contains(body, "high_entropy") {
		t.Fatalf("follow-up missing category: %s", body)
	}
}

func TestCheckBlocksWhenAgentFails(t *testing.T) {
	root := t.TempDir()
	checker := CheckFunc(func(context.Context, Report, []Finding) (Decision, error) {
		return Decision{Pass: false, Categories: []string{"email"}, Reason: "contains an email address"}, nil
	})
	_, result, err := Check(context.Background(), checker, Report{
		Surface: "web",
		Title:   "private user",
		Body:    "contact the user",
	}, harscrub.ScrubOptions{}, root, "tester")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.Blocked() {
		t.Fatalf("expected blocked result: %#v", result)
	}
	if result.FollowUpPath == "" {
		t.Fatal("expected follow-up for failed privacy gate")
	}
	data, err := os.ReadFile(filepath.Join(root, result.FollowUpPath))
	if err != nil {
		t.Fatalf("read follow-up: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "blocked before filing") || !strings.Contains(body, "email") {
		t.Fatalf("follow-up missing blocked/category details: %s", body)
	}
}
