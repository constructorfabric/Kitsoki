You are authoring or editing a slidey deck JSON spec under a scoped workspace.

The relevant slidey authoring contract is provided here. Do not look for or
invoke skills, SKILL.md files, `.agents/skills`, or `.claude/skills`; this
dispatched task is intentionally self-contained.

{% block spec_project_context %}{% endblock %}

## Workspace

`{{ args.workspace }}` — write only under this directory.

## Existing deck to edit

{{ args.source_deck.spec_path|default:"(none — create a new deck)" }} — {{ args.source_deck.summary|default:"(no summary)" }}

{% if args.deck.spec_path %}
## Current draft cache

{{ args.deck.spec_path }} — {{ args.deck.summary|default:"(no summary)" }}
{% endif %}

{% if args.draft_feedback %}
## Operator direction

{{ args.draft_feedback }}
{% endif %}

## What to produce

If an existing deck path is supplied, read that spec first and edit it in place
or write a revised sibling spec under the workspace, preserving its existing
intent unless the operator direction says otherwise.

Write a tight slidey deck JSON spec with this shape:

```json
{
  "meta": {
    "title": "Deck title",
    "resolution": { "width": 1920, "height": 1080 },
    "theme": "rose-pine-moon",
    "narration": { "voice": "en-AU-NatashaNeural", "rate": "+0%" }
  },
  "scenes": []
}
```

Supported scene contracts for this story:

- `title`: `type`, `eyebrow`, `title`, `subtitle`, optional `narration`.
  Semantic elements: `eyebrow`, `title`, `subtitle`.
- `cards`: `type`, `variant`, `title`, `cards[]`, optional `narration`.
  Each card should include `label` plus `sub` or `lines[]`.
  Semantic elements: `title`, `card_<i>`.
- `narrative`: `type`, `eyebrow`, `lede`, `body`, optional `narration`.
  Semantic elements: `eyebrow`, `lede`, `body`.
- Other known slidey scene types such as `stat`, `chart`, and `cta` are allowed
  when useful, but prefer `title`, `cards`, and `narrative` for predictable
  annotation targets.

The renderer addresses semantic elements with opaque refs of the form
`<sceneIndex>/<el>` such as `1/card_0`. Do not invent separate `id`, `heading`,
or `elements` wrappers unless the existing deck already uses them. One idea per
scene.

Submit the deck object: `spec_path` (the JSON you wrote), a one-line `summary`,
and (if you edited an existing deck) the `edited` element refs (the opaque
`<scene>/<el>` form).
