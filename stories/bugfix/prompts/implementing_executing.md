# Implementing the fix — apply edits to the worktree

You are implementing the fix for **{{ args.ticket_id }}** — *{{ args.ticket_title }}*
inside the worktree at `{{ args.workdir }}`.

The proposing room produced this proposal:

> {{ args.fix_description }}

**Root cause:** {{ args.root_cause }}

**Affected files:** {{ args.affected_files }}

{% if args.refine_feedback %}**Refinement feedback from the previous attempt
(cycle {{ args.cycle }}):**

> {{ args.refine_feedback }}
{% endif %}

## What to do

Make the edits described in the proposal. You are running with cwd set to
the worktree (`{{ args.workdir }}`), so file paths in the proposal's
`affected_files` are valid relative paths you can hand straight to
the `Read` and `Edit` (or `Write`) tools.

You MUST actually modify the files. The downstream `testing` room will
re-run the repro tests against this worktree's HEAD — if you don't make
the edits, those tests will still fail and the pipeline will reject the
work.

## Constraints

- Stay inside the worktree. Do not edit `.git` metadata or any path
  outside `{{ args.workdir }}`.
- Touch only the files in `affected_files`. If you genuinely need to
  edit something outside that list, add it to `files_changed` in the
  artifact so the reviewer sees the scope.
- Don't run `git commit` yourself — the pipeline does that after you
  return. Just leave the worktree dirty with your edits staged or
  unstaged; the commit step picks up everything.
- Don't run `go test` either — the next room runs CI.

## Output

Submit an `implementing_artifact` (see `schemas/implementing_artifact.json`).
Required fields:
- `summary_title`: one-line description of what you applied.
- `summary_markdown`: human-readable diff narrative (what changed, where,
  why each edit). This is what a human reviewer reads at the testing
  checkpoint.
- `files_changed`: every path you actually edited (or attempted to edit).
- `applied`: `true` if every required edit was made, `false` if you hit a
  blocker. When `false`, populate `blockers` with the reason.
