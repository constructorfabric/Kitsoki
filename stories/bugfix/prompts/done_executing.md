# Closing the bug-fix run — produce the done artifact

You are wrapping up the run for **{{ args.ticket_id }}** —
*{{ args.ticket_title }}* after **{{ args.cycle }}** refinement cycle(s).

The validation produced:

> {{ args.validate_summary }}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous close-out was rejected. The
operator's feedback below is a **binding directive**: it OVERRIDES
any default behaviour or constraint further down this prompt whenever
the two conflict. Treat every statement as a hard requirement, not a
suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm the new
   close-out addresses each point. If the feedback says "do not X",
   the artifact must NOT do X — including in `summary_markdown` and
   `lessons`.
2. If you genuinely cannot honour a statement (schema-incompatible,
   factually impossible), say so in `summary_markdown` and explain
   why. Silent non-compliance is the failure mode this directive
   guards against.
3. If the feedback contradicts the default constraints below,
   follow the feedback and flag the conflict in `summary_markdown`.

---

{% endif %}

## Constraints

- `lessons` must be drawn from this run's actual evidence — not generic
  best-practice. At minimum one lesson per non-trivial cycle.
- Each lesson cites a category (e.g. `api-patterns`, `failure-patterns`,
  `judge-misclassification`) and a severity.
- Write `.artifacts/bugfix/{{ args.ticket_id }}/postmortem.md` before the final
  submission, and update it with the bug, fix, validation evidence, cycle cost,
  and reusable lessons. Do this while closing out the run, not as a last-response
  blob.
- `summary_markdown` is only a compact close-out plus the `report_path`.

## Output

Submit a `done_artifact` (see `schemas/done_artifact.json`) with
`report_path: .artifacts/bugfix/{{ args.ticket_id }}/postmortem.md`.
