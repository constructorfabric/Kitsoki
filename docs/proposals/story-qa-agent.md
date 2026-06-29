# Epic: Story QA agent — drive a story as a human would

**Status:** Draft v1. Re-scoped: the frame composer, `kitsoki drive`, and
`kitsoki shot` slices were absorbed into the **mcp-studio** epic (which exposes
author/drive/see as an MCP server) and have since **shipped** — see
[`docs/architecture/mcp-studio.md`](../architecture/mcp-studio.md). This epic now
owns only its one remaining slice — the **QA agent skill**, a *consumer* of that
shipped server. A local skill scaffold and runner now exist, but the full live
persona/scenario loop still needs completion. **Slices:** 1 owned (0.5/1
shipped); its substrate shipped under mcp-studio.

## Why

The `devstory` story (and every operator story) is built by an AI agent and
driven by a human. **Every bug only the human sees is one the AI wrote blind** —
that framing is the whole reason
[`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) exists. The shipped
trace + `turn` + `inspect` surfaces let the AI *probe* a story; they don't let it
*use* one with judgement: read a view, decide what to type next, and discover that
the menu is confusing or the objective is two turns further than it should be.

The substrate to *use* a story — the human-fidelity frame, an interactive drive
loop, a screenshot — is now built by [`mcp-studio`](../architecture/mcp-studio.md) and exposed to
any external agent as MCP tools. This epic is the **agent that wields it**: handed
a *persona* and a *scenario*, it walks a story end-to-end through the studio's
`session.drive` + `render.*` tools, reading the exact screen a human would, and
reports on view quality, navigability, intuitiveness, and whether the process
objective is actually reachable.

## What changes

Once this slice ships (on top of `mcp-studio`):

- **A `story-qa` agent skill** connects to the `kitsoki mcp` studio server, opens
  a driving session, and walks a story against a persona + scenario — submitting
  free text via `session.drive`, reading the `Frame` + a `render.web`/`render.tui_png`
  screenshot each turn, deciding its own next input — then scores a UX rubric and
  emits a markdown report with embedded screenshots and a concrete, file-grounded
  bug list.

The capabilities it consumes — the `Frame` (`{text, ansi, metadata}`), the
interactive drive loop with VCR record/replay, and the terminal/web screenshots —
are designed and owned by the `mcp-studio` slices, not here.

## Impact

- **Spans:** tooling (the agent skill + rubric). The tui/runtime substrate moved
  to [`mcp-studio`](../architecture/mcp-studio.md).
- **Net surface:** new `.agents/skills/story-qa/` only; everything else is a
  consumer of the MCP studio tools.
- **Builds on:** [`mcp-studio`](../architecture/mcp-studio.md) (frame composer, `kitsoki drive`,
  `kitsoki shot`, web screenshot, and the `session.*`/`render.*` tools) and the
  [`view-rendering-readability`](view-rendering-readability.md) epic (fidelity
  improves for free as that lands).
- **Docs on ship:** the new skill's `SKILL.md`, `docs/testing.md` (exploratory UX
  QA vs. the deterministic flow gate).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Frame seam | tui | One composer → the full human screen | — | **Shipped** (mcp-studio) | — |
| 2 | `kitsoki drive` | runtime | Interactive headless driver + VCR | 1 | **Shipped** (mcp-studio) | — |
| 3 | `kitsoki shot` | tui | ANSI→PNG of a Frame | 1 | **Shipped** (mcp-studio) | — |
| 4 | `story-qa` agent | tooling | Persona + scenario → studio drive loop → scored UX rubric + report + screenshots + bug list | mcp-studio | Draft | [`qa-agent-skill.md`](qa-agent-skill.md) |

Slices 1–3 (plus the web-screenshot seam) shipped under the **mcp-studio**
epic — [`docs/architecture/mcp-studio.md`](../architecture/mcp-studio.md) and
[`docs/tui/frame-composition.md`](../tui/frame-composition.md); only slice 4
remains.

## Sequencing

The substrate ships under `mcp-studio`; this epic's one slice lands on top once
the studio's `session.*` + `render.*` tools (mcp-studio #7) exist.

```
mcp-studio (#1 frame, #2 drive, #3 shot, #4 web-shot, #7 session+render tools)
        └────────────────────────────────────────────────▶ #4 story-qa agent
```

## Shared decisions

Deferred to [`mcp-studio`](../architecture/mcp-studio.md): the `Frame` as the unit of fidelity
(its shared decision 2), the no-LLM-by-default / live-opt-in posture (its 3), and
"don't fork the renderers" (its 4). This epic adds one:

1. **Every finding is labeled with the mode that produced it.** View/rendering
   findings are deterministic and replay-safe; objective-achievability findings
   need a live model (the studio's `harness: live`). The agent skill tags each
   finding so a reviewer knows which were free and which cost a token.

## Cross-cutting open questions

1. **Does the agent need scrollback, or only the current screen?** *Lean: the
   `Frame` is the current screen; the studio's per-turn JSONL/trace preserves the
   history, so the agent has both (mcp-studio inherits this).*

## Non-goals

- The frame composer, drive loop, or screenshots — owned by
  [`mcp-studio`](../architecture/mcp-studio.md).
- A new view renderer — [`view-rendering-readability`](view-rendering-readability.md).
- Replacing flow fixtures / `kitsoki test` — those stay the deterministic
  correctness gate; this is exploratory UX QA on top.
