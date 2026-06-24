# Runtime: Local small-model agent (llama.cpp sidecar)

**Status:** Largely shipped — trimmed to the unfinished tail. The engine,
registry, fallback, grammar-subset gate, sidecar lifecycle, fast cross-transport
tests, opt-in wiring, make targets, docs, **real download pins, and a verified
end-to-end zero-touch fetch+cache+inference run** all landed (see "What
shipped"). Throughput is now measured (former Open Question 3). **Two** items
remain before this proposal is deleted: the calibrated no_match → routing verdict
mapping, and a live A/B against `agent.claude` inside a real story. The broader
benchmarking and evidence-based promotion mechanism now lives in
[`agent-contract-eval.md`](agent-contract-eval.md).
**Kind:**   runtime
**Epic:**   — standalone

## What shipped

The `builtin.local_llm` agent is implemented, opt-in, and additive;
`agent.claude` stays the default and every existing story/cassette is
untouched. The settled design and behaviour now live in the narrative docs:

- **Backend, grammar, fallback, lifecycle, acquisition:**
  [`docs/architecture/agent-plugin.md` §9 "Local model backend"](../architecture/agent-plugin.md).
- **LLM-tier backend choice + the `extract_llm_on_no_match` opt-in:**
  [`docs/architecture/semantic-routing.md` §2.1](../architecture/semantic-routing.md).

Landed in code:

- `internal/agent/local_llm.go` — the OpenAI `/v1/chat/completions` `Agent`
  (best-effort `json_schema` response-format from `AskRequest.SchemaJSON`,
  `Meta.grammar` reflecting whether the constraint was actually applied,
  typed `AskError` mapping). `ValidateSubmission` stays the sole structural
  guarantee.
- `internal/agent/grammar_subset.go` + leaf package `internal/agent/grammar`
  (`SubsetOK`) — the supported-subset gate, reused at load time.
- `internal/agent/server/` — the managed sidecar: `Fetcher`/`Spawner` seams,
  lazy-once spawn, `/health` gate, SIGTERM-then-SIGKILL teardown, endpoint
  mode never fetches/spawns. `fetch.go` ships the full fetch-and-verify path
  with **real pinned URLs+sha256** for the linux/amd64 llama.cpp release, the
  default GGUF, and (older-glibc Linux only) a libstdc++ shim; it extracts the
  release tarball preserving SONAME symlinks and runs the server with the
  bundled libs on `LD_LIBRARY_PATH`. Verified end-to-end on a glibc-2.34 box:
  cold cache → download all three → spawn → grammar-constrained decide →
  schema-valid verdict → warm run reuses the cache with no re-download
  (`internal/agent/local_llm_e2e_test.go`, gated behind `KITSOKI_LLM_E2E=1`).
  An opt-in regression test asserts a managed agent fetches nothing at
  construction (download is strictly lazy, only inside a dispatched `Ask`).
- `BuildRegistry` `case "builtin.local_llm"` + loader invariants
  (`model:` or `endpoint:` required) + the load-time schema-subset check for
  `grammar: true` `decide` effects.
- Validation-reject fallback to `agent.claude` (one `call_id`, recorded via
  the new `substitution` field on `AgentReturned`/`AgentError`, gated to
  local backends via `Registry.IsLocalLLM`).
- Fast, no-live-model tests: a hand-seeded cassette in the conformance suite,
  a stateless httptest decide probe (live A/B gated behind
  `KITSOKI_PROBE_LOCAL_MODEL=1`), grammar fail-closed, and fallback coverage.
- `app.RoutingConfig.ExtractLLMOnNoMatch` (default off) + the `TrySemantic`
  no_match breadcrumb; `make fetch-models` / `make fetch-llama-server` over
  `tools/agent-fetch` (same `Fetcher`); `.gitignore` + `.gitkeep`'d cache dir.

## Remaining work

```
- [x] 3.3 DONE. Real download pins filled in internal/agent/server/fetch.go
          (linux/amd64 AND darwin/arm64 llama.cpp release b9444 + default
          Qwen2.5-1.5B GGUF + conda-forge libstdcxx-ng shim on older-glibc Linux,
          all sha256-pinned). darwin/arm64 archive sha verified on Apple Silicon
          and the managed e2e (TestLocalLLMManagedEndToEnd) passes there; the
          macOS dylibs resolve via @loader_path so flat extraction works with no
          extra env. Managed mode now zero-touch on both platforms.
- [ ] 3.2 Live A/B: point one real story's decide gate (e.g. the pr-refinement
          merge judge) at agent.local behind a flag and compare against
          agent.claude. Treat this as the local-backend pilot for
          agent-contract-eval.md's task-adherence benchmark: same bounded
          examples, same schema/toolbox conformance, same pass-rate and
          latency/cost evidence. The fetchable model now exists (3.3), so this
          is unblocked. NOTE the calibration gap surfaced by the e2e: the 1.5B
          returns confidence as a percentage unless the prompt states the 0..1
          scale — the decide prompt must specify field scales, and out-of-range
          output correctly trips ValidateSubmission → the agent.claude fallback.
- [ ] 4.  Calibrated no_match routing: wire TrySemantic's ExtractLLMOnNoMatch
          path to actually invoke the extract LLM tier on a deterministic
          no_match and map the free-form verdict onto the semroute.Verdict
          confidence bands (0.90/0.80/0.65/0.50). Today the flag is honoured as
          a recorded breadcrumb and the turn falls through to the main-turn LLM.
          This is Open Question 4 below; it needs calibration like the Oregon
          Trail work and must respect TrySemantic's pre-session-lock ordering.
```

## Open question (still open)

**Routing verdict mapping (drives remaining item 4).** How a free-form model
verdict maps onto the `semroute.Verdict` confidence bands
(0.90/0.80/0.65/0.50) — likely a constrained `{intent, slots, confidence}`
schema with a fixed confidence ceiling for LLM-tier matches. Needs calibration
like the Oregon Trail work in the routing calibration test. Until it is
calibrated, `extract_llm_on_no_match` is honoured as a breadcrumb only.

**Throughput/latency (former Open Question 3) — MEASURED and closed.** On a
10-core CPU Rocky/RHEL 9.4 VM with Qwen2.5-1.5B-Instruct Q4_K_M and
`--parallel 4`: generation ~6.9 tok/s, prompt ~56 tok/s, weights load ~2-3s. A
cold managed `decide` (download ~1.1 GB + load + decode) ran ~24-28s end-to-end;
a warm `decide` (server already up) ~12-13s. macOS/Metal MEASURED on an
M-series laptop via the managed e2e: cold `decide` (download + load + decode)
~27s, warm `decide` ~0.7s (Metal-accelerated) — substantially faster warm than
the CPU VM.

## Non-goals (unchanged)

- **MLX.** `mlx_lm.server` has no native `response_format`/`json_schema`/grammar
  parameter, so it can't even attempt constrained decoding without a second
  Outlines-style sidecar. llama.cpp with Metal gives uniform (best-effort,
  fail-open) GBNF enforcement on both platforms for far less surface;
  `ValidateSubmission` is then the identical guarantee everywhere. (Re-open
  only if Mac throughput proves inadequate in 3.2 *and* MLX gains native
  constrained decoding upstream.)
- **cgo / embedded inference.** No `go-llama.cpp`-style in-binary engine — it
  breaks cross-compilation and there is no production-quality pure-Go GGUF
  runtime. The sidecar is the simpler seam.
- **Replacing `claude` for the hard verbs.** `ask`/`task`/`converse` and any
  genuinely reasoning-heavy `decide` stay on `agent.claude`.
- **NuExtract / multimodal document extraction.** A future, separate
  document-extraction verb — not routing/decide.
