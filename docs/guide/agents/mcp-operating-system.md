# MCP operating-system rollout guide

The MCP operating-system profile is a controlled candidate for work that needs
an objective, a managed workspace, typed validation gates, and retained
evidence. It is **not enabled as the default**. The current replay decision is
**HOLD** because strict handling of `trace-stalled-turn` is incorrect.

For normal Studio MCP use, follow [Use Kitsoki Through MCP](mcp.md). That guide
and the existing `kitsoki-mcp-driver` remain the active path while the hold is
open.

## Do not migrate yet

Do not replace an existing MCP attachment, default driver profile, or working
automation with `kitsoki-mcp-driver-operating-system` today. Its files express
the preview contract for final integration; they do not register tools or make
the strict profile available by default. A profile present in a checkout is not
promotion evidence.

If an agent is explicitly attached to a future eligible strict surface, its safe
sequence is:

```text
studio.ping → studio.handles
objective.open → workspace.create
workspace.read/search → policy.authorize_mutation → workspace.patch/write or workspace.codeact
gate.catalog → gate.run → evidence.record → receipt.list → objective.close
```

Use `studio.diagnose`, `session.explain`, or `trace.explain` before changing a
workspace, attachment, or policy assumption. These operations make failure
evidence inspectable rather than masking it with a broad shell command.

## Strict, escape, and legacy

`strict` is the only profile eligible for future promotion. It has no
`host.run`, `host.patch`, or raw-worktree tool path. A strict policy rejection
does not permit an agent to retry through another tool family.

`escape` is a separately authorized exception path. It requires a recorded
reason and evidence, and its outcome never counts as strict replay evidence.
`legacy` describes the existing toolbox for compatibility and comparison; it is
not a safe fallback and cannot be promoted while its replay safety failures
remain.

During the HOLD, use the existing documented Studio MCP workflow when that is
the authorized path. For candidate work, run deterministic replay and diagnosis
only; do not claim strict rollout or default migration.

## Verify before a future opt-in

Before offering any opt-in, a maintainer must run:

```sh
python3 tools/arena/tests/test_mcp_operating_system_decision.py
GOCACHE=/private/tmp/kitsoki-gocache go test ./internal/mcp/studio -run 'TestToolSchema'
```

The replay bundle must show every strict safety and correctness cell passing.
Until then, the expected result is HOLD with `trace-stalled-turn` named in the
review evidence. Cost or latency cannot make a held candidate eligible.

## Live calibration requires an operator decision

Automated tests never invoke a live model. A live calibration request needs the
exact token `I_UNDERSTAND_LIVE_CALIBRATION`, a recorded budget of at least USD
1.00, and a separately approved live execution plan. The current authorization
command records only `authorized-not-dispatched`; it does not call a provider or
promote the profile.

## Rollback

If a future opt-in regresses, stop new opt-ins, preserve receipts and traces,
and return the affected caller to the explicitly recorded legacy/default
attachment. Do not delete evidence or silently broaden strict. Fix the failing
replay case, regenerate the no-LLM decision, and wait for final integration
approval before trying rollout again.

For architecture and ownership, see
[MCP operating system — controlled rollout architecture](../../architecture/mcp-operating-system.md).
