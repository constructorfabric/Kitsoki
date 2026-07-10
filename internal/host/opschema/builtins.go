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
func Builtins() *Registry {
	r := NewRegistry()

	// ticket -> host.local_files.ticket
	r.Register("host.local_files.ticket", "search", Op{
		Input:  fields("query", "string", "limit", "int"),
		Output: fields("tickets", "list"),
	})
	r.Register("host.local_files.ticket", "get", Op{
		Input: fields("id", "string"),
		Output: fields(
			"id", "string", "title", "string", "body", "string", "status", "string",
			"priority", "string", "assignee", "string", "url", "string", "comments", "list",
		),
	})
	r.Register("host.local_files.ticket", "comment", Op{
		Input:  fields("id", "string", "body", "string", "thread", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.local_files.ticket", "transition", Op{
		Input:  fields("id", "string", "to", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.local_files.ticket", "list_mine", Op{
		Input:  fields("filter", "string"),
		Output: fields("tickets", "list"),
	})

	// ticket -> host.local_github.ticket
	r.Register("host.local_github.ticket", "search", Op{
		Input:  fields("query", "string", "limit", "int"),
		Output: fields("tickets", "list"),
	})
	r.Register("host.local_github.ticket", "get", Op{
		Input: fields("id", "string"),
		Output: fields(
			"id", "string", "title", "string", "body", "string", "status", "string",
			"priority", "string", "assignee", "string", "url", "string", "comments", "list",
		),
	})
	r.Register("host.local_github.ticket", "comment", Op{
		Input:  fields("id", "string", "body", "string", "thread", "string"),
		Output: fields("ok", "bool", "comment_id", "string"),
	})
	r.Register("host.local_github.ticket", "transition", Op{
		Input:  fields("id", "string", "to", "string"),
		Output: fields("ok", "bool"),
	})
	r.Register("host.local_github.ticket", "list_mine", Op{
		Input:  fields("filter", "string"),
		Output: fields("tickets", "list"),
	})

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

	// transport -> host.append_to_file
	r.Register("host.append_to_file", "post", Op{
		Input:  fields("thread", "string", "body", "string"),
		Output: fields("ok", "bool", "message_id", "string"),
	})

	return r
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
