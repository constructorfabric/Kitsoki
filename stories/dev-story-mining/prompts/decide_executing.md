Rank the ENRICH/GAP items from the map and select what to ticket next.

Map themes (JSON): {{ args.themes }}
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Rank ENRICH/GAP items by **(# distinct intents × how mechanical the surrounding recipe is)** — "mechanical" meaning the brief's validator can become a deterministic re-check, so the decider can start as `default-rule` rather than LLM. ALREADY-MODELED items are out of scope here.

Prefer the lowest-risk highest-value item first: an ENRICH with a deterministic validator beats a GAP requiring a whole new room. For each selected item give the exact `room` file path to edit (or the path a new room would live at) and a one-sentence `gate` description.

{% block spec_rubric %}Default: select the single top item for this pass (one gate per loop keeps each flow fixture reviewable); list runners-up in `rationale`.{% endblock %}

Return `selected`, `rationale`, and a `summary_markdown`.
