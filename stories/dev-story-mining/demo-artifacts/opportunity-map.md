# Dev-Story-Mining Opportunity Map

Brief: `stories/dev-story-mining/demo-artifacts/brief.md`

## Summary

The mined corpus produced 9 in-scope opportunities: 4 actionable improvements, 4 already modeled behaviors, and 1 platform-limited enforcement item.

| Status | Opportunity | Artifact kind | Target | Validator |
|---|---|---|---|---|
| ACTIONABLE | Promote transcript-source planning into deterministic Starlark. | STARLARK-SCRIPT | `stories/dev-story-mining/scripts/plan_sources.star` | `go run ./cmd/kitsoki test flows stories/dev-story-mining/app.yaml --flows flows/happy_human.yaml --v` |
| ACTIONABLE | Render per-harness mining totals before the operator accepts the brief. | ENRICH-STORY | `stories/dev-story-mining/rooms/mine.yaml` | Flow fixture shows conversations, messages, tokens, and total intents. |
| ACTIONABLE | Link markdown reports directly from room KV values. | ENRICH-STORY | `stories/dev-story-mining/rooms/mine.yaml`, `rooms/map.yaml`, `rooms/record.yaml` | Playwright clicks each markdown link and observes the modal. |
| ACTIONABLE | Expose authored changes through the existing diff viewer. | ENRICH-STORY | `tools/runstatus/src/components/ViewElement.vue` | Playwright opens a `.diff` KV value and observes `UnifiedDiff`. |
| LIMITED | Codex cannot be hard-intercepted before the model today. | ENFORCEMENT-LIMIT | `docs/architecture/prompt-intercept.md` | Story copy names the limit and routes through launchers/MCP/mining feedback. |
| ALREADY-MODELED | Human/LLM judge polymorphism. | EXISTING-STORY | `stories/dev-story-mining/rooms/*.yaml` | Existing `judge_mode` gates mirror `bugfix`. |
| ALREADY-MODELED | No-LLM flow coverage for the happy path. | EXISTING-STORY | `stories/dev-story-mining/flows/happy_human.yaml` | Flow skips live LLM calls through seeded artifacts. |
| ALREADY-MODELED | Refine budget exits. | EXISTING-STORY | `flows/prepare_refine_budget.yaml`, `flows/map_refine_budget.yaml` | Existing flow tests cover budget exhaustion. |
| ALREADY-MODELED | Progressive determinism terminology. | EXISTING-STORY | `stories/dev-story-mining/scripts/plan_sources.star` | Prepare room renders the L0-L4 ladder. |

## Selected Next Step

Apply the UI/story polish first: it makes the mining loop readable and reviewable, then shows the actual markdown and diff artifacts needed to trust the result.
