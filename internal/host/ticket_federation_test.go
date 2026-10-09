package host_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/host"
)

func federationSource(id, label, provider, kind, mode string, args map[string]any) map[string]any {
	return map[string]any{
		"id": id, "label": label, "provider": provider,
		"kind": kind, "mode": mode, "args": args,
	}
}

func TestTicketFederation_ListPreservesSourcesAndDeduplicatesOnlyMigratedLocal(t *testing.T) {
	reg := host.NewRegistry()
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		if args["root"] == "local-root" {
			return host.Result{Data: map[string]any{"tickets": []map[string]any{
				{"id": "L-1", "title": "migrated local"},
				{"id": "L-2", "title": "local only"},
			}}}, nil
		}
		repo, _ := args["repo"].(string)
		switch repo {
		case "fork/project":
			return host.Result{Data: map[string]any{
				"repo":    "fork/project",
				"tickets": []map[string]any{{"id": "7", "title": "fork", "legacy_id": "L-1"}},
			}}, nil
		case "upstream/project":
			return host.Result{Data: map[string]any{
				"repo":    "upstream/project",
				"tickets": []map[string]any{{"id": "7", "title": "upstream"}},
			}}, nil
		default:
			return host.Result{Error: "unexpected repo"}, nil
		}
	})
	handler := host.TicketFederationHandler(reg)
	sources := []any{
		federationSource("local", "Local", "host.test.ticket", "local", "local", map[string]any{"root": "local-root"}),
		federationSource("origin", "Fork", "host.test.ticket", "github", "remote", map[string]any{"repo": "fork/project"}),
		federationSource("upstream", "Upstream", "host.test.ticket", "github", "remote", map[string]any{"repo": "upstream/project"}),
	}

	res, err := handler(context.Background(), map[string]any{"op": "search", "sources": sources, "repo": "ambient/wrong"})
	if err != nil || res.Error != "" {
		t.Fatalf("search: infra=%v domain=%s", err, res.Error)
	}
	tickets, _ := res.Data["tickets"].([]map[string]any)
	if len(tickets) != 3 {
		t.Fatalf("tickets = %#v, want local-only plus both repo-local issue 7 rows", tickets)
	}
	want := []struct{ id, source, ref string }{
		{"L-2", "local", "local:L-2"},
		{"7", "origin", "origin:7"},
		{"7", "upstream", "upstream:7"},
	}
	for i, w := range want {
		row := tickets[i]
		if row["id"] != w.id || row["source"] != w.source || row["source_id"] != w.source || row["ref"] != w.ref {
			t.Fatalf("ticket[%d] = %#v, want id/source/ref %s/%s/%s", i, row, w.id, w.source, w.ref)
		}
		if row["index"] != i+1 {
			t.Fatalf("ticket[%d].index = %v, want %d", i, row["index"], i+1)
		}
	}
	groups, _ := res.Data["source_groups"].([]map[string]any)
	if len(groups) != 3 || groups[0]["count"] != 1 || groups[1]["count"] != 1 || groups[2]["count"] != 1 {
		t.Fatalf("source_groups = %#v", groups)
	}
	if groups[0]["repo"] != "" || tickets[0]["source_repo"] != "" {
		t.Fatalf("ambient remote repo leaked into local source: group=%#v ticket=%#v", groups[0], tickets[0])
	}
	if groups[1]["repo"] != "fork/project" || groups[2]["repo"] != "upstream/project" {
		t.Fatalf("resolved group repos = %#v", groups)
	}
	if res.Data["ticket_local_count"] != 1 || res.Data["ticket_github_count"] != 2 {
		t.Fatalf("compat counts = local %v github %v", res.Data["ticket_local_count"], res.Data["ticket_github_count"])
	}
}

func TestTicketFederation_ListKeepsHealthySourcesAndSurfacesProviderProblems(t *testing.T) {
	reg := host.NewRegistry()
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		switch fmt.Sprint(args["repo"]) {
		case "healthy/project":
			return host.Result{Data: map[string]any{
				"tickets":         []map[string]any{{"id": "1", "title": "healthy"}},
				"provider_errors": []string{"one malformed candidate was skipped"},
			}}, nil
		case "down/project":
			return host.Result{Error: "upstream unavailable"}, nil
		default:
			return host.Result{}, nil
		}
	})
	handler := host.TicketFederationHandler(reg)
	res, err := handler(context.Background(), map[string]any{
		"op": "search",
		"sources": []any{
			federationSource("healthy", "Healthy", "host.test.ticket", "github", "remote", map[string]any{"repo": "healthy/project"}),
			federationSource("down", "Downstream", "host.test.ticket", "github", "remote", map[string]any{"repo": "down/project"}),
		},
	})
	if err != nil || res.Error != "" {
		t.Fatalf("search: infra=%v domain=%s", err, res.Error)
	}
	if tickets := res.Data["tickets"].([]map[string]any); len(tickets) != 1 {
		t.Fatalf("tickets = %#v, want healthy source retained", tickets)
	}
	errs, _ := res.Data["provider_errors"].([]string)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"Healthy: one malformed candidate was skipped", "Downstream: upstream unavailable"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("provider_errors = %#v, missing %q", errs, want)
		}
	}
	groups := res.Data["source_groups"].([]map[string]any)
	if groups[0]["warnings"] == nil || groups[1]["error"] != "upstream unavailable" {
		t.Fatalf("source group diagnostics = %#v", groups)
	}
}

