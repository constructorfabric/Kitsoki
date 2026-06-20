# Epic: Vector embeddings — shared index substrate, `agent.search`, and the routing tier

**Status:** v1 — all 3 slices shipped. See [docs/architecture/embeddings.md](../architecture/embeddings.md) (substrate + agent.search) and [docs/architecture/semantic-routing.md](../architecture/semantic-routing.md) (routing tier).
**Kind:**   epic
**Slices:** 3 (3/3 shipped)

## Why

kitsoki has exactly one way to judge whether two pieces of text *mean* the
same thing: ask an LLM. The deterministic semantic router
(`internal/semroute`) is **lexical** — a bare-string synonym check and a
`{slot}` template NFA — so it cannot see paraphrase ("walk the wagon over"
never reaches `ford`), and stories that want to retrieve relevant text from a
document set have no primitive at all. Both gaps fall through to the
expensive, non-deterministic main-turn LLM.

The blocker that deferred embeddings — *"ONNX/gomlx add cgo"*
([`semantic-routing-proposal.md` §A](semantic-routing-proposal.md)) — died
when [`local-model-agent.md`](local-model-agent.md) shipped a managed
llama.cpp `llama-server` sidecar (`internal/agent/server/`) with zero-touch
fetch+cache+verify. That server speaks `/v1/embeddings` the same way it speaks
`/v1/chat/completions`. So a vector substrate is now **a second model load on
infra we already run and acquire deterministically — no new dependency.**

Once we have that substrate, two consumers fall out of it: a story-facing
semantic-search verb (`agent.search`) and the paraphrase routing tier the
[deep-research note](../../.context/semantic-embeddings-research.md) settled.
This epic builds the substrate once and lights up both.

## What changes

When every slice has shipped:

- A new pure-Go `internal/embed` package owns a brute-force cosine `Index` and
  an `Embedder` seam; an embedding `Sidecar` serves `/v1/embeddings`. This is
  the shared substrate — both consumers below call it, neither owns it.
- A new **`host.agent.search`** host verb lets a room semantically search a
  set of files on disk: embed the corpus once (chunked, gob-cached), embed the
  query, bind the top-`k` ranked chunks to world. Retrieval becomes a
  first-class, recorded story primitive.
- A new **embedding routing tier** sits between the lexical template tier and
  the LLM inside `TrySemantic`, catching paraphrase the lexical layer misses
  and emitting a `semroute.Verdict` at a new confidence band.

Both consumers are **deterministic given a pinned (model, dim, pooling) +
corpus hash**, **opt-in**, and **additive** — an app that uses neither pays
nothing and its cassettes are byte-identical to today.

```
deterministic ─miss─▶ lex synonym ─miss─▶ lex template ─miss─▶ [EMBED tier] ─miss─▶ LLM
   (1.00)              (0.90)              (0.80/0.65)          (slice 3)          (main turn)

room ──host.agent.search(query, corpus=files)──▶ [internal/embed] ──▶ top-k chunks → world   (slice 2)
```

## Impact

- **Spans:** runtime (×3) — a substrate slice plus two consumer slices. No
  story/tui/tracing novelty beyond the breadcrumb each consumer records.
- **Net surface:** new `internal/embed/`; a second `Sidecar` mode in
  `internal/agent/server/`; one new host verb (`internal/host/`); one new
  tier in `internal/orchestrator/semantic.go`. No new effects, world-key
  conventions, or cgo.
- **Docs on ship:** [`docs/architecture/agent-plugin.md`](../architecture/agent-plugin.md)
  §9 (the embedding sidecar mode), a new `docs/architecture/embeddings.md`
  (the substrate + `agent.search`), [`docs/architecture/semantic-routing.md`](../architecture/semantic-routing.md)
  (the routing tier), and `internal/embed/doc.go`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | Docs |
