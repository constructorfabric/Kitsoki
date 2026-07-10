You are editing a slidey deck behind a LOCATION-TIED annotation. The operator
pointed at a specific place on the rendered deck and left an instruction. Your
job is to resolve the anchor to the scene/element it targets and apply the
instruction THERE — never anywhere else, never silently dropped.

The relevant slidey editing contract is provided here. Do not look for or
invoke skills, SKILL.md files, `.agents/skills`, or `.claude/skills`; this
dispatched task is intentionally self-contained.

Do not use shell commands or generic filesystem tools. The only allowed deck
IO is the Slidey MCP. Read the deck with `slidey_read_spec`, apply the focused
change with `slidey_patch_spec` or `slidey_write_spec`, and call
`slidey_validate` before submitting.

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

## Deck

Repository path: `{{ args.deck.spec_path }}`
Workspace-relative path: `{{ args.deck.workspace_spec_path|default:args.deck.spec_path }}`

{{ args.deck.summary }}

Read and write the workspace-relative path above through the Slidey MCP.

## The slide you are editing (edit ONLY this one)

The operator is looking at **{{ args.scene_label|default:"(slide not identified)" }}**
— scene index **{{ args.scene_index }}**. Apply the instruction to THIS slide and
no other. Every element you edit MUST be on scene {{ args.scene_index }} (its refs
are `{{ args.scene_index }}/<el>`). Do not touch any other scene.

If `args.scene_index` is `-1` the slide could not be identified — do NOT guess a
slide; make no change and report `edited: []` with a summary saying you need the
operator to point at a slide.

This slide's CURRENT content (edit it in place at the workspace-relative path
above):

```json
{{ args.scene_json|default:"{}" }}
```

## The annotation

The operator's annotation anchor and instruction:

- Pointed at: {{ args.annotation.anchor.label|default:"(an unnamed spot on the frame)" }}
- Anchor kind: {{ args.annotation.anchor.kind|default:"(unknown)" }}
- Plugin: {{ args.annotation.anchor.semantic_element.plugin|default:"(none)" }}
- Element ref: {{ args.annotation.anchor.semantic_element.ref|default:"(none)" }}
- Bbox: {{ args.annotation.anchor.semantic_element.bbox|default:"(none)" }}
- Instruction: {{ args.refine_feedback|default:args.annotation.instruction|default:"(none — infer from the anchor)" }}

{% if args.visual.anchor %}
The operator pointed at a SPECIFIC element on the LIVE slide — this is the
authoritative target. PREFER it over everything above.

- Element you must edit: `{{ args.visual.anchor.semantic_element.ref|default:"(see live anchor)" }}` ({{ args.visual.anchor.semantic_element.plugin|default:"slidey" }})
- Live anchor: {{ args.visual.anchor }}

Edit exactly this element (its ref is `<sceneIndex>/<field>`); it is on the slide
named above. Do not edit any other element or slide.
{% endif %}

## Resolving the anchor → scene element

The anchor union (`semantic_element` | `region` | `dom_node`) resolves as follows:

- `semantic_element` — `semantic_element.plugin` ("slidey") + an OPAQUE
  `semantic_element.ref` string of the form `<sceneIndex>/<el>` (e.g. `1/card_0`)
  name the exact deck element. Split the ref on `/` to get the scene index and
  element key, then edit that element. `semantic_element.bbox` [x,y,w,h] is its
  box on the rendered frame (overlay hint only).
- `region` — a `region.bbox` [x,y,w,h] drawn on the frame; map it through the
  deck's `.semantic.json` sidecar to the dominant element it overlaps (compare
  against each element's `bbox`), then edit that element.
- `dom_node` — a resolved `dom_node.selector` of the form
  `[data-slidey-el='<scene>/<el>']`; read the ref out of the selector and edit
  that element.

Scene element keys for this story:

- `title`: `eyebrow`, `title`, `subtitle`.
- `cards`: `title`, `card_<i>` where `i` is the zero-based card index.
- `narrative`: `eyebrow`, `lede`, `body`.

Edit the matching type-specific field directly in the deck JSON. Do not add
separate `id`, `heading`, or `elements` wrappers.

## What to produce

Apply the instruction to the resolved scene element only. Submit the deck
object: the repository-render `spec_path` (repo-relative, not an absolute
filesystem path), a one-line `summary` of what you changed, and the `edited`
element refs you touched (the opaque `<scene>/<el>` form, e.g. `1/card_0`).
