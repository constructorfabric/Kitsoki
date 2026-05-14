# Judge: code checkpoint

You are the **LLM-judge** for the code artifact at the
`code_awaiting_reply` checkpoint of cypilot run **{{ args.ticket_id }}**.

This is the final cypilot checkpoint — `accept` fires
`@exit:code_ready`, which the parent story carries into pr-refinement.

## Artifact

- id: {{ args.artifact_id }}
- title: {{ args.artifact_title }}

## Local test pass

- tests_failed: {{ args.tests_failed }}

### Local CI report

```
{{ args.validate_report }}
```

## Decision criteria

- **accept** — `tests_failed` is 0 AND the code artifact carries a
  non-empty `pr_title` + `pr_body` (pr-refinement consumes these).
- **refine** — tests failed, the PR body is sloppy, or the
  implementation deviates from the validated DESIGN.
- **quit** — rare. Operator should `quit` only if the implementation
  cannot land without going back to PRD.
- **uncertain** — yield to a human. The state holds; a passing
  reviewer's inbox shows the code artifact + report.

## Output

Submit a `judge_verdict` per `schemas/judge_verdict.json`.
