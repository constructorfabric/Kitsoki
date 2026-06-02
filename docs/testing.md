# Testing

Kitsoki has two test modes — together they let an app author exercise both
the **state logic** (does the right transition fire?) and the **LLM
intent recognition** (does free text reach the right intent?) without
paying for tokens.

| Mode | Cost | Determinism | Purpose |
|---|---|---|---|
| **Mode 2 — flow tests** | Zero | Yes | State logic, effects, world transitions. Runs on every PR. |
| **Mode 1 — intent tests** | Variable | Optional | LLM pass-rate on natural-language inputs. Run on demand. |

Both modes live in `internal/testrunner/` and are exposed via
`kitsoki test flows` and `kitsoki test intents`.

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

  - input: "hang up the cloak"            # routed via recording (replay)
    expect_state: cloakroom
    expect_world: { wearing_cloak: false }

expect_no_errors: true
```

A turn uses **either** `intent:` (skips the recording entirely — the
authoritative way to test state logic) or `input:` (requires a
recording file and exercises the routing). Mix freely.

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
kitsoki test flows testdata/apps/cloak/app.yaml
kitsoki test flows testdata/apps/cloak/app.yaml --flows "flows/winning*.yaml"
kitsoki test flows testdata/apps/cloak/app.yaml --json /tmp/results.json
```

Exit codes: `0` pass, `1` fail, `2` setup error.

### Recording for `input:` turns

When a fixture uses `input:`, the runner needs a **recording** —
a YAML mapping `(state, input) → (intent, slots)`. Pass one
explicitly with `--recording <path>` or let the runner auto-discover
`<app-dir>/recording.yaml`. Recording shape:

```yaml
kind: recording
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

### Asserting on chained `on_enter:` host calls

When step N+1 of an `on_enter:` block references a slot bound by step
N, the orchestrator re-renders step N+1's args against the post-bind
world at dispatch time (see
[`architecture.md` §11.5](architecture.md#115-chained-host-call-rerender-contract)).
Two events fire for each call: `HostInvoked` carries the *pre-bind*
args (snapshotted at machine time), and `HostDispatched` carries the
*post-rerender* args (what the handler actually receives) plus a
`rerender_fell_back` flag. When a test cares what step N+1's handler
saw, assert against `HostDispatched` — `HostInvoked` will still show
the un-substituted template.

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

`expect_jobs` pins the terminal status of jobs that landed during the
turn. Catches a regression class `expect_inbox` cannot see: a handler
that fails silently (e.g. `cmd:` passed as a list, type-assertion in
`handlers.go` returns `Result{Error: ...}`) lands `status=failed`, yet
`on_complete:` still runs and the game continues — only the per-job
terminal status is wrong.

```yaml
turns:
  - intent: { name: continue }
    advance_clock: "300ms"
    expect_jobs:
      - namespace: host.run        # job.Kind to match
        status:    done            # done | failed | cancelled | awaiting_input
```

Matching is order-sensitive against jobs that newly reached a terminal
status this turn (creation-time ASC). Surplus newly-terminal jobs not
asserted pass silently, so fixtures don't have to enumerate every
side-effect dispatch. A job that transitions from `awaiting_input` →
`done` after a clarification answer counts as newly terminal this turn.

The full lifecycle (clarification, retry, error paths) is documented
in [`background-jobs/testing.md`](background-jobs/testing.md).

---

## 3. Host cassettes

`host_handlers:` gives each handler one canned envelope for the entire fixture.
That is fine for single-dispatch arcs but breaks down once a fixture must drive
a handler through multiple calls that each return a different response — for
example, a 14-phase walk where `host.oracle.ask_with_mcp` is called once per
phase and must return a different schema envelope each time. Cassettes solve
this by recording a flat, ordered episode list across all handlers; the
testrunner replays episodes in declared order, and any call that matches no
remaining episode is an immediate hard failure.

### Minimal example

**`flows/cassettes/bugfix-happy.yaml`** — the cassette file:

```yaml
kind: host_cassette
app_id: bugfix
source_run: .bug-fix/ABR-429271-033
generated_at: 2026-05-25T00:00:00Z
match_on: [handler, phase, schema_name]

episodes:
  - id: phase_1_repro_oracle
    match:
      handler:     host.oracle.ask_with_mcp
      phase:       phase_1
      schema_name: 01-repro-report.schema.json
    response:
      data:
        submitted: !include 01-repro-report.json

  - id: phase_1_jira_create
    match:
      handler:   host.transport.post
      kind:      create
    response:
      data: { comment_id: "8344778", posted: true }
