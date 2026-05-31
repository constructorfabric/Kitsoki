# Draft the PRD — write the document, then summarise it

You are the **author**. Write a product requirements document for this
idea:

> {{ args.idea }}

against the working directory `{{ args.workdir }}`.

{% if args.upstream_paths %}## Existing requirement docs

Read these before writing — fold their constraints into the PRD and do
not contradict them:

> {{ args.upstream_paths }}
{% endif %}

{% if args.clarification_log %}## Clarification transcript

Every Q&A round so far, newest first. These answers are the operator's
authoritative input — honour them:

{{ args.clarification_log }}
{% endif %}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous draft was rejected. The feedback
below is a **binding directive**: it OVERRIDES any default structure or
constraint further down whenever the two conflict. Treat every statement
as a hard requirement.

> {{ args.refine_feedback }}

Before submitting, walk the feedback statement-by-statement and confirm
the new draft addresses each point. If you cannot honour a statement,
say so in `summary_markdown` and explain why — silent non-compliance is
the failure mode this guards against.

---
{% endif %}

## What to write

1. Write the full PRD as markdown to **`{{ args.output_path }}`** (relative
   to the working directory) using `Write`. A solid PRD covers: problem &
   context, target users, goals & non-goals, requirements (functional and
   non-functional), success metrics, and open questions.
2. Write for a reader who has not seen the idea or the transcript.

## Self-assessment (be honest)

- Set `needs_clarification: true` and populate `follow_up_questions` when
  the inputs left material gaps you had to guess at. The operator will
  route those into another clarification round. Set it `false` only when
  the PRD genuinely stands on the input you were given.
- `confidence` is your own estimate in [0.0, 1.0] that the PRD is solid.

## Output

Submit a `prd_artifact` (see `schemas/prd_artifact.json`): `title`,
`summary_markdown` (the checkpoint view — the PRD body or a faithful
digest), `file_path` (where you wrote it), `confidence`,
`needs_clarification`, and `follow_up_questions`.
