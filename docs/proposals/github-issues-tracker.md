# Epic: kitsoki's own tracker moves to GitHub Issues

**Status:** **All four slices shipped & verified** — their detail has migrated to
[hosts.md → host.gh.ticket](../architecture/hosts.md#hostghticket--github-issues-backed-tracker)
and the child proposals are deleted. `host.gh.ticket` has the `create` op +
conventions; bug filing (web + CLI) and feature filing open real issues; the
`kitsoki-dev` dogfood loop is **rebound to `host.gh.ticket`** (flows green); the
`issues/` pile is frozen with a migration tool. Proofs (real, on the operator's
fork): bug `…/issues/3` (web), `…/issues/5` (CLI), feature `…/issues/6` (design
pipeline). The cross-site **demo** is built + recorded + QA-passed
(`.agents/skills/kitsoki-ui-demo/scripts/record-gh-issues-demo.sh`). **Only one
operator action remains:** run the real bulk migration of the existing 15-ticket
pile onto `constructorfabric/Kitsoki` (`kitsoki issues migrate`). Delete this epic
+ the slice-#4 child once that lands.
**Kind:**   epic
**Slices:** 4 (all shipped; bulk-migration run is the last operator step)

## Why

kitsoki tracks its own bugs and features in an **in-repo Markdown pile**
(`issues/bugs/*.md`, `issues/features/*.md`) read by the dogfood app
through `host.local_files.ticket` (`internal/host/localfiles_ticket.go`).
That "issues hack" was the right call while the repo was private and had no
issue tracker. kitsoki is now hosted on a **public GitHub**
(`constructorfabric/Kitsoki`), so the hack costs more than it saves: bugs
aren't visible to outside contributors, there's no notification/assignment/
search anyone expects, and every filing path writes a bespoke file format
instead of using the tracker the project already has.

The substrate to switch is **mostly already built**. `internal/host/github.go`
ships a `gh`-backed `ticket` provider (`host.gh.ticket`) with
`search`/`get`/`comment`/`transition`/`list_mine` mirroring the local-files
contract exactly — it was written as "the obvious next provider after local
files" but never dogfooded. The only filing path it lacks is **creating** an
issue. Close that gap, pin the dogfood at `constructorfabric/Kitsoki`, route
the two filing paths (CLI + web) and the design pipeline at it, migrate the
existing pile, and freeze `issues/` as a deprecated archive.

## What changes

Once every slice ships:

- `host.gh.ticket` gains a **`create`** op; `kitsoki-dev` rebinds
  `iface.ticket` from `host.local_files.ticket` to `host.gh.ticket`, pinned
  at `constructorfabric/Kitsoki`. Searching, picking, commenting, and
  closing a ticket in the dogfood loop all hit **real GitHub Issues**.
- **Bug filing** (`kitsoki bug create` CLI and the web `runstatus.bug.report`
  RPC) creates a GitHub issue instead of an `issues/bugs/<id>.md` file. The
  web modal's evidence (screenshot, HAR, rrweb) is **uploaded to the issue**.
- **Feature filing** (the design pipeline's `publish_design.py`) mints a
  GitHub issue instead of `issues/features/<id>.md`, linking the published
  proposal.
- The 15 bugs + 2 features already on disk are **migrated** into GitHub
  Issues, and `issues/` becomes a **frozen, deprecated archive** (a
  `DEPRECATED.md` notice; the old files stay in git history and on disk for
  reference, but nothing reads or writes them).
- Everything points at **`constructorfabric/Kitsoki`** as the canonical
  project, even when the operator's `origin` is a personal fork.

## Impact

- **Spans:** runtime (the `create` op + the filing-path rewrites + the
  migration tool), story (the design-pipeline publish step).
- **Net surface:** one new op on an existing Go host; two filing call sites
  re-pointed; one Python publish script re-pointed; one one-shot migration
  command; one `host_bindings` line + one world key on `kitsoki-dev`; exec
  cassettes for every new `gh` call so flows/tests never touch real GitHub.
- **Docs on ship:** `docs/architecture/hosts.md` (the `create` op + the
  `gh` ticket binding), `docs/stories/bugs.md` + `docs/tui/web-ui.md` (bug
  filing now targets GitHub), the `kitsoki-dev` / `dev-story` READMEs (the
  rebinding), and `issues/DEPRECATED.md`. As each slice ships its detail
  migrates per that child's own plan.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | Where |
|---|---|---|---|---|---|---|
| 1 | gh issue **create** + constructorfabric pin | runtime | Add `create` op to `host.gh.ticket`; establish the `constructorfabric/Kitsoki` repo pin | — | **Shipped** | [hosts.md → host.gh.ticket](../architecture/hosts.md#hostghticket--github-issues-backed-tracker) |
| 2 | Bug filing → GitHub | runtime | `runstatus.bug.report` (web) + `kitsoki bug create --github` (CLI) create issues; web evidence is saved under `.artifacts/bug-reports/` for developer-local review | 1 | **Shipped** | [hosts.md](../architecture/hosts.md#filing-a-bug-with-evidence), [bugs.md](../stories/bugs.md) |
| 3 | Feature filing → GitHub | story | The design pipeline's publish step mints a GitHub issue instead of `issues/features/<id>.md` | 1 | **Shipped** | [dev-story README](../../stories/dev-story/README.md) |
| 4 | Migrate + deprecate `issues/` | runtime | One-shot `kitsoki issues migrate`; freeze `issues/`; rebind `kitsoki-dev` to `host.gh.ticket` | 1 | **Tooling shipped; cutover deferred** | [`issues-migration-to-github.md`](issues-migration-to-github.md) |

## Sequencing

```
#1 (gh create + pin) ──▶ #2 (bug filing → GitHub)
                     ├──▶ #3 (feature filing → GitHub)
                     └──▶ #4 (migrate + rebind + deprecate)
```

#1 is the substrate every other slice consumes (the `create` op and the
repo-pin convention). #2, #3, #4 are independent of each other once #1 lands
and can proceed in parallel. **#4's rebind is the cutover** — land it last so
the dogfood loop only flips to GitHub once the migrated tickets are present
and the filing paths (#2/#3) write there too.

## Shared decisions

1. **Hard cutover, not dual-write.** GitHub Issues becomes the single source
   of truth. `kitsoki-dev` rebinds `iface.ticket` to `host.gh.ticket`; the
   filing paths write **only** to GitHub; `issues/` is frozen. No
   local-then-sync correlation (this is why the
   [`bug-sync-proposal.md`](bug-sync-proposal.md) "local authoritative,
   remote projection" model is **superseded** for kitsoki's own tracker —
   see Non-goals).
2. **`constructorfabric/Kitsoki` is canonical, even from a fork.** Every
   `gh` call in the dogfood path passes an explicit
   `repo: constructorfabric/Kitsoki` so it never silently resolves to the
   operator's `origin` (a personal fork like `bsacrobatix/Kitsoki`). The
   slug is **not** hard-coded in Go — it's a `kitsoki-dev` world key
   (`ticket_repo`) threaded into the provider args, so a different fork-of-a-
   fork or a downstream project can override it. Default lives in one place.
   (`host.gh.ticket` already accepts a `repo` arg, `internal/host/github.go:78`;
   today it's empty so `gh` falls back to the local remote.)
3. **`gh` CLI, not the REST API / a token.** Auth rides the operator's
   existing `gh auth` — kitsoki handles no tokens. Every op degrades cleanly
   when `gh` is absent/unauthenticated (the provider already returns a clean
   `Result.Error`, `github.go:54`); rooms route the `on_error:` arc. The one
   exception is **binary attachment upload** (slice #2), which `gh` can't do
   natively — see that slice's design for the `gh api` REST fallback.
4. **Cassettes for every `gh` call.** All shell-outs go through the `cliExec`
   seam (`internal/host/cli_exec.go`) and are recorded as exec cassettes, so
   flows and tests **never** call real GitHub (CLAUDE.md). Each slice ships
   the fixtures for its new calls.
5. **The frozen `issues/` pile stays in-repo.** Per the user's instruction:
   keep the archive (don't `git rm` it), add a `DEPRECATED.md`, and stop all
   reads/writes against it. Git history + the on-disk files remain for
   reference; GitHub carries the live work.

## Cross-cutting open questions

1. **Label / state vocabulary mapping.** The local format carries
   `severity: P0..P3`, `component:`, `status: open|in_progress|resolved|wontfix`
   (`issues/README.md:52`). GitHub has labels + open/closed only. *Lean:*
   map `severity`/`component`/`target` to **labels** (`P1`, `comp:tui`,
   `target:kitsoki`), `status` to open/closed (the provider's `transition`
   already maps `resolved`/`wontfix`→close, `github.go:238`), and put
   `in_progress` on a label. Pin the exact label set in slice #1 so #2/#3/#4
   all emit the same.
2. **Can a fork contributor file/triage?** Anyone can *create* an issue on a
   public repo, but *labeling/closing* needs triage permission — a pure-fork
   contributor's `transition`/label calls may 403. *Lean:* `create` works for
   everyone; treat label/transition failures as the existing clean
   `on_error` degradation (the comment thread still works), and document the
   permission floor. Decided per #1.
3. **What carries the kitsoki-specific frontmatter** (`trace_ref`,
   `kitsoki_rev`, `filed_by`) that GitHub has no field for? *Lean:* a small
   machine-readable block in the issue body (a fenced `kitsoki` metadata
   block) the provider writes on create and parses on get — keeps the data
   without inventing GitHub custom fields. Decided per #1 (it sets the body
   template #2/#3/#4 reuse).

## Non-goals

- **Jira / Linear / other forges.** Only `gh` / GitHub Issues. The `ticket`
  interface seam leaves room for siblings (the
  [`gh-ticket-adapter.md`](gh-ticket-adapter.md) slice of
  [`external-project-targeting.md`](external-project-targeting.md) targets
  *foreign* GitHub repos with the same provider — this epic supplies the
  `create` op that slice also wants, and pins it at kitsoki's *own* repo).
- **`kitsoki bug sync`** (the [`bug-sync-proposal.md`](bug-sync-proposal.md)
  local→remote push). A hard cutover makes the local file non-authoritative,
  so there's nothing to sync *from*; that proposal is superseded for
  kitsoki's own tracker and should be deleted once #2 ships (it may still be
  relevant for a future "export my local notes" use, but not here).
- **Two-way mirroring** of GitHub comments/labels back into local files —
  there are no live local files after cutover.
- **Promoting the provider to anything beyond the existing Go host** — it
  already exists; this epic only adds an op and dogfoods it.
- **Real-GitHub tests.** Every slice is exercised with exec cassettes /
  flows (CLAUDE.md, shared decision #4).
