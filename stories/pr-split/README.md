# pr-split — concern-grouped PRs from a branch's commits

Takes the commits on a feature branch that are ahead of integration, groups them
into **concern buckets**, and opens **one PR per concern** — each independently
reviewable and revertable.

## Why a story (not a script)
All the logic lives inside kitsoki as a story; the external harness only invokes
it. The split honors progressive determinism:

- **Deterministic (no LLM):** `idle` lists `integration..HEAD`, reads each
  commit's changed files, and maps paths → concern (`internal/**`→core,
  `tools/bugfix-bakeoff/**`→harness, `docs/**`→docs, `stories/**`→stories, …).
  `splitting` calls `host.git.split_prs`, which does every branch,
  cherry-pick, push, and native PR-create operation in a throwaway worktree so
  the caller's tree is never disturbed.
- **LLM (one step):** `planning` runs a single fenced `bucketer` agent that
  finalizes mixed-concern commits and authors each PR's title/body. It has no
  Bash — the story drives every command. A `host.starlark.run` step then
  serializes the plan to JSON for the deterministic splitter.

## Flow
`idle` (classify) → `planning` (agent buckets + serialize) → `splitting`
(branch/cherry-pick/push/PR per bucket) → `done` (summary). `toggle_dry_run`
plans without pushing.

## Run it
Drive through the studio MCP (no external logic needed):

```
session.new {
  story: "stories/pr-split/app.yaml",
  harness: "live",
  initial_world: { working_dir: "<checkout>", integration_branch: "main" }
}
```

Then `session.drive {intent: proceed}` → review the proposed PRs →
`session.drive {intent: accept}`. Or `make pr-split WORKDIR=<checkout>`.

## Tests (no LLM)
`kitsoki test flows stories/pr-split/app.yaml` — `happy_path_two_prs` stubs the
agent + every host call and asserts the full idle→…→done path opens two PRs.

## Runtime issues this story exposes (per stories/AGENTS.md — to be fixed in the engine, not papered over)
A live drive surfaced two ordering/serialization behaviors. The story is authored
to the documented sequential pattern; the runtime does not fully honor it:

1. **A same-turn on_enter `host.run` bind is not visible to a later on_enter
   step in that turn.** `list_commits` + the bucketer agent in one on_enter left
   the agent seeing zero commits (its args rendered pre-bind). Worked around
   *correctly* by splitting across a turn boundary: `idle` lists commits and the
   operator's `proceed` enters `planning`, so the bind has settled. A `when:`
   guard on a later on_enter step has the same race (it reads pre-bind world) —
   gate on a scalar known before the room, never on a freshly-bound value.

2. **Keep GitHub plumbing in native hosts.** The first splitter used `host.run`
   to bridge plan JSON into Bash and then shell out to the GitHub CLI. The
   current story passes the serialized plan to `host.git.split_prs` instead, so
   provider auth, PR creation, and failure reporting stay inside the gitops
   host surface.
