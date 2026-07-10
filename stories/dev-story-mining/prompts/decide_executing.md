Rank the actionable opportunities from the map and select what to apply next.

Opportunities (JSON): {{ args.opportunities }}
Prepared source plan (JSON): {{ args.source_plan }}
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Rank actionable items by **(# distinct intents × how mechanical the surrounding recipe is × Kitsoki-adoption leverage)**. "Mechanical" means the validator can become a deterministic re-check or a recorded L2 gate, so the next artifact is not just prose.

Ignore `ALREADY-MODELED` and `OUT-OF-SCOPE` items. Treat `ENFORCEMENT-LIMIT` as actionable only when the output is an honest route/issue/doc change, not a fake claim of enforcement.

Prefer the lowest-risk highest-value item first:
- `STARLARK-SCRIPT` or `ENRICH-STORY` with a deterministic validator is usually best.
- `HUB-ROUTE` is high leverage when it prevents missed Kitsoki usage.
- `NEW-STORY` is worth it when no existing room owns the workflow.
- `SKILL-ONLY` should win only when state-machine automation would surprise users.

For each selected item, give `artifact_kind`, `target_path`, optional `room`, a one-sentence `gate`, `determinism_from`, `determinism_to`, and `acceptance_gate`.

{% block spec_rubric %}Default: select the single top item for this pass (one improvement per loop keeps each flow fixture reviewable); list runners-up in `rationale`.{% endblock %}

Return `selected`, `rationale`, and a `summary_markdown`.