func TestTicketFederation_MigrationDedupeIsScopedToOneLocalSource(t *testing.T) {
	reg := host.NewRegistry()
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		switch fmt.Sprint(args["root"]) {
		case "a", "b":
			return host.Result{Data: map[string]any{"tickets": []map[string]any{{"id": "same", "title": fmt.Sprint(args["root"])}}}}, nil
		default:
			return host.Result{Data: map[string]any{"tickets": []map[string]any{{"id": "9", "legacy_id": "same", "legacy_source": "local-a"}}}}, nil
		}
	})
	handler := host.TicketFederationHandler(reg)
	res, err := handler(context.Background(), map[string]any{"op": "search", "sources": []any{
		federationSource("local-a", "Local A", "host.test.ticket", "local", "local", map[string]any{"root": "a"}),
		federationSource("local-b", "Local B", "host.test.ticket", "local", "local", map[string]any{"root": "b"}),
		federationSource("remote", "Remote", "host.test.ticket", "github", "remote", map[string]any{"repo": "remote/project"}),
	}})
	if err != nil || res.Error != "" {
		t.Fatalf("search: infra=%v domain=%s", err, res.Error)
	}
	rows := res.Data["tickets"].([]map[string]any)
	if len(rows) != 2 || rows[0]["source"] != "local-b" || rows[1]["source"] != "remote" {
		t.Fatalf("source-scoped migration rows = %#v, want Local B plus remote", rows)
	}
}

func TestTicketFederation_ExactRoutingUsesSourceIdentityAndSafeArgumentMerge(t *testing.T) {
	reg := host.NewRegistry()
	var mu sync.Mutex
	var calls []map[string]any
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		mu.Lock()
		calls = append(calls, copyAnyMap(args))
		mu.Unlock()
		// Mutation providers commonly return only an acknowledgement. Federation
		// must restore the routed identity instead of emitting source:<nil>.
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})
	handler := host.TicketFederationHandler(reg)
	sources := []any{
		federationSource("origin", "Fork", "host.test.ticket", "github", "remote", map[string]any{"repo": "fork/project"}),
		federationSource("upstream", "Upstream", "host.test.ticket", "github", "remote", map[string]any{
			"repo": "upstream/project", "op": "transition", "body": "configured body must not win", "message": "configured message must not win",
		}),
	}

	res, err := handler(context.Background(), map[string]any{
		"op": "comment", "sources": sources, "ref": "upstream:7", "source": "origin",
		"repo": "runtime/wrong", "body": "operator body", "message": "operator message",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("comment: infra=%v domain=%s", err, res.Error)
	}
	if res.Data["source"] != "upstream" || res.Data["ref"] != "upstream:7" {
		t.Fatalf("annotated result = %#v", res.Data)
	}
	mu.Lock()
	call := calls[len(calls)-1]
	mu.Unlock()
	if call["op"] != "comment" || call["id"] != "7" || call["repo"] != "upstream/project" || call["body"] != "operator body" || call["message"] != "operator message" {
		t.Fatalf("nested args = %#v", call)
	}
	for _, control := range []string{"sources", "source", "source_id", "ref"} {
		if _, found := call[control]; found {
			t.Fatalf("nested args leaked federation control %q: %#v", control, call)
		}
	}
}

func TestTicketFederation_FutureProviderDeclaresSourceOwnedLocatorKeys(t *testing.T) {
	reg := host.NewRegistry()
	reg.RegisterTicketProvider("host.acme.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		project := fmt.Sprint(args["project"])
		return host.Result{Data: map[string]any{"tickets": []map[string]any{{"id": project, "title": project}}}}, nil
	})
	first := federationSource("eng-a", "Engineering A", "host.acme.ticket", "acme", "remote", map[string]any{"project": "ENG-A"})
	first["locator_keys"] = []any{"project"}
	second := federationSource("eng-b", "Engineering B", "host.acme.ticket", "acme", "remote", map[string]any{"project": "ENG-B"})
	second["locator_keys"] = []any{"project"}
	res, err := host.TicketFederationHandler(reg)(context.Background(), map[string]any{
		"op": "search", "sources": []any{first, second}, "project": "AMBIENT-WRONG",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("search: infra=%v domain=%s", err, res.Error)
	}
	rows := res.Data["tickets"].([]map[string]any)
	if len(rows) != 2 || rows[0]["id"] != "ENG-A" || rows[1]["id"] != "ENG-B" {
		t.Fatalf("future-provider locators were not source-owned: %#v", rows)
	}
}

