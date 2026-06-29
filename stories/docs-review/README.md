# docs-review

A single-shot story that asks a read-only review agent whether the
repository's documentation is in sync with the code. Two modes:

- **`start`** (recent): focuses on the last ~20 commits + working-tree
  changes. Use after a PR lands or in CI.
- **`baseline`**: ignores commit range and sweeps the whole repo —
  enumerates `docs/**`, `README.md`, `CLAUDE.md`, `stories/*/README.md`
  against the `cmd/` / `internal/` surface. Use to seed a fresh audit,
  catch long-accumulated drift, or audit a freshly-cloned tree where
  commit history is uninformative.

The verdict is written to `.artifacts/docs-review-<mode>-<sha>.json`
as a **valid, schema-conforming JSON file** (no markdown wrapper) so
downstream tooling can pipe it through `jq`, parse it in Python, etc.
The `reviewed` room renders the same data as a human-readable listing.

## Graph

```
idle ── start ──▶ reviewing ── done ──────▶ reviewed ── quit ──▶ @exit:done
                     │                          │   │
                     │                          │   └── review_again ──▶ reviewing
                     │                          │
                     │                          └── fix_docs
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

**Review phase** (`idle` → `reviewing` → `reviewed`): The `docs_reviewer`
agent audits docs against code and emits a verdict with decision ∈
{`needs_update`, `up_to_date`}, stale doc listings, and rationale.

**Fix phase** (explicit `reviewed` → `fixing` → `fixed`): If
decision is `needs_update`, the `reviewed` room exposes `fix_docs`.
Picking it routes to `fixing`. The `docs_writer` agent takes the
`stale_docs[]` listing as its worklist and applies targeted edits to
bring cited doc sections back in sync. **Changes are left uncommitted**
in the working tree — the operator reviews with `git diff`, then stages
and commits themselves. The story owns no workspace plumbing (no
auto-commit, no branch creation). `up_to_date` verdicts terminate at
`reviewed` (no fix phase).

`idle.on_enter` probes the operator's context with `host.run` so the
working directory and detected `repo_root` are shown before they hit
`start` — useful for confirming you're auditing the tree you think you
are.

## Two agents

The story uses two distinct Opus agents with different tool sets:

- **`docs_reviewer`** (read-only): reads the entire repo/docs and
  emits a verdict. Tools: `Read`, `Grep`, `Glob`, `Bash` (read-only
  profile). `Edit` and `Write` are rejected at both load time and call
  time. Used by `host.agent.decide` in the `reviewing` room.

- **`docs_writer`** (write-capable): takes the verdict's `stale_docs[]`
  worklist and applies targeted edits to bring cited doc sections back
  in sync. Tools: `Read`, `Grep`, `Glob`, `Edit`, `Write`, `Bash`
  (read-only profile). Used by `host.agent.task` in the `fixing` room.
  **Does not commit** — changes sit uncommitted in the working tree for
  the operator to review and stage.

`reviewing.on_enter` chains:

1. `host.run` — capture HEAD sha, total commit count, recent `git log`,
   and a `git diff --stat` of the last N commits (with `N = min(20,
   count-1)`, falling back to `git show --stat HEAD` on a 1-commit
   tree) plus any uncommitted working-tree diff into one JSON envelope.
2. `host.agent.decide` — Opus agent (`Read`, `Grep`, `Glob`, `Bash`
   under the read-only profile; `Edit`/`Write` rejected at both load
   time and call time) returns a `docs_review_verdict`. Binds the
   full verdict object **and** a scalar `verdict_decision = submitted.decision`.
3. `host.artifacts_dir` — write the verdict map as JSON to
   `.artifacts/docs-review-<mode>-<sha>.json`.
4. Two conditional `emit_intent:` effects (see "The pre-bind emit
   trap" below):
   - `emit_intent: done` `when: world.verdict_decision != ''` →
     `reviewed`.
   - `emit_intent: no_submit` `when: world.commit_sha != '' &&
     world.verdict_decision == ''` → `review_failed`.

## The pre-bind emit trap

This is the load-bearing detail of the story's wiring. `emit_intent:`
effects inside `on_enter` are evaluated **twice** by the engine:

- **Pre-bind**, during `machine.Turn`: the on_enter chain is walked,
  host calls are collected for the orchestrator to dispatch later, and
  emit_intents are dispatched **immediately** against a world snapshot
  where none of this chain's binds have applied yet.
- **Post-bind**, after the orchestrator runs each host call and writes
  its `bind:` outputs: `settlePostBindEmits` →
  `machine.DispatchPostBindEmits` re-walks the same on_enter list and
  fires any `emit_intent:` whose `when:` guard now passes.

A transition guard on the **destination room's** `on:` arm only sees
the pre-bind world — too early. Guards must therefore live **on the
emit_intent effect itself**:

```yaml
- invoke: host.agent.decide
  bind: { verdict: submitted, verdict_decision: submitted.decision }
