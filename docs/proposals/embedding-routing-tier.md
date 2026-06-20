# Runtime: Embedding routing tier (paraphrase recall between lexical and LLM)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`embeddings.md`](embeddings.md)

## Why

The deterministic semantic router (`internal/semroute`) resolves utterances by
**lexical** means: a bare-string synonym subset check and a `{slot}` template
NFA. It cannot see paraphrase — "walk the wagon over" never reaches `ford`
unless an author wrote that exact phrasing. Today that gap is the dominant
reason Oregon Trail sits at **37.5% LLM fallthrough** under the corrected gate
([`semantic-routing-proposal.md` §2](semantic-routing-proposal.md)): every
paraphrase the lexical tier misses falls through to the expensive,
non-deterministic main-turn LLM.

The [embedding substrate](embed-substrate.md) lets us catch most of those
paraphrases deterministically and for nearly free — a second model load on the
sidecar we already run. The swap that `semantic-routing-proposal.md` §A priced
against a cgo dependency ("~5% better routing is a poor swap") is, priced
against a sidecar we already ship, close to free.

## What changes

A new **embedding tier** in the routing pipeline, between the lexical template
tier and the LLM tier:

> Embed every allowed intent/synonym as a *document* once per app, embed the
> incoming utterance as a *query*, rank by cosine (a plain dot product over the
> substrate's pre-normalized vectors), and emit a `semroute.Verdict` when the
> top-1 score and its margin over top-2 clear a confidence band.

```
deterministic ─miss─▶ lex synonym ─miss─▶ lex template ─miss─▶ [EMBED tier] ─miss─▶ LLM
   (1.00)              (0.90)              (0.80/0.65)          (new band)         (main turn)
```

It is **deterministic given a pinned model + pooling**, **opt-in**, and
**additive** — `agent.claude` and the lexical tiers are untouched, and an app
that doesn't enable it pays nothing.

## Impact

- **Code seams:**
  - a new matcher consulted after `semroute.Matcher.Match`
    (`internal/semroute/matcher.go:61`) returns a zero verdict, built on
    `internal/embed` (Open Q1: separate package + orchestrator wiring vs. a
    third path inside `semroute`).
  - `internal/orchestrator/semantic.go:242` (`TrySemantic`) — call the embed
    tier on a lexical miss, before the `ExtractLLMOnNoMatch` LLM hop, on the
    same pre-lock path the LLM tier already respects.
  - reuses the embedding `Sidecar` from [slice 1](embed-substrate.md).
- **Vocabulary:** a `routing.embedding.{…}` config block (table below); a new
  `semroute` confidence constant. No new effects / host calls / world keys.
- **Stories affected:** none by default. Oregon Trail is the calibration
  target, as it was for the lexical tier.
- **Backward compat:** default **off**. An absent `embedding:` block leaves the
  pipeline byte-identical to today; no cassette changes.
- **Docs on ship:** [`docs/architecture/semantic-routing.md`](../architecture/semantic-routing.md)
  (new tier section), trim `semantic-routing-proposal.md` §A.

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| config | `routing.embedding.enabled` | `bool` | opt-in; default false |
| config | `routing.embedding.model` | `string` | default `nomic-embed-text-v1.5` (fallback `bge-small-en-v1.5`) |
| config | `routing.embedding.dim` | `int` | Matryoshka truncation for nomic (256 default); ignored for bge-small |
| config | `routing.embedding.endpoint` | `string` | optional; talk to an already-running embedding server instead of managing one |
| config | `routing.embedding.confident_bar` | `float64` | top-1 cosine ≥ this ⇒ confident match |
| config | `routing.embedding.margin` | `float64` | top1 − top2 must clear this; else tie/clarify |
| const | `semroute.ConfidenceEmbedding` | `float64` | the band an embedding hit emits onto the existing 5-band scale |

## The model

At app compile time every allowed intent (its `examples`, declared `synonyms`,
and id) is embedded as a **`Document`** and stored in an `embed.Index`. At turn
time the utterance is embedded as a **`Query`**; the tier ranks by dot product
and applies the confident-bar/margin gate:

```
utterance ──embed(Query)──▶ dot-rank over allowed-intent docs
   top1 ≥ confident_bar  AND  (top1 − top2) ≥ margin   ──▶ Verdict{Intent, Confidence: ConfidenceEmbedding}
   top1 ≥ confident_bar  AND  margin too small         ──▶ tie  (Candidates, ConfidenceTie)
   otherwise                                            ──▶ zero Verdict (fall through to LLM)
```

Prefix discipline is the substrate's job (epic shared decision 5): intents go
in as `Document`, the utterance as `Query`; this tier never writes a prefix.

The tier produces **no slots** — embedding similarity says "this looks like a
purchase," not "the user said $240." Slot extraction stays with the typed
parsers (`internal/slotparse`); an embedding hit on a slot-bearing intent that
still needs an unfilled slot falls through, exactly as a bare-synonym hit does
under the `RequiresUnfilledSlot` guard (`semantic-routing-proposal.md` §2).

## Decision recording

This is an **interpretive decision** and lands as a recorded, replayable
routing breadcrumb (the moat). It records:

- `tier: "embedding"`, the chosen intent, `top1` and `top2` cosine scores, the
  `margin`, and the `confident_bar`/`margin` thresholds in force — so the
  verdict is reconstructable and a reviewer sees *why* it was confident or
  bailed.
- `model`, `dim`, and a short corpus hash (epic shared decision 4) — the
  determinism anchor.

The existing turncache (`internal/turncache`) memoizes the verdict keyed by
`(state, utterance, corpus hash)` the same way the lexical tier's hits are
cached, so a re-asked utterance skips the sidecar hop.

## Engine seams & invariants

- **Load time:** when `routing.embedding.enabled`, the loader validates the
  model id is one of the pinned models (or that `endpoint:` is set), and that
  `confident_bar`/`margin` are in `[0,1]`. A managed model with no pin and no
  endpoint is a load-time hard-fail (mirrors `local-model-agent`'s
  `model:`-or-`endpoint:` invariant).
- **Ordering:** the embed tier runs inside `TrySemantic`
  (`internal/orchestrator/semantic.go:242`) **before** the session lock, on the
  same pre-lock path the LLM tier already respects, so it never blocks
  concurrent turns. It runs only after `Matcher.Match`
  (`internal/semroute/matcher.go:61`) returns a zero verdict, and only when
  routing already dispatches through the `host.agent.extract` tiered resolver
  (the agent-split path `semantic.go` already uses).

## Backward compatibility / migration

Existing stories and cassettes are unchanged: the tier is gated on a
default-off `embedding:` block. No story must migrate. Oregon Trail opts in
behind the flag for calibration; if the tier proves out, its
`# Phase-7 calibration` synonym list (`stories/oregon-trail/intents.yaml`) can
shrink, since paraphrase recall moves from hand-authored synonyms to the model.

## Tasks

```
## 1. Tier + wiring
- [ ] 1.1 ConfidenceEmbedding band; confident_bar/margin gate → Verdict (or tie / fall-through)
- [ ] 1.2 Build the allowed-intent Index at compile time (examples + synonyms + id as Document)
- [ ] 1.3 RoutingConfig.Embedding block + DefaultRoutingConfig (default off) + loader invariants
- [ ] 1.4 TrySemantic calls the tier on a lexical miss, pre-lock, before the LLM hop
- [ ] 1.5 Routing breadcrumb records tier/top1/top2/margin/model/dim/corpus-hash

## 2. Verification (no live model)
- [ ] 2.1 Stateless: TrySemantic with the fake embedder resolves a paraphrase the lexical tier misses
- [ ] 2.2 Flow fixture: embed tier hit + tie + fall-through; legacy lexical path still green
- [ ] 2.3 Gated live e2e (KITSOKI_EMBED_E2E=1): real sidecar fetch→embed→rank

## 3. Calibrate + document
- [ ] 3.1 Oregon Trail bake-off behind the flag; pick confident_bar/margin; record the LLM-fallthrough delta
- [ ] 3.2 semantic-routing.md new tier section; trim semantic-routing-proposal.md §A; mark this slice shipped
```

## Verification

A reviewer confirms the tier without an LLM via the fake `Embedder` (fixed
vectors): `kitsoki turn --state … --intent "" --world @w.json` with a
paraphrase utterance routes to the expected intent at `ConfidenceEmbedding`,
while a low-margin utterance falls through. The gated live e2e (§2.3) — opt-in,
following `internal/agent/local_llm_e2e_test.go`'s shape — is the only test
that touches a real model and is never run by default (memory: [no-llm-tests]).
Calibration (§3.1) reuses the Oregon Trail recording the lexical tier was tuned
against.

## Open questions

1. **Peer-of-Matcher vs. inside-semroute.** Make the embed tier a separate
   matcher the orchestrator consults after `Matcher.Match`, or a third path
   inside `semroute`? *Lean: separate package + orchestrator-level wiring* —
   `semroute` is deliberately dependency-light (lexical, no model), and the
   embed tier needs the sidecar/HTTP seam that would pollute it.
2. **Confident-match threshold + margin** (research open Q). *Settle
   empirically in §3.1 on Oregon Trail; do not guess literals now.*
3. **Short-text model choice.** Routing on *very short* utterances may favor a
   different model than general MTEB (or the RAG case) suggests; the bake-off
   confirms whether routing wants a different default than `agent.search`
   (epic cross-cutting Q1).

## Non-goals

- **Slot extraction.** Embeddings classify intent, not slot values; the typed
  `internal/slotparse` parsers stay the slot authority.
- **Replacing the main-turn LLM.** Generative, open-ended, and out-of-band
  cases (`semantic-routing-proposal.md` §2 "What stayed LLM-only") remain the
  LLM's job; the embed tier only widens the deterministic catch.
- **Auto-promoting embedding hits to authored synonyms** — see the
  [epic non-goals](embeddings.md).
