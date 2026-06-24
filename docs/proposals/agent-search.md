# Runtime: `host.agent.search` — semantic search over files on disk

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [`embeddings.md`](embeddings.md)

## Why

A story today has no way to ask *"which of these documents is relevant to
what the user just said?"* without spending a full main-turn LLM call to read
the whole corpus into the prompt — expensive, non-deterministic, and bounded
by context. Every story that wants retrieval (a bugfix story grepping prior
fix notes, a PRD story pulling matching requirements, a support story finding
the right runbook) reaches for the LLM or hand-rolls a keyword grep that
misses paraphrase.

With the [embedding substrate](embed-substrate.md) in place, retrieval becomes
a cheap, deterministic, **recorded** host call: point a room at a set of files,
hand it a query, get back the ranked chunks. That is the moat applied to
retrieval — which documents were judged relevant is an interpretive decision,
and now it is a replayable datapoint instead of an opaque slice of an LLM
prompt.

## What changes

A new host verb **`host.agent.search`**. A room hands it a `query`, a
`corpus` of files on disk, and a `top_k`; the verb chunks + embeds the corpus
once (gob-cached by content hash via [`embed.Store`](embed-substrate.md)),
embeds the query, ranks by cosine, and binds the top-`k` chunks (text + source
path + score) to a world key. It dispatches to `internal/embed` directly, not
through the generative agent registry (epic shared decision 2).

```
host.agent.search:
  query:  "{{ world.user_question }}"
  corpus: "docs/runbooks/**/*.md"      # glob, relative to the story working_dir
  top_k:  5
  bind:   matches                       # world.matches ← ranked []{path, text, score}
```

## Impact

- **Code seams:**
  - new `internal/host/agent_search.go` — `AgentSearchHandler(ctx, args)
    (Result, error)`, mirroring the shape of `agent_decide.go:85` but
    dispatching to the embed substrate rather than `TryDispatchVerb`.
  - new `internal/embed/ingest.go` (or a sibling) — file globbing + chunking
    (the ingestion half this slice owns).
  - registered in the host-call registry alongside the other `agent.*`
    handlers.
- **Vocabulary:** one new host call (table below). No new effects or world-key
  conventions — the result lands at the caller's `bind:` key like any host call.
- **Stories affected:** none by default; opt-in per call site. A demo corpus
  (a small `docs/` subset) is the calibration target.
- **Backward compat:** purely additive. A story that never calls
  `agent.search` is unchanged.
- **Docs on ship:** `docs/architecture/embeddings.md` (the `agent.search`
  section) + a mention in [`docs/architecture/hosts.md`](../architecture/hosts.md)
  /`agent-plugin.md` §2 (calling an agent verb from a room).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.agent.search` | `{query, corpus, top_k?, min_score?, chunk?} → bind: []Hit` | reads files under `working_dir`; no mutation |

`query` (string, rendered), `corpus` (string glob or list of globs, relative
to the story `working_dir`), `top_k` (int, default 5), `min_score` (float64,
optional cosine floor), `chunk` (block, below; optional). The bound result is
a ranked list of `{path, chunk_id, text, score}` descending by `score`, plus a
convenience `top` (the first hit) for the common single-result case.

## The model

This slice is the substrate's `Embedder` + `Index` wrapped in two
story-specific concerns the substrate deliberately doesn't own: **ingestion**
(turning files into chunks) and **the host-call surface**.

**Ingestion + chunking.** `corpus` globs are resolved under the story
`working_dir`; each matched file is read, skipped if binary or over a size cap,
and split into chunks:

- **markdown** (`.md`/`.markdown`) — split on headings into section-sized
  chunks; a section longer than the max window is sub-split with overlap.
- **everything else** — fixed-size token-ish windows with a small overlap, so
  a match near a boundary still surfaces its neighbourhood.

`chunk:` overrides the defaults (`max`, `overlap`, `mode: heading|window`).
Each chunk becomes an `embed.Entry` with `ID = "<path>#<n>"` and `Meta =
{path, ordinal}`; the corpus is embedded as **`Document`** role and stored.
The store key includes the chunking params, so changing `chunk:` rebuilds.

**Lookup.** The query is embedded as **`Query`** role, `Index.Rank(query,
top_k)` returns the hits, `min_score` filters the floor, and the handler binds
the ranked `{path, chunk_id, text, score}` list to world.

```
corpus globs ─▶ read+chunk ─▶ embed(Document) ─▶ embed.Store[corpus hash]
                                                        │
