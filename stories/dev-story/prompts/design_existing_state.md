# Check whether this idea overlaps work already in flight

You are the **scout**. Before a new proposal is drafted, find out whether
the idea duplicates, extends, or conflicts with work that already exists —
so we can amend the existing proposal rather than blindly create a new one.

The idea:

> {{ args.idea }}

{% if args.brief_path %}The operator's brief is at **`{{ args.brief_path }}`** — read it for the
full framing before searching.
{% endif %}

## Where to look (use `Read` / `Grep` / `Glob`)

1. **In-progress proposal/design drafts** — configured workspaces such as
   `docs/proposals/.workspace/*/` or `.context/designs/.workspace/*/`.
   `kind: in_progress`.
2. **Accepted proposals/design docs** — the repo's existing proposal, design,
   ADR, PRD, RFC, or feature-spec directories. In this repo that is commonly
   `docs/proposals/*.md`; in external repos, find and use the equivalent.
   `kind: accepted`.
3. **Feature / bug docs** — local issue docs such as `issues/features/*.md`,
   `issues/bugs/*.md`, `.artifacts/issues/bugs/*.md`, and story-local issue
   folders. `kind: feature`.

## What to report

For each genuine overlap, give the `path`, the `kind`, a one-line
`summary` of how it overlaps, and a `recommendation`:

- **amend** — the idea belongs in this existing artifact; fold it in
  rather than create a new proposal. **This is the default whenever the
  overlap is substantial** — even if the operator framed the request as
  "create a new proposal."
- **supersede** — the existing artifact is stale and this idea replaces it.
- **new** — the overlap is incidental; a separate proposal is correct.

Only list **real** overlaps. A greenfield idea with no prior art should
return an **empty `overlaps` list** — that is the expected answer and lets
the operator proceed to a new proposal cleanly.

Finally, set `roadmap_fit`: one or two sentences on where this idea sits
relative to the current proposal queue and the broader direction.

## Output

Submit an `existing_state` object (see `schemas/existing-state.json`):
`{ overlaps: [{ path, kind, summary, recommendation }], roadmap_fit }`.
