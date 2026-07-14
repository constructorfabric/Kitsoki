package opschema

// Builtins returns the registered-schema table for the Go handlers backing
// dev-story's five host_interfaces defaults (ticket/vcs/ci/workspace/
// transport — see internal/app/synthesis.go's DevStoryIfaces). This is a
// hand-maintained mirror of stories/dev-story/app.yaml's host_interfaces
// block, kept in sync the same way DevStoryIfaces is: a drift here is a
// authoring bug in either file, and opschema_test.go cross-checks the two by
// parsing dev-story's app.yaml host_interfaces block directly (not via the
// full loader, to keep the test independent of import resolution).
//
// Scope note (flagged decision, S4): this seed intentionally covers only the
// handlers dev-story's own host_interfaces already document — it is NOT a
// registration of every host.* verb in the engine (~200+ of them). A kit
// author who reuses one of these five handlers under their own
// host_interfaces declaration gets real op-shape checking; a kit that binds
// to any other Go handler gets none yet (CheckInterfaceOpShapes treats an
// unregistered handler as "cannot check", not a failure). Growing this table
// per-handler as kits actually reuse them is left to follow-up work — see
// the S4 PR description.
//
// Deliberate scope expansion (graph-mcp plan P1, Workstream D "verify
// closure"): registerGraphBuiltins below adds every host.graph.* op
// (internal/host/graph_handlers.go + graph_read_ops.go) even though
// host.graph is not one of dev-story's five host_interfaces defaults — the
// object-graph kit (kits/object-graph) is a second real consumer of this
// table, and closing kit verify's op-shape-checking gap for it was the
// explicit P1 deliverable. opschema_test.go's dev-story-specific drift check
// only cross-checks the five dev-story handlers above; it does not (and
// should not) assert anything about host.graph.
func Builtins() *Registry {
	r := NewRegistry()

	// ticket -> host.local_files.ticket
	r.Register("host.local_files.ticket", "search", Op{
		Input:  fields("query", "string", "limit", "int", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})
	r.Register("host.local_files.ticket", "get", Op{
		Input: fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields(
			"id", "string", "title", "string", "body", "string", "status", "string",
			"priority", "string", "assignee", "string", "url", "string", "comments", "list",
			"source", "string", "source_label", "string", "source_kind", "string",
			"source_mode", "string", "source_repo", "string", "ref", "string",
		),
	})
	r.Register("host.local_files.ticket", "comment", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "body", "string", "thread", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.local_files.ticket", "transition", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "to", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.local_files.ticket", "assign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "assignee", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.local_files.ticket", "unassign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.local_files.ticket", "list_mine", Op{
		Input:  fields("filter", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})

	// ticket -> host.local_github.ticket
	r.Register("host.local_github.ticket", "search", Op{
		Input:  fields("query", "string", "limit", "int", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})
	r.Register("host.local_github.ticket", "get", Op{
		Input: fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields(
			"id", "string", "title", "string", "body", "string", "status", "string",
			"priority", "string", "assignee", "string", "url", "string", "comments", "list",
			"source", "string", "source_label", "string", "source_kind", "string",
			"source_mode", "string", "source_repo", "string", "ref", "string",
		),
	})
	r.Register("host.local_github.ticket", "comment", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "body", "string", "thread", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.local_github.ticket", "transition", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "to", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.local_github.ticket", "assign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "assignee", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.local_github.ticket", "unassign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.local_github.ticket", "list_mine", Op{
		Input:  fields("filter", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})

	registerTicketFederationBuiltins(r)

	// vcs -> host.git
	r.Register("host.git", "branch", Op{
		Input:  fields("workdir", "string", "name", "string", "base", "string"),
		Output: fields("ok", "bool", "branch", "string"),
	})
	r.Register("host.git", "diff", Op{
		Input:  fields("workdir", "string"),
		Output: fields("diff", "string", "files", "list"),
	})
	r.Register("host.git", "commit", Op{
		Input:  fields("workdir", "string", "message", "string", "files", "list"),
		Output: fields("ok", "bool", "sha", "string"),
	})
	r.Register("host.git", "push", Op{
		Input:  fields("workdir", "string", "remote", "string"),
		Output: fields("ok", "bool", "url", "string"),
	})
	r.Register("host.git", "open_pr", Op{
		Input:  fields("workdir", "string", "title", "string", "body", "string", "base", "string", "remote", "string", "repo", "string", "head", "string", "draft", "bool"),
		Output: fields("ok", "bool", "outcome", "string", "url", "string", "pr_id", "string", "repo", "string"),
	})
	r.Register("host.git", "split_prs", Op{
		Input:  fields("workdir", "string", "buckets_json", "string", "integration_branch", "string", "source_branch", "string", "remote", "string", "repo", "string", "dry_run", "bool"),
		Output: fields("ok", "bool", "opened_prs", "object"),
	})
	r.Register("host.git", "pr_status", Op{
		Input:  fields("pr_id", "string"),
		Output: fields("state", "string", "checks", "list", "checks_summary", "string", "failed_log", "string", "comments", "list"),
	})
	r.Register("host.git", "pr_comment", Op{
		Input:  fields("pr_id", "string", "body", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.git", "merge", Op{
		Input:  fields("pr_id", "string", "strategy", "string"),
		Output: fields("ok", "bool", "sha", "string"),
	})

	// ci -> host.local
	r.Register("host.local", "run_tests", Op{
		Input:  fields("workdir", "string", "target", "string"),
		Output: fields("ok", "bool", "passed", "int", "failed", "int", "log", "string", "junit", "string"),
	})
	r.Register("host.local", "build", Op{
		Input:  fields("workdir", "string", "target", "string"),
		Output: fields("ok", "bool", "log", "string"),
	})
	r.Register("host.local", "remote_status", Op{
		Input:  fields("pr_id", "string"),
		Output: fields("state", "string", "checks", "list"),
	})

	// workspace -> host.git_worktree
	r.Register("host.git_worktree", "list", Op{
		Input:  fields(),
		Output: fields("workspaces", "list"),
	})
	r.Register("host.git_worktree", "get", Op{
		Input:  fields("id", "string"),
		Output: fields("id", "string", "path", "string", "branch", "string", "dirty", "bool"),
	})
	r.Register("host.git_worktree", "create", Op{
		Input:  fields("id", "string", "name", "string", "base", "string"),
		Output: fields("ok", "bool", "path", "string"),
	})
	r.Register("host.git_worktree", "sync", Op{
		Input:  fields("id", "string"),
		Output: fields("ok", "bool", "log", "string"),
	})
	r.Register("host.git_worktree", "cleanup_scan", Op{
		Input:  fields("base", "string", "exclude", "string", "protected", "string"),
		Output: fields("ok", "bool", "base", "string", "exclude", "string", "candidates", "list", "recommended_count", "int"),
	})
	r.Register("host.git_worktree", "cleanup_apply", Op{
		Input:  fields("candidates", "list"),
		Output: fields("ok", "bool", "deleted", "list", "skipped", "list", "errors", "list"),
	})
	r.Register("host.git_worktree", "clone_create", Op{
		Input:  fields("id", "string", "name", "string", "base", "string", "root", "string", "session_id", "string"),
		Output: fields("ok", "bool", "id", "string", "path", "string", "branch", "string", "root", "string"),
	})
	r.Register("host.git_worktree", "clone_cleanup_scan", Op{
		Input:  fields("root", "string", "min_age_hours", "string", "exclude", "string"),
		Output: fields("ok", "bool", "root", "string", "min_age_hours", "float", "exclude", "string", "candidates", "list", "recommended_count", "int"),
	})
	r.Register("host.git_worktree", "clone_cleanup_apply", Op{
		Input:  fields("root", "string", "candidates", "list"),
		Output: fields("ok", "bool", "deleted", "list", "skipped", "list", "errors", "list"),
	})

	// Capsule-backed workspace contract. It is intentionally separate from the
	// historical branch-argument worktree surface during migration.
	r.Register("host.capsule_workspace", "list", Op{
		Input:  fields(),
		Output: fields("ok", "bool", "workspaces", "list", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "create", Op{
		Input:  fields("id", "string", "definition", "string", "name", "string", "base", "string", "session_id", "string", "owner", "string"),
		Output: fields("ok", "bool", "id", "string", "generation", "int", "path", "string", "branch", "string", "state", "string", "head", "string", "dirty", "bool", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "get", Op{
		Input:  fields("id", "string"),
		Output: fields("ok", "bool", "id", "string", "generation", "int", "path", "string", "branch", "string", "state", "string", "head", "string", "dirty", "bool", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "status", Op{
		Input:  fields("id", "string"),
		Output: fields("ok", "bool", "id", "string", "generation", "int", "path", "string", "branch", "string", "state", "string", "head", "string", "dirty", "bool", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "sync", Op{
		Input:  fields("id", "string"),
		Output: fields("ok", "bool", "id", "string", "generation", "int", "path", "string", "branch", "string", "state", "string", "head", "string", "dirty", "bool", "log", "string", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "commit", Op{
		Input:  fields("id", "string", "message", "string"),
		Output: fields("ok", "bool", "id", "string", "generation", "int", "path", "string", "branch", "string", "state", "string", "head", "string", "dirty", "bool", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "close", Op{
		Input:  fields("id", "string", "owner", "string"),
		Output: fields("ok", "bool", "id", "string", "closed", "bool", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "cleanup_scan", Op{
		Input:  fields("base", "string", "exclude", "string", "protected", "string"),
		Output: fields("ok", "bool", "base", "string", "exclude", "string", "candidates", "list", "recommended_count", "int", "diagnostics", "object"),
	})
	r.Register("host.capsule_workspace", "cleanup_apply", Op{
		Input:  fields("candidates", "list", "owner", "string"),
		Output: fields("ok", "bool", "deleted", "list", "skipped", "list", "errors", "list", "diagnostics", "object"),
	})

	// transport -> host.append_to_file
	r.Register("host.append_to_file", "post", Op{
		Input:  fields("thread", "string", "body", "string"),
		Output: fields("ok", "bool", "message_id", "string"),
	})

	registerGraphBuiltins(r)

	return r
}

// registerTicketFederationBuiltins records the provider-neutral composition
// carrier. Its declared ticket operations intentionally mirror dev-story's
// ticket interface while adding the GitHub-compatible operations already
// supported by the runtime handler for callers outside that story.
func registerTicketFederationBuiltins(r *Registry) {
	r.Register("host.ticket_federation", "search", Op{
		Input:  fields("query", "string", "limit", "int", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})
	r.Register("host.ticket_federation", "get", Op{
		Input: fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields(
			"id", "string", "title", "string", "body", "string", "status", "string",
			"priority", "string", "assignee", "string", "url", "string", "comments", "list",
			"source", "string", "source_label", "string", "source_kind", "string",
			"source_mode", "string", "source_repo", "string", "ref", "string",
		),
	})
	r.Register("host.ticket_federation", "create", Op{
		Input: fields(
			"source", "string", "sources", "list", "repo", "string", "root", "string",
			"title", "string", "body", "string", "severity", "string", "component", "string",
			"target", "string", "labels", "list", "assignee", "string",
		),
		Output: fields("ok", "bool", "id", "string", "url", "string", "ref", "string"),
	})
	r.Register("host.ticket_federation", "comment", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "body", "string", "thread", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.ticket_federation", "comment_edit", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "comment_id", "string", "body", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.ticket_federation", "comment_reactions", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "comment_id", "string"),
		Output: fields("ok", "bool", "reactions", "list", "has_thumbsdown", "bool", "has_thumbsup", "bool"),
	})
	r.Register("host.ticket_federation", "transition", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "to", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.ticket_federation", "assign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string", "assignee", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.ticket_federation", "unassign", Op{
		Input:  fields("id", "string", "ref", "string", "source", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("ok", "bool", "assignee", "string"),
	})
	r.Register("host.ticket_federation", "list_mine", Op{
		Input:  fields("filter", "string", "sources", "list", "repo", "string", "root", "string"),
		Output: fields("tickets", "list", "source_groups", "list", "provider_errors", "list"),
	})
}

// registerGraphBuiltins registers every host.graph.* op (graph-mcp plan P1
// verify closure, Workstream D): the object-graph kit's "graph" interface
// binds default: host.graph and declares its operations against these
// entries so CheckInterfaceOpShapes actually checks host.graph op shapes —
// before this, host.graph had zero opschema entries and every declared op
// silently skipped shape checking (registry.Lookup miss reads as
// "unregistered handler, cannot check", not a failure). See
// internal/host/graph_handlers.go (load/lint/diff/apply/propose/authorize/
// withdraw/rebase/query/project/presentation) and internal/host/graph_read_ops.go
// (get/find/neighbors/type_census/changeset — the P1 read ops) for the
// concrete Result{Data: ...} shapes these mirror.
func registerGraphBuiltins(r *Registry) {
	r.Register("host.graph", "load", Op{
		Input:  fields("catalog_path", "string", "overlay_path", "string"),
		Output: fields("node_count", "int", "node_ids", "list", "warnings", "list"),
	})
	r.Register("host.graph", "lint", Op{
		Input:  fields("catalog_path", "string"),
		Output: fields("issues", "list", "issue_count", "int", "clean", "bool"),
	})
	r.Register("host.graph", "diff", Op{
		Input:  fields("catalog_path", "string", "overlay_path", "string"),
		Output: fields("nodes", "list"),
	})
	r.Register("host.graph", "apply", Op{
		Input: fields("catalog_path", "string", "changeset_id", "string", "dry_run", "bool"),
		Output: fields(
			"rejected", "bool", "reject_reasons", "list", "lint_issues", "list", "changed_files", "list",
		),
	})
	r.Register("host.graph", "propose", Op{
		Input: fields(
			"catalog_path", "string", "title", "string", "operations", "list",
			"visibility", "string", "provenance", "object", "validate_only", "bool",
		),
		Output: fields(
			"changeset_id", "string", "status", "string", "lint", "list", "rejected", "bool",
			"guard_fills", "list", "validated_only", "bool", "reject_reasons", "list",
		),
	})
	r.Register("host.graph", "authorize", Op{
		Input: fields("catalog_path", "string", "changeset_id", "string"),
		Output: fields(
			"rejected", "bool", "reject_reasons", "list", "lint_issues", "list", "changed_files", "list",
		),
	})
	r.Register("host.graph", "withdraw", Op{
		Input: fields("catalog_path", "string", "changeset_id", "string"),
		Output: fields(
			"rejected", "bool", "reject_reasons", "list", "lint_issues", "list", "changed_files", "list",
		),
	})
	r.Register("host.graph", "rebase", Op{
		Input: fields("catalog_path", "string", "changeset_id", "string"),
		Output: fields(
			"rejected", "bool", "reject_reasons", "list", "lint_issues", "list", "changed_files", "list",
		),
	})
	r.Register("host.graph", "project", Op{
		Input:  fields("catalog_path", "string", "overlay_path", "string", "graph_id", "string"),
		Output: fields("graph", "object", "registry", "list"),
	})
	r.Register("host.graph", "presentation", Op{
		Input:  fields(),
		Output: fields("layers", "list"),
	})
	r.Register("host.graph", "query", Op{
		Input: fields("catalog_path", "string", "mode", "string", "target", "string", "to_type", "string"),
		Output: fields(
			"references", "list", "type_id", "string", "schema", "string", "extends", "string",
			"summary", "string", "required_fields", "list", "edge_fields", "list", "ancestry", "list",
			"node_id", "string", "current_type", "string", "explain_type", "object", "incompatible_refs", "list",
		),
	})
	r.Register("host.graph", "get", Op{
		Input:  fields("catalog_path", "string", "ids", "list", "fields", "list"),
		Output: fields("nodes", "list", "missing", "list"),
	})
	r.Register("host.graph", "find", Op{
		Input: fields(
			"catalog_path", "string", "type", "string", "status", "list", "visibility", "string",
			"edge", "object", "no_inbound", "object", "no_outbound", "object", "field", "object",
			"text", "string", "limit", "int", "offset", "int", "count_only", "bool",
		),
		Output: fields("total", "int", "rows", "list", "truncated", "bool"),
	})
	r.Register("host.graph", "neighbors", Op{
		Input: fields(
			"catalog_path", "string", "id", "string", "direction", "string",
			"edges", "list", "depth", "int", "limit", "int",
		),
		Output: fields("triples", "list", "rows", "list"),
	})
	r.Register("host.graph", "type_census", Op{
		Input: fields("catalog_path", "string", "type_id", "string"),
		Output: fields(
			"type_id", "string", "schema", "string", "extends", "string", "summary", "string",
			"required_fields", "list", "edge_fields", "list", "ancestry", "list",
			"instance_count", "int", "status_breakdown", "object", "types", "list",
		),
	})
	r.Register("host.graph", "changeset", Op{
		Input: fields("catalog_path", "string", "action", "string", "changeset_id", "string", "node_id", "string"),
		Output: fields(
			"changesets", "list", "status_counts", "object", "id", "string", "title", "string",
			"status", "string", "operations", "list", "touching", "list",
		),
	})
	r.Register("host.graph", "history", Op{
		Input:  fields("catalog_path", "string", "id", "string", "since", "string", "limit", "int", "cursor", "string"),
		Output: fields("entries", "list", "next_cursor", "string"),
	})
}

// fields builds a map[string]FieldSpec from alternating name/type pairs, a
// terser literal form than a repeated FieldSpec{} for each of the ~40 fields
// above. Panics on an odd argument count — this only ever runs against a
// fixed literal call site, so a mistake here is a build-breaking typo, not a
// runtime hazard.
func fields(nameType ...string) map[string]FieldSpec {
	if len(nameType)%2 != 0 {
		panic("opschema.fields: odd number of arguments")
	}
	out := make(map[string]FieldSpec, len(nameType)/2)
	for i := 0; i < len(nameType); i += 2 {
		out[nameType[i]] = FieldSpec{Type: nameType[i+1]}
	}
	return out
}
