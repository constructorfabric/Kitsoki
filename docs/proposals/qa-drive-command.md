# Runtime: `kitsoki drive` — interactive headless driver

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   [story-qa-agent.md](story-qa-agent.md) (slice 2)

## Why

No surface lets an actor **read a view, decide what to type, and submit
free text** against a persistent session. The three candidates each miss
one axis:

| Surface | persistent? | free-text input? | routing | replay (free) |
|---|---|---|---|---|
| `kitsoki turn --state` (`turn.go`) | ✗ stateless | ✓ (`--input`) | live only | ✗ |
| `kitsoki turn --trace` (`turn.go:304`) | ✓ | ✗ `--intent` only (`noRunHarness`, `turn.go:524`) | none | ✗ |
| flow fixtures / `kitsoki test` | ✓ | pre-scripted list | replay | ✓ but fixed |

The trace path wires `&noRunHarness{}` precisely so the `--intent` path
never routes (`turn.go:526`). So an agent that wants to type *"go back to
the debug room"* and learn whether it routes to `go_debug` has nowhere to
go: live routing exists only on the stateless path, and the deterministic
path can't route at all. `kitsoki drive` is the missing combination.

## What changes

**One sentence:** `kitsoki drive` runs the real orchestrator turn loop
against a persistent trace, accepting **free text** per turn through a
**live or replay** harness, emitting the full human-fidelity `Frame`
(slice 1) per turn as JSONL — with VCR-style record/playback modes so a
live run captures a cassette a later run replays for free.

