package studio_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	studio "kitsoki/internal/mcp/studio"
)

// gh_tools_test.go — verification for the gh.* surface. The GitHub-backed tools
// are native and covered with fake GitHub HTTP APIs.

func TestGHIssuesUsesNativeTicketProvider(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/search/issues", r.URL.Path)
		assert.Equal(t, "2", r.URL.Query().Get("per_page"))
		gotQuery = r.URL.Query().Get("q")
		writeGHToolsJSON(t, w, map[string]any{"items": []map[string]any{{
			"number":    12,
			"title":     "Native issue",
			"state":     "open",
			"html_url":  "https://github.com/acme/repo/issues/12",
			"assignees": []map[string]any{{"login": "brad"}},
			"labels":    []map[string]any{{"name": "bug"}},
		}}})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := callTool(ctx, cs, "gh.issues", map[string]any{
		"repo":     "acme/repo",
		"state":    "open",
		"assignee": "@me",
		"search":   "label:bug",
		"limit":    2,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "gh.issues: %s", contentText(res))
	require.Contains(t, gotQuery, "repo:acme/repo")
	require.Contains(t, gotQuery, "is:issue")
	require.Contains(t, gotQuery, "is:open")
	require.Contains(t, gotQuery, "assignee:@me")
	require.Contains(t, gotQuery, "label:bug")

	var out studio.GHIssuesOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	require.True(t, out.OK)
	rawIssues, ok := out.Issues.([]any)
	require.True(t, ok, "issues should decode as an array: %#v", out.Issues)
	require.Len(t, rawIssues, 1)
	issue := rawIssues[0].(map[string]any)
	assert.Equal(t, "12", issue["id"])
	assert.Equal(t, "Native issue", issue["title"])
	assert.Equal(t, "github", issue["source"])
}

func writeGHToolsJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func TestGHPRViewUsesNativeProvider(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())

	var sawPR, sawFiles, sawDiff bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/pulls/51" && strings.Contains(r.Header.Get("Accept"), "diff"):
			sawDiff = true
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("diff --git a/app.go b/app.go\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/pulls/51":
			sawPR = true
			writeGHToolsJSON(t, w, map[string]any{
				"number":   51,
				"title":    "Fix owner routing",
				"state":    "open",
				"html_url": "https://github.com/acme/repo/pull/51",
				"body":     "Regression test included.",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/pulls/51/files":
			sawFiles = true
			assert.Equal(t, "100", r.URL.Query().Get("per_page"))
			writeGHToolsJSON(t, w, []map[string]any{{
				"filename":  "app.go",
				"additions": 5,
				"deletions": 2,
			}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := callTool(ctx, cs, "gh.pr_view", map[string]any{
		"repo":         "acme/repo",
		"number":       51,
		"include_diff": true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "gh.pr_view: %s", contentText(res))
	require.True(t, sawPR, "expected PR metadata request")
	require.True(t, sawFiles, "expected PR files request")
	require.True(t, sawDiff, "expected PR diff request")

	var out studio.GHPRViewOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	require.True(t, out.OK)
	require.Contains(t, out.Diff, "diff --git")
	pr, ok := out.PR.(map[string]any)
	require.True(t, ok, "PR should decode as object: %#v", out.PR)
	assert.Equal(t, float64(51), pr["number"])
	assert.Equal(t, "Fix owner routing", pr["title"])
	files, ok := pr["files"].([]any)
	require.True(t, ok, "files should decode as array: %#v", pr["files"])
	require.Len(t, files, 1)
	file := files[0].(map[string]any)
	assert.Equal(t, "app.go", file["path"])
	assert.Equal(t, float64(5), file["additions"])
	assert.Equal(t, float64(2), file["deletions"])
}

func TestGHCommentUsesNativeIssueProvider(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())

	var postedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/repos/acme/repo/issues/7/comments", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		postedBody, _ = payload["body"].(string)
		writeGHToolsJSON(t, w, map[string]any{"html_url": "https://github.com/acme/repo/issues/7#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := callTool(ctx, cs, "gh.comment", map[string]any{
		"repo":   "acme/repo",
		"number": 7,
		"body":   "Repro confirmed.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "gh.comment: %s", contentText(res))
	require.Equal(t, "Repro confirmed.", postedBody)

	var out studio.GHCommentOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	require.True(t, out.OK)
	require.Equal(t, "https://github.com/acme/repo/issues/7#issuecomment-1", out.URL)
}

func TestGHCommentUsesNativePRProvider(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())

	var postedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/repos/acme/repo/issues/42/comments", r.URL.Path)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		postedBody, _ = payload["body"].(string)
		writeGHToolsJSON(t, w, map[string]any{"html_url": "https://github.com/acme/repo/pull/42#issuecomment-2"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := callTool(ctx, cs, "gh.comment", map[string]any{
		"repo":   "acme/repo",
		"number": 42,
		"on":     "pr",
		"body":   "Review note.",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "gh.comment: %s", contentText(res))
	require.Equal(t, "Review note.", postedBody)

	var out studio.GHCommentOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	require.True(t, out.OK)
	require.Equal(t, "https://github.com/acme/repo/pull/42#issuecomment-2", out.URL)
}

func TestGH_ArgValidation(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	cases := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"pr_view requires number", "gh.pr_view", map[string]any{}},
		{"comment requires number", "gh.comment", map[string]any{"body": "hi"}},
		{"comment requires body", "gh.comment", map[string]any{"number": 5}},
		{"comment rejects bad on", "gh.comment", map[string]any{"number": 5, "body": "hi", "on": "branch"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := callTool(ctx, cs, tc.tool, tc.args)
			assertRejected(t, res, err)
		})
	}
}

// TestGH_ReadOnlyOmitsComment confirms the mutating gh.comment is dropped from a
// read-only server while the read tools stay registered.
func TestGH_ReadOnlyOmitsComment(t *testing.T) {
	ctx := context.Background()
	cs := newStudioReadOnly(ctx, t)

	tools, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["gh.issues"], "read tool gh.issues should be present")
	assert.True(t, names["gh.pr_view"], "read tool gh.pr_view should be present")
	assert.False(t, names["gh.comment"], "mutating gh.comment must be omitted read-only")
	// Spot-check the other families' read/write split too.
	assert.True(t, names["vcs.status"], "read tool vcs.status should be present")
	assert.False(t, names["vcs.integrate"], "mutating vcs.integrate must be omitted read-only")
	assert.True(t, names["trace.read"], "read tool trace.read should be present")
	assert.False(t, names["trace.to_flow"], "mutating trace.to_flow must be omitted read-only")
	assert.False(t, names["story.turn"], "story.turn must be omitted read-only")
}
