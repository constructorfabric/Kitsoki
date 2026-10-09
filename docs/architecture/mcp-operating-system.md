# MCP operating system — strict-default architecture

The MCP operating system is a candidate control plane for agents that need a
scoped change, proof, and durable evidence. Strict is the default Studio MCP
profile; the older toolbox is available only through an explicit `legacy`
profile selection.

## Rollout state

**Operator-selected strict default; refreshed replay pending.** `kitsoki mcp`
now starts strict unless an operator explicitly selects `legacy` or `escape`.
The recorded replay below predates the strict lifecycle repairs, so it remains
historical evidence rather than a current promotion record. This default also
does **not** establish a model-quality advantage over raw Codex; the native
GPT-5.4 comparison remains a tie on correctness with materially higher MCP
time and token use.

The decision is generated from
[`tools/arena/specs/mcp-operating-system-replay.yaml`](../../tools/arena/specs/mcp-operating-system-replay.yaml)
and enforced by
[`internal/agentbench/mcp_operating_system_decision.go`](../../internal/agentbench/mcp_operating_system_decision.go).

| Historical replay candidate | Safety | Correctness | Recorded disposition |
|---|---:|---:|---|
| `strict` | pass | fail (`trace-stalled-turn`) | hold |
| `escape` | fail | pass | not promotable |
| `legacy` | fail | pass | not promotable |

The existing Studio MCP architecture remains documented in [MCP studio](mcp-studio.md).
This page describes the default governance boundary and its explicit escapes.

## Proposed control plane

The strict profile makes a mutation an evidence-bearing lifecycle:

1. `objective.open` records the bounded outcome.
2. `workspace.create` creates a server-held managed workspace; reads, search,
   patch/write, CodeAct, commit, merge, and teardown stay on that plane.
3. `policy.authorize_mutation` checks the requested mutation belongs to the
   objective.
4. `gate.catalog` and `gate.run` execute a named, typed validation gate.
5. `evidence.record` and `receipt.list` retain validation proof.
6. `objective.close` requires retained evidence; `objective.reopen` makes a
   changed or unproven outcome explicit.

`studio.diagnose`, `session.explain`, and `trace.explain` are the bounded
diagnostic path. They are preferred over changing an attachment, workspace, or
policy assumption based on an opaque failure.

The strict profile deliberately omits `host.run` and `host.patch`, and it does
not create raw git worktrees. Those are containment decisions, not aliases: a
tool absent from the profile cannot be reached by changing the prompt.

Strict nevertheless retains a bounded direct-submit session-driver loop:
`session.new`, `session.submit`, `session.answer`, `session.status`,
`session.inspect`, `session.world`, `session.trace`, and `session.close`.
It exists so an attached agent can run and observe the strict pipeline itself;
it does not expose free-text `session.drive`, story authoring, or a raw host
execution path.

## Profiles and migration boundary

`strict` is the default. `escape` is a separate, receipt-bearing exception
path; `legacy` is an explicit compatibility toolbox. A strict rejection never
silently falls back to `host.run`, `host.patch`, raw worktree tools, or legacy.
An operator must choose the alternative profile deliberately and retain its
exception evidence.

## Escape and rollback

An escape is not an automatic fallback. The caller must state why strict cannot
proceed, use the separately authorized escape profile, and record the exception
and resulting evidence. The escape result cannot become strict promotion
evidence.

The rollout is reversible: preserve managed-workspace receipts and traces, then
return an affected client to an explicitly recorded `legacy` or `escape`
attachment while the strict defect is repaired and replayed.

## Decision and live-calibration boundary

The replay matrix is deterministic and provider-free. Regenerate it with:

```sh
python3 tools/arena/arena/mcp_operating_system_report.py report \
  --spec tools/arena/specs/mcp-operating-system-replay.yaml \
  --out .artifacts/mcp-operating-system/replay
```

The derived report, JSON decision, and Slidey deck are evidence; none dispatches
a provider call. Regenerate the replay against the strict-default source before
using it as a release gate. It is a governance gate, not a model-superiority
claim.

Live calibration is outside automated tests. It needs an operator to supply the
exact authorization token `I_UNDERSTAND_LIVE_CALIBRATION`, a recorded budget of
at least USD 1.00, and a separately approved live execution path. The current
`live-calibration` command records only `authorized-not-dispatched`; it does not
call a provider. Tests must never pass a live-calibration flag or treat
authorization as a promotion.

## Ownership

This document owns the default governance behavior; Studio registration and the
canonical agent guide must remain aligned with it.