- emit_intent: done
  when: "world.verdict_decision != ''"      # false pre-bind → suppressed
                                            # true post-bind → fires
- emit_intent: no_submit
  when: "world.commit_sha != '' && world.verdict_decision == ''"
```

Pre-bind, every world key referenced is at its default (`""`), so both
guards are false and nothing emits. Post-bind, the correct branch fires.

Without this pattern, the room walks straight to `reviewed` against the
pre-bind world and renders a verdict block full of `(missing)` once
the bind eventually lands. The flow fixture `empty_submitted.yaml`
regression-tests exactly this case.

## Run

Interactively:

```sh
kitsoki run stories/docs-review/app.yaml
```

The verdict lands at `.artifacts/docs-review-recent-<sha>.json` (or
`-baseline-` if baseline mode is used).

> **Note on `kitsoki turn`**: `OneShot` runs `machine.Turn` + the host
> dispatcher but **not** `settlePostBindEmits`. The host calls run,
> the verdict is bound, the artifact is written — but the conditional
> `emit_intent:` is never re-evaluated post-bind, so the next-state
> stays at `reviewing` (with `done`/`no_submit` in `allowed_intents`).
> For an end-to-end live verification use `kitsoki run` or check the
> artifact directly:
>
> ```sh
> rm -f .artifacts/docs-review-recent-$(git rev-parse HEAD).json
> kitsoki turn stories/docs-review/app.yaml \
>   --state idle --intent start --world '{}' >/dev/null
> jq . .artifacts/docs-review-recent-$(git rev-parse HEAD).json \
>   | kitsoki mcp-validator \
>       --schema stories/docs-review/schemas/docs_review_verdict.json \
>       --validate-once
> ```

## Tests

Five flow fixtures (the first four stubbed by cassettes, the fifth a
hybrid that lets the real `host.artifacts_dir` handler run — see
"Round-trip artifact test" below). No LLM cost.

```sh
kitsoki test flows stories/docs-review/app.yaml
```

| Fixture | What it pins |
|---|---|
| `flows/happy_needs_update.yaml` | Agent returns `needs_update`; story stops at `reviewed` with `verdict_decision`, then an explicit `fix_docs` operator pick runs `fixing` to `fixed`. |
| `flows/happy_up_to_date.yaml` | Agent returns `up_to_date`; operator re-runs via `review_again`. |
| `flows/agent_abandoned.yaml` | `host.agent.decide` returns `Result.Error` (validator exhaustion); `on_error: review_failed` fires. |
| `flows/empty_submitted.yaml` | Agent returns `ok=true` with `submitted: {}` (silent-failure mode that bit us live). Asserts the conditional `no_submit` emit catches it and routes to `review_failed`. |
| `flows/artifact_round_trip.yaml` | Hybrid: stubs `host.run` + `host.agent.decide` inline, but lets the **real** `host.artifacts_dir` handler run against a `t.TempDir()` set via `$KITSOKI_ARTIFACTS_ROOT`. Driven by `cmd/kitsoki/docs_review_artifact_test.go`, which asserts the artifact body actually contains the verdict (no `{}` silent-failure, no `<map[…]>` go-stringification). |

### Round-trip artifact test

The first four fixtures stub `host.artifacts_dir` and only check
`state` + `world`. They cannot see whether `world.verdict` actually
makes it through the chain:

```
agent.decide → Data["submitted"] → bind: verdict: submitted
   → body: "{{ world.verdict }}" → bodyArg JSON pretty-print → file on disk
