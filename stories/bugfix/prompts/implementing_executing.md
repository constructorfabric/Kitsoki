# Implementing the fix — apply edits to the workspace

You are implementing the fix for **{{ args.ticket_id }}** — *{{ args.ticket_title }}*
inside the managed workspace at `{{ args.workdir }}`.

The proposing room produced this proposal:

> {{ args.fix_description }}

**Root cause:** {{ args.root_cause }}

**Affected files:** {{ args.affected_files }}

{% if args.refine_feedback %}## ⚠ Operator refinement directive (cycle {{ args.cycle }})

This is a refine cycle — the previous implementation was rejected.
The operator's feedback below is a **binding directive**: it
OVERRIDES the proposal text above AND any default behaviour or
constraint further down this prompt whenever they conflict. Treat
every statement as a hard requirement, not a suggestion.

> {{ args.refine_feedback }}

Before submitting:

1. Walk the feedback statement-by-statement and confirm your edits
   address each point. If the feedback says "do not X", do NOT do X
   in the workspace — even if the proposal above says to.
2. If you genuinely cannot honour a statement (incompatible with
   the schema, factually impossible), say so in `summary_markdown`
   and explain why. Silent non-compliance is the failure mode this
   directive guards against.
3. If the feedback contradicts the proposal or constraints below,
   follow the feedback and flag the conflict in `summary_markdown`.

---

{% endif %}

## What to do

Make the edits described in the proposal. You are running with cwd set to
the workspace (`{{ args.workdir }}`), so file paths in the proposal's
`affected_files` are valid relative paths you can hand straight to
the `Read` and `Edit` (or `Write`) tools.

You MUST actually modify the files. The downstream `testing` room will
re-run the full suite against this workspace's HEAD — if your edits don't
compile or they break other tests, the pipeline rejects the work and bounces
it back to you. Catch that **now**, before you submit.

## Verify your own work before submitting (REQUIRED)

Do not submit a fix you have not verified. You have a shell — use it. Work
this loop until it is green, then submit:

1. **Build.** Run the project's build (e.g. `go build ./...`). If it does not
   compile, fix it and rebuild. Never submit a fix that does not build.
2. **Targeted test.** Run the test(s) that exercise this bug — the
   reproduction test and the package(s) you changed
   (e.g. `go test ./path/to/changed/pkg/...` or `-run <TestName>`). The
   reproduction must now PASS where it failed before.
3. **Don't break the neighbours.** Run the broader suite for the area you
   touched (the changed packages and any package that imports them). If your
   change made a previously-passing test fail, that is a regression — fix it
   or choose a narrower edit. A fix that solves the bug but breaks other tests
   is NOT done.
4. **Iterate.** If any step fails, edit and repeat from step 1. Stay in this
   loop until build + targeted tests + neighbours are all green.

Prefer the **smallest, most local** change that fixes the root cause. A broad
edit to shared/engine internals is far more likely to break unrelated tests —
if a narrow, local fix works, take it.

## Constraints

- Stay inside the workspace. Do not edit `.git` metadata or any path
  outside `{{ args.workdir }}`.
- Keep the change minimal and in-scope. Touch the files in `affected_files`;
  if you genuinely need to edit something else (including to keep other tests
  green), add it to `files_changed` so the reviewer sees the scope.
- Don't run `git commit` yourself — the pipeline does that after you
  return. Leave the workspace dirty with your edits; the commit step picks up
  everything (including any test you added or repaired while verifying).
- Set `applied: false` with `blockers` ONLY if you genuinely could not reach a
  green build+test state — never submit `applied: true` for a fix you could
  not get to compile and pass.

## Output

Submit an `implementing_artifact` (see `schemas/implementing_artifact.json`).
Required fields:
- `summary_title`: one-line description of what you applied.
- `summary_markdown`: human-readable diff narrative (what changed, where,
  why each edit). This is what a human reviewer reads at the testing
  checkpoint.
- `files_changed`: every path you actually edited (or attempted to edit).
- `applied`: `true` if every required edit was made, `false` if you hit a
  blocker. When `false`, populate `blockers` with the reason.
