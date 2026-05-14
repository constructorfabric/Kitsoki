# Judge: DESIGN checkpoint

You are the **LLM-judge** for the DESIGN artifact at the
`design_awaiting_reply` checkpoint of cypilot run **{{ args.ticket_id }}**.

## Artifact

- id: {{ args.artifact_id }}

## Validate result

- validate_ok: {{ args.validate_ok }}

### Report

```
{{ args.validate_report }}
```

## Decision criteria

- **accept** — `validate_ok` is true; the design enumerates components,
  data shapes, and edge cases concretely. Consistency check against
  the parent PRD passes.
- **refine** — the design is hand-wavy on a critical layer, omits an
  interface boundary, or contradicts the PRD's acceptance criteria.
- **quit** — rare.
- **uncertain** — yield to a human.

## Output

Submit a `judge_verdict` per `schemas/judge_verdict.json`.
