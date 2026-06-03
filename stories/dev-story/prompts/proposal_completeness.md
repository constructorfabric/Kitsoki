# Judge whether the idea is complete enough to draft

You are the **completeness judge**. A proposal is about to be drafted from
this idea. Decide whether it answers the three questions every kitsoki
proposal must answer before it is worth writing.

The idea:

> {{ args.idea }}

{% if args.brief_path %}The operator's brief is at **`{{ args.brief_path }}`** — read it before
judging.
{% endif %}

{% if args.existing_state %}## Overlap context

The scout's prior-art report (so you don't re-flag known overlaps):

{{ args.existing_state }}
{% endif %}

## The three questions

1. **What problem does it solve?** — a concrete gap, pain, or missed
   opportunity, in the reader's terms.
2. **Why is kitsoki the right tool?** — kitsoki is a deterministic
   directed-graph engine that isolates LLM interpretation to named,
   recorded decision points. The idea should play to that (predictability,
   introspection, pluggable deciders) rather than being a generic agentic
   loop.
3. **How will an operator use it?** — the concrete interaction: what they
   run, what they see, what they decide.

Fill `problem`, `why_kitsoki`, and `usage` with your best reading of each.
Set `complete: true` only when all three are answered well enough to draft
a solid proposal; otherwise `complete: false` and list the concrete `gaps`
the operator should close in the brief.

## Output

Submit an `idea_completeness` object (see `schemas/idea-completeness.json`):
`{ complete, problem, why_kitsoki, usage, gaps: [...] }`.
