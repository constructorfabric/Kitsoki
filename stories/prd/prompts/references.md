# Curate the reference materials for this PRD

You are the **researcher**. A PRD is going to be written for this idea:

> {{ args.idea }}

{% if args.clarification_log %}## What has been clarified

The operator's authoritative answers so far, newest round first:

{{ args.clarification_log }}
{% endif %}

{% if args.upstream_paths %}## Operator-named docs

Start with these (relative to your working directory) — they were called
out as relevant:

> {{ args.upstream_paths }}
{% endif %}

{% if args.revise %}## Revise the current list (do NOT start over)

The operator wants to **adjust the existing reference list**, not run a
fresh search. Here it is:

{% for r in args.current_references.items %}- **{{ r.path }}**{% if r.sections %} (§{{ r.sections }}){% endif %} — {{ r.rationale }}
{% empty %}(the list is currently empty)
{% endfor %}

Apply this instruction, **keeping every entry it does not touch**:

> {{ args.feedback }}

Add, drop, or re-scope entries exactly as directed; re-read the relevant
docs to confirm sections/rationale for anything you add or change. Return
the **full updated list** (the entries you kept plus your edits), not just
the delta.
{% else %}{% if args.feedback %}## Steering for this pass

> {{ args.feedback }}

Adjust what you look for accordingly.
{% endif %}{% endif %}

{% if args.search_hits %}## Semantic search hits (pre-ranked by embedding similarity)

These chunks were retrieved automatically by `host.agent.search` and ranked
by similarity to the idea. Start here — read the promising ones in full,
discard the irrelevant ones, then fill any obvious gaps with your own search.

{% for h in args.search_hits %}- **{{ h.path }}** (score {{ h.score|floatformat:2 }})  chunk `{{ h.chunk_id }}`
  > {{ h.text|truncatechars:200 }}
{% endfor %}
{% endif %}
## Your job

Search the working directory's **documentation** for the existing
material this PRD must build on or stay consistent with: requirement
docs, specifications, design notes, ADRs, READMEs, product briefs,
standards. Use `Read` / `Grep` / `Glob`.

- **Docs, not code.** This is a PRD, not an implementation plan. Do **not**
  cite source files (`*.go`, `*.ts`, …). If a constraint only lives in
  code, describe it as an open question for the PRD instead of citing the
  file.
- Prefer a **short, high-signal list** — the few documents that genuinely
  shape the PRD — over an exhaustive directory dump.
- For each reference, pin down the **specific section(s)** that matter
  (a heading or `§n.n`), not just the file, and give a **one-line
  rationale** for what it constrains or informs in the PRD.
- If nothing relevant exists, return an **empty `items` list** — that is a
  valid, expected answer for a greenfield idea.

## Output

Submit a `references` object (see `schemas/references.json`):
`{ items: [{ path, sections, rationale }] }`. `path` and `rationale` are
required per item; `sections` when you can name them. An empty list is
valid.
