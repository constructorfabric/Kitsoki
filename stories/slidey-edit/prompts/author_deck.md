You are authoring or editing a slidey deck JSON spec through the Slidey MCP.

The relevant slidey authoring contract is provided here. Do not look for or
invoke skills, SKILL.md files, `.agents/skills`, or `.claude/skills`; this
dispatched task is intentionally self-contained.

Do not use shell commands or generic filesystem tools. The only allowed deck
IO is the Slidey MCP:

- `slidey_workspace_tree` to discover editable deck specs under the MCP root
- `slidey_read_spec` to inspect an existing deck
- `slidey_write_spec` to create or replace a deck spec
- `slidey_patch_spec` or `slidey_remove_slide` for focused edits
- `slidey_schema`, `slidey_layout_gallery`, and `slidey_validate` for authoring
  help and validation

If you are running in Codex and one of these tools is not immediately visible,
call `tool_search` for the exact Slidey tool name, then use the returned tool.
Do not report the Slidey MCP as unavailable until `tool_search` has failed for
the needed `slidey_*` tool and for `submit`.

{% block spec_project_context %}{% endblock %}

## Workspace

Repository workspace: `{{ args.workspace }}`
Managed workdir: `{{ args.workdir|default:"(current checkout)" }}`

The Slidey MCP root is this workspace. When calling Slidey MCP tools, use the
workspace-relative path, not the repository path joined onto the workspace.

## Existing deck to edit

Repository path: `{{ args.source_deck.spec_path|default:"(none — create a new deck)" }}`
Workspace-relative path: `{{ args.source_deck.workspace_spec_path|default:args.source_deck.spec_path|default:"(none — create a new deck)" }}`

{{ args.source_deck.summary|default:"(no summary)" }}

{% if args.deck.spec_path %}
## Current draft cache

Repository path: `{{ args.deck.spec_path }}`
Workspace-relative path: `{{ args.deck.workspace_spec_path|default:args.deck.spec_path }}`

{{ args.deck.summary|default:"(no summary)" }}
{% endif %}

{% if args.draft_feedback %}
## Operator direction

{{ args.draft_feedback }}
{% endif %}

## What to produce

If an existing deck path is supplied, call `slidey_read_spec` on the
workspace-relative path first and edit it in place or write a revised sibling
spec under the MCP root, preserving its existing intent unless the operator
direction says otherwise.

If you create a new deck, prefer a filename ending in `.slidey.json` unless you
are replacing an existing `.json` deck.

Before submitting, call `slidey_validate` on the workspace-relative path you
wrote. In the submitted object, `spec_path` must be the repository-render path,
not an absolute filesystem path: use the provided repository path when
editing/replacing it, or join the repository workspace with the new
workspace-relative filename when creating a new sibling.

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
- `objectives`: `type`, optional `title`, `items[]`, optional `caption`, optional
  `narration`. Each item must include `label`, `status`, and `detail`. Use this
  for report/eval objective status; `status: "done"` renders a large green
  checkmark and `status: "issue"`/`"blocked"` renders a large exclamation mark.
- `narrative`: `type`, `eyebrow`, `lede`, `body`, optional `narration`.
  Semantic elements: `eyebrow`, `lede`, `body`.
- Other known slidey scene types (for this story) are also allowed when useful,
  including `book`, `diagram-svg`, `table`, `thread`, `trace`, `cta`,
  `personas`, `video`, `evidence`, `stat`, and `personas`.
  Prefer `title`, `cards`, and `narrative` when you need the most predictable
  annotation targeting.

Deck-level edits are allowed:

- add, remove, or reorder scenes in `scenes`
- change deck `meta` (theme, resolution, narration, `title`)
- swap scene `type` to change layout/format
- update diagram `panels`/`nodes`/`edges`

The renderer addresses semantic elements with opaque refs of the form
`<sceneIndex>/<el>` such as `1/card_0`. Do not invent separate `id`, `heading`,
or `elements` wrappers unless the existing deck already uses them. One idea per
scene.

## Readability rules

Slidey decks are visual reports, not prose documents. Make every scene readable
at a glance:

- Lead with the actual verdict, objective state, or decision. Do not make the
  reader infer the useful output from a paragraph.
- For report or eval decks, include an early objective-status scene with explicit
  labels such as `done`, `progress`, `blocked`, `issue`, or `next`, plus the
  concrete evidence or missing condition for each label. Use `type:
  "objectives"` for that scene unless an existing deck cannot render it yet.
- Prefer `cards` for 3-5 parallel points and `table` for statuses, commands,
  costs, scores, or per-target comparisons.
- Use `narrative` only for one short claim. Keep `body` to at most 30 words and
  avoid joining multiple results, commands, and caveats into one sentence.
- Put long explanations, chronology, raw logs, and stack traces in `.context/`
  or supporting reports. In the deck, cite the artifact path in a card or table
  cell.
- Commands should be their own table cells or short card subtitles, not buried
  inside narrative prose.
- Before submitting, scan the deck for any slide that would render as a wall of
  text. If a scene has more than one comma-heavy sentence or combines multiple
  facts with "while", "and", or "then", split it into cards/table rows or
  multiple scenes.

Submit the deck object: `spec_path` (the repository-render path for the JSON you
wrote), a one-line `summary`, and (if you edited an existing deck) the `edited`
element refs (the opaque `<scene>/<el>` form).
