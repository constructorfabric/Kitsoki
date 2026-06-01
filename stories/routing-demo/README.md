# Routing Playground

A small teaching story for kitsoki's **command router** — how a line of typed
text becomes an intent. It exists to make the four routing tiers visible and to
double as a routing test bed. It is *not* importable and invokes no
`host.oracle.*` calls; the only model use is the router's own LLM tier.

## The four tiers (run in order, first hit wins)

| # | Tier | What matches | Model? |
|---|------|--------------|--------|
| 1 | **synonyms / examples** | an intent's declared `synonyms:` + menu `examples:` (and exact text) | no — offline |
| 2 | **slot_template** | patterns like `budget {amount}` that also fill slots | no — offline |
| 3 | **semantic bands** | a deterministic hit's *confidence* picks the action | no — offline |
| 4 | **`extract_llm_on_no_match`** | on a deterministic miss, the local model classifies the command into an allowed intent | yes — `oracle.local` |

Tier 3 turns a confidence number into a behaviour:

- `>= semantic_high_bar` (0.80) → route directly.
- `>= semantic_mid_bar` (0.65) → ask a clarification first.
- `== 0.50` (a **tie** across 2+ intents) → show a disambiguation card.

Tier 4 is opt-in (`extract_llm_on_no_match: true`) and additive: a confident
verdict routes; a `"none"` / low-confidence verdict falls through to the
main-turn LLM exactly as before. See
[`docs/architecture/semantic-routing.md`](../../docs/architecture/semantic-routing.md)
and [`docs/architecture/oracle-plugin.md` "Local model backend"](../../docs/architecture/oracle-plugin.md).

The whole `routing:` config lives at the top of [`app.yaml`](./app.yaml).

## Rooms (one lesson per tier)

| Room | Teaches | Try typing… |
|------|---------|-------------|
| `hub` | overview + the live `routing:` config | `deterministic`, `local model`, `ambiguous` |
| `deterministic` | tiers 1 & 2 | `lamp on`, `illuminate`, `budget 200`; then the paraphrase `could you brighten this place up` |
| `local_model` | tier 4 | `should I bring an umbrella today`, `put on some tunes`, `did anyone write to me` |
| `ambiguous` | tier 3 (tie) | `save report` (ties `save {document}` vs `save {game}` → disambiguation card) |

## Run it

```sh
kitsoki run stories/routing-demo/app.yaml
```

Tier 4 needs the local model. First use auto-fetches it (~1.2 GB into
`~/.cache/kitsoki`); pre-warm with `make fetch-models fetch-llama-server`, or
set `extract_llm_on_no_match: false` in `app.yaml` to do the offline tiers only.

## Test it

**Transitions (fast, offline, CI):** flow fixtures invoke intents *by name* —
they lock the state machine, not the router.

```sh
kitsoki test flows stories/routing-demo/app.yaml
```

**Routing resolution — all four tiers (fast, offline, CI):** the real router
only runs inside `orchestrator.Turn` (the path the TUI uses). The story is
covered end-to-end through that path — with a fake local model — in
`internal/orchestrator/routing_demo_test.go`:

```sh
go test ./internal/orchestrator/ -run 'TestRoutingDemo_AllTiers|TestSemanticLLMTier' -v
```

It asserts: a bare synonym → `lamp_on` (tier 1), `budget 350` → `amount=350`
(tier 2), `save report` → `AMBIGUOUS_INTENT` (tier 3 tie), and a paraphrase →
the local model's verdict (tier 4, proven not to be a synonym).

> ⚠️ **Do NOT use `kitsoki turn --input` to test routing.** That stateless probe
> uses `OneShot`, which routes free text straight through the **main-turn LLM
> harness** — it bypasses the deterministic/semantic/local-model tiers entirely.
> Use it to test the *harness*, not the router. The router lives in `Turn`
> (the TUI and the Go test above).

**Live (the real router, all tiers):** play it in the TUI and type the phrases
each room suggests:

```sh
kitsoki run stories/routing-demo/app.yaml
```

## Why a tiny, narrow story?

Routing only reaches tier 4 on a genuine deterministic `no_match`. A large story
with many intents and broad examples fuzzy-matches almost everything, so the
local tier rarely fires (oregon-trail is like this). The intents here have
deliberately narrow examples so paraphrases reliably miss the deterministic
tiers and hand off to the model — which is the whole point of the lesson.
