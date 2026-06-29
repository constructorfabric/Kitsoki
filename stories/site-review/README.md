# site-review

A single-shot story that asks a read-only review agent whether the
**feature catalog** (`features/*.yaml`) and **product site**
(`tools/site/`) still advertise what kitsoki has actually shipped. It is
the sibling of [`docs-review`](../docs-review/README.md): same two-phase
shape (review → fix), same pre-bind emit wiring, same hands-off
"leave it uncommitted" positioning — but the audit target is the
promo-facing surface instead of `docs/**`.

Two modes:

- **`start`** (recent): focuses on the last ~20 commits + working-tree
  changes. Use after a feature lands or in CI to catch a capability that
  shipped without a catalog entry / demo.
- **`baseline`**: ignores commit range and sweeps the whole catalog +
  site. Use to catch long-accumulated drift.

The verdict is written to `.artifacts/site-review-<mode>-<sha>.json`
as a **valid, schema-conforming JSON file** (no markdown wrapper), and
the `reviewed` room renders the same data as a human-readable listing.

## Graph

```
idle ── start ──▶ reviewing ── done ──────▶ reviewed ── quit ──▶ @exit:done
                     │                          │   │
                     │                          │   └── review_again ──▶ reviewing
                     │                          │
                     │                          └── fix_gaps
                     │                              [operator picks after needs_update]
                     │                              ▼
                     │                            fixing ── fix_done ──▶ fixed ── quit ──▶ @exit:done
                     │                              │                      │
                     │                              │                      └── review_again ──▶ reviewing
                     │                              └── on_error ──▶ review_failed
                     │
                     ├── no_submit ──▶ review_failed ── review_again ──▶ reviewing
                     │                       │
                     │                       └── quit ──▶ @exit:abandoned
                     │
                     └── on_error (host.* fails) ──▶ review_failed
```

## Two-phase flow: Review → Fix

**Review phase** (`idle` → `reviewing` → `reviewed`): the `site_reviewer`
agent compares recently-shipped capabilities against the catalog + site
and emits a verdict with decision ∈ {`needs_update`, `up_to_date`}, a
`gaps[]` listing, and rationale.

**Fix phase** (explicit `reviewed` → `fixing` → `fixed`): if decision is
`needs_update`, the `reviewed` room exposes `fix_gaps`. Picking it routes
to `fixing`, where the `site_writer` agent takes the `gaps[]` listing as
its worklist — authoring missing `features/<id>.yaml` catalog entries and
patching stale site copy. **Changes are left uncommitted** in the working
tree; the operator runs `make features` (to regenerate the tour manifests
+ JSON schema), reviews with `git diff`, then stages and commits. The
story owns no workspace plumbing. `up_to_date` verdicts terminate at
`reviewed`.

## Two agents

- **`site_reviewer`** (read-only): reads the repo + catalog + site and
  emits a `site_review_verdict`. Tools: `Read`, `Grep`, `Glob`, `Bash`
  (read-only profile); `Edit`/`Write` rejected at load and call time.
  Used by `host.agent.decide` in `reviewing`.
- **`site_writer`** (write-capable): applies the fixes. Tools add
  `Edit`/`Write`. Used by `host.agent.task` in `fixing`. **Does not
  commit.** When a gap needs an artifact the agent cannot produce (e.g. a
  recorded demo video), it records the target in `unresolved[]` rather
  than faking completion.

`reviewing.on_enter` chains `host.run` (capture HEAD sha + recent
log/diffstat **and** the current feature-catalog ids + site page
inventory — both modes need the inventory to find what's missing) →
`host.agent.decide` (binds the verdict + a scalar
`verdict_decision = submitted.decision`) → `host.artifacts_dir` (write
the verdict JSON) → two conditional `emit_intent:` effects.

The conditional-emit guards rely on the **pre-bind emit trap** — the
load-bearing wiring detail documented authoritatively in
[`docs-review`'s README](../docs-review/README.md#the-pre-bind-emit-trap).
The same pattern applies here verbatim: `verdict_decision` is a flat
scalar shadow of `verdict.decision` so the post-bind `emit_intent: done`
guard has a flat key to test. `flows/empty_submitted.yaml` regression-tests
the silent-failure case the guard exists for.

## Run

```sh
kitsoki run stories/site-review/app.yaml
```

The verdict lands at `.artifacts/site-review-recent-<sha>.json` (or
`-baseline-` in baseline mode). See the
[`docs-review` note on `kitsoki turn`](../docs-review/README.md#run) —
`OneShot` skips the post-bind emit, so for end-to-end verification use
`kitsoki run` or read the artifact directly.

## Tests

Four flow fixtures, all stubbed by host cassettes — no LLM cost. Part of
`make test` (auto-discovered via `stories/*/app.yaml`).

```sh
kitsoki test flows stories/site-review/app.yaml
```

| Fixture | What it pins |
|---|---|
| `flows/happy_needs_update.yaml` | Agent returns `needs_update` citing an uncataloged capability (the GitHub agent); story stops at `reviewed`, then an explicit `fix_gaps` pick runs `fixing` → `fixed`. |
| `flows/happy_up_to_date.yaml` | Agent returns `up_to_date` (deps-only diff); operator re-runs via `review_again`. |
| `flows/agent_abandoned.yaml` | `host.agent.decide` returns `Result.Error` (validator exhaustion); `on_error: review_failed` fires and the operator can retry. |
| `flows/empty_submitted.yaml` | Agent returns `ok=true` with `submitted: {}`; asserts the conditional `no_submit` emit catches it and routes to `review_failed` instead of rendering a `(missing)`-filled `reviewed`. |

## Verdict / artifact format

The artifact is the raw `world.verdict` map serialised as JSON. See
`schemas/site_review_verdict.json` for the authoritative contract. Shape
(required fields bolded):

- **`decision`**: `"needs_update"` | `"up_to_date"`
- **`summary`**: one-paragraph rationale (≥ 10 chars); cites feature ids,
  file paths, and commit short-shas.
- **`confidence`**: 0..1.
- **`commits`**: `[{ sha, subject, capability }]` — every in-scope
  commit. Lookup table referenced by `gaps[].evidence[].commit`.
- **`gaps`**: `[{ kind, target, lines?, detail?, evidence: [...] }]`,
  empty `[]` when `up_to_date`. `kind` ∈ {`missing-catalog-entry`,
  `missing-demo`, `site-inconsistency`, `stale-doc`}; `target` is a
  feature id or file path.
  - `evidence[]` (≥ 1): `[{ commit, source?, change, reason }]`.
- `recommended_actions`: optional `[string]`, one per gap row.

The fix artifact (`schemas/site_fix_artifact.json`) records
`applied`, `summary`, `files_changed[]`, `unresolved[]`, `blockers[]`.

## Relationship to the catalog validation

The deterministic completeness checks in `pnpm features:check`
(promo ⇒ demo, recordable tour demo ⇒ posterStep, in-site docs ⇒
allowlisted — see `features/AGENTS.md`) catch gaps **within** an existing
catalog entry. This story is the layer above: it finds capabilities that
have **no catalog entry at all** (which no validator can flag, since the
feature was never authored) and drives the authoring. Run the story to
discover + draft; run `features:check` to verify the drafts are complete.

## Exits

- `done` — operator dismissed the verdict.
- `abandoned` — operator quit before a verdict landed.

## Imports

Standalone-runnable. Concrete hosts (`host.run`, `host.agent.decide`,
`host.agent.task`, `host.artifacts_dir`) are wired directly — the story
has one job and substituting them buys nothing.
