# Curate the reference materials for this proposal

You are the **researcher**. A proposal is going to be written for this
idea:

> {{ args.idea }}

{% if args.brief_path %}The operator's brief is at **`{{ args.brief_path }}`** — read it for the
full framing.
{% endif %}

{% if args.existing_state %}## Prior-art context

The scout's overlap report — the references should include anything the
proposal must stay consistent with:

{{ args.existing_state }}
{% endif %}

{% if args.revise %}## Revise the current list (do NOT start over)

The operator wants to **adjust the existing reference list**, not run a
fresh search. Here it is:

{% for r in args.current_references.items %}- **{{ r.path }}**{% if r.sections %} (§{{ r.sections }}){% endif %} — {{ r.rationale }}
{% empty %}(the list is currently empty)
{% endfor %}

Apply this instruction, **keeping every entry it does not touch**:

> {{ args.feedback }}

Return the **full updated list**, not just the delta.
{% else %}{% if args.feedback %}## Steering for this pass

> {{ args.feedback }}

Adjust what you look for accordingly.
{% endif %}{% endif %}

{% if args.search_hits %}## Semantic search hits (pre-ranked by embedding similarity)

These chunks were retrieved automatically by `host.agent.search` and ranked
by cosine similarity to the idea. Start here — read the promising ones in
full, discard the irrelevant ones, then fill any obvious gaps with your own
search.

{% for h in args.search_hits %}- **{{ h.path }}** (score {{ h.score|floatformat:2 }})  chunk `{{ h.chunk_id }}`
  > {{ h.text|truncatechars:200 }}
{% endfor %}
{% endif %}
## Your job

Search the repo's **documentation, rules, and conventions** for the
material this proposal must build on or stay consistent with. Use
`Read` / `Grep` / `Glob`. High-signal sources (for any target repo, find
the local equivalents and skip paths below that do not exist):

- `docs/proposals/templates/` and `docs/proposals/README.md` — the
  Kitsoki fallback proposal spine and lifecycle;
- local proposal/design/ADR/PRD templates, if the repo has them — these win
  over the fallback style;
- local contributor, agent, and architecture rules (`AGENTS.md`, `CLAUDE.md`,
  `docs/`, `README.md`, `.agents/skills/`) — the authoring discipline;
- `docs/proposals/process-design.md` — the fallback process-design methodology;
- the **gold-standard stories** (`stories/prd/`, `stories/bugfix/`) and
  `docs/stories/` when the change is story-shaped;
- `docs/architecture/`, `docs/tui/`, `docs/tracing/` for runtime / tui /
  tracing changes;
- any existing proposal or feature doc the change touches.

For each reference, pin down the **specific section(s) / line range** that
matter, not just the file, and give a **one-line rationale**. Prefer a
short, high-signal list. If nothing relevant exists, return an **empty
`items` list** — valid for a greenfield idea.

## Output

Submit a `references` object (see `schemas/references.json`):
`{ items: [{ path, sections, rationale }] }`. An empty list is valid.
