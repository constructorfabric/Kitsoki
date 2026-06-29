# `issues/` is deprecated — kitsoki tracks on GitHub Issues now

kitsoki's bugs and features are tracked as **GitHub Issues** on the canonical
repo, [`constructorfabric/Kitsoki`](https://github.com/constructorfabric/Kitsoki/issues),
not as the in-repo Markdown files under `issues/bugs/` and `issues/features/`.

That in-repo "issues hack" was the right call while the repo was private and had
no tracker. Now that kitsoki is hosted on public GitHub, the hack costs more than
it saves (no visibility for contributors, no notifications/assignment/search, a
bespoke file format). See the migration epic that retired it:
`docs/proposals/github-issues-tracker.md` (and its slices).

## What replaced each path

| Was | Now |
|---|---|
| `kitsoki bug create` → `issues/bugs/<id>.md` | `kitsoki bug create --github <owner/repo>` → a GitHub issue |
| Web **Report bug** modal → local file + `<id>.artifacts/` | `kitsoki web --ticket-repo <owner/repo>` → a GitHub issue with evidence saved under `.artifacts/bug-reports/` for developer-local review |
| Design pipeline publish → `issues/features/<id>.md` | a GitHub feature issue (labels `target:kitsoki` + `comp:proposal`) |
| `.kitsoki/stories/kitsoki-dev` reads `host.local_files.ticket` | binds `host.gh.ticket`, pinned at `constructorfabric/Kitsoki` |

The label vocabulary GitHub uses mirrors the old frontmatter axes:
`severity P0..P3` → `P0..P3`, `component: x` → `comp:x`, `target: x` →
`target:x`, plus an `in_progress` label. The kitsoki-specific fields GitHub has
no home for (`trace_ref`, `kitsoki_rev`, `filed_by`, the original `legacy_id`)
ride in a fenced ```kitsoki block in the issue body.

## This archive

The files here are a **frozen reference**, not a live tracker:

- Nothing reads or writes them once the dogfood loop binds `host.gh.ticket`.
- They stay in git (history + on disk); they are **not** deleted.
- The one-shot `kitsoki issues migrate --repo <owner/repo>` lifts them into
  GitHub issues (replaying each file's `## Comment` thread, closing
  resolved/wontfix tickets) and writes the new issue ref back into each file's
  `external:` frontmatter, so it is idempotent and records where each ticket went.

## ⚠️ Pending: the bulk migration has **not** been run yet

The filing paths and the dogfood loop already use GitHub (slices #1–#4 shipped,
`kitsoki-dev` is rebound to `host.gh.ticket`), but the **existing 15 tickets in
this folder have not yet been lifted to GitHub**. That is a deliberate one-time
**maintainer action** — a mass external write (~15 issues + their comment threads
+ closes) onto the canonical repo — so it isn't run automatically.

To do it (idempotent — safe to re-run; already-migrated files are skipped):

```sh
kitsoki issues migrate --repo constructorfabric/Kitsoki --dry-run   # preview
kitsoki issues migrate --repo constructorfabric/Kitsoki             # for real
```

Each migrated file gets an `external: {github: "constructorfabric/Kitsoki#<n>",
url: …}` line written back into its frontmatter. **Until that command is run, the
tickets below live only here.** Once it completes, delete the migration epic
(`docs/proposals/github-issues-tracker.md`) and its remaining child
(`docs/proposals/issues-migration-to-github.md`).

Current status of each ticket: a file whose `external:` frontmatter is still `{}`
(or absent a `github:` ref) has **not** been migrated. As of this note, **none**
have been — the whole pile is pending.
