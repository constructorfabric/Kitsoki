# Dev-Story-Mining Final Report

Job: `intents-20260615`

## What Was Found

- 158 conversations were reviewed across Claude Code and Codex.
- 5,980 messages were reviewed.
- 612,000 Claude tokens were available from local usage records; Codex token totals were unavailable and were not estimated.
- 203 intents were mined.
- The top missed opportunity was repeated ad hoc transcript-source planning that should be owned by the dev-story-mining prepare room and its Starlark planner.

## What Was Applied

- PREPARE now renders a short source plan instead of a clipped code table.
- MINE now renders per-harness counts, total messages, known tokens, total intents, and an explicit Green/Yellow/Red legend.
- The brief, opportunity map, and final report are linked as markdown artifacts.
- `.diff` and `.patch` KV values open a real unified diff modal in the web UI.
- Mining is announced before MINE, and mapping now has its own pre-MAP checkpoint before review.

## Progressive Determinism

The source-policy step moved from L1 prose to L2 deterministic structure: the story records the source matrix, target artifact classes, enforcement boundary, and L0-L4 ladder before any mining agent runs.

No L3/L4 move was claimed. The corpus shows a strong repeated pattern, but future recorded decisions should accumulate before replacing the remaining human/LLM gates with defaults.

## Validation

- Story flow: `go run ./cmd/kitsoki test flows stories/dev-story-mining/app.yaml --flows flows/happy_human.yaml --v`
- Browser demo: `WEB_CHAT_PACE=0 pnpm -C tools/runstatus exec playwright test dev-story-mining-video --project=chromium`
- Visual QA: `.agents/skills/kitsoki-ui-qa/scripts/qa.sh`
