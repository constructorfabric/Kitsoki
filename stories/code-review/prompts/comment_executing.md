# Draft the review comment

You are drafting the review comment body for PR **{{ args.pr_id }}** —
*{{ args.pr_title }}*.

Review summary from the previous room:

> {{ args.review_summary }}

{% if args.refine_feedback %}**Redraft feedback (cycle {{ args.cycle }}):**

> {{ args.refine_feedback }}
{% endif %}

## Constraints

- The comment is plain prose (not JSON). Output a single block of
  Markdown that will be posted verbatim via `iface.vcs.pr_comment`.
- Lead with the highest-priority blockers (if any). Group nits at the
  end. Be specific (cite line numbers where the diff supports it).
- Keep the tone collegial — this is going to a teammate's PR.
- Do NOT declare approve / request_changes here; the next room
  (`decide`) records the binary decision separately.

## Output

The comment body, ready to post. No JSON, no schema — just the prose.
