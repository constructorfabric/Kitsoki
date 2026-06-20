# Runtime: Embedding substrate — `internal/embed` cosine index + the `/v1/embeddings` sidecar

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`embeddings.md`](embeddings.md)

## Why

The two consumers in the [embeddings epic](embeddings.md) — `agent.search`
([slice 2](agent-search.md)) and the embedding routing tier
([slice 3](embedding-routing-tier.md)) — both need exactly the same two
things: a way to turn text into vectors, and a way to rank a query vector
against a corpus of document vectors. Building either inside its consumer
would duplicate the model client, the prefix discipline, the normalization
contract, and the persistence keying. This slice builds that shared core
once, with no consumer logic in it, so both slices sit on top of one tested
substrate.

## What changes

A new pure-Go `internal/embed` package with two cooperating pieces — a
brute-force cosine `Index` and an `Embedder` seam — plus a second `Sidecar`
mode in `internal/agent/server/` that serves `/v1/embeddings`. Nothing
consumes it yet; this slice ends when a fake embedder ranks a corpus in a unit
test and the gated live e2e embeds against a real sidecar.

## Impact

- **Code seams:**
  - new `internal/embed/` — `Index`, `Embedder`, `Role`, the deterministic
    fake, and an `embed.Store` (gob persistence).
  - `internal/agent/server/fetch.go:74` (`modelPins`) — an embedding model
    pin (nomic-embed-text-v1.5; bge-small-en-v1.5 fallback).
  - `internal/agent/server/sidecar.go` — a second `Sidecar` constructed with
    `--embeddings --pooling mean` + the embedding GGUF; a `/v1/embeddings`
    HTTP client modelled on `internal/agent/local_llm.go`'s
    `/v1/chat/completions` client.
- **Vocabulary:** none author-facing. This slice adds Go types only; the
  config blocks live in the consumer slices.
- **Stories affected:** none.
- **Backward compat:** purely additive. No story, cassette, or trace changes.
- **Docs on ship:** `internal/embed/doc.go`; a new substrate section in
  `docs/architecture/embeddings.md`; `agent-plugin.md` §9 gains the embedding
  sidecar mode.

## The model

**1. `internal/embed` — the index (pure Go, no new deps).** Per research §3,
brute force is *correct* at kitsoki's scale (dozens–low-thousands of vectors),
not a placeholder: `kelindar/search` validates brute-force + SIMD as a "great
fit" under ~100k entries. Design:

- `type Index struct{ entries []Entry }` where `Entry = struct{ ID string;
  Meta map[string]any; Vec []float32 }`, vectors **pre-normalized**.
- `/v1/embeddings` returns vectors **already L2-normalized**, so cosine reduces
  to a plain dot product — no normalization in Go, rank by dot (research §2).
- `Index.Rank(query []float32, k int) []Hit` returns the top-`k` by descending
  dot product, each `Hit` carrying `ID`, `Meta`, and the `Score`.
- `embed.Store` gob-encodes the `[]Entry`, keyed by `(model, dim, pooling,
  content hash of the input corpus)`, so an index is rebuilt only when the
  model, truncation, or the input text changes — not on every startup. This is
  the single store both consumers key into (epic shared decision 2).

**2. `Embedder` — the model seam.** An interface

```go
type Role int
const ( Document Role = iota; Query )

type Embedder interface {
    Embed(ctx context.Context, texts []string, role Role) ([][]float32, error)
}
```

is the **stub seam**: the live implementation POSTs to the embedding sidecar's
`/v1/embeddings`; tests inject a deterministic fake (fixed vectors per phrase).
This applies the [agent stub-by-id](../architecture/agent-plugin.md)
discipline to embeddings and keeps the no-LLM-in-tests rule (CLAUDE.md) intact.

> **Prefix discipline — get this wrong and ranking silently degrades**
> (research §1.3, epic shared decision 5). The live embedder applies the task
> prefix from `(model id, role)` — nomic documents get `search_document:`,
> queries get `search_query:`; bge-small prefixes the query only. The `role`
> argument exists so a caller can never forget; no caller ever writes a prefix.

