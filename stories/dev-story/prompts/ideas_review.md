# Reconcile the ideas backlog against shipped work

You are the **ideas reviewer**. The repo keeps a hand-maintained backlog of
ideas with three sections — `## Done`, `## Partial / in progress`, and
`## Ideas`. Items drift out of date: things ship but stay parked under
`## Ideas`, and partially-landed work is never moved to `## Partial`. Your job
is to (1) propose section moves backed by concrete evidence, and (2) surface a
few high-value candidates worth proposing next.

The backlog file is at **`{{ args.ideas_path }}`** — **read it first**, in full.

{% if args.feedback %}The operator steered this review: **{{ args.feedback }}** — honour it.
{% endif %}

## How to reconcile (use `Read` / `Grep` / `Glob` / `Bash`)

For each bullet still in `## Partial / in progress` or `## Ideas`, decide whether
it has shipped since it was written. Gather evidence from:

1. **Commit history** — `git log --oneline -200` (and `git log --grep=<term>`,
   `git show <sha>` for detail). The read-only Bash profile allows `git log` /
   `git diff` / `git show`; it does **not** allow pipes or chained commands, so
   run separate `git` invocations rather than `git log | grep`.
2. **Proposals** — `docs/proposals/*.md` (accepted / in-flight design) and
   `docs/proposals/.workspace/*/` (drafts). A proposal that shipped and was
   trimmed/deleted is itself evidence the idea landed.
3. **Docs** — `docs/` proper (`architecture.md`, `state-machine.md`,
   `transports.md`, `hosts.md`, `docs/stories/*`, topic subfolders). A feature
   documented as shipped in `docs/` is strong evidence for `to: done`.

{% block spec_repo_context %}This is **kitsoki** — a deterministic-state-machine PoC for LLM workflows.
Shipped features live as runtime code (`internal/`), stories (`stories/`), and
narrative docs (`docs/`); design-in-flight lives in `docs/proposals/`. A
proposal is **deleted** once fully implemented, so its absence (with shipped
docs/code) means the idea is done, not lost.{% endblock %}

## Classification rules

- **`to: done`** — the item is fully shipped: cite the commit/proposal/doc that
  proves it. Many `## Ideas` and `## Partial` bullets already include a trailing
  `— <note>` saying what's done; corroborate it against the repo, don't trust it
  blindly.
- **`to: partial`** — meaningfully started but not finished (e.g. "X done; Y
  TBD"). Note in the evidence what landed and what remains.
- **Leave it alone** — if you cannot find concrete evidence, **omit the item**.
  Be conservative: a wrong move silently rewrites the operator's backlog. Never
  guess. Never move an item *into* `## Ideas`.

Copy the `item` text **verbatim** from the file (without the leading `- `), so
the reconcile script can find and move the exact line.

## Candidates (writer's-block helper)

Independently, pick **2-4** items still under `## Ideas` that would be the most
valuable to propose next. For each give a one-line `rationale` (impact /
unblocks-other-work / low-cost) and a rough `effort` (small | medium | large).
Rank them best-first. These are *suggestions to start a proposal from* — choose
ones that are concrete enough to write a proposal about.

## Output

Submit an `ideas_review` object (see `schemas/ideas-review.json`):
`{ reclassifications: [{ item, from, to, evidence, confidence }],
   candidates: [{ item, rationale, effort }], summary }`.
An empty `reclassifications` list is the correct answer when the backlog is
already current.