query ─embed(Query)─▶ Index.Rank(k) ─filter(min_score)─▶ world[bind] = [{path, text, score}, …]
```

The verb produces **text, not an answer** — it surfaces the relevant chunks; a
downstream `agent.ask`/`agent.decide` (or a view) consumes them. Keeping
search and generation separate keeps the retrieval decision independently
inspectable.

## Decision recording

`agent.search` is an **interpretive decision** (which chunks are relevant) and
records a breadcrumb that makes it replayable:

- the `query`, the resolved corpus globs + file count, the chunking params, and
  the full ranked `top_k` with each `score` — so a reviewer sees *what* it
  surfaced and *how confident* each hit was.
- `model`, `dim`, `pooling`, and the short corpus hash (epic shared decision 4)
  — the determinism anchor. Same pinned model + corpus reproduces the ranking
  exactly.

This rides the existing agent-call breadcrumb shape so the trace and
runstatus surfaces render it as a recorded agent decision, even though
dispatch bypasses the generative registry. Whether it reuses
`AgentCalled`/`AgentReturned` verbatim or gets a sibling `kind` is the open
question below.

## Engine seams & invariants

- **Read-only.** The handler reads files under `working_dir` and writes
  nothing — `working_dir` is the read scope; paths escaping it are a load- or
  call-time error. (`agent.search` carries no write capability regardless of
  any toolbox grant — cf. [agent-capability-model](agent-capability-model.md).)
- **Sidecar:** reuses the embedding `Sidecar` from
  [slice 1](embed-substrate.md); `endpoint:` mode skips fetch/spawn. A managed
  model with no pin and no endpoint is a load-time hard-fail with a clear
  message (mirrors `local-model-agent`'s `model:`-or-`endpoint:` invariant).
- **Empty corpus / no hits:** a glob that matches nothing, or all hits below
  `min_score`, binds an empty list (not an error); the room branches on it.

## Backward compatibility / migration

Additive and opt-in; no existing story or cassette changes. New call sites
adopt it explicitly.

## Tasks

```
## 1. Ingestion (internal/embed)
- [ ] 1.1 Glob resolution under working_dir + binary/size-cap skip
- [ ] 1.2 Chunker: markdown-by-heading + window-with-overlap fallback; chunk: overrides
- [ ] 1.3 Unit: deterministic chunk boundaries for fixed input

## 2. Verb (internal/host)
- [ ] 2.1 AgentSearchHandler: args validation, ingest→embed(Document)→store, embed(Query)→rank
- [ ] 2.2 Bind ranked []{path, chunk_id, text, score} + top to world[bind]; min_score floor
- [ ] 2.3 Register in the host-call registry; read-scope (working_dir) enforcement

## 3. Recording
- [ ] 3.1 Breadcrumb: query, corpus globs+count, chunk params, ranked top_k+scores, model/dim/corpus-hash

## 4. Verification (no live model)
- [ ] 4.1 Stateless: AgentSearchHandler with the fake Embedder ranks fixture files deterministically
- [ ] 4.2 Flow fixture: a room calls agent.search over on-disk fixtures; result binds; legacy paths green
- [ ] 4.3 Gated live e2e (KITSOKI_EMBED_E2E=1): real sidecar over a small real corpus

## 5. Adopt + document
- [ ] 5.1 Wire one demo story to retrieve from a docs/ subset
- [ ] 5.2 docs/architecture/embeddings.md agent.search section; mark this slice shipped in the epic
```

## Verification

No LLM. With the fake `Embedder` and a fixture corpus on disk, a stateless
handler test (and an intent-only flow fixture, `host_handlers:` opting the
fixture into the orchestrator runner) confirm: the query routes to the
expected chunks at the expected order, `min_score` drops low hits, and an
empty glob binds `[]`. The gated live e2e (§4.3) is the only real-model test
and never runs by default (memory: [no-llm-tests]).

## Open questions

1. **Breadcrumb kind.** Reuse `AgentCalled`/`AgentReturned` verbatim (the
   trace already renders them; `meta` carries the scores), or add a sibling
   `agent.search` event kind so runstatus can badge retrieval distinctly?
   *Lean: reuse, with a `verb: "search"` discriminator* — fewer trace
   consumers to touch; revisit if the UI needs a dedicated lane.
2. **Corpus freshness.** Re-hash + re-embed on every call (correct, costs a
   stat+hash per file), or trust an mtime cache? *Lean: content-hash every
   call* — it is cheap relative to the embed and keeps replay honest; an
   mtime fast-path is a later optimization.
3. **Per-file vs. per-chunk results.** Bind chunks (precise, may return several
   from one file) or collapse to best-chunk-per-file? *Lean: chunks, with the
   file path in `Meta`* — the room can group; collapsing loses precision.

## Non-goals

- **A reranker / generation step.** `agent.search` returns ranked text; an
  answer is a downstream `agent.ask`/`agent.decide`'s job (epic non-goal).
- **Non-file corpora.** World-key or app-declared named indexes are not in
  scope here — files on disk only. A world-key corpus is a plausible later
  extension on the same substrate.
- **Recursive crawling beyond the glob, PDF/binary extraction, OCR.** Text
  files the glob names, chunked; nothing fancier.
