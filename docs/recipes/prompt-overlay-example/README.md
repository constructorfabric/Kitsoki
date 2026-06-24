# Recipe: a project prompt overlay

A worked example of **prompt extension** — specializing a generic story's
prompts for a project without forking the story. Full reference:
[`../../stories/prompts.md`](../../stories/prompts.md).

This overlay specializes the `bugfix` story's reproduction prompt for a
fictional "Acme" project. It does **not** copy the story's prompt; it
`{% extends %}` the base and overrides only the `spec_` blocks the base
author marked as project-specialization surfaces.

## See the surface a story exposes

```bash
kitsoki prompts spec stories/bugfix/app.yaml
```

```
Specialization surface for Bug-fix pipeline (2 block(s)):

prompts/reproducing_executing.md
  HOLE     spec_project_context         (empty — project must fill)
  DEFAULT  spec_repro_conventions       - Do not fabricate evidence. …
```

`spec_project_context` is a **hole** (empty default — a project fills it);
`spec_repro_conventions` is a **provisional default** (a working body a
project may refine).

## The overlay

`prompts/reproducing_executing.md` mirrors the story's prompt path and:

```pongo
{% extends "@story/prompts/reproducing_executing.md" %}
{% block spec_project_context %}…Acme repo conventions…{% endblock %}
{% block spec_repro_conventions %}…Acme reproduction standards…{% endblock %}
```

The base prompt's structural text (everything outside the `spec_` blocks) is
inherited unchanged, so upstream improvements to the story keep flowing.

## Preview it (no LLM call)

See the exact assembled prompt before running anything:

```bash
kitsoki prompts render stories/bugfix/app.yaml prompts/reproducing_executing.md \
  --prompt-overlay docs/recipes/prompt-overlay-example \
  --arg ticket_id=ACME-1 --arg ticket_title="login fails" --arg workdir=/tmp/acme
```

It warns if you've overridden a block the base doesn't declare (a typo that
would silently do nothing).

## Run with the overlay

```bash
kitsoki run stories/bugfix/app.yaml \
  --prompt-overlay docs/recipes/prompt-overlay-example
```

The effect's `prompt: prompts/reproducing_executing.md` is unchanged — the
bare reference resolves overlay-first, so the overlay is picked up
automatically. Drop the flag and the story runs on its generic defaults.

A run that used an overlay records `prompt_overlay` (and which `spec_` blocks
were overridden vs. left at their default) in the agent trace.
