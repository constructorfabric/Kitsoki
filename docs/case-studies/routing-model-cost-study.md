# Case study: room-by-room routing model benchmarks

This is the corrected evidence report for cheaper room-level routing models in
Kitsoki. The earlier report mixed mined corpus counts, existing eval-pilot
artifacts, and pricing estimates. That was not sufficient for the question:
can Haiku, `syn:small:text`, or a GPT mini tier actually route Kitsoki rooms?

Current answer: **not proven yet.** The repo now has a real live fixture path,
and the first successful model-backed fixture run shows that a supported Codex
default model routes only 2 of 7 fixtures in `general_store.reviewing`. Haiku,
GPT mini, and Synthetic small were attempted, but they were blocked by local
auth or account/model support before producing valid benchmark data.

## What was benchmarked

Benchmark fixture:

```sh
go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml \
  --harness claude \
  --agent codex \
  --runs 1 \
  --intents stories/oregon-trail/intents/refine_purchase.yaml \
  --json .artifacts/routing-model-live-bench/codex-default-refine-purchase.json
```

This is a real model-backed `kitsoki test intents` run. The runner builds the
same routing harness used for free-text turns, passes the room's allowed intents
and world snapshot into `TurnInput`, and records per-input latency, actual
intent, and actual slots.

## Result

| candidate | fixture | calls | pass rate | elapsed | status |
|---|---|---:|---:|---:|---|
| Codex CLI default model | `general_store.reviewing` / `refine_purchase` | 7 | 2/7, 28.6% | 55.754s | real run, failed quality bar |
| Claude Haiku via Claude CLI | `buy_supplies` smoke | 0 valid | n/a | n/a | blocked: Claude CLI returned `Not logged in` |
| Anthropic live / Haiku-compatible SDK path | `refine_purchase` | 0 valid | n/a | n/a | blocked: no Anthropic credential visible |
| `synthetic-codex/syn:small:text` | `refine_purchase` | 0 valid | n/a | n/a | blocked: Codex account rejects `syn:small:text` |
| `gpt-5-mini` through Codex CLI | `refine_purchase` | 0 valid | n/a | n/a | blocked: Codex account rejects `gpt-5-mini` |

The only valid routing benchmark in this slice is Codex CLI default. It failed
the room's 80% fixture threshold.

## Failure shape

For the five failing `refine_purchase` fixtures, the model selected the correct
intent name but did not satisfy the expected slot contract:

| input | actual intent | actual slots | verdict |
|---|---|---|---|
| `actually give me 12 oxen` | `refine_purchase` | `{feedback:"", oxen:"12"}` | failed expected slot shape |
| `actually give me 12 draaft animalz` | `refine_purchase` | `{feedback:"actually give me 12 draaft animalz", oxen:"12"}` | failed expected slot shape |
| `I want 7 oxen not 6` | `refine_purchase` | `{feedback:"", oxen:"7"}` | failed expected slot shape |
| `add 50 more bullets` | `refine_purchase` | `{feedback:"", bullets:"50"}` | failed expected slot shape |
| `actually 250 lbs of food` | `refine_purchase` | `{feedback:"", food:"250"}` | failed expected slot shape |

The two passing cases were the looser feedback-style fixtures:

| input | actual intent | actual slots |
|---|---|---|
| `refine` | `refine_purchase` | `{feedback:""}` |
| `less food` | `refine_purchase` | `{feedback:"less food"}` |

This matters for cost savings: cheaper routing is not just intent
classification. In Kitsoki rooms, slot fidelity is part of the routing contract.

## Fixture and runner changes

`kitsoki test intents` now supports live model-backed intent fixtures instead of
printing the previous PoC stub. The command can run:

- `--harness claude` with `--agent claude|codex|copilot`;
- `--profile <name>` from `.kitsoki.yaml` / `.kitsoki.local.yaml`, including
  provider environment such as Synthetic;
- JSON reports with `HarnessType`, `HarnessModel`, `AgentBackend`,
  `ProfileName`, elapsed time, actual routed intent, actual slots, and errors.

The runner also now passes allowed intents and initial world into the live
router. Without that, a benchmark would not match a real Kitsoki turn.

## Blockers before a savings claim

1. Haiku needs working Claude/Anthropic auth in the benchmark environment. The
   current Claude CLI path is installed but unauthenticated for `claude -p`, and
   the direct Anthropic SDK path has no visible credential.
2. GPT mini is not benchmarkable through the installed Codex CLI account path;
   direct smoke returned a 400 saying `gpt-5-mini` is not supported with the
   current ChatGPT account.
3. `syn:small:text` is configured in `.kitsoki.local.yaml`, but direct Codex
   smoke returned a 400 saying the model is not supported with the current
   ChatGPT account.
4. The Codex default model needs either a better room prompt/schema or a
   fallback policy; 2/7 is not promotable.
5. The report still needs per-call token/cost capture in the JSON artifact
   before it can support a cost-savings table. The live logs emit usage events,
   but the intent report does not yet aggregate them.

## Recommendation

Do not switch room routing to Haiku, GPT mini, Synthetic small, or Codex default
based on the current evidence.

The credible next case-study run is:

1. fix auth/model access for Haiku and any GPT/Synthetic candidate;
2. run the same fixture set across all candidates;
3. include token/cost aggregation in the fixture JSON;
4. promote only rooms that meet the authored threshold, including slot checks
   and hard negatives;
5. keep a stronger fallback for low-confidence or failed-schema cases.

The mechanism for cost savings is still sound: deterministic tiers first, then
the cheapest passing model per room, with fallback. But this benchmark says the
candidate promotion evidence is not there yet.