func TestTicketFederation_ExactIDBeatsKindAliasAndRepoSelectsLegacyCaller(t *testing.T) {
	reg := host.NewRegistry()
	var gotRepo string
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		gotRepo = fmt.Sprint(args["repo"])
		return host.Result{Data: map[string]any{"id": fmt.Sprint(args["id"])}}, nil
	})
	handler := host.TicketFederationHandler(reg)
	sources := []any{
		federationSource("github", "Fork", "host.test.ticket", "github", "remote", map[string]any{"repo": "fork/project"}),
		federationSource("upstream", "Upstream", "host.test.ticket", "github", "remote", map[string]any{"repo": "upstream/project"}),
	}

	res, err := handler(context.Background(), map[string]any{"op": "get", "sources": sources, "source": "github", "id": "7"})
	if err != nil || res.Error != "" || res.Data["source"] != "github" || gotRepo != "fork/project" {
		t.Fatalf("exact id route = data %#v repo %q, infra=%v domain=%s", res.Data, gotRepo, err, res.Error)
	}
	res, err = handler(context.Background(), map[string]any{"op": "get", "sources": sources, "repo": "upstream/project", "id": "7"})
	if err != nil || res.Error != "" || res.Data["source"] != "upstream" || gotRepo != "upstream/project" {
		t.Fatalf("repo route = data %#v repo %q, infra=%v domain=%s", res.Data, gotRepo, err, res.Error)
	}
}

func TestTicketFederation_RawGitHubRefInfersRepository(t *testing.T) {
	reg := host.NewRegistry()
	var gotID string
	reg.RegisterTicketProvider("host.test.ticket", func(_ context.Context, args map[string]any) (host.Result, error) {
		gotID = fmt.Sprint(args["id"])
		return host.Result{Data: map[string]any{"id": "77", "title": "found"}}, nil
	})
	handler := host.TicketFederationHandler(reg)
	sources := []any{
		federationSource("origin", "Fork", "host.test.ticket", "github", "remote", map[string]any{"repo": "fork/project"}),
		federationSource("upstream", "Upstream", "host.test.ticket", "github", "remote", map[string]any{"repo": "upstream/project"}),
	}
	for _, ref := range []string{
		"https://github.com/upstream/project/issues/77",
		"upstream/project#77",
	} {
		res, err := handler(context.Background(), map[string]any{"op": "get", "sources": sources, "ref": ref, "repo": "fork/project"})
		if err != nil || res.Error != "" {
			t.Fatalf("get ref %q: infra=%v domain=%s", ref, err, res.Error)
		}
		if gotID != ref {
			t.Fatalf("nested id = %q, want raw ref %q for provider parsing", gotID, ref)
		}
		if res.Data["source"] != "upstream" || res.Data["ref"] != "upstream:77" {
			t.Fatalf("result for %q = %#v", ref, res.Data)
		}
	}
}

func TestTicketFederation_RejectsAmbiguousAndUntrustedRouting(t *testing.T) {
	reg := host.NewRegistry()
	reg.Register("host.not_a_ticket_provider", func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
	reg.RegisterTicketProvider("host.test.ticket", func(context.Context, map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"id": "7"}}, nil
	})
	handler := host.TicketFederationHandler(reg)
	two := []any{
		federationSource("one", "One", "host.test.ticket", "github", "remote", map[string]any{"repo": "one/project"}),
		federationSource("two", "Two", "host.test.ticket", "github", "remote", map[string]any{"repo": "two/project"}),
	}
	for name, args := range map[string]map[string]any{
		"bare duplicate id": {"op": "get", "sources": two, "id": "7"},
		"unknown ref":       {"op": "get", "sources": two, "ref": "missing:7"},
		"arbitrary handler": {"op": "search", "sources": []any{federationSource("unsafe", "Unsafe", "host.not_a_ticket_provider", "local", "local", nil)}},
		"recursive":         {"op": "search", "sources": []any{federationSource("loop", "Loop", "host.ticket_federation", "local", "local", nil)}},
		"duplicate labels": {"op": "search", "sources": []any{
			federationSource("one", "Same", "host.test.ticket", "local", "local", nil),
			federationSource("two", "same", "host.test.ticket", "github", "remote", nil),
		}},
		"payload locator": {"op": "search", "sources": []any{
			func() map[string]any {
				source := federationSource("unsafe", "Unsafe", "host.test.ticket", "local", "local", map[string]any{"body": "configured"})
				source["locator_keys"] = []any{"body"}
				return source
			}(),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := handler(context.Background(), args)
			if err != nil {
				t.Fatalf("infra: %v", err)
			}
			if res.Error == "" {
				t.Fatalf("expected safe rejection, got %#v", res.Data)
			}
		})
	}
}

func copyAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
