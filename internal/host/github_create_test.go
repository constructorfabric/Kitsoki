package host_test

import (
	"context"
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
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{stdout: "gh version 2.x\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

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
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{stdout: "gh version 2.x\n"}
	// gh issue create prints the new issue URL; the body+labels vary so let the
	// default response carry the URL and assert on the recorded argv.
	fr.defaultResp = fakeResp{stdout: "https://github.com/constructorfabric/Kitsoki/issues/77\n"}
	restore := host.SetExecRunnerForTest(fr.run)
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

	// Find the create invocation and assert the conventions are on the wire.
	var create string
	for _, c := range fr.calls {
		if strings.HasPrefix(c, "gh issue create") {
			create = c
		}
	}
	if create == "" {
		t.Fatal("no `gh issue create` call recorded")
	}
	for _, want := range []string{
		"--repo constructorfabric/Kitsoki",
		"--label P1",
		"--label comp:tui",
		"--label target:kitsoki",
		"--label in_progress",
	} {
		if !strings.Contains(create, want) {
			t.Errorf("create argv missing %q\n  got: %s", want, create)
		}
	}
	// The ```kitsoki body-metadata block carries the GitHub-homeless fields.
	for _, want := range []string{"```kitsoki", "trace_ref: trace://abc123", "kitsoki_rev: deadbeef", "filed_by: brad"} {
		if !strings.Contains(create, want) {
			t.Errorf("create body missing %q\n  got: %s", want, create)
		}
	}
}

// TestGitHubTicket_Create_LabelPermissionDegrades proves a fork contributor
// without triage still files the issue (unlabelled) with a warning, rather than
// failing the create.
func TestGitHubTicket_Create_LabelPermissionDegrades(t *testing.T) {
	var labelled, unlabelled bool
	runner := func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		if len(args) > 0 && args[0] == "--version" {
			return "gh version 2.x\n", "", 0, nil
		}
		hasLabel := false
		for _, a := range args {
			if a == "--label" {
				hasLabel = true
			}
		}
		if hasLabel {
			labelled = true
			return "", "could not add label: you must have triage permission (HTTP 403)", 1, nil
		}
		unlabelled = true
		return "https://github.com/constructorfabric/Kitsoki/issues/88\n", "", 0, nil
	}
	restore := host.SetExecRunnerForTest(runner)
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
	if !labelled || !unlabelled {
		t.Fatalf("expected a labelled attempt then an unlabelled retry (labelled=%v unlabelled=%v)", labelled, unlabelled)
	}
	if res.Data["id"] != "88" {
		t.Fatalf("issue number: %v", res.Data["id"])
	}
	if w, _ := res.Data["warning"].(string); w == "" {
		t.Fatal("expected a warning that labels were dropped")
	}
}

// TestGitHubTicket_Get_ParsesMetadata proves get() recovers the create-written
// ```kitsoki block (the round-trip slice #4's migration relies on).
func TestGitHubTicket_Get_ParsesMetadata(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["gh --version"] = fakeResp{stdout: "gh version 2.x\n"}
	body := "Pressing Esc hangs.\n\n```kitsoki\ntrace_ref: trace://abc123\nlegacy_id: 2026-05-14T103205Z-tui-hang\n```\n"
	fr.responses["gh issue view 88"] = fakeResp{
		stdout: `{"number":88,"title":"Esc hangs","body":` + jsonQuote(body) + `,"state":"OPEN","url":"https://github.com/o/r/issues/88","comments":[]}`,
	}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{
		"op": "get",
		"id": "88",
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

// jsonQuote renders s as a JSON string literal (incl. surrounding quotes).
func jsonQuote(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\t", "\\t", "\r", "\\r")
	return "\"" + r.Replace(s) + "\""
}
