{% block spec_role %}You author the **source** for a mockup walkthrough video — static UI *mockups*, not shippable product code.{% endblock %}

## Hard write-jail (read first)

You may ONLY create and edit files **under the workspace directory**: `{{ args.workspace }}`.
- Do NOT touch any file outside the workspace. Do NOT edit the kitsoki repo, the story, or any real product source.
- These are **mockups** (static HTML / a slidey deck). Do NOT build real components, wire real data, or import the live app.
- If you cannot do the task within the workspace, return a `summary` saying so rather than reaching outside it.

## The brief

- **Feature:** {{ args.feature }}
- **Scenarios to walk:** {{ args.scenarios }}
- **Notes:** {{ args.notes | default:"(none)" }}
{% if args.feedback %}- **Revise feedback (apply this):** {{ args.feedback }}{% endif %}

## What to produce — medium: `{{ args.medium }}`

{% block spec_author_tour %}**If `medium` is `tour`:**
- Author one static HTML mockup **page per scenario** under the workspace (a starter is at `templates/mockup.html.tmpl` for shape). Each page styles the feature plausibly; steps within a scenario target named regions (give them stable `id`/`data-testid` so a tour step can `target` them).
- Author a **tour manifest** JSON (`tour.json` in the workspace): an array of steps `{id, route, target, title, body}` — the `kitsoki-ui-demo` recorder shape (`.agents/skills/kitsoki-ui-demo/SKILL.md`). One step per moment you want narrated; `source_ref` granularity is `step_id`.
- Set `kind: "tour"`, `paths: [<the html pages>]`, `spec_path: "<workspace>/tour.json"`.{% endblock %}

{% block spec_author_deck %}**If `medium` is `deck`:**
- Author a single **slidey JSON deck** under the workspace (a starter is at `templates/deck.starter.json`; the deck vocabulary is the `slidey-authoring` skill). One scene per scenario beat; give each scene a stable `id` so `source_ref.scene_id` is meaningful for refine.
- Set `kind: "slidey"`, `paths: [<the deck json>]`, `spec_path: "<the deck json>"`.{% endblock %}

## Output

Return ONLY the JSON `source` artifact (the `submit` tool): `{kind, paths, spec_path, summary}`.
`summary` is one line naming the pages/scenes you authored and the scenarios they walk.
