# Story QA

Story QA is the review pass between "the story loads" and "this is ready for
humans to use." It combines deterministic checks, graph review, rendered-view
review, skeptical operator walkthroughs, and targeted flow hardening. The output
is either a short QA report in `.context/` or a commit that fixes the story and
records what was verified.

The local runnable wrapper for this repository is [`tools/story-qa/run.py`](../../tools/story-qa/run.py):
it summarizes the three project lanes the exploratory QA pass cares about and
executes the deterministic `gears-rust` verifier through a temporary no-local
clone when a local checkout is available.

Automated QA must not call a live LLM. Use flow fixtures, recordings, cassettes,
and stubbed host handlers. Live runs are optional exploratory checks and must be
explicitly requested.

## The Loop

1. **Inventory the story.** Read `README.md`, `app.yaml`, rooms, prompts, schemas,
   recordings/cassettes, and existing flows. Identify the intended operator path,
   exits, host calls, budgets, and known non-goals.
2. **Validate load and graph shape.** Load the story, render docs, and emit a
   graph. Confirm the root is a real action room, exits are reachable, no room is
   ceremony, and host allow-list / world references are declared.
3. **Run deterministic flows.** Run `kitsoki test flows <story>/app.yaml --v`.
   Every non-trivial branch should have a fixture, and every fixture should set
   `expect_no_errors: true` unless it is intentionally asserting a validation
   error.
4. **Tighten fixtures into contracts.** Do not stop at `expect_state`. Assert the
   world values that define the outcome, plus `expect_host_calls` /
   `expect_no_host_calls` for side effects. Use `by_call:` on stubs when one
   handler appears at multiple call sites.
5. **Probe human-facing views.** Use `kitsoki turn` for important rooms and edge
   states. Check that views are nonblank, show actionable choices, surface errors,
   and do not require a pointless `begin` / `continue` turn.
6. **Walk it as a skeptical operator.** Ask what a capable engineer would do if
   they did not already buy Kitsoki's framing. The first screen must make the
   next action obvious, accept natural phrasing where the story claims to be
   free-form, and show why the graph adds value over a plain CLI prompt.
7. **Cover failure and guard paths.** Add fixtures for host errors, blocked
   launches, budget exhaustion, retry/reconfigure/abort paths, and any branch that
   exists specifically to prevent runaway or costly behavior.
8. **Record evidence.** Save transient reports or generated artifacts under
   `.context/` and `.artifacts/`; keep committed docs focused and reusable.

Useful commands:

```sh
go run ./cmd/kitsoki test flows stories/<story>/app.yaml --v
go run ./cmd/kitsoki test flows stories/<story>/app.yaml --json .artifacts/<story>/flows.json
go run ./cmd/kitsoki render stories/<story>/app.yaml -o .artifacts/<story>/rendered.md
go run ./cmd/kitsoki viz stories/<story>/app.yaml --mermaid --out .artifacts/<story>/state.mmd
go run ./cmd/kitsoki turn stories/<story>/app.yaml --state <room> --intent look
```

## Coverage Checklist

Use this as a minimum bar for an operator story:

- The story loads through the same loader used by MCP studio or `kitsoki test`.
- `root:` is the first useful room; there is no pass-through idle/begin room.
- Each public exit has at least one flow that reaches it.
- Each host-backed room has a flow that proves the expected handler fired.
- Each free-form entry point has an `input:` flow backed by `recording.yaml`, not
  only structured `intent:` fixtures.
- Each expensive or stateful host call is either idempotent or covered by a
  reload/re-entry/error fixture.
- Each guard that protects cost, budget, validity, or safety has a negative flow.
- Each `on_error:` target surfaces the error and has an operator recovery path.
- Every autonomous loop has a termination fixture and a fixture proving it carries
  feedback or state forward correctly.
- The rendered documentation and graph match the story's README narrative.
- Cassettes used for demos lint cleanly.

## Case Study: `stories/cherny-loop`

The Cherny loop story is the current worked example for this loop.

Scope:

- Root room: `configuring`, intentionally no idle/begin ceremony.
- Public exits: `achieved`, `exhausted`, `abandoned`.
- Host calls: `host.run` for script gates, `host.agent.decide` for agent gates,
  `host.agent.task` for maker iterations, and `host.artifacts_dir` for iteration
  records.
- Core promise: one free-form setup turn chooses the right proof strategy, then
  one `launch` proves the gate is RED and runs maker -> gate autonomously until
  achievement or budget exhaustion.

QA hardening applied:

- Flow fixtures assert `expect_no_errors: true`.
- Happy paths assert the exact host-call sequence via `expect_host_calls`.
- The baseline-green fixture asserts `host.agent.task` and `host.artifacts_dir`
  do not fire, proving the loop refuses to spend on a gate that already passes.
- Free-form fixtures prove ad hoc input can be recorded, then planned into an
  agent gate or a hybrid script-plus-review gate.
- A maker failure fixture covers `on_error: iterating_error` and retry recovery.
- The web-tour fixture remains a deterministic no-LLM proof of the autonomous
  multi-iteration path.

Evidence commands:

```sh
go run ./cmd/kitsoki test flows stories/cherny-loop/app.yaml --v
go run ./cmd/kitsoki cassette lint stories/cherny-loop/flows/cassettes/web_tour.cassette.yaml
go run ./cmd/kitsoki render stories/cherny-loop/app.yaml -o .artifacts/cherny-loop/rendered.md
go run ./cmd/kitsoki viz stories/cherny-loop/app.yaml --mermaid --out .artifacts/cherny-loop/state.mmd
```

The corresponding transient QA report for the first pass is
`.context/cherny-loop-qa.md`.
