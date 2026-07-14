Cross-reference the mined automation brief against the existing Kitsoki story/script/hook inventory, and classify each opportunity.

Job: `{{ args.job }}`
Brief: `{{ args.brief_path }}`
Prepared source plan (JSON): {{ args.source_plan }}
Target story tree: `{{ args.stories_dir }}`
Target artifact classes: `{{ args.target_artifacts }}`
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Process:

1. **Mined opportunities** — read the brief and keep opportunities whose outcome would make developer work run through Kitsoki more often or more deterministically. Include missed use of an existing story, missing room/gate, deterministic Starlark glue, flow/cassette fixture, hub-route/intercept entry, or skill-only guidance.
2. **Existing inventory** — regenerate, do not work from memory. Inspect:
   - `{{ args.stories_dir }}/**/app.yaml`
   - `{{ args.stories_dir }}/**/rooms/*.yaml`
   - `{{ args.stories_dir }}/**/scripts/*.star`
   - `.agents/skills/*/SKILL.md`
   - `cmd/kitsoki/hook.go`
   - `docs/architecture/prompt-intercept.md`
3. **Classify** every in-scope opportunity with:
   - `artifact_kind`: `EXISTING-STORY`, `ENRICH-STORY`, `NEW-STORY`, `STARLARK-SCRIPT`, `HUB-ROUTE`, `SKILL-ONLY`, `ENFORCEMENT-LIMIT`, or `OUT-OF-SCOPE`.
   - `status`: `ALREADY-MODELED`, `ACTIONABLE`, `LIMITED`, or `OUT-OF-SCOPE`.
   - `target_path`: exact file path to edit/create, or the doc/code path that explains the limit.
   - `validator`: no-LLM flow/cassette/test/trace validation, or a recorded-decision gate for L2.
   - `determinism_rung`: current/starting rung (usually L2 for story/script skeletons, L1 for skills, LIMITED for hard platform gaps explained as `ENFORCEMENT-LIMIT` with an L1/L2 mitigation).
   - `kitsoki_usage_policy`: how this routes work into Kitsoki; for Codex, be explicit that hard pre-model interception is unavailable today.
4. **Cluster** duplicate opportunities and count distinct mined intents per cluster.
5. **Write the review artifact** under `.artifacts/session-mining/{{ args.job }}/OPPORTUNITY_MAP.md`. The markdown file must contain the summary table, status counts, concrete target paths, and validators. Do not write source files in this room.

{% block spec_project_context %}{% endblock %}

Return the `opportunities` array, `map_path`, display strings for `opportunity_count_display`, `actionable_display`, `limited_display`, and `already_modeled_display`, plus a `summary_markdown` table. Be concrete; cite file paths.