```

**`flows/happy.yaml`** — the fixture that references it:

```yaml
test_kind: flow
app: ../app.yaml
initial_state: bootstrap
host_cassette: cassettes/bugfix-happy.yaml

turns:
  - intent: { name: start, slots: { ticket: ABR-429271 } }
    advance_clock: "200ms"
    expect_state: phase_1.awaiting_oracle
```

### Key properties

**`host_cassette:` and `host_handlers:` are mutually exclusive.** Setting both
is a load-time error. `host_cassette:` is compatible with `host_bindings:` — an
iface rebound to a real handler via `host_bindings:` provides the fallback on a
cassette miss; without `host_bindings:`, a miss is `ErrCassetteMiss` and the
fixture fails immediately.

**Miss-fails-loudly.** A host call that matches no remaining episode is a hard
fixture failure (`ErrCassetteMiss{handler, args, available_episode_ids}`). This
is the load-bearing safety property: a workflow change that adds a new host call
cannot silently route to idle or trigger a real side effect — it surfaces as an
explicit miss instead of a misleading state mismatch.

**Record mode.** `KITSOKI_CASSETTE_RECORD=new_episodes` downgrades a miss from
a failure to an append: the dispatcher delegates to the fallback handler,
captures the result, and appends a new episode to the cassette file. Default
is `none`. `KITSOKI_CASSETTE_STRICT=1` makes any non-`none` record value a
hard error before any fixture runs — CI sets this to prevent accidental
re-recording against live transports.

For the complete cassette file format, matching rules, `!include` semantics,
and `record_mode` details, see [`docs/cassettes.md`](cassettes.md).

---

## 4. Intent tests (Mode 1, pass-rate)

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
| `static` | Zero | Yes | Default; reads a recording as a deterministic lookup. |
| `live` | Paid | No | Real Anthropic SDK calls. Use to seed a recording. |
| `claude` | Free* | No | Shells out to the `claude` CLI. |

`*` *Free via your Claude Code login.*

### Running

```sh
kitsoki test intents testdata/apps/cloak/app.yaml --harness static
kitsoki test intents testdata/apps/cloak/app.yaml --harness live --runs 10

# Compile a live run into a recording for use by Mode 2 / static
kitsoki test intents testdata/apps/cloak/app.yaml \
    --harness live --emit-recording testdata/apps/cloak/recording.yaml

# Compare against a baseline pass-rate file
kitsoki test intents testdata/apps/cloak/app.yaml \
    --harness live --baseline /tmp/baseline.json
```

Default harness is `static` unless `ANTHROPIC_API_KEY` is set.

---

## 5. Recordings

A **recording** is the source of truth for a deterministic replay —
a YAML lookup of `(state, input) → (intent, slots)` plus optional
metadata (confidence, majority count). The `replay` and `static`
harnesses read recordings; the `recording` harness produces JSONL
that can be compiled into one.

```sh
# Capture a real LLM session as JSONL while you play the app
kitsoki run myapp.yaml --harness recording --record /tmp/rec.jsonl

# Compile a live intent-test run directly into a YAML recording
kitsoki test intents myapp.yaml --harness live --emit-recording recording.yaml
```

The JSONL recording is one object per turn:
`{state, input, intent, slots, ts, model, tokens_in, tokens_out}`.

---

## 6. Recording demo GIFs

`kitsoki record` replays a flow YAML through the state machine and
encodes each state's view as an animated GIF — the same flow file
that drives `kitsoki test flows`.

```sh
kitsoki record testdata/apps/cloak/app.yaml \
    --flow testdata/apps/cloak/flows/winning.yaml \
    -o /tmp/cloak-win.gif

# All flows in a directory, dracula theme, custom timing
kitsoki record myapp.yaml --flow myapp/flows/ -o demo.gif \
    --theme dracula --frame-ms 3000
```

The output is byte-reproducible: same flow + same flags → identical
GIF bytes. No external dependencies (no VHS, no ttyd, no ffmpeg).

---

## 7. Stubbing oracle calls

Every `host.oracle.*` handler reads its claude subprocess from the context via
`host.WithClaudeRunner`. Tests inject a `ClaudeRunner` function in-process so no
real subprocess is forked. Phase 1 ships per-verb fake factories for the five new
verbs:

```go
// Simplest form — always returns the scripted text.
ctx := host.WithClaudeRunner(context.Background(), host.FakeDecide("verdict"))
res, _ := host.OracleDecideHandler(ctx, args)

