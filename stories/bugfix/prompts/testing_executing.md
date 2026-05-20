# Reviewing tests & implementation — produce the testing artifact

You are reviewing the implementation of the fix for **{{ args.ticket_id }}** —
*{{ args.ticket_title }}*.

The proposed fix was:

> {{ args.fix_summary }}

The CI / test run produced:

```
{{ args.ci_log }}
```

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous test review was rejected. The
operator's feedback below is a **binding directive**: it OVERRIDES
any default behaviour or constraint further down this prompt whenever
the two conflict. Treat every statement as a hard requirement, not a
suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm the new
   review addresses each point. If the feedback says "do not X",
   the review must NOT do X — including in `summary_markdown`,
   `status`, `tests_added`, and `blockers`.
2. If you genuinely cannot honour a statement (schema-incompatible,
   factually impossible), say so in `summary_markdown` and explain
   why. Silent non-compliance is the failure mode this directive
   guards against.
3. If the feedback contradicts the default constraints below,
   follow the feedback and flag the conflict in `summary_markdown`.

---

{% endif %}

## Constraints

- `status` must be `passed` only if the tests actually ran and the bug
  reproduction now produces the expected outcome. `blocked` is for
  unrunnable suites (compile error, missing dependency); `failed` is for
  ran-but-broken.
- `tests_added` must list new / modified test files. Reuse existing tests
  where possible; only add fresh ones if no existing test covers the bug.
- `blockers` are review-grade objections that must be fixed before the
  PR can advance.

## Output

Submit an `implement_review_artifact` (see `schemas/testing_artifact.json`).
The `summary_markdown` should walk the reviewer through tests-added,
tests-run results, and any blockers in plain prose.
