package host_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestGitHubPRView_Native(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var sawPR, sawFiles, sawDiff bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7" && strings.Contains(r.Header.Get("Accept"), "diff"):
			sawDiff = true
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("diff --git a/a.go b/a.go\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7":
			sawPR = true
			writeJSON(w, map[string]any{
				"number":   7,
				"title":    "Fix bug",
				"state":    "open",
				"html_url": "https://github.com/o/r/pull/7",
				"body":     "Regression proof.",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/7/files":
			sawFiles = true
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Fatalf("per_page = %q", got)
			}
			writeJSON(w, []map[string]any{{
				"filename":  "a.go",
				"additions": 3,
				"deletions": 1,
			}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	got, err := host.GitHubPRView(context.Background(), host.GitHubPRViewOptions{
		Repo:        "o/r",
		Number:      7,
		IncludeDiff: true,
	})
	if err != nil {
		t.Fatalf("GitHubPRView: %v", err)
	}
	if !sawPR || !sawFiles || !sawDiff {
		t.Fatalf("expected PR/files/diff requests; sawPR=%v sawFiles=%v sawDiff=%v", sawPR, sawFiles, sawDiff)
	}
	if got.PR.Number != 7 || got.PR.Title != "Fix bug" || got.PR.URL != "https://github.com/o/r/pull/7" {
		t.Fatalf("PR projection: %#v", got.PR)
	}
	if len(got.PR.Files) != 1 || got.PR.Files[0].Path != "a.go" || got.PR.Files[0].Additions != 3 || got.PR.Files[0].Deletions != 1 {
		t.Fatalf("files: %#v", got.PR.Files)
	}
	if !strings.Contains(got.Diff, "diff --git") {
		t.Fatalf("diff: %q", got.Diff)
	}
}
