package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	studio "kitsoki/internal/mcp/studio"
)

func TestIssueFilerUsesNativeGitHubAPI(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var sawIssueCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		sawIssueCreate = true
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["title"] != "native issue" {
			t.Fatalf("title = %v", payload["title"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":123,"html_url":"https://github.com/o/r/issues/123"}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := ghIssueFiler(context.Background(), studio.IssueRequest{
		Repo: "o/r", Title: "native issue", Body: "body", Labels: []string{"source-autonomous"},
	})
	if err != nil {
		t.Fatalf("ghIssueFiler: %v", err)
	}
	if !sawIssueCreate {
		t.Fatal("expected native issue create request")
	}
	if res.URL != "https://github.com/o/r/issues/123" || res.Number != 123 {
		t.Fatalf("result = %+v", res)
	}
}

func TestIssueFilerInfersRepoFromRoot(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	root := t.TempDir()
	var sawIssueCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		sawIssueCreate = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":77,"html_url":"https://github.com/o/r/issues/77"}`))
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		if d != root || name != "git" || strings.Join(args, " ") != "remote get-url origin" {
			t.Fatalf("unexpected repo inference command: dir=%q cmd=%s args=%v", d, name, args)
		}
		return "https://github.com/o/r.git\n", "", 0, nil
	})
	defer restoreExec()

	res, err := ghIssueFiler(context.Background(), studio.IssueRequest{
		Root: root, Title: "native issue", Body: "body",
	})
	if err != nil {
		t.Fatalf("ghIssueFiler: %v", err)
	}
	if !sawIssueCreate {
		t.Fatal("expected native issue create request")
	}
	if res.URL != "https://github.com/o/r/issues/77" || res.Number != 77 {
		t.Fatalf("result = %+v", res)
	}
}

func TestIssueCreateDocsDescribeNativeFiler(t *testing.T) {
	docPath := filepath.Join("..", "..", "docs", "architecture", "mcp-studio.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	text := string(data)
	if !strings.Contains(text, "production: native `host.gh.ticket` / GitHub REST API") {
		t.Fatalf("issue.create docs must describe the native production filer")
	}
	for _, stale := range []string{
		"prod: `gh`",
		"production: `gh`",
		"shells to `gh issue create`",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("issue.create docs still contain stale GitHub CLI wording %q", stale)
		}
	}
}

func TestIssueNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/constructorfabric/Kitsoki/issues/123": 123,
		"https://github.com/owner/repo/issues/7":                  7,
		"https://github.com/owner/repo/issues/":                   0, // trailing slash, no number
		"not-a-url":                                               0,
		"":                                                        0,
	}
	for url, want := range cases {
		if got := issueNumberFromURL(url); got != want {
			t.Errorf("issueNumberFromURL(%q) = %d, want %d", url, got, want)
		}
	}
}
