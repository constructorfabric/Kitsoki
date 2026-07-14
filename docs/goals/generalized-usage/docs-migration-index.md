# Docs-migration index — generalized-usage

Ground truth for `tools/lint-docs-migrated.py` (change WP.1): the migration
discipline lint that keeps shipped, proposal-driven changes from leaving stale
planning material behind.

Per [`docs/proposals/README.md`](../../proposals/README.md): when a
proposal-driven change ships, its **source proposal must be gone-or-trimmed**
and an **authoritative narrative page must exist under `docs/`**. This table is
the mapping the lint checks. Add a row whenever a proposal-driven **WS/WM/WB**
(or other) change reaches `integrated` in the goal-seeker ledger.

Columns:

- **change** — the ledger change id.
- **source proposal** — the `docs/proposals/<x>.md` it grew from, or `-` if the
  proposal was fully deleted.
- **narrative doc** — the authoritative `docs/**` page for the shipped capability.

| change | source proposal | narrative doc |
|--------|-----------------|---------------|
<!-- No proposal-driven change has reached `integrated` in this goal yet.
     The changes shipped so far (0.2 .mcp.json, 4.5 getting-started, 0.5 README,
     WM.0/2.3/WM.1 goal-seeker machinery) grew from the decomposition itself,
     not from a docs/proposals/*.md, so there is nothing to migrate yet.
     Append a row here as each proposal-driven WS/WM/WB change lands. -->

The lint is exercised by fixtures under
[`tools/testdata/lint-docs-migrated/`](../../../tools/testdata/lint-docs-migrated):
`green/` (proposal trimmed + doc present → passes) and `red/` (proposal still
present unedited + doc missing → fails).
