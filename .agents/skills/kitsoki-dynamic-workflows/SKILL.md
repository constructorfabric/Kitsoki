---
name: kitsoki-dynamic-workflows
description: Use Kitsoki's dynamic workflow and Studio MCP surfaces from Codex to turn a broad implementation, research, QA, mining, or review goal into a traceable workflow with worker fan-out, deterministic validation, and optional export to a reusable story. Use when a task is too broad for one linear Codex pass, naturally decomposes into multiple worker briefs, should be driven through `workflow.*` / `session.*` MCP tools, or asks for dynamic workflows, agent fan-out, Kitsoki MCP workers, or Claude-Code-like worker orchestration from Codex.
---

# Kitsoki Dynamic Workflows

## Overview

Drive broad Codex work through Kitsoki instead of hand-spawning ad hoc workers.
Kitsoki owns the workflow draft, worker model/profile, session trace, receipts,
and exportable story artifacts; Codex supervises, verifies, and lands the result.

Use this skill when the useful unit is a workflow, not a single file edit:

- a goal needs discovery, implementation, review, and verification phases;
- independent slices can run as worker briefs;
- the result needs an auditable trace or reusable story package;
- the user asks to dogfood Kitsoki, use Studio MCP, or use dynamic workflows.

For tiny changes, normal Codex editing is still cheaper and clearer.

## Studio MCP First

Prefer the attached Kitsoki MCP tools when they are available. Start every run
by proving the server is current:

1. Call `studio.ping`.
2. Call `studio.handles`.
3. Compare `studio.ping.revision` with `git rev-parse HEAD` for the checkout you
   are about to use. If `revision` is missing, differs from `HEAD`, or
   `working_dir` / `executable` point at an unexpected checkout, treat the MCP
   process as stale and reconnect/reload before trusting `workflow.*` output.
4. If the workflow depends on recently added fields, make a harmless
   `workflow.status`, `story.validate`, or similar call before spending a live
   run.

If the attached MCP server is stale, reconnect or reload it before diagnosing the
story. Rebuilding Kitsoki does not update an already attached MCP process.

When no MCP attachment is available, use the CLI fallback from the repo root:

```sh
go run ./cmd/kitsoki workflow create "<goal>" --slug <slug>
go run ./cmd/kitsoki workflow validate <workflow-id>
go run ./cmd/kitsoki workflow run <workflow-id>
```

Do not build a persistent Kitsoki binary for this unless the task specifically
requires it.

## Dynamic Workflow Loop

1. **Shape the goal.** Write a one-paragraph goal that includes the target repo,
   expected deliverables, validation gates, artifact locations, and cost limits.
   Save substantial drive briefs under `.context/`.

2. **Create the workflow.** Use `workflow.create` with `{goal, slug?}`. Inspect
   the receipt fields: `workflow_id`, draft path, manifest path, validation
   report, events path, launch command, and trace path.

3. **Validate before launch.** Use `workflow.validate`. Treat validation failures
   as workflow-shape bugs to fix before any worker fan-out.

4. **Launch through Studio.** Use `workflow.launch`. The receipt should include a
   `session_id`, `session_handle`, `trace_path`, and session route. Use
   `workflow.status` to reacquire receipt state after interruptions.

5. **Drive intentionally.** Prefer `session.submit` for deterministic choices.
   Use `session.drive` when the point is to exercise free-text routing or worker
   interpretation. Poll `session.status` when a turn returns `running`.

6. **Inspect the trace.** Use `session.trace` when behavior is surprising. The
   trace is the ground truth for routing, host calls, worker calls, cost, and
   swallowed `on_error` arcs.

7. **Export only when useful.** Use `workflow.export` when the run should become
   a reusable story package. Review the generated `README.md`, flow fixture,
   cassette, and `export-report.json` before treating it as finished.

## Worker Discipline

Let Kitsoki dispatch worker agents through the workflow/story. Do not replace
that with informal local subagents unless Kitsoki lacks the required surface and
you file or note the gap.

Worker briefs should include:

- the exact worktree or workspace boundary;
- the intended deliverable and non-goals;
- deterministic validation commands;
- where to write review artifacts under `.context/` or `.artifacts/`;
- whether live LLM use is allowed. Default to no real LLM in tests.

For a long-running implementation worker, also give a report path under
`.artifacts/` or `.context/` and require it to write the report after the first
useful diagnosis, updating it during implementation and validation. The final
structured response should return that path plus compact status facts, not a
large required prose report. The supervisor should inspect the file before
accepting the receipt.

Keep the supervisor role separate from the worker role. The supervisor checks
the receipt, trace, diff, untracked files, and gates. Do not accept a worker's
claim of success without independent verification.

## Validation

Use deterministic gates before and after worker fan-out:

```sh
go test ./internal/dynamicworkflow ./internal/mcp/studio -run 'Test.*Workflow'
go run ./cmd/kitsoki mcp-test --stories-dir ./stories --tool workflow.create --tool-args '{"goal":"smoke dynamic workflow fan-out","slug":"smoke-dynamic-workflow"}'
```

Adjust the test scope to the files touched. Prefer `story.validate`,
`story.test`, `workflow.validate`, `render.tui`, and `render.web` over live
agent reruns when verifying a change. Automated tests must use replay,
cassettes, mocked agents, or deterministic MCP calls; do not spend LLM tokens in
tests unless the user explicitly asks for live integration.

## References

- `docs/stories/dynamic-workflows.md` - dynamic workflow CLI/MCP behavior and
  receipt artifacts.
- `docs/architecture/mcp-studio.md` - `workflow.*`, `session.*`, `story.*`, and
  render tool contracts.
- `docs/recipes/studio-mcp-dogfood.md` - strict Studio MCP dogfood runbook for
  Codex and Claude.
