# Validating the fix — produce the validation artifact

You are validating the applied fix for **{{ args.ticket_id }}** —
*{{ args.ticket_title }}* against the full environment.

{% if args.ticket_body %}## Public ticket details

```markdown
{{ args.ticket_body }}
```

Reject a result whose public compatibility/API terms are not evidenced by the
committed change and focused tests.

{% endif %}

The review found:

> {{ args.review_body }}

The build / deploy / validation run produced:

```
{{ args.build_log }}
```

{% if args.ide_connected %}The attached editor reports **{{ args.ide_diagnostics_count }}** diagnostic(s)
in its Problems panel:

```
{{ args.ide_diagnostics }}
```

Weigh these alongside the build log — a clean build with live editor
diagnostics still open is not a clean pass.
{% else %}No editor is attached (`host.ide.get_diagnostics` reports
`connected: false`) — diagnostics are unavailable for this run; judge on the
build log and review alone.
{% endif %}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous validation was rejected. The
operator's feedback below is a **binding directive**: it OVERRIDES
any default behaviour or constraint further down this prompt whenever
the two conflict. Treat every statement as a hard requirement, not a
suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm the new
   validation addresses each point. If the feedback says "do not X",
   the artifact must NOT do X — including in `summary_markdown`,
   `outcome`, and `next_action_hint`.
2. If you genuinely cannot honour a statement (schema-incompatible,
   factually impossible), say so in `summary_markdown` and explain
   why. Silent non-compliance is the failure mode this directive
   guards against.
3. If the feedback contradicts the default outcomes / constraints
   below, follow the feedback and flag the conflict in
   `summary_markdown`.

---

{% endif %}

## Outcomes

- `pass` — the bug's reproduction now produces the expected outcome and
  no other regressions surfaced. Before choosing `pass`, independently read
  the ticket and the regression test: enumerate every observable promise in
  `summary_markdown` and cite the assertion/evidence for each. A passing test
  that verifies only one symptom is `fail_short`, with the unasserted promise
  named in `next_action_hint`; do not let a refreshed state/view stand in for a
  required user-visible message, transcript, side effect, persisted value, or
  terminal state.
- `fail_short` — a minor adjustment to the implementation will fix it
  (control returns to `implementing`).
- `fail` — the fix is wrong; the proposal needs to be redrafted (control
  returns to `proposing`).
- `infra_error` — the environment was unreachable / broken in a way that
  has nothing to do with the fix.

## Output

Submit a `validate_artifact` (see `schemas/validating_artifact.json`).
`next_action_hint` is consumed by the next iteration's prompt to steer
the refinement — be specific about what should change.
