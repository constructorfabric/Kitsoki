# Proposing a fix — produce the fix-proposal artifact

You are proposing a fix for **{{ args.ticket_id }}** — *{{ args.ticket_title }}*
against `{{ args.workdir }}`.

This run's implementation mode is `{{ args.implementation_mode }}`.
{% if args.implementation_mode == "codeact_decomposed" or args.implementation_mode == "codeact_decomposed_fallback" %}
This is a decomposed CodeAct run. You MUST include a schema-complete,
dependency-ordered `implementation_plan`; without it the executor has no
write grant and cannot apply the proposed fix.
{% endif %}

You have a reproduction artifact from the previous room:

> {{ args.reproduction_summary }}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous fix proposal was rejected. The
operator's feedback below is a **binding directive**: it OVERRIDES
any default behaviour or constraint further down this prompt whenever
the two conflict. Treat every statement as a hard requirement, not a
suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm the new
   proposal addresses each point. If the feedback says "do not X",
   the proposal must NOT do X — including in `fix_description`,
   `root_cause`, and `affected_files`.
2. If you genuinely cannot honour a statement (schema-incompatible,
   factually impossible), say so in `summary_markdown` and explain
   why. Silent non-compliance is the failure mode this directive
   guards against.
3. If the feedback contradicts the default constraints below,
   follow the feedback and flag the conflict in `summary_markdown`.

---

{% endif %}

## Constraints

- `fix_description` must be concrete — not "fix the bug". Describe the
  edit in enough detail that another engineer could implement it without
  rereading the ticket.
- `root_cause` must explain *why* the bug happens, not *what* the bug is.
  Cite the offending file / line where possible.
- `affected_files` must be real, relative paths in the repo (no leading
  slash; must have an extension). At least one.
- `confidence` is your own estimate in [0.0, 1.0]; under 0.3 is rejected
  downstream.
- `reasoning` is the chain from evidence → cause → fix.
- Before proposing, enumerate **every independently observable promise** in the
  ticket (for example: rendered state, user-visible message/transcript,
  persisted value, API result, terminal mode). A proposal that fixes one
  visible symptom while leaving another stated outcome absent is incomplete.
  Include that acceptance inventory and the planned assertion for each item in
  `summary_markdown` so implementation and test review can audit it. Do not
  collapse distinct UI/data/side-effect outcomes into a vague "the view
  refreshes" claim.
- Propose the **smallest, most local** change that addresses the root cause.
  Favour a narrow edit at the bug's own site over a broad change to shared or
  engine-level internals — the latter is far likelier to break unrelated
  tests. The fix must resolve the bug WITHOUT regressing existing behaviour; if
  the only correct fix is invasive, say so and name the call sites most at risk
  so the implementer verifies them.

## Optional decomposed CodeAct plan

Only when the caller asks for the decomposed CodeAct treatment, include
`implementation_plan`: one to four dependency-ordered, independently testable
work items. Do not create a plan merely by splitting one file into many edits.
Each item must supply every field in the schema and must use narrow repository
paths (never `**`). The acceptance commands are deterministic host gates: do
not claim that CodeAct itself can run them. Ordinary proposals should omit this
optional field so their established execution paths remain unchanged.

## Output

Submit a `propose_fix_artifact` (see `schemas/proposing_artifact.json`).
The `summary_markdown` field is what a human reviewer reads at the
checkpoint — write it for them: bug, cause, fix, files, confidence.
