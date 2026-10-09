package host_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestGitHubTicket_RegisteredAsBuiltin proves the registry's prefix-fallback
// dispatches every ticket op to the single `host.gh.ticket` registration.
func TestGitHubTicket_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, name := range []string{
		"host.gh.ticket",
		"host.gh.ticket.search",
		"host.gh.ticket.get",
		"host.gh.ticket.comment",
		"host.gh.ticket.comment_edit",
		"host.gh.ticket.transition",
		"host.gh.ticket.list_mine",
	} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("registry: %s missing (prefix-fallback should resolve)", name)
		}
	}
}

func TestGitHubTicket_MissingOp(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing op")
	}
}

func TestGitHubTicket_Search_RequiresRepo(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "search",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when repo missing")
	}
	if !strings.Contains(res.Error, "repo argument is required") {
		t.Fatalf("error should mention repo requirement: %s", res.Error)
	}
}

func TestGitHubTicket_Search_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "30" {
			t.Fatalf("per_page = %q", got)
		}
		q := r.URL.Query().Get("q")
		for _, want := range []string{"repo:o/r", "is:issue", "esc"} {
			if !strings.Contains(q, want) {
				t.Fatalf("query %q missing %q", q, want)
			}
		}
		writeJSON(w, map[string]any{
			"items": []map[string]any{{
				"number":   42,
				"title":    "Esc hangs the TUI",
				"state":    "OPEN",
				"html_url": "https://github.com/o/r/issues/42",
				"assignees": []map[string]any{
					{"login": "brad"},
				},
			}},
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.search must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"query": "esc",
		"repo":  "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 1 {
		t.Fatalf("expected 1, got %d (%v)", len(tickets), res.Data)
	}
	if tickets[0]["id"] != "42" {
		t.Fatalf("id: %v", tickets[0]["id"])
	}
	if tickets[0]["assignee"] != "brad" {
		t.Fatalf("assignee: %v", tickets[0]["assignee"])
	}
	if tickets[0]["status"] != "open" {
		t.Fatalf("status (should be lowercased): %v", tickets[0]["status"])
	}
}

// TestGitHubTicket_Search_NewestFirst proves the search returns issues strict
// id-DESC (newest bug first). GitHub's Search `sort=created` orders by
// created_at, which diverges from the issue number on transferred/imported
// issues — so the handler asks for sort=created (a recency hint) AND re-sorts
// the fetched page by number. The stub returns a deliberately out-of-order page
// (mirroring the real bsacrobatix/Kitsoki tail: …1181, 1178, 1180, 1179) and
// the test asserts the projection is strict highest-number-first.
func TestGitHubTicket_Search_NewestFirst(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("sort"); got != "created" {
			t.Fatalf("sort = %q, want \"created\" (newest-first hint)", got)
		}
		if got := r.URL.Query().Get("order"); got != "desc" {
			t.Fatalf("order = %q, want \"desc\"", got)
		}
		// Out-of-order by number, as GitHub returns for a real repo.
		writeJSON(w, map[string]any{"items": []map[string]any{
			{"number": 1181, "title": "a", "state": "open"},
			{"number": 1178, "title": "b", "state": "open"},
			{"number": 1180, "title": "c", "state": "open"},
			{"number": 1179, "title": "d", "state": "open"},
			{"number": 1202, "title": "e", "state": "open"},
		}})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"query": "",
		"repo":  "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	got := make([]string, 0, len(tickets))
	for _, tk := range tickets {
		got = append(got, tk["id"].(string))
	}
	want := []string{"1202", "1181", "1180", "1179", "1178"}
	if len(got) != len(want) {
		t.Fatalf("got %v tickets %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tickets not newest-first: got %v, want %v", got, want)
		}
	}
}

