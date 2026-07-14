Review whether any existing gate's recorded decisions justify dropping a determinism rung.

Job: `{{ args.job }}`
Target story tree: `{{ args.stories_dir }}`
Prepared source plan (JSON): {{ args.source_plan }}
Brief: `{{ args.brief_path }}`
Opportunity map: `{{ args.map_path }}`
Authored diff: `{{ args.diff_path }}`
Selected: {{ args.selected_theme }} ({{ args.selected_artifact_kind }})
Newly authored this loop: {{ args.author_summary }}

The determinism ladder (tools/session-mining/README.md): a gate climbs L2→L3→L4 as recorded decisions accumulate. An LLM-decided gate whose recorded decisions are dominated by one branch is a candidate to drop to a default rule (LLM only on low confidence), then to a pure rule.

Inspect the recorded decisions available (gate trace history / decision logs) and report any gate where one branch dominates enough to fit a default. It is entirely valid to find **no** ladder moves — report an empty `ladder_moves` honestly rather than inventing one.

Write `.artifacts/session-mining/{{ args.job }}/FINAL_REPORT.md` before returning. The report must summarize:

- what was found in the mining brief;
- which opportunity was selected and why;
- what was authored or changed;
- the validation result;
- the authored diff path;
- any ladder moves, or why none were claimed.

{% block spec_rubric %}Default threshold: propose L2→L3 only when one branch holds ≥ 85% of recorded decisions over a meaningful sample.{% endblock %}

Return `final_report_path`, `ladder_moves`, `ladder_summary_markdown`, and a `summary_markdown` close-out.
