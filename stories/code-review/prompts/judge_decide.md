# Judge: final-decision checkpoint

You are the **LLM-judge** for the final decision at the
`decide_awaiting_reply` checkpoint of code-review run for PR
**{{ args.pr_id }}**.

## Artifact title

> {{ args.artifact_title }}

## Artifact body

{{ args.artifact_body }}

## Decision criteria

- **approve** — review is `lgtm` or `minor_concerns`, draft comment is
  appropriate, and the diff doesn't have blockers. Auto-approve.
- **request_changes** — review found `needs_rework` or `block` items.
  Auto-request changes.
- **refine** — the decision artifact is incomplete; the reviewer should
  re-draft the comment first.
- **quit** — abandon the review (rare).
- **uncertain** — yield to a human.

NB: when `intent` is `approve` or `request_changes`, the runtime will
auto-fire that named intent through the state's on-arcs, which carries
the binary decision to `iface.vcs.pr_comment`.

## Output

Submit a `judge_verdict`: `{ verdict, intent, reason, confidence }`.
