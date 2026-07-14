package host_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestGitVCS_OpenPR_PublishesBranchBeforeCreate(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	runner := &openPRPublishRunner{}
	restore := host.SetExecRunnerForTest(runner.run)
	defer restore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/pulls" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if !runner.published {
			http.Error(w, `{"message":"No commits between main and feature/fix"}`, http.StatusUnprocessableEntity)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["head"] != "feature/fix" || payload["base"] != "main" {
			t.Fatalf("payload: %#v", payload)
		}
		writeJSON(w, map[string]any{"number": 42, "html_url": "https://github.com/o/r/pull/42"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitVCSHandler(context.Background(), map[string]any{
		"op":      "open_pr",
		"workdir": "/repo",
		"repo":    "o/r",
		"title":   "PR",
		"body":    "body",
		"base":    "main",
		"head":    "feature/fix",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("open_pr did not publish the feature branch before PR creation; creation aborted with: %s", res.Error)
	}
	if res.Data["pr_id"] != "42" {
		t.Fatalf("pr_id: %v", res.Data["pr_id"])
	}
	if got := fmt.Sprint(res.Data["url"]); !strings.Contains(got, "/pull/42") {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

type openPRPublishRunner struct {
	published bool
}

func (r *openPRPublishRunner) run(_ context.Context, _ string, name string, args ...string) (string, string, int, error) {
	key := name + " " + strings.Join(args, " ")
	switch {
	case key == "git push -u origin HEAD":
		r.published = true
		return "", "", 0, nil
	default:
		return "", fmt.Sprintf("unexpected command: %s", key), 1, nil
	}
}
