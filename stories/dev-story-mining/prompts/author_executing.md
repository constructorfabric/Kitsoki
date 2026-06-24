Author the selected gate into the target room, with a flow fixture, and prove it green.

Selected item (JSON): {{ args.selected }}
Target story tree: `{{ args.stories_dir }}`
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Follow the `kitsoki-story-authoring` skill:
1. Add the decider gate to the named room YAML. Start the decider at the kind the decision-record dictates (default-rule where the validator is deterministic, LLM where it is not). Wire the brief's validator as the gate's deterministic re-check.
2. Write a **cassette-free** flow fixture under that story's `flows/` covering each branch — seed the artifacts in `initial_world` so `once:` short-circuits any agent call (no live LLM, per AGENTS.md). Assert the side effects, not just `next_state`.
3. Run `kitsoki test flows <story>/app.yaml` and confirm green.

{% block spec_project_context %}{% endblock %}

Do **not** call git — the operator owns commits. Return `files_changed`, `flow_files`, `flows_green` (true only if the flow test actually passed), and a `summary_markdown`.