## Decision recording

This slice records nothing on its own — it has no decision point. It *exposes*
the determinism anchor the consumers record: the `(model, dim, pooling, corpus
hash)` tuple that keys `embed.Store` is the same tuple each consumer's
breadcrumb carries (epic shared decision 4). The fields are defined here so
both consumers report them identically.

## Engine seams & invariants

- **Sidecar lifecycle:** a dedicated embedding `Sidecar` (separate port, GGUF,
  `--embeddings --pooling mean`) reusing the shipped `EnsureRunning` lazy-spawn
  + `/health` gate + SIGTERM teardown (`internal/agent/server/sidecar.go:170`,
  `:271`). `--embeddings` restricts a server to embedding use, so this is a
  *second* server instance, not a flag on the chat sidecar (research §2).
- **Acquisition:** the embedding GGUF is fetched + sha256-verified against a
  baked pin (`fetch.go:74`) exactly like the chat model, honouring the
  zero-touch promise. A managed model with no pin and no `endpoint:` is a
  load-time hard-fail — but the load-time wiring of that invariant belongs to
  whichever consumer enables a sidecar, so it is stated, not built, here.
- **Pooling metadata (research open Q):** confirm the chosen GGUF's pooling
  metadata makes `--pooling mean` redundant or required; pin whichever the
  verified build needs so replay is stable.

## Backward compatibility / migration

Nothing exists that this changes. The package is dead code until a consumer
imports it; no story migrates, no cassette moves.

## Tasks

```
## 1. Index (internal/embed) — pure Go, no sidecar yet
- [ ] 1.1 Index type + Entry/Hit; normalized dot-product Rank(query, k)
- [ ] 1.2 Embedder interface + Role{Document,Query}; deterministic fake embedder
- [ ] 1.3 embed.Store: gob persistence keyed by (model, dim, pooling, corpus hash)
- [ ] 1.4 Unit + bench: rank correctness, persistence round-trip, ~ns/op dot at N=1k

## 2. Embedding sidecar (internal/agent/server)
- [ ] 2.1 Embedding model pin (nomic-embed-text-v1.5; bge-small fallback) in fetch.go
- [ ] 2.2 Dedicated Sidecar spawn with --embeddings --pooling mean
- [ ] 2.3 Live /v1/embeddings client (modelled on local_llm.go) implementing Embedder
- [ ] 2.4 Confirm the chosen GGUF's pooling metadata vs explicit --pooling (research open Q)

## 3. Document
- [ ] 3.1 internal/embed/doc.go + docs/architecture/embeddings.md substrate section
- [ ] 3.2 agent-plugin.md §9 embedding sidecar mode; mark this slice shipped in the epic
```

## Verification

No LLM. The fake `Embedder` (fixed vectors per phrase) drives a unit test that
builds an `Index`, ranks a query, and asserts the order; a second test
round-trips an index through `embed.Store` and confirms the cache key changes
when the model/dim/corpus changes. The only test touching a real model is a
gated live e2e (`KITSOKI_EMBED_E2E=1`, following
`internal/agent/local_llm_e2e_test.go`'s shape): real sidecar
fetch→embed→rank, plus a second-run cache hit. Never run by default (memory:
[no-llm-tests]).

## Open questions

1. **Sidecar hop vs. `kelindar/search`** (research §4). Is a second
   llama-server endpoint genuinely simpler than `kelindar/search` (embed+index
   in one Go API via purego, no cgo)? *Lean: reuse the sidecar* — it is the
   infra we already manage and acquire deterministically; one fewer
   model-loading convention to own.

## Non-goals

- **Consumer logic.** No search verb, no routing tier — those are slices 2
  and 3. This slice has zero knowledge of stories or routing.
- **An ANN library, a reranker, cgo** — see the [epic non-goals](embeddings.md).
