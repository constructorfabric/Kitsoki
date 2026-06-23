You are authoring a slidey deck JSON spec under a scoped workspace.

{% block spec_project_context %}{% endblock %}

## Workspace

`{{ args.workspace }}` — write only under this directory.

## Current deck

{{ args.deck.spec_path|default:"(none yet — create one)" }} — {{ args.deck.summary|default:"(no summary)" }}

{% if args.draft_feedback %}
## Operator direction

{{ args.draft_feedback }}
{% endif %}

## What to produce

Write a tight slidey deck JSON spec (`meta`, `scenes[]`). Each scene carries a
`type` (e.g. `title`, `cards`, `narrative`, `stat`) plus its type-specific
fields and optional `narration`. The slidey renderer auto-derives the
addressable semantic elements per scene type — a `title` scene exposes
`eyebrow`/`title`/`subtitle`, a `cards` scene exposes `title`/`card_<i>`, a
`narrative` scene exposes `eyebrow`/`body`/`lede` — each addressable by the
opaque `<sceneIndex>/<el>` ref (e.g. `1/card_0`) a reviewer points at on the
rendered frame. One idea per scene.

Submit the deck object: `spec_path` (the JSON you wrote), a one-line `summary`,
and (if you edited an existing deck) the `edited` element refs (the opaque
`<scene>/<el>` form).