It is `turn --trace` with the real harness wired in instead of
`noRunHarness`, the slice-1 `Frame` on the way out, and python-vcr-style
cassette modes. It **supersedes** the scripted-input `kitsoki drive` of
[`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) §1 (a
pre-written input file is wrong for an agent that chooses inputs from what
it sees).

## Command

```
kitsoki drive <app.yaml> --trace out.jsonl \
   --harness live|replay \
   --cassette path/to/recording.yaml \
   --record once|none|all|new \
   --cols 100 --rows 30
```

Two ways to feed input:

- **Interactive / piped (the agent's path):** read one free-text line from
  stdin per turn, submit it, write the resulting `Frame` (as a JSON object)
  to stdout. The agent reads the frame, thinks, writes the next line. This
  is the REPL the `story-qa` skill drives.
- **`--script inputs.txt`:** convenience batch of newline-separated inputs
  (the old §1 use case) — same loop, inputs from a file. Kept for smoke
  tests in CI.

Per-turn stdout is one JSON object: `{ frame, routed_intent, confidence,
exit: accepted|rejected|terminal|error, host_calls }`. The trace JSONL is
the durable session (resumable like `turn --trace`).

## Impact

- **Code seams:** new `cmd/kitsoki/drive.go` — reuses the trace setup of
  `runTraceTurn` (`turn.go:304`: `store.OpenJSONL`, story reconstruction,
  `orch.RecordEffectiveStory`) but loops, submits **free text** via the
  routing path (`orch.Turn`, as the TUI does at `tui.go:781`) instead of
  `SubmitDirect`, and renders the slice-1 `Frame`.
- **Vocabulary:** no story/world vocabulary changes — this is a driver, not
  an engine-semantics change. New CLI flags only (table below).
- **Stories affected:** none change behavior; any story becomes
  drive-able.
- **Backward compat:** additive. `turn`/`record`/`run` untouched. Cassette
  files are the existing `recording.yaml` shape (`replay.go:17`,
  `docs/tracing/cassettes.md`).
- **Docs on ship:** `docs/architecture/developer-guide.md` §6,
  `docs/testing.md` (cassette modes).

## Vocabulary changes

CLI flags only — no engine vocabulary:

| Kind | Name | Shape | Notes |
|---|---|---|---|
| flag | `--harness` | `live \| replay` | live = `LiveHarness`; replay = `ReplayHarness` (`replay.go:67`) |
| flag | `--cassette` | path | recording.yaml to replay and/or record into |
| flag | `--record` | `once \| none \| all \| new` | VCR mode (below) |
| flag | `--cols`/`--rows` | int | frame width/height (epic open Q2) |
| flag | `--script` | path | optional batch input file (CI smoke) |

## The model

Deterministic by construction except at the one interpretive seam —
routing free text to an intent — which is exactly where the moat says an
interpretive decision lives and must be recorded:

```
stdin line ──▶ orch.Turn(free text) ──▶ [routing harness] ──▶ intent ──▶ machine transition ──▶ Frame
                                          (live | replay)        │
                                          recorded to cassette ◀─┘  (per --record mode)
```

The machine transition, effects, and host calls downstream of the routed
intent are the same deterministic execution every other surface runs. Only
the route is interpretive — and it is already recorded (slice-1 metadata +
the trace + the cassette).

### VCR record/playback modes

After python-vcr, applied to the routing cassette
(`RecordingHarness`/`ReplayHarness` already give record + replay; this adds
the *modes* and unifies the file shape — epic shared decision 4):

| `--record` | On a cassette **hit** | On a cassette **miss** |
|---|---|---|
| `none` | replay | **error** (`ErrRecordingMiss`, `replay.go:48`) — pure deterministic |
| `once` | replay | call live + append (only if cassette was empty/new) |
| `new` | replay | call live + append the new entry |
| `all` | ignore cassette | always call live + (re)record |

`none` is the CI/regression mode (free, fails on drift); `new` is the
exploratory-QA mode (replay what's known, fall through to live for novel
inputs, grow the cassette); `all` re-bakes after a story change.

This is the bridge between the epic's two QA modes (shared decision 3):
the agent explores under `--harness live --record new`, and the resulting
cassette replays under `--harness replay --record none` for a free,
deterministic re-run that catches rendering regressions.

## Decision recording

The routed intent is an interpretive decision and lands in the trace
exactly as today: `kitsoki drive` writes through the same JSONL event sink
as `turn --trace` (`store.OpenJSONL` + `WithEventSink`, `turn.go:330`,
`398`), so `oracle.call.*` / routing events, confidence, and the resulting
transition are all recorded and replayable. The cassette is the
distilled `(state, input) → intent` projection of those decisions
(`recording.go:14`). Nothing new to record — `drive` reuses the recording
substrate.

## Engine seams & invariants

- Reuses `runTraceTurn`'s setup verbatim where possible — factor the
  shared "open trace, resolve/reconstruct story, new session, record
  effective story" block out of `turn.go:304` into a helper both call (the
  refactor the original §1 proposal already anticipated).
- The one new wiring: build the **real** harness (`LiveHarness` or
  `ReplayHarness`, optionally wrapped in `RecordingHarness` per
  `--record`) instead of `noRunHarness`, and submit via the routing path
  (`orch.Turn`) rather than `SubmitDirect`.
- **Invariant:** `--harness replay --record none` must make **zero** live
  LLM calls (CLAUDE.md: tests never hit a real LLM by default). A miss is
  a hard error, not a silent live fallthrough. Verified by a test that
  injects a failing live harness behind the replay harness and asserts it
  is never called.

## Backward compatibility / migration

Purely additive — a new command. Existing cassettes
(`testdata/apps/*/recording.yaml`) replay unchanged under
`--harness replay`. The `RecordingHarness` JSONL output (`recording.go:14`)
and the `ReplayHarness` YAML input (`replay.go:17`) currently differ in
shape; this slice unifies them on the `recording.yaml` shape (add a
`drive`-time converter, or teach `RecordingHarness` to emit the YAML
shape) so a recorded run is directly replayable without a manual convert
step.

## Tasks

```
## 1. Engine
- [ ] 1.1 Factor turn.go:304's trace+story+session setup into a shared helper
- [ ] 1.2 drive.go: loop reading free text (stdin / --script), submit via orch.Turn
- [ ] 1.3 Wire LiveHarness | ReplayHarness (+ RecordingHarness per --record)
- [ ] 1.4 VCR modes once|none|all|new over the routing cassette
- [ ] 1.5 Unify record-output / replay-input cassette shape (recording.yaml)
- [ ] 1.6 Emit slice-1 Frame as per-turn JSONL on stdout

## 2. Verification
- [ ] 2.1 Stateless: drive a known cassette under --record none reproduces a known transcript byte-for-byte
- [ ] 2.2 No-live-fallthrough: replay+none never calls the injected (failing) live harness
- [ ] 2.3 Round-trip: live --record new produces a cassette that replays under --record none with identical frames
- [ ] 2.4 Free-text routing exercised against an existing app's recording.yaml (no live)

## 3. Adopt + document
- [ ] 3.1 Drive one real story (bugfix or dev-story) end-to-end against its cassette
- [ ] 3.2 developer-guide.md §6 + testing.md; supersede ai-collaboration §1; update epic slice row
```

## Verification

A reviewer confirms it with **no LLM**: pick an app with a
`recording.yaml`, `kitsoki drive app.yaml --harness replay --record none`
piping its recorded inputs, and diff the emitted frames against a golden.
The round-trip and no-fallthrough tests above are the teeth. The one path
that *needs* a live model — novel free text under `--harness live` — is
gated behind `--harness live` and never runs in the default suite (memory:
no-LLM-tests).

## Open questions

1. **`orch.Turn` vs. a new entry point.** `orch.Turn` is the TUI's
   routing submit; confirm it threads cleanly without a TUI model. *Lean:
   reuse `orch.Turn` — it is the orchestrator API, not TUI-bound; the TUI
   just happens to be today's only caller.*
2. **Async host calls / `on_enter` waits.** Some turns kick slow
   `host.run` work. *Lean: `drive` blocks per turn until the orchestrator
   settles (same as a flow replay), emitting the settled frame; a
   `--timeout` guards a hung host call.*

## Non-goals

- Oracle-output cassettes (converse/decide/task bodies) — parallel
  cassette-quality proposal (epic shared decision 4).
- A TUI. `drive` is headless; the agent reads JSON frames, not a screen.
- Screenshot rendering — slice 3 (`kitsoki shot`) consumes the frame this
  emits.
