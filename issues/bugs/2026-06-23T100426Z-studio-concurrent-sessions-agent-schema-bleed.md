---
id: 2026-06-23T100426Z-studio-concurrent-sessions-agent-schema-bleed
title: "Concurrent live driver sessions in one studio process cross-contaminate host.agent.task acceptance-schema resolution (story dir bleeds between sessions)"
target: kitsoki
filed_at: 2026-06-23T10:04:26Z
status: fixed
severity: P1
component: mcp
kitsoki_rev: 8cd8657f
trace_ref: "fixed: per-call renderer resolution; regression TestAgentAskWithMCP_SchemaResolvesAgainstPerCallRenderer (host pkg, green)"
external: {}
assignee: ""
related:
  - 2026-06-23T092411Z-mcp-live-harness-no-profile-uses-synthetic
url: "issues/bugs/2026-06-23T100426Z-studio-concurrent-sessions-agent-schema-bleed.md"
---

## Body

When MULTIPLE live driving sessions run concurrently in a single `kitsoki mcp`
studio process, a `host.agent.task` dispatch in one session can resolve its
`acceptance.schema` against the WRONG session's story directory. Observed: a
session driving `stories/bugfix` had its `implementing`-room implementer agent
resolve `acceptance.schema: schemas/implementing_artifact.json` to
`stories/cherny-loop/schemas/implementing_artifact.json` (a different story) and
fail with "schema not found" before the LLM ran — bouncing `implementing → idle`
via `on_error` and dead-ending the pipeline.

The studio process had 4 concurrent live sessions, at least one driving
cherny-loop. The wrong base (`stories/cherny-loop`) strongly implicates a
process-global / last-active "current story dir" being read by the agent
dispatch instead of the per-session story path.

## Expected

Each driving session's `host.agent.task` resolves story-relative paths
(`acceptance.schema`, `prompt`/`prompt_path`, `working_dir` defaults) against
THAT session's own story directory, isolated from any other concurrent session.

## Actual

The implementer's schema base leaked from a concurrently-active cherny-loop
session into the bugfix session. Reproducible: re-driving start→accept→accept
hit the deterministically identical failure. Within the SAME bugfix session,
earlier rooms resolved correctly (`reproducing`/`proposing` → `stories/bugfix`);
only the `implementing` dispatch (later, while cherny-loop was active) resolved
to `stories/cherny-loop`.

## Evidence

- Trace (turn 2): `agent.call.start` (implementer, claude-sonnet-4-6) →
  `world.update last_error` (cherny-loop schema not found) →
  `machine.transition implementing→idle (on_error)`.
- `story_read` confirmed `stories/bugfix/schemas/implementing_artifact.json`
  EXISTS, so the correct path was available; the dispatch used the wrong base.
- Trace bundle: `.artifacts/dogfood/bugfix-live-trace.jsonl`.

## Impact

P1 for the MCP-first parallel-delegation model: you cannot safely run more than
one live driver session per studio process — a second session silently corrupts
the first's agent dispatch, blocking real deliveries. It blocked the
`mcp-live-harness-no-profile-uses-synthetic` fix mid-pipeline (the reproducer's
RED-gate test landed uncommitted; the implementer never ran).

## Suggested fix

Thread the driving session's story dir into the `host.agent.task` handler via
the dispatch context (alongside the existing `KITSOKI_SESSION_ID`) and resolve
`acceptance.schema` (and other story-relative paths) relative to it per-dispatch,
isolated per concurrent driving session — so one session's story dir cannot
contaminate another's agent dispatch.

## Notes

Hypothesis (cross-session bleed) is the driver's best explanation from trace +
the 4-concurrent-sessions context; the root cause in `internal/mcp/studio`
session-runtime / agent-dispatch path is not yet pinned by reading the code. The
symptom and reproducibility are confirmed. Surfaced 2026-06-23 running two live
drivers in parallel (a bug fix + a proposal delivery) per an explicit parallel
request — the failure is the signal that concurrent live drivers aren't isolated.
