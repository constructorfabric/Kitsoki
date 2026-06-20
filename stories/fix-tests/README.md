# fix-tests — auto-fix failing tests

A self-driving, operator-facing maintenance story. It runs the project's test
suite and, if anything fails, uses **claude (sonnet)** to fix the failures —
re-running the suite to confirm — then writes a Markdown report. It is meant to
be run headless from a Makefile, not played at a TUI.

```
make fix-tests
```

## What it does

```
idle ──start──▶ running_executing ──green──▶ done_clean        (exit 0)
                       │ red
                       ▼
                 fixing_executing ──needs_decision──▶ blocked   (exit ≠0)
                       │ fixed
                       ▼
                 verifying_executing ──green──▶ done_clean      (exit 0)
                       │ red, budget left          ▲
                       └──────────▶ fixing_executing ┘
                       │ red, budget exhausted
                       ▼
                 done_exhausted                                 (exit ≠0)
```

1. **running_executing** — runs `make test` (background) and branches on the
   exit code: green → `done_clean`; red → `fixing_executing`.
2. **fixing_executing** — the `fixer` agent (claude / `claude-sonnet-4-6`, with
   `Read/Grep/Glob/Edit/Write/Bash`) reads the failure output and applies the
   minimal fix. It returns a structured artifact
   ([`schemas/fix_artifact.json`](./schemas/fix_artifact.json)). If a failure
   needs a product/architecture decision it must not make alone, it sets
   `needs_decision: true`, makes no edits, and routes to `blocked`.
3. **verifying_executing** — re-runs `make test`. Green → `done_clean`; red with
   budget left → another `fixing_executing` cycle; red with the budget
   exhausted → `done_exhausted`.
4. The three terminals write a report to `.artifacts/fix-tests/report-*.md`
   (via [`scripts/write_report.py`](./scripts/write_report.py)) and mirror a
   checkpoint into the operator inbox.

It "one-shots" by default — there is no human in the loop. The single point
where it stops and *asks* is a real, decision-requiring question: it surfaces
that in the report (and exits nonzero) rather than guessing.

## Architecture notes

- **Background jobs + `on_complete`.** Every test run and the fix are
  `background: true` invokes whose `on_complete:` targets the next room. Each
  completion is a fresh synthetic turn, so the engine's synchronous
  post-bind-emit recursion cap (4) never applies and the retry loop can run a
  real 3 cycles. `kitsoki session continue` drains the `*_executing` chain to a
  terminal in one invocation (its drain loop iterates while the state ends in
  `_executing`, up to 8 passes — the 3-cycle worst case is 7 jobs).
- **`max_cycles` defaults to 3 and should stay ≤ 3** for that drain budget.
- **The fixer edits your working tree.** Review the diff afterwards. It is
  forbidden (by prompt) from weakening tests, touching git, or making network
  calls; `external_side_effect: false`.

## Configuration

`world.test_cmd` (default `make test`) and `world.max_cycles` (default `3`) can
be overridden via the `start` intent's slots, e.g. with a custom driver:

```
kitsoki session continue --app stories/fix-tests/app.yaml --id <sid> \
  --intent start --slots '{"test_cmd":"go test ./internal/foo/...","max_cycles":2}'
```

## Standalone story

This story is **not importable** — it has no `exits:`, `host_interfaces:`, or
`world_in:` contract. It is a top-level app driven by `make fix-tests`.

Hosts used: `host.run`, `host.agent.task`, `host.inbox.add`.

## Tests

Deterministic, no-LLM flow fixtures under [`flows/`](./flows/) cover every
branch (stubbed host results, `advance_clock` to drain the background jobs):

| Flow | Path |
|---|---|
| Suite green on first run | [`clean.yaml`](./flows/clean.yaml) |
| One fix cycle then green | [`fix_then_pass.yaml`](./flows/fix_then_pass.yaml) |
| Still red after 3 cycles | [`exhausted.yaml`](./flows/exhausted.yaml) |
| Fixer needs a decision | [`blocked.yaml`](./flows/blocked.yaml) |

```
kitsoki test flows stories/fix-tests/app.yaml
```
