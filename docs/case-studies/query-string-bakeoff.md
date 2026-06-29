# Should I use kitsoki for my project? — the query-string bake-off

This case study answers the question a prospective user actually asks — *if I
onboard my repo and let kitsoki fix a real bug, do I get a good fix, and what
does it cost versus the fix the maintainers actually shipped?* — by running the
[bugfix bake-off](bugfix-bakeoff.md) against a **third-party** repository instead
of kitsoki's own code.

## Why query-string

The target has to feel like a customer repo: small enough to onboard and test in
seconds, but **mature** enough to have real, filed-issue bugs with regression
tests we can grade against.

[`sindresorhus/query-string`](https://github.com/sindresorhus/query-string) fits
exactly:

- **Small / simple** — a single ~558-LOC parser (`base.js`), AVA tests, plain
  `npm install && npm test`.
- **Mature** — **274 commits, 90 releases, since 2013**, with a long history of
  filed issues fixed by PRs that each shipped a regression test.

That combination is what makes a deterministic, byte-reproducible benchmark
possible at all.

## The 3 bugs (the report)

Each bug is a real filed issue (or a self-contained fixing PR), fixed by a real
commit that added a regression test. The reproducible baseline is the fix
commit's **first parent** (`fix_sha^`) — the bug is present there, and the
checkout is immune to later history movement.

| id | issue / PR | the bug | `baseline_sha` (`fix^`) | `fix_sha` | source |
|----|------------|---------|--------------------------|-----------|--------|
| **qs1** | [#336](https://github.com/sindresorhus/query-string/issues/336) | With `arrayFormat: 'separator'`, a value that merely *contains* an encoded separator (`foo=a%7Cb`) is wrongly split into `['a','b']` instead of `{foo:'a\|b'}`. | `2e1f45a` | `ec67fea` | `base.js` |
| **qs2** | [#404](https://github.com/sindresorhus/query-string/issues/404) → [#406](https://github.com/sindresorhus/query-string/pull/406) | With `arrayFormat: 'comma'` + a `types` schema, a single-element value isn't coerced to the declared array type: `parse('a=1', {types:{a:'number[]'}})` → `{a:'1'}` not `{a:[1]}`. | `88e1e36` | `3e61882` | `base.js` |
| **qs3** | [#392](https://github.com/sindresorhus/query-string/pull/392) | With `arrayFormat: 'bracket-separator'`, a single bracket key with a URL-encoded value isn't split: `foo%5B%5D=a%2Cb%2C…` → `{foo:['a,b,…']}` not `{foo:['a','b',…]}`. | `4287e77` | `19c43d4` | `base.js` |

The exact regression test each real PR added is captured, isolated, as the
**hidden oracle** in
[`tools/bugfix-bakeoff/external/projects/query-string/oracles/`](../../tools/bugfix-bakeoff/external/projects/query-string/oracles).
It is kept out of the candidate's tree until scoring.

> Provenance note: qs1 and qs2 have a clean filed-issue → fixing-PR linkage. qs3
> is a self-contained PR with no separately filed issue (the bug is described in
> the PR). All three are fully RED→GREEN reproducible, which is what the grader
> requires.

## The deterministic good/bad detector

A fix is graded with **no LLM and no judgment call**:
[`bench.py`](../../tools/bugfix-bakeoff/external/bench.py) overlays the
hidden oracle onto the candidate's tree and runs it
(`python3 bench.py score --project query-string --bug qs1 --tree <worktree>`).

```
GREEN oracle  -> the fix is behaviorally correct (bug gone)
RED   oracle  -> the fix is wrong / incomplete (bug remains)
```

It also runs the full AVA suite as a secondary signal. The two together give a
three-way verdict matching the shared [result schema](../../tools/bugfix-bakeoff/results/SCHEMA.md):

| verdict | meaning |
|---------|---------|
| `solved` | oracle GREEN **and** full suite GREEN |
| `partial` | oracle GREEN but a pre-existing test now fails (fix didn't update an affected test) |
| `failed` | oracle RED — the bug is still there |

**This is where kitsoki's pipeline can be "better with more tests".** For qs1 and
qs2 a *correct* behavioral fix legitimately flips one pre-existing test's
expectation (the real PR edited that test too). A source-only fix therefore
scores `partial` until the candidate also updates the affected test:

| bug | real fix, source-only (`base.js`) | why |
|-----|-----------------------------------|-----|
| qs1 | oracle GREEN, suite RED → **partial** | a pre-existing encoded-comma test asserted the buggy `[1,2,3]`; correct fix makes it `'1,2,3'` |
| qs2 | oracle GREEN, suite RED → **partial** | sibling `string[]` test was `.failing`; correct fix un-fails it |
| qs3 | oracle GREEN, suite GREEN → **solved** | no pre-existing expectation flipped |

kitsoki's `bugfix` pipeline runs the full suite before submitting and updates the
affected test (→ `solved`); a careless single-prompt that only edits `base.js`
lands at `partial`. The bake-off makes that difference **measurable**, not
anecdotal.

## Reproducing it — the gated test

```sh
make qs-bakeoff
```

~32s, deterministic, **free**. It:

1. clones query-string at each pinned baseline;
2. **onboards** the repo via the binary's embedded dev-story — proving a
   binary-only user can stand up a working kitsoki environment on a real, mature
   JS repo (config + instance + studio MCP + skill/agent toolkit);
3. for each bug, proves the hidden-oracle detector is **armed** — RED at the
   baseline, GREEN once the real fix's source is applied.

It is excluded from `make test` (the `qsbakeoff` build tag) and skips cleanly if
`kitsoki`/`git`/`node` are absent. Passing run, 2026-06-25:

```
--- PASS: TestQueryStringBakeoff (31.30s)
    --- PASS: .../onboard (3.45s)   onboarded query-string@2e1f45a -> working kitsoki env
    --- PASS: .../qs1     (2.32s)   RED@baseline, GREEN@real-fix
    --- PASS: .../qs2     (1.89s)   RED@baseline, GREEN@real-fix
    --- PASS: .../qs3     (1.95s)   RED@baseline, GREEN@real-fix
```

## Results — GPT-5.5 through the pipeline, 3 / 3 solved

A real live run drove the seven-room `bugfix` pipeline against each baseline
worktree under the **`codex-native` profile (GPT-5.5)** — headless, through the
studio MCP via [`tools/mcp-drive`](../../tools/mcp-drive/README.md). The fix is
generated entirely by GPT-5.5 inside the session; the orchestrator only advances
the pipeline.

| bug | verdict | GPT-5.5's fix (`base.js`) | matches real fix? |
|-----|---------|---------------------------|-------------------|
| qs1 | **solved** (oracle + suite green) | drop the `isEncodedArray` heuristic; split only on literal separators (`717c863`, 1+/3−) | ✅ same approach as `ec67fea` |
| qs2 | **solved** | coerce a single comma value to the declared typed array (`1452aa7`, 12+/1−) | ✅ same as `3e61882` |
| qs3 | **solved** | decode then split the bracket-separator value (`c29258a`, 11+/3−) | ✅ same as `19c43d4` |

All three pass the hidden oracle **and** the full AVA suite — the pipeline ran
the suite, saw the pre-existing test a correct fix legitimately breaks, and
updated it (the `solved` vs `partial` quality lever, realised). GPT-5.5 also
converged on the **same root-cause fix the maintainers shipped** in every case.

**Cost.** Codex traces carry no per-call price (ChatGPT-subscription auth); token
usage is ~1.2M in / ~11K out per cell. Metered providers expose authoritative
cost in the trace (`payload.meta.cost_usd`).

**GLM-5.2: pending.** The synthetic subscription was rate-limited at run time
(verified by a direct probe — *"exceeded your subscription rate limits"*), so the
worker calls 429'd at dispatch. Its cells are withheld rather than reported as a
capability result; they land once the throttle clears.

Durable results: [`tools/bugfix-bakeoff/external/results/`](../../tools/bugfix-bakeoff/external/results).
Narrated deck: [`docs/decks/query-string-bakeoff.slidey.html`](../decks/query-string-bakeoff.slidey.html).

## The cost comparison (operator-run)

The scaffold above is free and deterministic. The **cost** number — *what would
the fix kitsoki proposes have cost?* — comes from running the real, LLM-bearing
cells (operator-only, never in CI), exactly like the parent bake-off:

1. From a bug's `baseline_sha` worktree, drive `stories/bugfix` under a candidate
   model (kitsoki treatment) **or** a single multi-stage prompt (control).
2. Score the resulting tree with `bench.py score … --out results/cells/<cell>.json`.
3. The cell's `cost_usd` comes from the kitsoki trace
   (`payload.meta.cost_usd`) — the exact price of the proposed fix.

That yields, per bug, a head-to-head: **kitsoki's proposed diff + its verdict +
its dollar cost**, against the real maintainer fix — the concrete evidence a
prospective user needs. See
[`tools/bugfix-bakeoff/external/README.md`](../../tools/bugfix-bakeoff/external/README.md)
for the exact cell procedure and
[`bugfix-bakeoff.md`](bugfix-bakeoff.md) for the load-bearing gotchas (RED
pre-flight, hidden-oracle adjudication, one-basis cost).

## See also

- [bugfix-bakeoff.md](bugfix-bakeoff.md) — the parent study (kitsoki's own bugs)
- [tools/bugfix-bakeoff/external/](../../tools/bugfix-bakeoff/external) — the harness
- [project-onboarding.md](../project-onboarding.md) — onboarding any repo
