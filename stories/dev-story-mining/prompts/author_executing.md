Author the selected Kitsoki improvement, with no-LLM coverage, and prove it green.

Selected item (JSON): {{ args.selected }}
Prepared source plan (JSON): {{ args.source_plan }}
Target story tree: `{{ args.stories_dir }}`
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Follow the `kitsoki-story-authoring` skill:

1. Produce the artifact kind selected by the decide room:
   - `ENRICH-STORY`: add the named gate/room/import to an existing story.
   - `NEW-STORY`: create the smallest useful story with README and flows.
   - `STARLARK-SCRIPT`: add `scripts/*.star`, `*.star.yaml`, the invoking room, and a flow that runs or stubs it appropriately.
   - `HUB-ROUTE`: add the route/entry that makes the Kitsoki path the natural path.
   - `SKILL-ONLY`: update/add the skill only when a state machine would be surprising.
   - `ENFORCEMENT-LIMIT`: record the honest platform limit and add the strongest non-fabricated mitigation.
2. Wire progressive determinism:
   - L2: deterministic skeleton + named human/LLM gate with recorded decisions.
   - L3: default rule for the common case, human/LLM only on low confidence.
   - L4: deterministic rule, no model.
3. Write **no-LLM** coverage under the touched story's `flows/` or the relevant deterministic test. Seed artifacts/cassettes so tests never call a live LLM.
4. Run the focused validation and report the exact command.
5. Write a unified diff artifact for the authored changes and return its path as `diff_path`. Prefer `.artifacts/session-mining/<job>/author.diff` when the job id is available from the selected item/source plan. The diff must be a real unified diff of the changes you made; do not summarize a diff in prose.
6. After the first useful diagnosis, create a durable authoring report beside
   the diff (prefer `.artifacts/session-mining/<job>/author-report.md`) and
   update it during edits and validation. It records the selected improvement,
   changed files, exact no-LLM gate, outcome, and any remaining limitation.

{% block spec_project_context %}{% endblock %}

Do **not** create commits — the operator owns commits. Return `artifact_kind`, `files_changed`, `files_changed_display`, `diff_path`, `report_path`, `applied_improvements`, `flow_files`, `validation`, `flows_green` (true only if focused validation actually passed), and a compact `summary_markdown` that points to the report.
