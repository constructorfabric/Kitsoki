# Case study: cheaper models for room-by-room routing

This study asks whether Kitsoki can route ambiguous turns with cheaper models
such as Haiku, `syn:small:text`, or a GPT mini tier, without weakening the
parts of the system that need a stronger model.

The answer is deliberately scoped: **yes, but only per room or call site, after
that room passes a mined-input eval.** The mechanism is not a global model
downgrade. It is Kitsoki's existing progressive-determinism ladder: deterministic
tiers first, cheap bounded model next where evidence supports it, stronger
fallback only for uncertain or high-risk cases.

## Evidence Used

This report combines three evidence sources:

| source | what it contributes |
|---|---|
| Routing-focused session mine | 115 substantive Kitsoki sessions, 515 user turns, 461 routing-eval candidate inputs, 414 follow-through positives, 47 correction hard negatives |
| Static routing suites | Oregon Trail intent fixtures: 50 fixtures / 104 inputs, useful for deterministic-vs-LLM boundary checks |
| Existing model-harness eval reports | `merge_judge` candidate rows comparing Sonnet and `synthetic-codex` models for a bounded decision |

The session mine was local and unredacted, stored under
`.artifacts/routing-model-corpus/`. The corpus is intentionally not a blind
gold set: a turn is treated as a strong positive when it had concrete
follow-through, and the following user turn is used as a correction signal when
the prior outcome was challenged.

## Why Routing Is The Right Cost Lever

Kitsoki already avoids model spend for most routing:

| tier | role | cost |
|---|---|---|
| Deterministic / example match | exact rendered choices and examples | $0 |
| Semantic synonym and slot templates | authored natural-language aliases with typed slots | $0 |
| Turn cache / default intent | replayed or room-declared fallback | $0 |
| Embedding / contextual tiers | opt-in residual classifiers | low / varies |
| LLM fallback | genuinely ambiguous free text | model cost |

The cost opportunity is therefore not "make all routing cheap." The first
tiers are already free. The opportunity is the residual LLM tier: the turns that
are too ambiguous for synonyms/templates but bounded enough that a small model
may classify them reliably.

## Corpus Shape

The mined corpus is broad enough to stress room routing, not just happy-path
commands:

| label | count |
|---|--:|
| routing-eval items | 461 |
| gold follow-through items | 414 |
| correction / hard-negative items | 47 |
| sessions represented | 111 |
| average tool calls after candidate turn | 21.18 |

The largest routing surfaces in the mine were:

| surface | count |
|---|--:|
| general workbench / mixed | 245 |
| git-ops | 85 |
| runstatus web | 63 |
| dev-story | 38 |
| MCP | 30 |
| PRD | 24 |
| model harness | 17 |
| Cherny loop | 16 |
| meta-mode | 7 |

This is exactly the shape where a global router would be risky: "commit your
changes", "make a tour-driven demo video", "compare models", and "why did this
route wrong?" all look short, but they belong to different rooms with different
failure costs.

## Existing Measured Model Evidence

The current committed model-harness evidence is narrow but useful. The
`pr-refinement` `merge_judge` call site has candidate reports:

| call | profile/model | effort | observations | bar pass rate | effectiveness | p95 latency | avg cost |
|---|---|---|--:|--:|---|---|---|
| `merge_judge` | `claude/claude-sonnet-4-6` | medium | 1 | 100% | 100% / 100% / 100% | 5200ms | $0.0034 |
| `merge_judge` | `synthetic-codex/syn:small:text` | low | 3 | 67% | 100% / 55% / 100% | 3050ms | $0.0008 |
| `merge_judge` | `synthetic-codex/syn:large:text` | low | 1 | 0% | 90% / 90% / 90% | 3900ms | $0.0016 |

`syn:small:text` is 4.25x cheaper than Sonnet on average cost and faster in the
loaded reports, but it missed a hard negative: `missing-merge-sha-blocks-closeout`
returned `accept` where `refine` was expected. That is the important lesson. The
small model is attractive, but hard negatives must be in the eval and fallback
must stay explicit.

## Candidate Tiers

