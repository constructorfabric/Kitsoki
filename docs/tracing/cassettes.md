# Host Cassettes

A cassette is a YAML file that captures an ordered sequence of outbound host
calls and their canned responses. The testrunner replays episodes in declared
order — VCR-style — instead of installing per-handler inline stubs. One cassette
can be shared across multiple fixtures; a host call that matches no remaining
episode is an immediate hard fixture failure (`ErrCassetteMiss`).

For the fixture-level `host_cassette:` field and a short worked example, see
[`testing.md` §3](testing.md#3-host-cassettes). For an in-tree end-to-end
example see `stories/frontier_event/flows/scout_and_pay_cassette.yaml` (a
parallel of the sibling `scout_and_pay.yaml` fixture, with its `host_handlers:`
block replaced by a `host_cassette:` reference).

---

## File format

```yaml
# ── top-level header ────────────────────────────────────────────────────────
kind: host_cassette           # required; must be exactly this string
app_id: bugfix                # which app this cassette was recorded against
app_version: 0.1.0            # optional; semver of the app at record time
source_run: .bug-fix/ABR-429271-033  # optional; path to the real run dir
generated_at: 2026-05-25T00:00:00Z   # optional; ISO-8601 timestamp
match_on: [handler, phase, schema_name]  # default match keys (see §Matching)
record_mode: none             # none | new_episodes | all  (env var wins)
phase_from: ""                # optional Go regex override (see §Matching)

# ── episode list ─────────────────────────────────────────────────────────────
episodes:

  - id: phase_1_repro_agent        # unique ID; used in error messages
    match:
      handler:     host.agent.ask_with_mcp   # synthetic field (required)
      phase:       phase_1                    # synthetic field
      schema_name: 01-repro-report.schema.json # synthetic field
    response:
      data:
        submitted: !include 01-repro-report.json  # see §!include
    delay: 200ms                    # optional virtual-time delay
    # replay: any                   # uncomment to allow repeated plays

  - id: phase_1_jira_create
    match:
      handler:   host.transport.post
      transport: jira
      kind:      create
    response:
      data: { comment_id: "8344778", posted: true }

  - id: phase_3_jira_update
    match:
      handler:    host.transport.post
      kind:       update
      comment_id: "8344778"
    response:
      data: { comment_id: "8344778", posted: true, updated: true }

  - id: phase_2_infra_failure       # simulate an infrastructure error
    match:
      handler: host.run
      phase:   phase_2
    response:
      infra_error: "connection refused"   # returned as Go error to orchestrator

  - id: phase_4_domain_error        # simulate a domain-level error
    match:
      handler: host.run
      phase:   phase_4
    response:
      error: "exit status 1"        # returned in Result.Error
```

### Top-level fields

| Field | Type | Notes |
|---|---|---|
| `kind` | string | Must be `host_cassette`. Load fails otherwise. |
| `app_id` | string | Informational; not validated against the app. |
| `app_version` | string | Optional semver; informational only. |
| `source_run` | string | Path to the real run directory; provenance comment. |
| `generated_at` | string | ISO-8601 timestamp; informational only. |
| `match_on` | `[]string` | Default match keys. Not enforced by the matcher — each episode's `match:` map is authoritative. `match_on` is documentation for readers. |
| `record_mode` | string | `none` \| `new_episodes`. Overridden by `KITSOKI_CASSETTE_RECORD`. Any other value is rejected at load time. |
| `phase_from` | string | Optional Go regex override for `phase` derivation (see §Matching). |

### Episode fields

| Field | Type | Notes |
|---|---|---|
| `id` | string | Required; unique across the cassette. Appears in `ErrCassetteMiss`. |
| `match` | map | Key-value pairs matched against the call. At least `handler` should be present. |
| `response.data` | map | Returned in `host.Result.Data` on success. |
| `response.error` | string | Returned in `host.Result.Error` (domain-level error). Mutually exclusive with `response.infra_error`. |
| `response.infra_error` | string | Returned as a Go `error` (infrastructure-level failure). Mutually exclusive with `response.error`. |
| `delay` | string | Virtual-time duration (e.g. `200ms`, `2s`). Consumed by the fake clock's `Sleep`; requires the orchestrator path. |
| `replay` | string | `any` allows the episode to be replayed on every matching call. Default: episode plays once and is marked played. |
| `agent` | map | Optional. Present when the episode was recorded against a `host.agent.*` handler. Captures the agent-call metadata so replay emits the same `agent.call.start`/`agent.call.complete` trace events a real session would (see [`trace-format.md`](trace-format.md)). Omitted for non-agent episodes. |

When present, the `agent:` block carries: `verb` (`ask`\|`decide`\|`extract`\|`task`\|`converse`), `agent`, `model`, `duration_ms`, `prompt_tokens`, `response_tokens`, `cost_usd`, `system_prompt`, `prompt`, `input`, `response`, `error`, and `call_id` (advisory — recomputed on load). Long prompts and responses are best kept in `!include` sidecar files:

```yaml
  - id: phase_1_repro_agent
    match:
      handler:     host.agent.ask_with_mcp
      phase:       phase_1
      schema_name: 01-repro-report.schema.json
    response:
      data:
        submitted: !include 01-repro-report.json
    agent:
      verb:          ask
      agent:         reproducer
      model:         claude-opus-4-8
      duration_ms:   4200
      system_prompt: !include 01-repro-system.txt
      prompt:        !include 01-repro-prompt.txt
      response:      !include 01-repro-report.json
```

### Recorded agent-action transcripts

The `agent:` block may also carry an optional `transcript:` block that records
the call's native execution stream — the
[agent-action transcript sidecar](trace-format.md#agent-action-transcript-sidecar)
the web "Agent actions" drawer renders. On replay it is written to
`<trace_dir>/transcripts/<call_id>.{jsonl,timings}` **byte-verbatim**; no live
tool ever runs, and the golden contract is that a replayed cassette produces a
**byte-identical** sidecar.

```yaml
    agent:
      verb:        decide
      agent:       bugfix
      model:       claude-sonnet-4-6
      duration_ms: 1500
      response:    '{"decision":"refund","amount":49.0}'
      transcript:
        format: claude-stream-json
        # Each event is one verbatim JSON line; string elements are preserved
        # byte-for-byte (key order + number literals), so a live-captured-then-
        # folded transcript round-trips exactly. _kitsoki rows are the host-side
        # additions (validator reject/accept, the injected nudge) that the raw
        # -p stream omits.
        events:
          - '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0a","name":"mcp__validator__submit","input":{"decision":"refund","amount":"lots"}}]}}'
          - '{"_kitsoki":"validator_reject","source":"schema","reason":"amount: expected number, got string \"lots\""}'
          - '{"_kitsoki":"nudge","outer_iter":1,"text":"… the last submission attempt was rejected: amount: expected number …"}'
          - '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0b","name":"mcp__validator__submit","input":{"decision":"refund","amount":49.0}}]}}'
          - '{"_kitsoki":"validator_accept","outer_iter":1}'
          - '{"type":"result","subtype":"success","result":"refund","usage":{"input_tokens":1400,"output_tokens":320},"total_cost_usd":0.03}'
        timings: [0, 60, 80, 520, 720, 1000]   # ms offsets → the waterfall (optional)
```

`events` accepts either a verbatim JSON **string** per line (preserved
byte-for-byte) or an authored YAML/JSON **mapping** (marshaled compactly).
Malformed string events fail fast at `LoadCassette`. In **record mode** the live
captured transcript is folded back into this block automatically. A worked pair
lives in `stories/bugfix/flows/demo.cassette.yaml` (the `task` and `decide`
episodes).

---

## Matching

On each handler invocation the dispatcher walks `episodes[]` from the top and
returns the first unplayed episode whose `match:` map fully matches the call.

### How each key is resolved

For a key `k` in an episode's `match:` map:

1. **`handler`** — matched against the dispatched handler name (e.g.
   `host.agent.ask_with_mcp`). This is a synthetic field; it is not in the
   call's `args` map.
2. **`phase`** — matched against the first dot-separated segment of the
   orchestrator's current `StatePath`. For example, `phase_3.dispatching`
   yields `phase_3`. When `phase_from` is set on the cassette, the first
   capture group of that Go regex over the full `StatePath` is used instead.
   Useful when phase rooms nest non-trivially (e.g. `foo.bar.deciding` where
   you want `foo`).
3. **`schema_name`** — matched against `filepath.Base(args["schema"])`. Authors
   who dispatch two agent calls in the same phase with different schemas use
   this to distinguish them without splitting phases.
4. **`call`** — the author-assigned call-site id (`id:` on the invoke effect),
   threaded into args under the reserved `call` key. This is the most direct
   way to address two calls that share a handler name *and* a schema — give
   each invoke an `id:` and match with `call: <id>`. Preferred over picking a
   different agent verb to force distinct handler names. Distinct from the
   deterministic 16-hex `call_id` correlator recorded under each episode's
   `agent:` block. (Resolved via the general arg branch below; called out
   here because it is the canonical addressing key.)
5. **Any other key** — looked up directly in the call's `args` map (the `with:`
   values the effect threaded through).

### Match semantics

All key-value pairs in `match:` must match. A missing key in `args` does not
match unless the episode's value is also nil. Values are JSON-normalised before
comparison, so `1` (integer) and `"1"` (string) match correctly regardless of
YAML type coercion.

**First unplayed match wins.** Once played, an episode is skipped in future
walks — unless `replay: any` is set, in which case it matches on every call
that satisfies the map.

---

## `!include`

```yaml
response:
  data:
    submitted: !include relative/path.json
```

`!include` is resolved at cassette load time. The path is resolved relative to
the cassette file's directory. The referenced file must be valid JSON; it is
parsed and inlined as a compact JSON literal in the YAML value position.

Only the value side of a mapping entry is supported (e.g.
`someKey: !include path.json`). Block scalars and anchors using `!include` are
not supported. The `!include` pre-pass runs before YAML unmarshaling so any
structurally valid YAML key can carry an included value.

This lets cassettes reference real artifact files from a recorded run directory
without duplicating multi-KB JSON blobs inline. When the artifact is
regenerated (e.g. a schema changes), the cassette auto-reflects the new
content without edits.

---

## Record mode

```sh
KITSOKI_CASSETTE_RECORD=new_episodes kitsoki test flows app.yaml
```

| Mode | Behaviour on miss |
|---|---|
| `none` (default) | Miss returns `ErrCassetteMiss` — hard fixture failure. |
| `new_episodes` | Miss delegates to the fallback handler (real or stub), captures the result, appends a new episode to the cassette file, and returns the result. The fixture proceeds. |

The environment variable `KITSOKI_CASSETTE_RECORD` overrides the cassette
file's `record_mode` field. This lets CI run with `record_mode: new_episodes`
in the file during authoring without changing it before commit — just unset the
env var. Any value other than `none` or `new_episodes` is rejected.

**CI safety — `KITSOKI_CASSETTE_STRICT=1`.** When set, any non-`none` effective
record mode is a hard error before any fixture runs. CI sets this so an
accidental `KITSOKI_CASSETTE_RECORD=new_episodes` in a shell profile cannot
silently re-record against production transports. The check runs in
`buildOrchestratorRig` before any turn is executed.

```sh
# CI recipe (add to the existing flow-test invocations):
KITSOKI_CASSETTE_STRICT=1 kitsoki test flows testdata/apps/bugfix/app.yaml
```

`KITSOKI_CASSETTE_STRICT` is the only implemented gate — there is no separate
`--strict-recording` CLI flag.

---

## Fallback to `host_bindings:`

A cassette miss resolves as follows:

1. If an episode matches, play it.
2. Otherwise, if the fixture declared `host_bindings:` and a real handler was
   pre-registered for the iface, dispatch to that handler (the only path that
   escapes miss-failure).
3. Otherwise, return `ErrCassetteMiss` — a hard fixture failure.

In short: `host_bindings:` declares "this iface is live; the cassette does not
cover it." The cassette covers all other handlers and still fails closed on any
call that matches neither an episode nor a live binding.

`host_cassette:` and `host_handlers:` are mutually exclusive. Setting both is a
load-time error.

---

## CLI tools

```sh
kitsoki cassette diff old.yaml new.yaml          # structural diff keyed by episode id
kitsoki cassette lint cassette.yaml --against-app app.yaml  # orphan + duplicate + !include checks
```

`diff` exits 1 if the two cassettes differ, 0 if they match. `--verbose` also
prints unchanged ids; `--json` emits a machine-readable diff
(`{"added":[],"removed":[],"changed":[…]}`).

`lint` checks for duplicate episode ids, missing `!include` files, and — when
`--against-app` is supplied — episodes whose `match.handler` is not literally
referenced by any `Invoke:` in the app's effect graph (orphans). Orphans fail
the lint by default. `--strict` also fails on warnings.

Agent trace conformance is implemented as a deterministic substrate in
`internal/agenteval/conformance`: it reads JSONL traces, pairs
`agent.stream`/`agent.tool_call` tool-use events to their `agent.call.start`
contract by `call_id`, and fails when recorded tools exceed the declared
`allowed_tools`, `denied_tools`, or `effect` class. The current package-level
tests include compliant and deliberately out-of-box fixtures; cassette lint can
wire this checker when it starts validating recorded agent traces directly.

**Orphan check is iface-blind.** The walker compares episode handlers against
literal `Invoke:` strings only. An app that dispatches through an iface name
(e.g. `Invoke: iface.scout.run` bound to `default: host.run`) will flag a
`host.run` episode as orphaned even though the call reaches it at runtime.
Treat `--against-app` orphans as a hint, not a hard truth, for apps that use
ifaces. The in-tree smoke test
`stories/frontier_event/flows/scout_and_pay_cassette.yaml` exhibits this.

---

## Common gotchas / when to use what

**Stick with `host_handlers:`** for fixtures that dispatch three or fewer host
calls, all of which can return the same envelope on every invocation. The inline
stub is simpler to read and requires no separate file.

**Reach for cassettes** once you are sequencing five or more calls, need
different responses for the same handler in different phases, or want to share a
captured trace across multiple fixtures (happy path, error path, feedback path
all keyed off the same recorded run).

**Cassettes are outbound-only.** The cassette captures what the app sends out
and cans the response. Inbound message simulation — the user's `continue` /
`feedback` replies at checkpoint rooms — stays in the fixture's `intent:` turns
interleaved between dispatch turns.

**Cassettes do not replace `advance_clock:`.** Virtual time still requires per-turn
`advance_clock:` declarations in the fixture. The cassette stubs the host-call
result; it has no knowledge of when background jobs complete relative to the
clock. Every background-job phase that needs `on_complete:` to fire still needs
a corresponding `advance_clock:` in the fixture turn that submitted the job.

**`replay: any` for polling loops.** If an app polls a handler in a loop (e.g.
waiting for a CI check to pass), add `replay: any` to the episode so it
replays without exhausting the episode list. Without it, the second call misses
and fails the fixture.

**Episode IDs must be unique.** Duplicate IDs are accepted at load time but will
confuse `ErrCassetteMiss` error messages and the planned `cassette lint` check.
Use a `<phase>_<handler_suffix>` convention (e.g. `phase_3_jira_update`) so IDs
are self-documenting and sort stably in diffs.