```

A live regression where the agent submits successfully but the artifact
lands as `{}` (because bind dropped the map, or because templating
stringified it as `<map[string]interface {} Value>`) would pass every
cassette fixture and still ship a broken story. `artifact_round_trip`
closes that gap: it stubs the LLM-dependent hosts but exercises the
real `host.artifacts_dir` handler end-to-end, and the Go wrapper greps
the on-disk artifact for both the positive shape (`"decision": "needs_update"`,
`"invalidations":`) and the two known silent-failure signatures.

Run:

```sh
go test ./cmd/kitsoki/ -run TestDocsReview_ArtifactRoundTrip
```

Schema correctness is pinned separately by Go tests
(`cmd/kitsoki/docs_review_schema_test.go`) — 14 cases covering empty
payload, every missing-required permutation, out-of-enum decision,
short summary, confidence > 1, missing `lines`, empty `invalidations`,
and rogue fields at every level. Run via:

```sh
go test ./cmd/kitsoki/ -run TestDocsReviewSchema
```

## Verdict / artifact format

The artifact is the raw `world.verdict` map serialised as JSON. See
`schemas/docs_review_verdict.json` for the authoritative contract.
Shape (required fields bolded):

- **`decision`**: `"needs_update"` | `"up_to_date"`
- **`summary`**: one-paragraph rationale, ≥ 10 chars; cites file paths,
  line ranges, and commit short-shas.
- **`confidence`**: 0..1.
- **`commits`**: `[{ sha, subject, change }]` — every in-scope commit.
  Lookup table referenced by `stale_docs[].invalidations[].commit`.
  Empty only on a structural-audit fallback (shallow repo, deps-only
  diff).
- **`stale_docs`**: `[{ path, lines, anchor?, invalidations: [...] }]`.
  Empty `[]` when `decision == "up_to_date"`. Keyed by `(path, lines)`
  — one row per stale section, not per doc.
  - `invalidations[]` (≥ 1): `[{ commit, source?, change, reason }]`.
    `commit` references a row in top-level `commits[]`, or is the
    literal `"working-tree"` (uncommitted changes) / `"structural"`
    (no specific commit).
- `recommended_actions`: optional `[string]`, one per stale row.

The strict `additionalProperties: false` prevents the agent from
returning extra fields that would silently bypass the listing
rendering.

## Authoring notes

- The prompt (`prompts/decide.md`) carries a concrete JSON example of
  the submit payload. This is the difference between the agent
  calling `submit()` with structured JSON vs. dumping a YAML code
  block into chat (which the host discards as free-text). If you
  rework the schema, keep the example aligned.
- `verdict_decision` is a deliberate scalar shadow of
  `verdict.decision`. The conditional `emit_intent:` guards need a
  flat key (see "The pre-bind emit trap"). If you add a similar
  conditional emit elsewhere, prefer flat-scalar binds over walking
  nested map fields.

## Exits

- `done` — operator dismissed the verdict.
- `abandoned` — operator quit before a verdict landed (from any of
  `reviewing`, `review_failed`, or `reviewed`).

## Imports

Standalone-runnable. No `host_interfaces:` — concrete hosts
(`host.run`, `host.agent.decide`, `host.artifacts_dir`) are wired
directly because the story has one job and substituting them buys
nothing.
