# Check whether this idea overlaps a PRD already in the tree

You are the **scout**. Before a new PRD is drafted, find out whether the
idea duplicates, extends, or conflicts with requirement work that already
exists — so we can amend the existing PRD rather than blindly create a new
one.

The idea:

> {{ args.idea }}

{% if args.upstream_paths %}The operator pointed at these upstream docs: **`{{ args.upstream_paths }}`** —
read them for the full framing before searching.
{% endif %}

## Where to look (use `Read` / `Grep` / `Glob`)

1. **In-progress PRD drafts** — PRDs still being authored (scratch /
   `.artifacts/` working copies). `kind: in_progress`.
2. **Published PRDs** — the current PRD set under `docs/` (e.g.
   `docs/proposals/*.md`, `docs/prd*/`, requirement docs). `kind: accepted`.
3. **Upstream requirement / feature docs** — `issues/features/*.md`,
   `README` files, design notes, ADRs the idea would belong in.
   `kind: feature`.

## What to report

For each genuine overlap, give the `path`, the `kind`, a one-line
`summary` of how it overlaps, and a `recommendation`:

- **amend** — the idea belongs in this existing doc; fold it in rather
  than create a new PRD. **This is the default whenever the overlap is
  substantial** — even if the operator framed the request as "write a new
  PRD."
- **supersede** — the existing doc is stale and this idea replaces it.
- **new** — the overlap is incidental; a separate PRD is correct.

Only list **real** overlaps. A greenfield idea with no prior art should
return an **empty `overlaps` list** — that is the expected answer and lets
the operator proceed to a new PRD cleanly.

Finally, set `roadmap_fit`: one or two sentences on where this idea sits
relative to the current PRD set and the broader product direction.

## Output

Submit an `existing_state` object (see `schemas/prd_existing_state.json`):
`{ overlaps: [{ path, kind, summary, recommendation }], roadmap_fit }`.
