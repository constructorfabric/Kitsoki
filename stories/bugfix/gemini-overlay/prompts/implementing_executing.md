# Implementing fix for {{ args.ticket_id }}

<context>
- Ticket ID: {{ args.ticket_id }}
- Title: {{ args.ticket_title }}
- Workdir: {{ args.workdir }}
- Proposal: {{ args.fix_description }}
- Root cause: {{ args.root_cause }}
- Affected files: {{ args.affected_files }}
</context>

{% if args.refine_feedback %}
<refinement>
- Cycle: {{ args.cycle }}
- Directive: {{ args.refine_feedback }} (Overrides proposal/constraints)
- State exceptions in summary_markdown.
</refinement>
{% endif %}

<instructions>
1. Apply proposal edits relative to `{{ args.workdir }}`.
2. Build project to ensure compilation.
3. Run targeted tests (repro and changed packages). Must pass.
4. Ensure no regression in neighbouring tests.
5. smallest, most local changes preferred.
6. Do not run `git commit`. Leave worktree dirty.
</instructions>

Submit an `implementing_artifact` conforming to `schemas/implementing_artifact.json`.