func TestListGitHubInboxItems_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var seenIssues, seenPRs bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("per_page"); got != "25" {
			t.Fatalf("per_page = %q", got)
		}
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "is:issue"):
			seenIssues = true
			for _, want := range []string{"repo:acme/repo", "is:open", "assignee:@me"} {
				if !strings.Contains(q, want) {
					t.Fatalf("issue query %q missing %q", q, want)
				}
			}
			writeJSON(w, map[string]any{"items": []map[string]any{{
				"number":    7,
				"title":     "Assigned issue",
				"html_url":  "https://github.com/acme/repo/issues/7",
				"assignees": []map[string]any{{"login": "brad"}},
			}}})
		case strings.Contains(q, "is:pr"):
			seenPRs = true
			for _, want := range []string{"repo:acme/repo", "is:open", "review-requested:@me"} {
				if !strings.Contains(q, want) {
					t.Fatalf("pr query %q missing %q", q, want)
				}
			}
			writeJSON(w, map[string]any{"items": []map[string]any{{
				"number":   42,
				"title":    "Review this",
				"html_url": "https://github.com/acme/repo/pull/42",
				"user":     map[string]any{"login": "alice"},
			}}})
		default:
			t.Fatalf("unexpected search query: %q", q)
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		if name == "gh" {
			t.Fatalf("ListGitHubInboxItems must not invoke gh: %s %s", name, strings.Join(args, " "))
		}
		return "", "", 1, nil
	})
	defer restoreExec()

	items, err := host.ListGitHubInboxItems(context.Background(), host.GitHubInboxOptions{
		Repo:          "acme/repo",
		IncludeIssues: true,
		IncludePRs:    true,
		Limit:         25,
	})
	if err != nil {
		t.Fatalf("ListGitHubInboxItems: %v", err)
	}
	if !seenIssues || !seenPRs {
		t.Fatalf("expected issue and PR search calls, seenIssues=%v seenPRs=%v", seenIssues, seenPRs)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d (%+v)", len(items), items)
	}
	if items[0].Kind != "issue" || items[0].Number != "7" || items[0].Author != "brad" {
		t.Fatalf("issue projection: %+v", items[0])
	}
	if items[1].Kind != "pr" || items[1].Number != "42" || items[1].Author != "alice" {
		t.Fatalf("pr projection: %+v", items[1])
	}
}

func TestListGitHubInboxItems_CustomFilters(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["git remote get-url origin"] = fakeResp{stdout: "git@github.com:acme/repo.git\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()
	t.Setenv("GH_TOKEN", "test-token")
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("per_page"); got != "5" {
			t.Fatalf("per_page = %q", got)
		}
		q := r.URL.Query().Get("q")
		for _, want := range []string{"repo:acme/repo", "is:issue", "is:open", "assignee:octo"} {
			if !strings.Contains(q, want) {
				t.Fatalf("query %q missing %q", q, want)
			}
		}
		if strings.Contains(q, "is:pr") {
			t.Fatalf("did not expect PR query: %q", q)
		}
		writeJSON(w, map[string]any{"items": []map[string]any{}})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	items, err := host.ListGitHubInboxItems(context.Background(), host.GitHubInboxOptions{
		IncludeIssues: true,
		Assignee:      "octo",
		Limit:         5,
	})
	if err != nil {
		t.Fatalf("ListGitHubInboxItems: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %+v", items)
	}
	if calls != 1 {
		t.Fatalf("expected one issue search, got %d", calls)
	}
	for _, cmd := range fr.calls {
		if strings.HasPrefix(cmd, "gh ") {
			t.Fatalf("did not expect gh query: %v", fr.calls)
		}
	}
}

func TestGitHubTicket_Search_BadJSON(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"query": "x",
		"repo":  "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for bad JSON")
	}
}

