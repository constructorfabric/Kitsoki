// Package server owns the managed lifecycle of the local-model sidecar: the
// llama.cpp OpenAI-compatible HTTP server that [kitsoki/internal/agent].LocalLLMAgent
// talks to. It is the deterministic, side-effecting half of the local-model
// backend — fetch-on-first-use of the server binary and the GGUF weights, spawn,
// health-gate, and orderly teardown — kept out of the agent transport so the
// transport stays a pure HTTP client and stays trivially testable in endpoint
// mode.
//
// The agent transport depends only on the narrow [Sidecar.EnsureRunning] /
// [Sidecar.Close] surface (its localSidecar interface). This package supplies
// the concrete [Sidecar] and, behind it, two further seams — [Fetcher] and
// [Spawner] — so that download and process-launch can be faked in tests. No test
// in this package ever downloads a file or spawns llama-server: the fakes
// substitute a temp path and an in-process [Process] backed by an httptest
// health server.
//
// # Modes
//
// A [Sidecar] runs in exactly one of two modes, fixed at construction:
//
//   - Endpoint (attach) mode — endpoint != "". EnsureRunning returns the
//     configured base URL verbatim and NEVER fetches or spawns; Close is a
//     no-op. This is bring-your-own-server (an operator already runs
//     llama-server, or a test points at an httptest.Server).
//   - Managed mode — endpoint == "". On the first EnsureRunning the sidecar
//     fetches the binary and weights (cached under ~/.cache/kitsoki), spawns
//     llama-server bound to 127.0.0.1, then polls GET /health until the server
//     is ready or ctx is done. Subsequent calls return the cached base URL.
//     Close terminates the process (SIGTERM, then SIGKILL after a grace
//     window).
//
// # Seams
//
//   - [Fetcher] resolves the server binary and a model's weights to local
//     paths, downloading and sha256-verifying against baked pins on first use.
//     [NewFetcher] returns the real implementation; tests inject a fake via
//     [WithFetcher].
//   - [Spawner] launches a process and returns a [Process] (Signal/Wait),
//     mirroring the os/exec surface SubprocessAgent uses. [NewSpawner] returns
//     the real implementation; tests inject a fake via [WithSpawner].
//
// # Invariants
//
//   - Lazy and once: managed startup happens on the first EnsureRunning under a
//     mutex; concurrent callers and repeat calls reuse the single process and
//     base URL. Endpoint mode never holds a process.
//   - Health gates readiness: EnsureRunning does not return a managed base URL
//     until GET /health returns 200, so the first agent POST never races a
//     half-open server.
//   - Close is idempotent and ordered: SIGTERM first, escalate to SIGKILL only
//     after a grace window (terminateTimeout, mirroring agent's
//     SubprocessTerminateTimeout). Endpoint mode Close is a no-op.
//   - Nothing binary is committed: the server binary and *.gguf weights live in
//     the cache dir, never the repo (see .gitignore). [Fetcher] provisions them.
//
// # Non-goals
//
//   - No request handling. This package starts and stops the server; the OpenAI
//     /v1/chat/completions call lives in [kitsoki/internal/agent].LocalLLMAgent.
//   - No ret/ model-management UI. EnsureRunning provisions exactly the binary
//     and one model it is asked for; eviction and multi-model pooling are out
//     of scope.
//   - No restart-on-crash. A wedged or exited server surfaces as a health-check
//     or transport error; whether to retry is the state machine's call, not the
//     sidecar's (mirrors the agent package's no-retry stance).
package server
