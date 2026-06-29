# Proposing a fix — produce the fix-proposal artifact

You are proposing a fix for **{{ args.ticket_id }}** — *{{ args.ticket_title }}*
against `{{ args.workdir }}`.

You have a reproduction artifact from the previous room:

> {{ args.reproduction_summary }}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous fix proposal was rejected. The
operator's feedback below is a **binding directive**: it OVERRIDES
any default behaviour or constraint further down this prompt whenever
the two conflict. Treat every statement as a hard requirement, not a
suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm the new
   proposal addresses each point. If the feedback says "do not X",
   the proposal must NOT do X — including in `fix_description`,
   `root_cause`, and `affected_files`.
2. If you genuinely cannot honour a statement (schema-incompatible,
   factually impossible), say so in `summary_markdown` and explain
   why. Silent non-compliance is the failure mode this directive
   guards against.
3. If the feedback contradicts the default constraints below,
   follow the feedback and flag the conflict in `summary_markdown`.

---

{% endif %}

## Constraints

- `fix_description` must be concrete — not "fix the bug". Describe the
  edit in enough detail that another engineer could implement it without
  rereading the ticket.
- `root_cause` must explain *why* the bug happens, not *what* the bug is.
  Cite the offending file / line where possible.
- `affected_files` must be real, relative paths in the repo (no leading
  slash; must have an extension). At least one.
- `confidence` is your own estimate in [0.0, 1.0]; under 0.3 is rejected
  downstream.
- `reasoning` is the chain from evidence → cause → fix.
- Propose the **smallest, most local** change that addresses the root cause.
  Favour a narrow edit at the bug's own site over a broad change to shared or
  engine-level internals — the latter is far likelier to break unrelated
  tests. The fix must resolve the bug WITHOUT regressing existing behaviour; if
  the only correct fix is invasive, say so and name the call sites most at risk
  so the implementer verifies them.

## Output

Submit a `propose_fix_artifact` (see `schemas/proposing_artifact.json`).
The `summary_markdown` field is what a human reviewer reads at the
checkpoint — write it for them: bug, cause, fix, files, confidence.
