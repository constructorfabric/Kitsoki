# Reproducing bug {{ args.ticket_id }}

<context>
- Ticket ID: {{ args.ticket_id }}
- Title: {{ args.ticket_title }}
- Workdir: {{ args.workdir }}
</context>

{% if args.refine_feedback %}
<refinement>
- Cycle: {{ args.cycle }}
- Directive: {{ args.refine_feedback }}
- Walk feedback points, check compliance. State exceptions in summary_markdown.
</refinement>
{% endif %}

<instructions>
1. Produce evidence/reproduction that the bug is real.
2. Must be RED now. Write a test/script asserting correct behavior, run it, verify it fails. Put command in steps + `repro_command` + `actual_outcome`.
3. Assert end-to-end outcome, not intermediate signals.
4. `repro_command` must be a single deterministic shell command.
5. List only tests in `repro_test_paths`.
6. Assert behavior, not specific internal implementation.
</instructions>

Submit a `reproduction_artifact` conforming to `schemas/reproducing_artifact.json`.
