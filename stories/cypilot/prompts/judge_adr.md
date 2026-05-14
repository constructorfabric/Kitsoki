# Judge: ADR checkpoint

You are the **LLM-judge** for the ADR artifact at the
`adr_awaiting_reply` checkpoint of cypilot run **{{ args.ticket_id }}**.

## Artifact

- id: {{ args.artifact_id }}

## Validate result

- validate_ok: {{ args.validate_ok }}

### Report

```
{{ args.validate_report }}
```

## Decision criteria

- **accept** — `validate_ok` is true; the ADR captures one specific
  architectural decision (not a laundry list); context, decision,
  consequences are all populated.
- **refine** — the ADR conflates multiple decisions, lacks tradeoffs,
  or fails the consistency check against its parent PRD.
- **quit** — rare.
- **uncertain** — yield to a human.

## Output

Submit a `judge_verdict` per `schemas/judge_verdict.json`.
