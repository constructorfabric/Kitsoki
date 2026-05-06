# Testing

Hally has two test modes — together they let an app author exercise both
the **state logic** (does the right transition fire?) and the **LLM
intent recognition** (does free text reach the right intent?) without
paying for tokens.

| Mode | Cost | Determinism | Purpose |
|---|---|---|---|
| **Mode 2 — flow tests** | Zero | Yes | State logic, effects, world transitions. Runs on every PR. |
| **Mode 1 — intent tests** | Variable | Optional | LLM pass-rate on natural-language inputs. Run on demand. |

Both modes live in `internal/testrunner/` and are exposed via
`hally test flows` and `hally test intents`.

---

## 1. Flow tests (Mode 2, deterministic)

Path: `<app-dir>/flows/*.yaml`. Each fixture is a YAML file with a
sequence of turns and per-turn assertions.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: foyer
initial_world:
  wearing_cloak: true

turns:
  - intent: { name: go, slots: { direction: south } }
    expect_state: bar
    expect_world: { wearing_cloak: true }

  - input: "hang up the cloak"            # routed via oracle (replay)
    expect_state: cloakroom
    expect_world: { wearing_cloak: false }

expect_no_errors: true
```

A turn uses **either** `intent:` (skips the oracle entirely — the
authoritative way to test state logic) or `input:` (requires an
oracle file and exercises the routing). Mix freely.

### Per-turn assertions

| Field | Meaning |
|---|---|
| `expect_state` | Exact state path the machine ends on. |
| `expect_world` | Partial map; every listed key must match. |
| `expect_view_matches` | Regex against the rendered view. |
| `expect_outcome` | One of `transitioned`, `rejected`, `clarified`. |
| `expect_error` | Specific intent error code (e.g. `GUARD_FAILED`). |
| `world_override` | Map applied to world *before* guard evaluation; lets you probe arcs that would otherwise need a long preceding flow. |

### Fixture-level assertions

| Field | Meaning |
|---|---|
| `expect_no_errors` | Default `false`. When `true`, any in-band validation error fails the fixture. |
| `expect_final_state` | The state the fixture should end on. |

### Running

```sh
hally test flows testdata/apps/cloak/app.yaml
hally test flows testdata/apps/cloak/app.yaml --flows "flows/winning*.yaml"
hally test flows testdata/apps/cloak/app.yaml --json /tmp/results.json
```

Exit codes: `0` pass, `1` fail, `2` setup error.

### Oracle for `input:` turns

When a fixture uses `input:`, the runner needs an oracle YAML —
either passed explicitly with `--oracle path` or auto-discovered at
`<app-dir>/oracle.yaml`. Oracle shape:

```yaml
kind: oracle
app_id: cloak-of-darkness
app_version: 0.1.0
generated_at: 2026-04-22T10:00:00Z
generator: hand
entries:
  - state: foyer
    input: "go south"
    intent: { name: go, slots: { direction: south } }
    confidence: 1.0
    majority_of: 1
```

Lookup is exact first, then case-insensitive. Missing entries cause
the turn to fail with `UNKNOWN_INTENT`.

---

## 2. Background-job fixtures

When any of `host_handlers:`, `advance_clock:`, or `expect_inbox:`
appear, the runner switches to the **orchestrator-backed path** —
fake clock, in-memory job store, stub host handlers — instead of the
pure-machine path.

```yaml
test_kind: flow
app: ../app.yaml
initial_state: lobby
initial_world: { result: "", last_job_id: "" }

host_handlers:
  host.run:
    data: { stdout: "hello", exit: 0 }
    delay: "1s"

turns:
  - intent: { name: enter }
    advance_clock: "2s"
    expect_world: { result: "hello" }
    expect_inbox:
      unread: 2
      severities: ["info", "success"]

