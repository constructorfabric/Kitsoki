package host_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestLocalGitHubTicket_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, name := range []string{
		"host.local_github.ticket",
		"host.local_github.ticket.search",
		"host.local_github.ticket.get",
		"host.local_github.ticket.comment",
		"host.local_github.ticket.transition",
		"host.local_github.ticket.list_mine",
	} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("registry: %s missing (prefix-fallback should resolve)", name)
		}
	}
}

func TestLocalGitHubTicket_SearchCombinesSections(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	root := seedTicketsRoot(t, map[string]string{
		"2026-07-08T10-local-one.md": sampleBug,
		"2026-07-08T09-local-two.md": strings.Replace(sampleBug, "Esc in foyer hangs the TUI", "Other local bug", 1),
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.URL.Query().Get("per_page"); got != "1" {
			t.Fatalf("per_page = %q, want 1", got)
		}
		writeJSON(w, map[string]any{"items": []map[string]any{{
			"number":   205,
			"title":    "User-submitted crash",
			"state":    "open",
			"html_url": "https://github.com/o/r/issues/205",
		}}})
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	res, err := host.LocalGitHubTicketHandler(context.Background(), map[string]any{
		"op":    "search",
		"root":  root,
		"repo":  "o/r",
		"limit": 1,
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 2 {
		t.Fatalf("tickets = %d (%v), want one local + one GitHub", len(tickets), res.Data)
	}
	if tickets[0]["source"] != "local" || tickets[1]["source"] != "github" {
		t.Fatalf("sources = %v, %v; want local then github", tickets[0]["source"], tickets[1]["source"])
	}
	if res.Data["local_count"] != 1 || res.Data["github_count"] != 1 {
		t.Fatalf("counts = local %v github %v", res.Data["local_count"], res.Data["github_count"])
	}
}

func TestLocalGitHubTicket_GetRoutesBySource(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	root := seedTicketsRoot(t, map[string]string{
		"2026-07-08T10-local-one.md": sampleBug,
	})
	var issueRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/205":
			issueRequests++
			writeJSON(w, map[string]any{
				"number":   205,
				"title":    "User-submitted crash",
				"state":    "open",
				"body":     "Remote body",
				"html_url": "https://github.com/o/r/issues/205",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/205/comments":
			writeJSON(w, []map[string]any{})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	localRes, err := host.LocalGitHubTicketHandler(context.Background(), map[string]any{
		"op":     "get",
		"root":   root,
		"repo":   "o/r",
		"source": "local",
		"id":     "2026-07-08T10-local-one",
	})
	if err != nil || localRes.Error != "" {
		t.Fatalf("local get: infra=%v domain=%s", err, localRes.Error)
	}
	if localRes.Data["source"] != "local" {
		t.Fatalf("local source = %v", localRes.Data["source"])
	}
	if issueRequests != 0 {
		t.Fatalf("local source should not hit GitHub, saw %d issue request(s)", issueRequests)
	}

	remoteRes, err := host.LocalGitHubTicketHandler(context.Background(), map[string]any{
		"op":     "get",
		"root":   root,
		"repo":   "o/r",
		"source": "github",
		"id":     "205",
	})
	if err != nil || remoteRes.Error != "" {
		t.Fatalf("github get: infra=%v domain=%s", err, remoteRes.Error)
	}
	if remoteRes.Data["source"] != "github" || remoteRes.Data["title"] != "User-submitted crash" {
		t.Fatalf("remote data = %v", remoteRes.Data)
	}
}
