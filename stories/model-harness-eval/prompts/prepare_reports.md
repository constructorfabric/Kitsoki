Prepare model/harness evaluation artifacts for this operator question:

`{{ args.question }}`

{% if args.refine_feedback %}Refine feedback from the operator: {{ args.refine_feedback }}{% endif %}

Write or refresh these artifacts:

- Markdown report: `{{ args.markdown_path }}`
- Slide-style HTML deck: `{{ args.deck_path }}`
- Machine summary JSON: `{{ args.summary_path }}`

Use the reusable offline pilot process in `docs/testing/model-harness-eval-pilot.md`.

Default no-cost evidence collection:

1. Create `{{ args.output_root }}/intent-reports/`.
2. Run static intent suites that already have local recordings. At minimum, run:
   `go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml --harness static --json {{ args.output_root }}/intent-reports/oregon-trail.json`
   A non-zero exit is acceptable if the command writes JSON; failed fixtures are evidence.
3. Run the committed transcript-derived coverage flagship:
   `bash tools/session-mining/examples/git-ops/run.sh --keep {{ args.output_root }}/git-ops-coverage`
4. Aggregate all local evidence:
   `python3 tools/session-mining/eval_pilot_report.py --root stories --intent-root {{ args.output_root }}/intent-reports --coverage-root {{ args.output_root }} --markdown {{ args.markdown_path }} --deck {{ args.deck_path }} --summary {{ args.summary_path }}`

Do not call live LLM providers or run a paid live benchmark matrix. If the
operator's question asks for live provider performance, answer from accepted
local evidence and list the missing live collection step as a limitation.

Return a JSON object matching `schemas/report_artifact.json`. Every count and
path must be read from the generated files or command results; do not invent
numbers. `summary_markdown` should include:

- the question answered;
- the headline result;
- effectiveness/speed/cost evidence available;
- intent-suite and transcript-coverage results;
- missing evidence/readiness gaps;
- the exact artifact paths.
