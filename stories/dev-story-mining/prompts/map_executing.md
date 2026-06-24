Cross-reference the mined brief's decision gates against the existing gate inventory of the target story tree, and classify each theme.

Brief: `{{ args.brief_path }}`
Target story tree: `{{ args.stories_dir }}`
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Process (the repeatable step ② from `.context/dev-story-from-transcripts.md`):

1. **Mined gates** — read every `- **gate:**` line in the brief. Keep only the dev inner-loop themes (action tags: implement-from-spec, verify-by-running, debug-from-error-or-trace, add-test-coverage, fix-failing-tests, explore-codebase, fan-out-agents-and-reconcile, review-feedback, build-compile-fix-loop, dev parts of commit-or-pr). Drop harness/infra gates the target story does not model (worktree cleanup, CI release config, web-UI/demo QA, proposal authoring, session-mining itself).
2. **Existing gates** — regenerate the inventory; do not work from memory:
   `grep -rn "agent.decide\\|agent.task" {{ args.stories_dir }}/{implementation,bugfix,prd,code-review,pr-refinement,dev-story}/rooms/*.yaml`
   For each, note (room, what it decides in plain English).
3. **Cluster** mined gates into decision themes; count distinct intents per theme.
4. **Classify** each theme exactly one of `ALREADY-MODELED` (name the existing gate), `ENRICH` (room exists, no gate for this fork), or `GAP` (no room models it). Record `home_room` (file path) and the `validator` lifted from the brief.

{% block spec_project_context %}{% endblock %}

Return the `themes` array and a `summary_markdown` table. Be concrete; cite room file paths.
