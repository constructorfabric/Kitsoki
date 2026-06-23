# Case study: using live intent benchmarks to improve room routing

This case study documents a real room-local routing benchmark loop in Kitsoki.
The target was the Oregon Trail general store review state:

```sh
stories/oregon-trail/intents/refine_purchase.yaml
state: general_store.reviewing
inputs: 7
runs per input: 1
threshold: 80%
```

The useful result was not just model comparison. The benchmark found a harness
contract bug, gave a tight fix target, and then proved that cheaper routers can
pass this room once the schema and slot guidance are correct.

## Executive summary

Before the fix, the live benchmark looked catastrophic: Haiku passed 2/7,
Opus passed 2/7, and Synthetic small passed 1/7.

After the fix:

- Claude Haiku passed 7/7.
- Synthetic small (`syn:small:text`) passed 7/7 through the
  `synthetic-claude` live profile.
- Claude Opus still did not pass this room in two post-fix attempts; it hit
  5/7 both times, with transient 529s and a stable over-conservative slot
  choice for `add 50 more bullets`.

This is the cost-saving mechanism Kitsoki should use: benchmark each room
against its validated intent-call fixture, then promote the cheapest passing
router for that room only. For `general_store.reviewing`, the measured result
supports Haiku or Synthetic small as room-local candidates, with fallback kept
available.

## What the harness exposed

The initial failures were mostly not wrong intent labels. The models usually
selected `refine_purchase`, but they returned string slot values such as
`{"oxen":"12"}` when the fixture expected numeric values such as
`{"oxen":12}`.

The root cause was in the LLM-facing transition schema. Kitsoki stories use
slot shorthand types such as `type: int`, but the schema generator only mapped
JSON Schema names such as `integer`. As a result, every `int` slot was exposed
to the model as a string. The benchmark failure was real, but it was measuring a
bad contract.

The fix:

- Map Kitsoki shorthand slot types to JSON Schema primitive types:
  `int -> integer`, `float -> number`, `bool -> boolean`.
- Convert numeric and boolean slot examples to JSON values in the generated
  schema, so examples match the declared type.
- Tighten `refine_purchase` slot descriptions so vague comparative changes
  like `less food` go to `feedback`, while numeric replacement slots are used
  only when the user provides explicit numbers.

## Before and after

| candidate | command path | before | after | elapsed after | measured cost after | verdict |
|---|---|---:|---:|---:|---:|---|
| Claude Haiku | `--harness claude --claude-model haiku` | 2/7 | 7/7 | 134.501s | $0.243895 | pass |
| Synthetic small | `--harness live --profile synthetic-claude --claude-model syn:small:text` | 1/7 | 7/7 | 30.043s | not emitted by SDK report | pass |
| Claude Opus | `--harness claude --claude-model opus` | 2/7 | 5/7 | 300.645s | $1.684418 | fail |
| Codex default | `--harness claude --agent codex` | 1/7 | not rerun | n/a | n/a | fail before fix |

Artifacts:

- `.artifacts/routing-model-live-bench/claude-haiku-refine-purchase.json`
- `.artifacts/routing-model-live-bench/claude-haiku-refine-purchase-after-schema-fix.json`
- `.artifacts/routing-model-live-bench/claude-haiku-refine-purchase-after-routing-fix.json`
- `.artifacts/routing-model-live-bench/synthetic-live-small-refine-purchase.json`
- `.artifacts/routing-model-live-bench/synthetic-live-small-refine-purchase-after-routing-fix.json`
- `.artifacts/routing-model-live-bench/claude-opus-refine-purchase.json`
- `.artifacts/routing-model-live-bench/claude-opus-refine-purchase-after-routing-fix.json`
- `.artifacts/routing-model-live-bench/claude-opus-refine-purchase-after-routing-fix-rerun.json`
- `.artifacts/routing-model-live-bench/codex-default-refine-purchase.json`

These are single-run fixtures, so they are not statistically stable. They are
enough to prove the harness can find and verify routing improvements, and enough
to reject Opus for this specific room under the current contract.

## Provider notes

Claude native runs require the process that can see the user's Claude
subscription. In this environment that meant running outside the sandbox; the
sandboxed process saw `claude auth status` as logged out, while the escalated
process could run both Haiku and Opus.

Synthetic small must use the Synthetic profile through the live
Anthropic-compatible SDK path:

```sh
go run ./cmd/kitsoki test intents stories/oregon-trail/app.yaml \
  --harness live \
  --profile synthetic-claude \
  --claude-model syn:small:text \
  --runs 1 \
  --intents stories/oregon-trail/intents/refine_purchase.yaml \
  --json .artifacts/routing-model-live-bench/synthetic-live-small-refine-purchase-after-routing-fix.json
```

Running `syn:small:text` through Codex/ChatGPT is the wrong provider path. The
successful run used the configured `synthetic-claude` profile.

GPT mini was not included in the final after-fix matrix because the installed
Codex CLI rejected explicit `gpt-5-mini` under the current ChatGPT account path.
That is a provider/account limitation, not evidence about GPT mini quality.

## Decision

Use the intent benchmark harness as the promotion gate for room-by-room routing.
For this room, the current measured candidates are:

- Promote candidate: Claude Haiku, subject to a multi-run confirmation pass.
- Promote candidate: Synthetic small, subject to cost capture and multi-run
  confirmation.
- Do not promote: Claude Opus for this room; it was slower, more expensive, and
  less reliable in the post-fix runs.

The important rule is that promotion should use validated intent-call results,
not label-only classification. Label-only scoring would have hidden the original
schema bug.

## Next benchmark steps

1. Add per-fixture filtering to `kitsoki test intents` so transient provider
   failures can be rerun without paying for the full room slice.
2. Run at least 3-5 repetitions per input for Haiku and Synthetic small.
3. Add Synthetic SDK cost calculation from response usage and catalog pricing.
4. Add a label-only diagnostic alongside validated-call scoring to distinguish
   intent confusion from slot-shape failures.
5. Expand the corpus across more rooms and promote only the cheapest model that
   passes each room's authored threshold with hard negatives.