| candidate | current status | recommendation |
|---|---|---|
| Haiku | configured under `claude-native` model catalog, not measured on routing corpus | first conservative cheap-router sweep |
| `synthetic-codex/syn:small:text` | measured on `merge_judge`; cheapest measured candidate; hard-negative miss observed | good candidate with fallback, not global default |
| `synthetic-claude/syn:small:text` | configured, not measured | run alongside synthetic-codex to separate backend effects from model effects |
| GPT mini / OpenAI mini tier | requested but not configured in the current harness profiles | add profile before claiming evidence |
| Sonnet / stronger default | measured quality floor | fallback and comparison baseline |

Pricing and model availability change, so the durable decision should be based
on the per-room eval report, not a hard-coded price table in this document.
Still, current public API pricing makes the tradeoff direction clear: OpenAI
lists GPT-5.4 mini at $0.75 / 1M input and $4.50 / 1M output tokens, while
Anthropic lists Haiku 4.5 at $1 / 1M input and $5 / 1M output tokens. Both are
in the right cost class for bounded routing decisions; neither should be trusted
without the mined hard negatives.

## What Can Switch Now

The only safe immediate switch is the already-scoped one: bounded call sites
that have passing evidence and a fallback profile. `merge_judge` already models
that shape with `selection:` metadata and fallback.

Do **not** switch the whole routing stack to a cheap model. The mined corpus
shows many rooms where short inputs depend on context:

| input shape | risk if globally routed |
|---|---|
| "commit" / "commit your work" | could mean git-ops, local git cleanup, or a user scolding the agent to commit only its changes |
| "do the capture" | depends on the active video/demo room |
| "compare models" | should route into model-harness-eval only in the dev-story workbench |
| "try again" | can mean refine, restart, rerun QA, re-record a demo, or resume failed delivery |

The right policy is:

1. Deterministic tiers stay first.
2. Room-local eval chooses the cheapest passing router for the residual.
3. The cheap router emits confidence and alternatives.
4. Low confidence or known hard-negative classes fall back to Sonnet/stronger.
5. Route receipts and rewind remain visible so mistakes are correctable.

## Room-By-Room Plan

| room / surface | first cheap-router candidate | required evidence |
|---|---|---|
| git-ops hub | `syn:small:text`, Haiku | mined command turns plus selective-staging / amend hard negatives |
| dev-story landing | Haiku first | mixed workbench corpus with ticket/design/implementation split |
| PRD clarifying/search | Haiku, then GPT mini if configured | answer-matching and overlap-decision evals |
| runstatus/meta-mode | Haiku | UI/demo/QA correction turns, especially "video is wrong" feedback |
| model-harness-eval | Haiku + `syn:small:text` | this corpus plus call-site eval reports |

## Cost Mechanism

For a residual routing call, cost roughly scales with:

```
prompt tokens for room context + user input + schema + output tokens
```

Moving residual routing from Sonnet-class to small-model-class can plausibly cut
that residual model spend by 3-5x on measured candidates, while preserving the
$0 deterministic tiers. The savings compound with the existing architecture:
you only pay the cheaper model on the residual, not on every turn.

This is different from raw agentic loops, where each turn reprocesses the whole
conversation. The companion [git-ops cost case study](git-ops-cost.md) measures
that reprocessing tax directly. Here the model-selection lever applies after
Kitsoki has already removed most turns from the model path.

## Required Story Improvements

This case study also revealed a product gap in `model-harness-eval`, now closed
in the story contract:

- report-only mode is supported, so a case-study run can avoid changing
  `.kitsoki.local.yaml`;
- operator-approved live evidence is allowed while default flows remain no-cost;
- outputs now include a durable `docs/case-studies/...` path and a Slidey JSON
  deck source path;
- deterministic flow coverage includes the report-mode case-study path.

## Next Steps

1. Add a routing-eval dataset format that consumes `.artifacts/routing-model-corpus/routing_inputs.json`.
2. Add an OpenAI mini profile if GPT mini remains in scope.
3. Run Haiku, `syn:small:text`, and the configured GPT mini candidate over the same hard-negative corpus.
4. Record per-example confidence and alternatives so threshold sweeps are real.
5. Promote only passing rooms via call-site `selection:` metadata with fallback.

The expected win is not "cheap model everywhere." It is a controlled cost ladder:
free deterministic routing first, cheap model where evidence says it is safe,
strong model only where the cheaper tier is uncertain.