func TestGitHubTicket_Get_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/42":
			writeJSON(w, map[string]any{
				"number":   42,
				"title":    "Esc hangs",
				"body":     "Expected x",
				"state":    "OPEN",
				"html_url": "https://github.com/o/r/issues/42",
				"assignees": []map[string]any{
					{"login": "brad"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/42/comments":
			writeJSON(w, []map[string]any{{"id": 1, "body": "repro"}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.get must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"id":   "42",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["title"] != "Esc hangs" {
		t.Fatalf("title: %v", res.Data["title"])
	}
	if res.Data["body"] != "Expected x" {
		t.Fatalf("body: %v", res.Data["body"])
	}
	if res.Data["url"] != "https://github.com/o/r/issues/42" {
		t.Fatalf("url: %v", res.Data["url"])
	}
	comments, _ := res.Data["comments"].([]any)
	if len(comments) != 1 {
		t.Fatalf("comments: %v", res.Data["comments"])
	}
}

func TestGitHubTicket_Get_PublicReadWithoutToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want none for public read", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105":
			writeJSON(w, map[string]any{
				"number":   105,
				"title":    "Native public issue read",
				"body":     "No token needed for public metadata.",
				"state":    "open",
				"html_url": "https://github.com/o/r/issues/105",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/105/comments":
			writeJSON(w, []map[string]any{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.get must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"id":   "105",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["title"] != "Native public issue read" {
		t.Fatalf("title: %v", res.Data["title"])
	}
	if res.Data["url"] != "https://github.com/o/r/issues/105" {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

// TestGitHubTicket_Search_ClassifiesType proves a GitHub-sourced ticket lands a
// concrete `type` (P3): a `bug` label → "bug", a `feature`/`enhancement` label →
// "feature", an `epic` label → "epic", and an unlabelled issue defaults to "bug"
// (never ""), so dev-story's type-guarded `drive` arc never falls through to its
// no-op self-loop. Every row also carries source="github" for the P5 mapping.
func TestGitHubTicket_Search_ClassifiesType(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		writeJSON(w, map[string]any{
			"items": []map[string]any{
				{"number": 42, "title": "Esc hangs", "state": "OPEN", "html_url": "u42", "labels": []map[string]any{{"name": "bug"}, {"name": "P1"}}},
				{"number": 43, "title": "Add export", "state": "OPEN", "html_url": "u43", "labels": []map[string]any{{"name": "enhancement"}}},
				{"number": 44, "title": "Tracker epic", "state": "OPEN", "html_url": "u44", "labels": []map[string]any{{"name": "epic"}}},
				{"number": 45, "title": "Unlabelled defect", "state": "OPEN", "html_url": "u45", "labels": []map[string]any{}},
			},
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.search must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "search",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	want := map[string]string{"42": "bug", "43": "feature", "44": "epic", "45": "bug"}
	if len(tickets) != len(want) {
		t.Fatalf("expected %d tickets, got %d", len(want), len(tickets))
	}
	for _, tk := range tickets {
		id, _ := tk["id"].(string)
		if got := tk["type"]; got != want[id] {
			t.Errorf("ticket %s type = %v, want %q", id, got, want[id])
		}
		if tk["source"] != "github" {
			t.Errorf("ticket %s source = %v, want github", id, tk["source"])
		}
	}
}

// TestGitHubTicket_Get_SurfacesIdentity proves ticket.get lifts the legacy local
// bug-file id out of the ```kitsoki metadata block to a top-level `legacy_id`
// field, and marks source=github — making the local-file ↔ GitHub-issue mapping
// visible to the ticket view (P5).
func TestGitHubTicket_Get_SurfacesIdentity(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	body := "Esc hangs the TUI.\n\n```kitsoki\nlegacy_id: 2026-06-19T12-00-00Z-esc-hang\nfiled_by: brad\n```\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/19":
			writeJSON(w, map[string]any{
				"number":    19,
				"title":     "Esc hangs",
				"body":      body,
				"state":     "OPEN",
				"html_url":  "https://github.com/o/r/issues/19",
				"labels":    []map[string]any{{"name": "bug"}},
				"assignees": []map[string]any{},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/19/comments":
			writeJSON(w, []map[string]any{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.get must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "get",
		"id":   "19",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["type"] != "bug" {
		t.Fatalf("type: %v (want bug)", res.Data["type"])
	}
	if res.Data["source"] != "github" {
		t.Fatalf("source: %v (want github)", res.Data["source"])
	}
	if res.Data["legacy_id"] != "2026-06-19T12-00-00Z-esc-hang" {
		t.Fatalf("legacy_id should be lifted from the kitsoki metadata block: %v", res.Data["legacy_id"])
	}
	meta, _ := res.Data["kitsoki_meta"].(map[string]any)
	if meta["filed_by"] != "brad" {
		t.Fatalf("kitsoki_meta should still carry the full block: %v", meta)
	}
}

func TestGitHubTicket_Get_RequiresID(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "get",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when id missing")
	}
}

func TestGitHubTicket_Comment_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues/42/comments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		body, _ = payload["body"].(string)
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/42#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.comment must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "comment",
		"id":   "42",
		"repo": "o/r",
		"body": "Repro confirmed.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	if body != "Repro confirmed." {
		t.Fatalf("posted body = %q", body)
	}
	if !strings.Contains(res.Data["comment_id"].(string), "issuecomment") || res.Data["url"] != res.Data["comment_id"] {
		t.Fatalf("comment URL fields: %v", res.Data)
	}
}

func TestGitHubTicket_Comment_AcceptsIssueURL(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	posted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/bsacrobatix/Kitsoki/issues/117/comments" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		posted = true
		writeJSON(w, map[string]any{"html_url": "https://github.com/bsacrobatix/Kitsoki/issues/117#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.comment must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "comment",
		"id":   "https://github.com/bsacrobatix/Kitsoki/issues/117",
		"body": "Fixed in abc123.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if !posted {
		t.Fatal("expected issue URL to provide repo + number")
	}
}

func TestGitHubTicket_CommentUsesContextToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/42#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	ctx := host.WithCLIExecEnv(context.Background(), map[string]string{
		"GH_TOKEN":     "app-token",
		"GITHUB_TOKEN": "app-token",
	})
	res, err := host.GitHubTicketHandler(ctx, map[string]any{
		"op":   "comment",
		"id":   "42",
		"repo": "o/r",
		"body": "Repro confirmed.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if auth != "Bearer app-token" {
		t.Fatalf("Authorization = %q, want bearer app-token", auth)
	}
}

func TestGitHubTicket_Comment_BodyRequired(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "comment",
		"id": "42",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when body missing")
	}
}

func TestGitHubTicket_CommentEdit_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/comments/1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		body, _ = payload["body"].(string)
		writeJSON(w, map[string]any{"html_url": "https://github.com/o/r/issues/42#issuecomment-1"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.comment_edit must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":         "comment_edit",
		"comment_id": "https://github.com/o/r/issues/42#issuecomment-1",
		"body":       "Updated status.",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	if got := res.Data["comment_id"]; got != "https://github.com/o/r/issues/42#issuecomment-1" {
		t.Fatalf("comment_id: %v", got)
	}
	if body != "Updated status." {
		t.Fatalf("patched body = %q", body)
	}
}

// comment_reactions (WS-C C4 gh-agent surface): reads the reactions on the
// gh-agent's ack comment via the native GitHub reactions endpoint, and
// precomputes the has_thumbsdown/has_thumbsup convenience flags.
func TestGitHubTicket_CommentReactions_DetectsThumbsdown(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/o/r/issues/comments/1/reactions" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		writeJSON(w, []map[string]any{
			{"id": 1, "content": "+1", "user": map[string]any{"login": "alice"}},
			{"id": 2, "content": "-1", "user": map[string]any{"login": "bob"}},
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":         "comment_reactions",
		"comment_id": "https://github.com/o/r/issues/42#issuecomment-1",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["has_thumbsdown"] != true {
		t.Fatalf("has_thumbsdown: %v", res.Data["has_thumbsdown"])
	}
	if res.Data["has_thumbsup"] != true {
		t.Fatalf("has_thumbsup: %v", res.Data["has_thumbsup"])
	}
	reactions, _ := res.Data["reactions"].([]any)
	if len(reactions) != 2 {
		t.Fatalf("reactions: got %d, want 2", len(reactions))
	}
}

func TestGitHubTicket_CommentReactions_NoDissatisfaction(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]any{{"id": 1, "content": "+1"}})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":         "comment_reactions",
		"comment_id": "1",
		"repo":       "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["has_thumbsdown"] != false {
		t.Fatalf("has_thumbsdown: %v", res.Data["has_thumbsdown"])
	}
}

func TestGitHubTicket_CommentReactions_CommentIDRequired(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "comment_reactions",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when comment_id missing")
	}
}

func TestGitHubTicket_Transition_CloseHappy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var state string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/42" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		state, _ = payload["state"].(string)
		writeJSON(w, map[string]any{"state": state})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.transition must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "transition",
		"repo": "o/r",
		"id":   "42",
		"to":   "resolved",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	if state != "closed" || res.Data["status"] != "closed" {
		t.Fatalf("state/status = %q/%v, want closed", state, res.Data["status"])
	}
}

func TestGitHubTicket_Transition_AcceptsIssueURL(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	transitioned := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/bsacrobatix/Kitsoki/issues/117" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		transitioned = true
		writeJSON(w, map[string]any{"state": "closed"})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.transition must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "transition",
		"id": "https://github.com/bsacrobatix/Kitsoki/issues/117",
		"to": "resolved",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if !transitioned {
		t.Fatal("expected issue URL to provide repo + number")
	}
}

func TestGitHubTicket_Transition_ReopenHappy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var state string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/42" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		state, _ = payload["state"].(string)
		writeJSON(w, map[string]any{"state": state})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.transition must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "transition",
		"repo": "o/r",
		"id":   "42",
		"to":   "open",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if state != "open" || res.Data["status"] != "open" {
		t.Fatalf("state/status = %q/%v, want open", state, res.Data["status"])
	}
}

func TestGitHubTicket_Transition_RequiresTo(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "transition",
		"id": "42",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when to missing")
	}
}

func TestGitHubTicket_ListMine_DefaultsToMe(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		q := r.URL.Query().Get("q")
		for _, want := range []string{"repo:o/r", "is:issue", "is:open", "assignee:@me"} {
			if !strings.Contains(q, want) {
				t.Fatalf("query %q missing %q", q, want)
			}
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Fatalf("per_page = %q", got)
		}
		writeJSON(w, map[string]any{
			"items": []map[string]any{
				{"number": 1, "title": "One", "state": "OPEN", "html_url": "u1", "assignees": []map[string]any{}},
				{"number": 2, "title": "Two", "state": "OPEN", "html_url": "u2", "assignees": []map[string]any{}},
			},
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		t.Errorf("ticket.list_mine must use native GitHub APIs, got exec: %s %s", name, strings.Join(args, " "))
		return "", "", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "list_mine",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 2 {
		t.Fatalf("expected 2, got %d (%v)", len(tickets), tickets)
	}
}

func TestGitHubTicket_ListMine_ErrorPropagates(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"token expired"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":   "list_mine",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when GitHub API rejects the request")
	}
	if !strings.Contains(res.Error, "token expired") {
		t.Fatalf("error should propagate API message: %s", res.Error)
	}
}

func TestGitHubTicket_UnknownOpRejected(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "smoke",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for unknown op")
	}
}

func TestGitHubTicket_ResolveRemoteRepo(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "repo:bsacrobatix/Kitsoki") {
			t.Fatalf("expected query to resolve remote to repo:bsacrobatix/Kitsoki, query was: %q", q)
		}
		writeJSON(w, map[string]any{
			"items": []map[string]any{},
		})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	restoreExec := host.SetExecRunnerForTest(func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		if name == "git" && len(args) == 3 && args[0] == "remote" && args[1] == "get-url" && args[2] == "origin" {
			return "https://github.com/bsacrobatix/Kitsoki.git\n", "", 0, nil
		}
		return "", "unknown command", 1, nil
	})
	defer restoreExec()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"query": "test",
		"repo":  "origin",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	if res.Data["source_repo"] != "bsacrobatix/Kitsoki" || res.Data["source_label"] != "bsacrobatix/Kitsoki" {
		t.Fatalf("resolved source identity = repo %v label %v, want bsacrobatix/Kitsoki", res.Data["source_repo"], res.Data["source_label"])
	}
}
