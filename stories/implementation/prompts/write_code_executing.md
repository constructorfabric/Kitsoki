# Write the code change — produce a code-artifact

You are writing the code change for task **{{ args.ticket_id }}** —
*{{ args.ticket_title }}* against `{{ args.workdir }}`.

Task understanding from the previous room:

> {{ args.task_summary }}

Acceptance criteria:

> {{ args.acceptance_criteria }}

{% if args.refine_feedback %}**Refinement feedback from the previous attempt
(cycle {{ args.cycle }}):**

> {{ args.refine_feedback }}
{% endif %}

## Constraints

- `summary_title` is the commit-message-friendly one-line description of
  the change (under 72 chars).
- After the first useful diagnosis, create
  `.artifacts/implementation/{{ args.ticket_id }}/code-report.md` and update it
  while you edit and verify. Include changed files, rationale, commands/results,
  and remaining risk. This report must exist before the final response; do not
  defer it to the end of the turn.
- `summary_markdown` is a one- or two-sentence checkpoint note that points to
  `report_path`, not a 200–500 word replacement for the report.
- `report_path` is `.artifacts/implementation/{{ args.ticket_id }}/code-report.md`.
- `affected_files` must be real, relative repo paths (no leading slash;
  must have an extension). At least one entry.
- `confidence` is your own estimate in [0.0, 1.0]; under 0.5 is rejected
  downstream.
- `reasoning` is the chain from "what the task asks" → "what code to
  write". Cite the acceptance criteria you're satisfying.
- `tests_planned` is the list of tests you intend the next room to
  exercise. It feeds the `test_executing` prompt.

## Output

Submit a `code_artifact` (`schemas/code_artifact.json`) including `report_path`.
