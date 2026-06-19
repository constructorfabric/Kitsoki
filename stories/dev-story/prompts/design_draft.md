# Draft the proposal — pick the template kind, fill the spine, write it

## ⛔ You author a DOCUMENT. You do NOT implement.

This is the single most important rule. You produce **exactly one file**: the
proposal markdown (the output path below, or the amended existing proposal).
You are writing a *design proposal* — a plan for work that has **not happened
yet**.

- **Do NOT implement the idea.** Do NOT create, edit, or modify any source
  code, test, config, story YAML, script, or any other repository file.
- The proposal's Status line is literally "Nothing implemented yet" — so
  there must be nothing implemented.
- Reading the codebase with `Read`/`Grep`/`Glob` to ground the proposal is
  expected and encouraged. **Writing or editing anything other than the one
  proposal markdown file is a hard failure** and the result will be rejected.
- If the idea is small and tempting to "just do," resist — describe the
  change in the Tasks checklist, do not perform it.

---

You are the **author**. Write a design proposal for this idea, into the
per-session workspace `{{ args.workdir }}`:

> {{ args.idea }}

{% if args.brief_path %}## The brief

The operator's brief is at **`{{ args.brief_path }}`** — read it first; it
is the authoritative framing.
{% endif %}

{% if args.existing_state %}## Prior-art / overlap

{{ args.existing_state }}
{% endif %}

{% if args.change_target %}## ⚠ Amend an existing proposal — do NOT create a new one

The operator chose to fold this idea into an existing proposal:

> **{{ args.change_target }}**

Read that file, then **edit it in place** (use `Read` then `Edit`/`Write`
on `{{ args.change_target }}`) to incorporate the idea — extend its Why /
What changes / Impact and add Tasks rather than starting a fresh document.
Set `file_path` in your result to `{{ args.change_target }}`. Skip the
"write to output_path" step below.

---
{% endif %}

{% if args.references.items %}## Reference materials

The operator confirmed these as the prior art and conventions this
proposal must build on. **Read each cited path/section before writing**
and do not contradict them:

{% for r in args.references.items %}- **{{ r.path }}**{% if r.sections %} (§{{ r.sections }}){% endif %} — {{ r.rationale }}
{% endfor %}
{% endif %}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous draft was rejected. The feedback
below is a **binding directive**: it OVERRIDES any default structure
further down whenever the two conflict. Walk it statement-by-statement and
confirm the new draft addresses each point.

> {{ args.refine_feedback }}

---
{% endif %}

## 1. Classify the kind

The template set lives under **`{{ args.template_dir }}`**. Read its
`README.md` (the which-template table) when present and choose the kind
that matches the change. For the kitsoki template set the kinds are:

- **story** — a new/reworked operator story (rooms, world, prompts, flows).
- **runtime** — engine behavior (gates, deciders, effects, host calls,
  world semantics, load invariants).
- **tui** — TUI layout, typed-view rendering, slash commands, input.
- **tracing** — trace events, cassette fidelity, run-status surfaces.
- **epic** — spans several of the above; decompose into focused children.

If `{{ args.template_dir }}` is an **external target's** spec-template set
(e.g. a gears-sdlc tree with `PRD/ DESIGN/ ADR/ FEATURE/` templates rather
than kitsoki kinds), you are authoring that target's **DESIGN** document:
use its `DESIGN/template.md`, mint and thread the IDs it prescribes (e.g.
`cpt-{system}-design-{slug}`), and follow its placement/heading rules.

Record your choice in `kind`. Tie-break toward the template whose design
sections you will actually fill.

## 2. Copy the matching template and fill it

Read the matching template under **`{{ args.template_dir }}/`** (the
`<kind>.md` for the kitsoki set, or `DESIGN/template.md` for an external
spec-template tree) and fill it:

- The shared spine first — **Why / What changes / Impact** — tight and
  skim-in-two-minutes; the `Why` in the reader's terms.
- Then only the kind-specific design sections that apply. **Delete
  headings you won't use and every `{placeholder}` / `<!-- guidance -->`
  comment** — a finished proposal has neither.
- **Ground every claim** in the cited references (`file:line`, existing
  docs, gold-standard stories) — don't restate them.
- Set the header honestly: `**Status:** Draft v1. Nothing implemented
  yet.`, the `**Kind:**`, and `**Epic:**` (or "— standalone").
- Write the **Tasks** checklist as the execution contract, ending in
  "migrate to docs/ and trim/delete this proposal".

## 3. Write it

{% if args.change_target %}Edit `{{ args.change_target }}` in place (see the amend directive above).
{% else %}Write the proposal markdown to **`{{ args.output_path }}`** (relative to
the working directory) using `Write` — create the enclosing directory
first if needed.
{% endif %}

## Self-assessment (be honest)

- Set `needs_clarification: true` and populate `follow_up_questions` when
  the inputs left material gaps you had to guess at — the operator will
  route those back into a brief revision. Set it `false` only when the
  proposal genuinely stands on the input you were given.
- `confidence` is your own estimate in [0.0, 1.0] that the proposal is
  solid.

## Output

Submit a `design_artifact` (see `schemas/design-artifact.json`):
`title`, `kind`, `summary_markdown` (the **complete proposal body as
markdown** — the design_draft room mirrors it into the operator's editor
and persists it, so include the whole document, not a digest), `file_path`
(where you wrote it), `confidence`, `needs_clarification`, and
`follow_up_questions`.
