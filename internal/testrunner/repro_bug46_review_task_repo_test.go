package testrunner_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/testrunner"
)

func TestBug46ReviewTaskFetchesGitHubIssueFromTicketRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("TestBug46ReviewTaskFetchesGitHubIssueFromTicketRepo: skipped under -short (full story run)")
	}

	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/constructorfabric/Kitsoki/issues/46/comments" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/repos/constructorfabric/Kitsoki/issues/46", r.URL.Path)
		title := "WRONG-REPO-ISSUE-46"
		body := "Issue #46 from the cwd fallback repository."
		url := "https://github.com/wrong/repo/issues/46"
		title = "Correct Kitsoki feature issue"
		body = "Issue #46 from constructorfabric/Kitsoki."
		url = "https://github.com/constructorfabric/Kitsoki/issues/46"

		payload, err := json.Marshal(map[string]any{
			"number":    float64(46),
			"title":     title,
			"body":      body,
			"state":     "OPEN",
			"labels":    []any{},
			"assignees": []any{},
			"url":       url,
			"comments":  []any{},
		})
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	dir := t.TempDir()
	flowPath := filepath.Join(dir, "bug46_review_task_repo.yaml")
	fixture := `test_kind: flow
app: ` + repoStoriesImplementationAppPath(t) + `
initial_state: idle
initial_world:
  ticket_id:     "46"
  ticket_repo:   "constructorfabric/Kitsoki"
  ticket_title:  "Inbox title before fetch"
  ticket_url:    "https://github.com/constructorfabric/Kitsoki/issues/46"
  thread:        "46"
  workdir:       ".worktrees/bug46"
  judge_mode:    "human"

host_bindings:
  ticket: host.gh.ticket

host_handlers:
  host.append_to_file:
    data: { ok: true, message_id: "msg-1" }
  host.inbox.add:
    data: { ok: true, id: "ib-1" }
  host.artifacts_dir:
    data: { ok: true, path: ".artifacts/bug46-review-task.md", message_id: "artifact-1" }
  host.agent.decide:
    data:
      ok: true
      submitted:
        summary_title:    "Stub task summary"
        summary_markdown: "Stub summary."
        scope:            "Stub scope."
        acceptance_criteria: ["stub criterion"]

turns:
  - intent: { name: start, slots: {} }
    expect_state: review_task_awaiting_reply
    expect_world:
      ticket_title: "Correct Kitsoki feature issue"

expect_no_errors: true
`
	require.NoError(t, os.WriteFile(flowPath, []byte(fixture), 0o644))

	report, err := testrunner.RunFlows(t.Context(), repoStoriesImplementationAppPath(t), flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "turn failures: %+v", report.Results[0].Turns)
}

func repoStoriesImplementationAppPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../stories/implementation/app.yaml")
	require.NoError(t, err)
	return abs
}
