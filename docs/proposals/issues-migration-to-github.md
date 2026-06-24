# Runtime: migrate the `issues/` pile to GitHub + deprecate it

**Status:** **Shipped except the real bulk-migration run.** Done:
`kitsoki issues migrate --repo <owner/repo>` ([`cmd/kitsoki/issues.go`](../../cmd/kitsoki/issues.go))
— idempotent create + comment-replay + close + `external:` write-back, dry-run
verified across all 15 tickets; the freeze
([`issues/DEPRECATED.md`](../../issues/DEPRECATED.md) + README banner); the
**rebind** — `kitsoki-dev` binds `ticket: host.gh.ticket` (pinned at
`constructorfabric/Kitsoki`), the dev-story rooms thread `repo`, and the 5
kitsoki-dev flow fixtures stub `host.gh.ticket` (kitsoki-dev flows 5/5, dev-story
31/31); and the superseded `bug-sync-proposal.md` is deleted.
**One step remains:** run the real bulk migration on `constructorfabric/Kitsoki`
— an outward mass-write (~15 issues + comments + closes) the maintainer triggers
once. After it lands, delete this proposal + the epic.
**Kind:**   runtime
**Epic:**   ./github-issues-tracker.md

## Why

This is the **cutover** slice. Once filing (#2/#3) writes to GitHub and the
`create` op exists (#1), two things remain: the **existing** pile of 15 bugs
(`issues/bugs/*.md`) + 2 features (`issues/features/*.md`) must move to
GitHub so the dogfood loop doesn't lose its open work, and `kitsoki-dev` must
**rebind** `iface.ticket` from `host.local_files.ticket` to `host.gh.ticket`
so every search/pick/comment/close hits GitHub. Per the user's instruction
the old pile is **kept in-repo as a frozen, deprecated archive** — not
deleted — so history and evidence stay reachable.

## What changes

- **One-shot migration** — a `kitsoki issues migrate` command (or a guarded
  script) reads every `issues/{bugs,features}/*.md` via the existing parser
  (`internal/host/localfiles_ticket.go` already parses frontmatter + the
  `## Comment` thread, `localfiles_ticket.go:33`), and for each: creates a
  GitHub issue (slice #1 `create`) with the mapped labels and the body
  metadata block carrying `legacy_id: <old-id>`; replays each `## Comment`
  block as a `gh issue comment`; and **closes** issues whose `status` is
  `resolved`/`wontfix` (slice #1 `transition`). Per-ticket `<id>.artifacts/`
  evidence is uploaded to its issue (reusing slice #2's attachment path).
- **Idempotency** — the migration records the new issue URL back into each
  source file's `external:` frontmatter block (the field
  `issues/README.md:63` reserved for exactly this) so re-running skips
  already-migrated files. `external:` is the one safe write into the
  otherwise-frozen pile.
- **Rebind** — `stories/kitsoki-dev/app.yaml`
  `host_bindings: { ticket: host.gh.ticket }` (replacing
  `host.local_files.ticket`), with `ticket_repo: constructorfabric/Kitsoki`
  (slice #1) threaded into the provider args. This is the moment the dogfood
  loop becomes GitHub-backed.
- **Deprecate** — add `issues/DEPRECATED.md` (and a banner atop
  `issues/README.md`) stating the pile is frozen, GitHub Issues on
  `constructorfabric/Kitsoki` is canonical, and each file's `external:` block
  links its issue. Remove the `ticket_globs`/local-files wiring from
  `kitsoki-dev` so nothing reads the pile.

One sentence: **the 15 bugs + 2 features move to GitHub Issues (comments,
state, and evidence preserved), `kitsoki-dev` reads/writes GitHub, and
`issues/` is frozen with a deprecation notice.**

## Design

- **Reader reuse.** Don't re-parse Markdown by hand — the local-files
  provider already turns a file into `{frontmatter, body, comments[]}`
  (`localfiles_ticket.go`). The migration calls that reader, then slice #1's
  `create`/`comment`/`transition` to project each into GitHub. One translation
  layer, well-tested.
- **Comment fidelity.** Each `## Comment <ts> by <author>` block
  (`issues/README.md:76-83`) becomes a GitHub comment prefixed with the
  original author + timestamp (GitHub will attribute all of them to the
  migrating user — the prefix preserves provenance). The bug file *is* the
  conversation log today; the issue becomes it after.
- **Ordering & safety.** Migrate → verify (each source file has an `external:`
  URL) → **then** rebind. The rebind is the last commit so the loop never
  points at GitHub before the tickets exist. The migration is
  re-runnable (idempotent via `external:`), dry-run-able (`--dry-run` prints
  the planned issues without creating), and **never** deletes a source file.
- **Cassettes / no real GitHub in tests.** The migration's `gh` calls go
  through `cliExec`; a fixture run against a handful of sample files with a
  recorded cassette proves the mapping (labels, closed-state, comment replay,
  `external:` write-back) with **no network** (CLAUDE.md). The *actual*
  one-time production migration is run by hand against real GitHub by the
  operator — that's an operational step, not a test.
- **Fork-permission reality.** The production migration must be run by someone
  with **triage** on `constructorfabric/Kitsoki` (it labels + closes). A pure
  fork contributor can't; document that this is a maintainer-run step.

## Impact

- **Code:** a `kitsoki issues migrate` command (`cmd/kitsoki/`) reusing the
  localfiles reader + slice #1 ops + slice #2's attachment upload;
  `stories/kitsoki-dev/app.yaml` (the `host_bindings` rebind + `ticket_repo`;
  remove local-files `ticket_globs`).
- **Repo:** `issues/DEPRECATED.md` + a banner on `issues/README.md`;
  `external:` blocks written into each migrated file (the only mutation to the
  frozen pile). The files themselves stay (no `git rm`).
- **Tests:** a migration unit/flow test over sample fixtures with a recorded
  cassette (asserts labels, closed-state for resolved/wontfix, comment replay,
  idempotent re-run via `external:`).
- **Docs on ship:** `issues/DEPRECATED.md`; the `kitsoki-dev` README's ticket
  section (now GitHub-backed); fold the migration command into
  `docs/architecture/` or the dev-story README.
- **Compat:** `host.local_files.ticket` stays in the codebase (other
  instances / `external-project-targeting` may still use file tickets); only
  the kitsoki-dev binding flips.

## Tasks

```
- [ ] `kitsoki issues migrate` reading issues/{bugs,features}/*.md via the
      localfiles reader; --dry-run; idempotent via external: write-back.
- [ ] Per ticket: create (labels + legacy_id body block) → replay ## Comment
      blocks → close if resolved/wontfix → upload <id>.artifacts/.
- [ ] Migration flow/unit test over sample fixtures + recorded cassette
      (labels, closed-state, comments, idempotency); no real gh.
- [ ] Rebind kitsoki-dev: host_bindings ticket → host.gh.ticket; ticket_repo
      pin; remove local-files ticket_globs. (Land last — the cutover.)
- [ ] issues/DEPRECATED.md + README banner; keep the files (no git rm).
- [ ] Run the real one-time migration (maintainer with triage); spot-check
      a sample of issues; record the run.
- [ ] Migrate the command/rebind into docs; close the epic once #1–#4 ship;
      delete bug-sync-proposal.md (superseded); trim/delete this proposal.
```

## Open questions

1. **Command vs. throwaway script.** A real `kitsoki issues migrate` subcommand
   is testable and re-runnable; a one-shot script is less surface. *Lean:
   subcommand* — it's the only thing exercising slice #1's `create` over the
   real pile, and idempotency/dry-run want test coverage.
2. **Attribution.** All migrated comments will show the migrating user as
   author. *Lean: accept it* + preserve the original author/timestamp in the
   comment prefix; GitHub offers no impersonation without the original users.

## Non-goals

- Deleting the `issues/` files (the user's instruction: freeze + deprecate,
  keep in-repo).
- Two-way sync from GitHub back into the frozen pile.
- Migrating any *story-level* `stories/*/issues/bugs/` seeds (e.g.
  `stories/oregon-trail/issues/bugs/`) — those exercise the multi-glob
  local-files reader and are out of scope; only kitsoki's own pile moves.
