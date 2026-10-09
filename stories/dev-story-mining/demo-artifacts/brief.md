# Dev-Story-Mining Brief

Job: `intents-20260615`

## Corpus Reviewed

| Harness | Conversations | Messages | Tokens reviewed | Intents mined | Signal notes |
|---|---:|---:|---:|---:|---|
| Claude Code | 120 | 4,860 | 612,000 | 154 | User corrections and usage records available. |
| Codex | 38 | 1,120 | unavailable | 49 | Tool outcomes join cleanly; token sidecar is missing. |
| Total | 158 | 5,980 | 612,000 known | 203 | Codex token count is explicitly unavailable. |

## Determinism Split

| Color | Meaning | Intents |
|---|---|---:|
| Green | Deterministic or defaultable story/script behavior. | 111 |
| Yellow | Agent-gated behavior that still needs an LLM or human on edge cases. | 87 |
| Red | Irreducible judgment that should remain a human/LLM decision. | 5 |

## Recurring Missed Opportunities

- Source planning was repeatedly handled as ad hoc shell/prose instead of a deterministic Kitsoki room plus Starlark script.
- Transcript-mining results were reviewed without a visible per-harness count breakdown.
- Operators had to infer what green/yellow/red meant instead of seeing a legend next to the split.
- Report markdown and authored diffs were not linked from the story views, even though the web UI can open artifacts in modals.
- Codex usage enforcement was sometimes discussed as if hard interception existed; the story must state that Codex currently needs launchers, MCP workflows, guidance, and mining feedback instead.

## Recommended First Improvement

Promote transcript-source planning into `stories/dev-story-mining/scripts/plan_sources.star` and render the resulting source policy before any mining agent runs. This starts the loop at L2: a deterministic skeleton with named recorded gates.
