package host_test

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
)

// TestGitHubTicket_RegisteredCreate proves the prefix-fallback dispatches the
// new create op like the other five.
func TestGitHubTicket_RegisteredCreate(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.gh.ticket.create"); !ok {
		t.Fatal("registry: host.gh.ticket.create missing (prefix-fallback should resolve)")
	}
}

func TestGitHubTicket_Create_RequiresTitle(t *testing.T) {
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "create",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error when title missing")
	}
}

func TestGitHubTicket_Create_Happy(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/constructorfabric/Kitsoki/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":77,"html_url":"https://github.com/constructorfabric/Kitsoki/issues/77"}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":          "create",
		"repo":        "constructorfabric/Kitsoki",
		"title":       "Esc hangs the TUI",
		"body":        "Pressing Esc twice hangs the input loop.",
		"severity":    "P1",
		"component":   "tui",
		"target":      "kitsoki",
		"status":      "in_progress",
		"trace_ref":   "trace://abc123",
		"kitsoki_rev": "deadbeef",
		"filed_by":    "brad",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["id"] != "77" || res.Data["number"] != "77" {
		t.Fatalf("issue number parse: id=%v number=%v", res.Data["id"], res.Data["number"])
	}
	if res.Data["url"] != "https://github.com/constructorfabric/Kitsoki/issues/77" {
		t.Fatalf("url: %v", res.Data["url"])
	}

	labels, _ := payload["labels"].([]any)
	gotLabels := strings.Join(anyStrings(labels), ",")
	for _, want := range []string{"P1", "comp:tui", "target:kitsoki", "in_progress"} {
		if !strings.Contains(gotLabels, want) {
			t.Errorf("create payload labels missing %q: %v", want, labels)
		}
	}
	// The ```kitsoki body-metadata block carries the GitHub-homeless fields.
	body, _ := payload["body"].(string)
	for _, want := range []string{"```kitsoki", "trace_ref: trace://abc123", "kitsoki_rev: deadbeef", "filed_by: brad"} {
		if !strings.Contains(body, want) {
			t.Errorf("create body missing %q\n  got: %s", want, body)
		}
	}
}

func TestGitHubTicket_CreateUsesSecretsFileWithoutProcessEnv(t *testing.T) {
	unsetEnvForTest(t, "GH_TOKEN")
	unsetEnvForTest(t, "GITHUB_TOKEN")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".kitsoki", "secrets.yaml"), []byte("GH_TOKEN: file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/constructorfabric/Kitsoki/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer file-token" {
			t.Fatalf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":77,"html_url":"https://github.com/constructorfabric/Kitsoki/issues/77"}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "create",
		"repo":  "constructorfabric/Kitsoki",
		"title": "Report bug cannot file",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["url"] != "https://github.com/constructorfabric/Kitsoki/issues/77" {
		t.Fatalf("url: %v", res.Data["url"])
	}
}

func TestGitHubTicket_CreateMissingAuthExplainsSetup(t *testing.T) {
	unsetEnvForTest(t, "GH_TOKEN")
	unsetEnvForTest(t, "GITHUB_TOKEN")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":    "create",
		"repo":  "constructorfabric/Kitsoki",
		"title": "Report bug cannot file",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected missing-auth domain error")
	}
	for _, want := range []string{
		"GitHub auth is not configured",
		"kitsoki gh-agent login",
		"kitsoki gh-agent setup app --name <app-name> --local-only",
		"kitsoki gh-agent token",
		"GH_TOKEN/GITHUB_TOKEN",
	} {
		if !strings.Contains(res.Error, want) {
			t.Fatalf("missing auth hint %q in:\n%s", want, res.Error)
		}
	}
}

func TestGitHubWriteAuthStatusReportsMissingAndConfigured(t *testing.T) {
	unsetEnvForTest(t, "GH_TOKEN")
	unsetEnvForTest(t, "GITHUB_TOKEN")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()

	missing := host.GitHubWriteAuthStatus(context.Background())
	if missing.Configured {
		t.Fatal("expected missing GitHub auth")
	}
	if !strings.Contains(missing.SetupHint, "GitHub auth is not configured") {
		t.Fatalf("missing setup hint: %#v", missing)
	}

	t.Setenv("GH_TOKEN", "test-token")
	configured := host.GitHubWriteAuthStatus(context.Background())
	if !configured.Configured {
		t.Fatal("expected configured GitHub auth from GH_TOKEN")
	}
	if configured.SetupHint != "" {
		t.Fatalf("configured status should not carry setup hint: %#v", configured)
	}
}

// TestGitHubTicket_Create_LabelPermissionDegrades proves a fork contributor
// without triage still files the issue (unlabelled) with a warning, rather than
// failing the create.
func TestGitHubTicket_Create_LabelPermissionDegrades(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	var labelled, unlabelled, labelEnsure bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/labels") {
			labelEnsure = true
			http.Error(w, `{"message":"Resource not accessible by integration (label)"}`, http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/repos/constructorfabric/Kitsoki/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, hasLabels := payload["labels"]; hasLabels {
			labelled = true
			http.Error(w, `{"message":"could not add label: you must have triage permission (HTTP 403)"}`, http.StatusForbidden)
			return
		}
		unlabelled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":88,"html_url":"https://github.com/constructorfabric/Kitsoki/issues/88"}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op":       "create",
		"repo":     "constructorfabric/Kitsoki",
		"title":    "A fork contributor's bug",
		"severity": "P2",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("create should degrade, not fail: %s", res.Error)
	}
	if !labelled || !unlabelled || !labelEnsure {
		t.Fatalf("expected labelled attempt, label ensure, and unlabelled retry (labelled=%v ensure=%v unlabelled=%v)", labelled, labelEnsure, unlabelled)
	}
	if res.Data["id"] != "88" {
		t.Fatalf("issue number: %v", res.Data["id"])
	}
	if w, _ := res.Data["warning"].(string); w == "" {
		t.Fatal("expected a warning that labels were dropped")
	}
}

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// TestGitHubTicket_Get_ParsesMetadata proves get() recovers the create-written
// ```kitsoki block (the round-trip slice #4's migration relies on).
func TestGitHubTicket_Get_ParsesMetadata(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	body := "Pressing Esc hangs.\n\n```kitsoki\ntrace_ref: trace://abc123\nlegacy_id: 2026-05-14T103205Z-tui-hang\n```\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/88":
			writeJSON(w, map[string]any{
				"number":   88,
				"title":    "Esc hangs",
				"body":     body,
				"state":    "OPEN",
				"html_url": "https://github.com/o/r/issues/88",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/issues/88/comments":
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
		"id":   "88",
		"repo": "o/r",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	meta, ok := res.Data["kitsoki_meta"].(map[string]any)
	if !ok {
		t.Fatalf("kitsoki_meta not parsed: %v", res.Data)
	}
	if meta["trace_ref"] != "trace://abc123" {
		t.Errorf("trace_ref: %v", meta["trace_ref"])
	}
	if meta["legacy_id"] != "2026-05-14T103205Z-tui-hang" {
		t.Errorf("legacy_id: %v", meta["legacy_id"])
	}
}