expect_no_errors: true
```

`host_handlers:` declares stub closures by handler name:

| Field | Meaning |
|---|---|
| `data` | Map returned in `Result.Data` on success. |
| `error` | Domain-level error string (the job terminates `failed`). |
| `infra_error` | Infrastructure error (returned as a Go error). |
| `delay` | Duration the stub blocks before resolving. |

`advance_clock: "2s"` moves virtual time forward and **then** drains
both the scheduler and the orchestrator's session listener, so
`on_complete:` effects are applied **before** assertions are evaluated.

`expect_inbox` asserts on the in-app notification queue:

| Field | Meaning |
|---|---|
| `unread` | Exact unread notification count. |
| `needs_attention` | `action_required` count (clarifications). |
| `severities` | Sorted severity list for all unread items. |

A background job produces **two** notifications — `info` when
submitted and `success`/`error`/`warn` when terminal — so a single
job → `unread: 2`.

The full lifecycle (clarification, retry, error paths) is documented
in [`background-jobs/testing.md`](background-jobs/testing.md).

---

## 3. Intent tests (Mode 1, pass-rate)

Path: `<app-dir>/intents/*.yaml`. Each fixture lists a target intent
and a set of natural-language phrasings that should map to it.

```yaml
test_kind: intents
app: cloak-of-darkness
state: foyer
defaults:
  runs: 5
  min_pass_rate: 0.8

fixtures:
  - id: go_south_plain
    intent: { name: go, slots: { direction: south } }
    inputs: ["go south", "head south", "s"]

  - id: nonsense
    expect_failure:
      any_of: [UNKNOWN_INTENT, INTENT_NOT_ALLOWED_IN_STATE]
    inputs: ["pet the goldfish", "recompile the kernel"]
```

Each input is run `runs` times. The fixture passes if at least
`min_pass_rate` of runs match the expected intent (or expected error
code).

### Harnesses

| Harness | Cost | Determinism | When |
|---|---|---|---|
| `static` | Zero | Yes | Default; reads the oracle as a deterministic lookup. |
| `live` | Paid | No | Real Anthropic SDK calls. Use to seed the oracle. |
| `claude` | Free* | No | Shells out to the `claude` CLI. |

`*` *Free via your Claude Code login.*

### Running

```sh
hally test intents testdata/apps/cloak/app.yaml --harness static
hally test intents testdata/apps/cloak/app.yaml --harness live --runs 10

# Compile a live run into a replay oracle for use by Mode 2 / static
hally test intents testdata/apps/cloak/app.yaml \
    --harness live --emit-oracle testdata/apps/cloak/oracle.yaml

# Compare against a baseline pass-rate file
hally test intents testdata/apps/cloak/app.yaml \
    --harness live --baseline /tmp/baseline.json
```

Default harness is `static` unless `ANTHROPIC_API_KEY` is set.

---

## 4. Recording and oracles

When you want to capture real LLM traffic for later replay:

```sh
# Record a session to JSONL
hally run myapp.yaml --harness recording --record /tmp/rec.jsonl

# Convert to oracle (currently via hally test intents --emit-oracle)
hally test intents myapp.yaml --harness live --emit-oracle oracle.yaml
```

The record format is one JSONL object per turn:
`{state, input, intent, slots, ts, model, tokens_in, tokens_out}`.

---

## 5. Recording demo GIFs

`hally record` replays a flow YAML through the state machine and
encodes each state's view as an animated GIF — the same flow file
that drives `hally test flows`.

```sh
hally record testdata/apps/cloak/app.yaml \
    --flow testdata/apps/cloak/flows/winning.yaml \
    -o /tmp/cloak-win.gif

# All flows in a directory, dracula theme, custom timing
hally record myapp.yaml --flow myapp/flows/ -o demo.gif \
    --theme dracula --frame-ms 3000
```

The output is byte-reproducible: same flow + same flags → identical
GIF bytes. No external dependencies (no VHS, no ttyd, no ffmpeg).

---

## 6. Public testing helpers (`pkg/hallytest`)

For Go-level tests that drive a hally machine directly,
`pkg/hallytest` exposes a thin wrapper over `internal/testrunner`.
It's still small — most of the value today is in declarative YAML
fixtures — but it's the right entry point if you're embedding hally
into another binary.

---

## 7. CI recipe

```sh
go vet ./...                                    # fast static check
go test -race ./...                             # unit + integration
hally test flows testdata/apps/cloak/app.yaml   # deterministic flows
hally test flows testdata/apps/dev-story/app.yaml
hally test flows testdata/apps/background_jobs/app.yaml
hally test flows testdata/apps/proposal_smoke/app.yaml
hally test intents testdata/apps/cloak/app.yaml --harness static
```

Total runtime under 30 seconds on a modern laptop. Every step exits
non-zero on regression and is safe to chain with `&&`.
