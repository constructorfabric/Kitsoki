# Review the PR — produce a review-summary artifact

You are reviewing PR **{{ args.pr_id }}** — *{{ args.pr_title }}* by
**{{ args.pr_author }}**.

Existing review comments (if any):

> {{ args.pr_comments }}

PR diff:

```
{{ args.pr_diff }}
```

{% if args.refine_feedback %}**Refinement feedback from the previous attempt
(cycle {{ args.cycle }}):**

> {{ args.refine_feedback }}
{% endif %}

## Constraints

- `summary_title` is the one-line review verdict ("LGTM with two nits.").
- `summary_markdown` is the structured rendering: high-level impression,
  blockers (if any), nits, suggested follow-ups. Aim for 100–500 words.
- `blockers` is the list of changes that MUST be made before approval.
- `nits` is the list of cosmetic / non-blocking comments.
- `overall_impression` is one of `lgtm | minor_concerns | needs_rework | block`.

## Output

Submit a `review_summary_artifact` (`schemas/review_summary_artifact.json`).
The `summary_markdown` is what the operator reads at the checkpoint —
write it for them.