// Meta form — embeds flag metadata so tests can assert forwarding.
ctx := host.WithClaudeRunner(context.Background(), host.FakeDecideWithMeta("verdict"))
res, _ := host.OracleDecideHandler(ctx, args)
result, sp, model, tools := host.ParseFakeMetaReply(res.Data["stdout"].(string))
// tools == "host.Read,host.Grep" — asserts --allowedTools was forwarded.
```

Available factories:

| Factory | Verb |
|---|---|
| `host.FakeExtract(text)` | `host.oracle.extract` |
| `host.FakeDecide(text)` | `host.oracle.decide` |
| `host.FakeAsk(text)` | `host.oracle.ask` |
| `host.FakeTask(text)` | `host.oracle.task` |
| `host.FakeConverse(text)` | `host.oracle.converse` |
| `host.FakeDecideWithMeta(text)` | decide — embeds flags in reply |
| `host.FakeAskWithMeta(text)` | ask — embeds flags in reply |
| `host.FakeExtractJSON(v)` | extract — JSON-encodes v as stdout |
| `host.FakeDecideJSON(v)` | decide — JSON-encodes v as stdout |

The `…WithMeta` factories append ` system=[<sp>] model=[<m>] tools=[<csv>]`
to the reply string. Use `host.ParseFakeMetaReply` to destructure it. This
lets a single test assert that an agent's `Tools`, `Model`, and `SystemPrompt`
were all threaded through correctly without writing a custom runner.

**Costs-nothing rule.** Real-LLM tests are opt-in and are never run by
default (they consume tokens and require a live claude binary). Use the
fake factories for all new tests; gate real-LLM tests behind a build tag
or an explicit environment variable.

---

## 8. Replay tooling

`kitsoki replay <session-id>` re-runs the `host.oracle.task` spans recorded
in a session's event log. It is used for regression testing of code-writing
tasks (did the agent still produce the same files?) and for evaluating model
upgrades (does a newer model diverge from the recorded output?).

### Modes

| Mode | Flag | What runs |
|---|---|---|
| `file_diff` | `--mode file_diff` (default) | Replay Mode A/B spans deterministically from `(initial_state_hash, final_diff)`. Mode C spans are skipped. |
| `llm_rerun` | `--mode llm_rerun` | Re-ask every recorded LLM prompt with a fresh Claude call. Diff the new output against the recorded output. |
| `hybrid` | `--mode hybrid` | Replay Mode A/B deterministically, then re-run LLM spans for divergence comparison. |

### Mode C skip behaviour

Spans with `replay_mode: external_side_effect` are never re-applied in
`file_diff` mode. At the end of a replay run, a summary line is printed for
any skipped spans:

```
skipped 2 external-side-effect spans (host.oracle.task, trace IDs: tsk-abc123, tsk-def456)
```

These spans can be inspected with `kitsoki inspect --session-id <id>
--span-kind task.end` and re-run interactively with `--mode llm_rerun`.

### Model selection

For `llm_rerun` and `hybrid` modes, `--model <model-id>` overrides the
model recorded in the span. Omit the flag to use the same model that ran
originally. This is the intended path for model-upgrade evaluation:

```sh
kitsoki replay ses-abc123 --mode llm_rerun --model claude-haiku-4-5
```

### Tier-swap detection (Phase 5)

For `host.oracle.extract` spans, the replay additionally checks whether a
recently-added synonym or slot-template would have resolved an input that
previously required an LLM call. This is the "progressive determinism" loop
documented in the oracle-split proposal §4: as the author grows the synonym
library, earlier LLM calls become unnecessary and the deterministic tier
covers more.

The authoring surface for suggesting synonyms is `kitsoki extract
suggest-synonym`, which is planned for Phase 5. The replay machinery hooks
are present in Phase 4; the CLI surface is not yet wired.

### Status

Journal traversal is not yet implemented (Phase 6 will wire the full
traversal against the oracle-serve surface). Phase 4 delivers the CLI
surface, flags, and mode classification. Running `kitsoki replay` in any
mode returns a structured error explaining what remains.

---

## 9. CI recipe

```sh
go vet ./...                                    # fast static check
go test -race ./...                             # unit + integration
kitsoki test flows testdata/apps/cloak/app.yaml   # deterministic flows
kitsoki test flows testdata/apps/dev-story/app.yaml
kitsoki test flows testdata/apps/background_jobs/app.yaml
kitsoki test flows testdata/apps/proposal_smoke/app.yaml
kitsoki test intents testdata/apps/cloak/app.yaml --harness static
```

Total runtime under 30 seconds on a modern laptop. Every step exits
non-zero on regression and is safe to chain with `&&`.