|---|---|---|---|---|---|---|
| 1 | embed-substrate | runtime | `internal/embed` cosine `Index` + `Embedder` seam + the `/v1/embeddings` sidecar | — | Shipped | [embeddings.md](../architecture/embeddings.md) |
| 2 | agent-search | runtime | `host.agent.search` — semantic search over files on disk, ranked chunks → world | 1 | Shipped | [embeddings.md §host.agent.search](../architecture/embeddings.md#hostagentsearch) |
| 3 | embedding-routing-tier | runtime | the embed tier in `TrySemantic`; `ConfidenceEmbedding` band; Oregon Trail calibration | 1 | Shipped | [semantic-routing.md §6](../architecture/semantic-routing.md#6-embedding-routing-tier) |

## Sequencing

```
#1 embed-substrate (runtime) ──┬──▶ #2 agent-search (runtime)
                               └──▶ #3 embedding-routing-tier (runtime)
```

Slice 1 is the only hard dependency; once its `Embedder` + `Index` + sidecar
land, slices 2 and 3 are independent and can be built in parallel. Each
consumer ships and calibrates on its own.

## Shared decisions

These span the slices, so no child re-litigates them.

1. **The substrate is NOT an `agent.Agent`.** The plugin contract
   (`internal/agent/agent.go:38`, `Ask(ctx, req) AskResponse`) is for
   *generative* completions — one prompt, one schema-shaped reply. Retrieval
   doesn't fit it. Slice 1 introduces a distinct `embed.Embedder`
   (`Embed(ctx, texts, role) ([][]float32, error)`) + `embed.Index`; both
   consumers call those directly, not through the generative registry.
2. **`agent.search` keeps the `agent.` namespace by intent, not by
   transport.** The verb is author-facing under `host.agent.*` because it is
   an *interpretive, recorded* decision (which chunks are relevant — the moat),
   but its dispatch goes to the `internal/embed` substrate, not to
   `TryDispatchVerb`/`plug.Ask`. The routing tier (slice 3) is internal — no
   author-facing verb — and reuses the same substrate and breadcrumb shape.
3. **One embedding sidecar instance.** A dedicated `Sidecar` spawned with
   `--embeddings --pooling mean`, separate port + GGUF from the chat sidecar
   (`--embeddings` restricts a server to embedding use — it is a *second*
   server, not a flag on the chat one). Reuses `EnsureRunning`'s lazy-spawn +
   `/health` gate + SIGTERM teardown (`internal/agent/server/sidecar.go:170`).
   `endpoint:` mode never fetches or spawns, mirroring `local-model-agent`'s
   `model:`-or-`endpoint:` invariant.
4. **Shared determinism anchor.** Both consumers persist a gob-encoded index
   keyed by `(model, dim, pooling, content-hash of the corpus)`, rebuilt only
   when the model, truncation, or the input text changes. Both breadcrumbs
   record `model`, `dim`, and a short corpus hash so the ranking is
   reproducible; pinning the llama.cpp release (as the chat sidecar already
   does, `fetch.go:60`) keeps replay stable across upgrades.
5. **Prefix discipline lives in the Embedder, never in callers.** nomic is
   asymmetric — documents embed with `search_document:`, queries with
   `search_query:`; bge-small prefixes the *query only*. The
   `Embedder.Embed(…, role Role)` argument (`Role ∈ {Document, Query}`) makes
   this impossible to forget: the live embedder applies the prefix from
   `(model id, role)`. Both consumers pass `Document` at index time and
   `Query` at lookup time and otherwise stay ignorant of prefixes.

## Cross-cutting open questions

1. **Model + dim default.** nomic-embed-text-v1.5 @256 (Matryoshka) vs.
   bge-small-en-v1.5 @384 (more forgiving prefixes, near-equal MTEB). *Lean:
   pin both, default nomic@256, let each consumer's calibration confirm.*
   Routing on *very short* texts may favor a different model than the RAG-over-
   docs case — the two consumers MAY end up defaulting differently.
2. **One index store or two.** Do `agent.search` and the routing tier share a
   single on-disk cache layout/keying, or keep separate caches? *Lean: one
   `embed.Store` in slice 1 keyed by corpus hash; both consumers are just
   different corpora through the same store.*

## Non-goals

- **An ANN library.** Brute force is exact and correct under ~100k vectors
  (research §3); `coder/hnsw` is a noted pure-Go escape hatch we will not reach
  for at kitsoki's scale.
- **cgo / in-binary inference.** Same stance as `local-model-agent.md` — the
  sidecar is the simpler seam than embedded engines.
- **A reranker / cross-encoder stage.** Top-`k` by cosine is the ceiling for
  v1; a second-stage reranker is a later proposal if recall proves insufficient.
- **Replacing the main-turn LLM.** Generative and out-of-band cases remain the
  LLM's job; embeddings only widen the deterministic catch and add retrieval.
- **Auto-promoting embedding hits to authored synonyms.** Same read-only
  contract as [`semantic-routing-proposal.md` §1.5](semantic-routing-proposal.md) —
  surfacing, never silent landing.

<!--
  Lifecycle: as each slice ships, update its row's Status and migrate its
  detail into docs/ per that child's plan, then delete the child file. When
  every slice has shipped, this epic is just an empty index — delete it too.
-->
