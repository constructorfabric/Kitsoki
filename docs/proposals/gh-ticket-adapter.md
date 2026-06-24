# Runtime: gh-backed ticket adapter

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../external-project-targeting.md

## Why

`dev-story`'s `ticket` interface (`search` / `get` / `comment` /
`transition` / `list_mine`, `stories/dev-story/app.yaml:174`) has exactly
one binding today: `host.local_files.ticket`, which reads
`<root>/issues/bugs/*.md`. A foreign target like gears-rust tracks work in
**GitHub issues + PRs** (`.prs/`, CONTRIBUTING.md §2.10) — there are no
`issues/bugs/*.md`. To pick up and comment on real work there, the `ticket`
interface needs a **GitHub-backed adapter**, and it should be reusable for
any GitHub repo, not gears-rust-specific.

## What changes

Add a `gh`-backed ticket provider implemented as **glue** (`host.run` +
Starlark, per the `starlark` skill) — no new Go host — that satisfies the
`ticket` interface by shelling out to the `gh` CLI:

| iface op | gh call |
|---|---|
| `search` | `gh issue list --search <q> --json number,title,labels,state` |
| `get` | `gh issue view <id> --json number,title,body,labels,state,comments` |
| `comment` | `gh issue comment <id> --body <text>` |
| `transition` | `gh issue edit <id> --add-label / --remove-label` (or close/reopen) |
| `list_mine` | `gh issue list --assignee @me --json …` |

The provider runs against `world.repo_root` (the external checkout, wired in
#1) via `gh -R <owner>/<repo>` or by running in that tree, so the same repo
the workspace/vcs hosts operate on is the one tickets come from. An instance
binds it with `host_bindings: { ticket: host.gh.ticket }` (or the glue
script path), the same one-line rebinding `kitsoki-dev` uses for
`host.local_files.ticket`.

## Design

- **Shape:** a Starlark `main(ctx) -> dict` per op (or one script dispatching
  on `ctx.inputs.op`), returning the normalized ticket dict the pipeline
  already consumes from `host.local_files.ticket` — so downstream rooms are
  unchanged. The `.star.yaml` sidecar declares the `gh` subprocess.
- **Auth:** relies on the operator's existing `gh auth` — no token handling
  in kitsoki. If `gh` is absent/unauthenticated the op fails cleanly
  (`on_error` keeps the room), mirroring how `host.agent.search` degrades.
- **Cassettes:** the subprocess is recorded via the HTTP/exec cassette
  mechanism so **flows and tests never call real `gh`** (CLAUDE.md). Fixture
  cassettes capture representative `gh issue list/view/comment` payloads.
- **Normalization:** GitHub's `number`→`ticket_id`, `labels`→state/transition
  vocabulary mapping lives in the script, isolated so a different forge
  (Jira) could be a sibling script with the same output contract.

## Impact

- **Files:** new `stories/.../scripts/gh_ticket.star` (+ `.star.yaml`) — or a
  shared `stories/_adapters/` home if we want it cross-instance; the
  instance `app.yaml` `host_bindings`; exec cassettes under the instance's
  `flows/` fixtures.
- **Hosts:** reuses `host.run`; no engine change. Adds the binding target to
  the instance `hosts:` allow-list (`hosts: declared`).
- **Compat:** purely additive — `host.local_files.ticket` stays the default;
  this is an alternative binding.

## Tasks

- [ ] Write `gh_ticket.star` covering the five ops; normalize to the
      existing ticket dict contract.
- [ ] `.star.yaml` sidecar + exec cassette for each op (recorded once,
      replayed in tests).
- [ ] A `ticket_search`→`get`→`comment` flow fixture against the cassette,
      asserting the normalized fields and that no real `gh` runs.
- [ ] Clean degradation when `gh` is missing/unauthed (`on_error` arc).
- [ ] Document the binding + the `gh auth` prerequisite; migrate the
      adapter contract into the external-targeting guide; trim this proposal.

## Open questions

1. **One dispatching script or one per op?** *Lean: one script dispatching
   on `ctx.inputs.op` — one cassette set, one place for the normalization
   map.*
2. **Shared adapter home (`stories/_adapters/`) vs per-instance?** *Lean:
   start inside the gears-rust instance (#4); promote to a shared home the
   moment a second GitHub target appears — avoid premature shared surface.*

## Non-goals

- Jira/Linear/other forges — only `gh`. The output contract leaves room for
  siblings.
- Promoting this to a Go host — glue until a real contract/perf need is
  shown (epic shared decision #2).
- PR-review / `.prs/` integration — this slice is the *ticket* (issue) seam
  only; PR workflows are the `vcs` interface's job (#4).
