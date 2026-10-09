# Authoring a kitsoki glue script (`host.starlark.run`)

This is the **procedural** companion to the authoritative contract reference at
[`docs/architecture/hosts.md#hoststarlarkrun`](../../../../docs/architecture/hosts.md#hoststarlarkrun).
That doc is the source of truth for the field table, sidecar types, the `ctx`
surface, error mapping, and the HTTP-cassette format — read it for *what* the
contract is. This file is *how* to write and validate one, and the gotchas that
bite in practice. It does not repeat the contract; it links to it.

`host.starlark.run` is the deterministic glue escape hatch: too fiddly for the
expr-lang `with:`/guard vocabulary, too small for a bespoke Go handler. Sandbox
source: `internal/host/starlark/` (design rationale in its `doc.go`); the
`host.Handler` adapter is `internal/host/starlark_run.go`.

For the broader Starlark experience — including the exact stdlib, CodeAct's
bounded Starlark loop, capability sandboxing, and promotion from CodeAct to a
checked-in script — see
[`docs/architecture/starlark.md`](../../../../docs/architecture/starlark.md). The exact
stdlib function list is [`stdlib.md`](stdlib.md).

## The three files

A glue capability is three artifacts that travel together:

```
stories/<story>/scripts/derive.star        the script — defines main(ctx) -> dict
stories/<story>/scripts/derive.star.yaml    the sidecar — the authoritative typed interface
stories/<story>/rooms/<room>.yaml            the effect — invoke: host.starlark.run, with: + bind:
```

The effect, in a room's `on_enter` or a transition's `effects:`:

```yaml
hosts: [host.starlark.run]            # MUST be in the app-level allow-list
# …
- invoke: host.starlark.run
  id: derive_widget
  with:
    script: scripts/derive.star       # relative to the app root; resolved at load time
    capabilities:
      http:
        methods: [GET]
    inputs:
      widget_id: "{{ world.selected }}"   # MUST be {{ }}-templated — a bare `world.selected` is passed as a literal string
  bind:
    name: widget_name                 # script output `name` → world.widget_name
  on_error: lookup_failed             # fail() / bad shape routes here
  once: true                          # optional: idempotent on re-entry / reload
```

The sidecar — **not** the script — is what the engine enforces. A missing
`required` input, a forgotten output, an undeclared returned key, or a type
mismatch is a domain error that fires `on_error:`. The full type list and
validation rules live in [hosts.md §The sidecar contract](../../../../docs/architecture/hosts.md#the-sidecar-contract).

> **Wire every `inputs:` value as a `{{ }}` template.** `host.starlark.run` does
> not expr-evaluate inputs — the machine resolves them first, and only a string
> wrapping the whole value in `{{ }}` is evaluated (a *sole* `{{ expr }}` keeps the
> typed value, so a declared `int` stays an `int`). A **bare** `world.foo` reaches
> the script as the literal string `"world.foo"` — the silent footgun that looks
> like the script "ignored" the value. The loader now rejects this at load;
> `kitsoki validate <app.yaml>` surfaces it without a session. Likewise, when a
> *transition-effect* script needs a world value, pass it as a templated input,
> not via `ctx.world` (a stale snapshot mid-transition).

## Writing `main(ctx)` — the things that bite

These are kitsoki-specific traps on top of the general
[language gotchas](language.md#python-3--starlark-divergence-cheatsheet):

- **`ctx.inputs` is a dict**, keyed (`ctx.inputs["x"]`), not an attribute
  (`ctx.inputs.x`). `ctx.world` and `ctx.http` are method objects, not dicts.
- **Capabilities are opt-in.** `ctx.inputs` and read-only `ctx.world` are
  available by default. `ctx.http`, `ctx.fs`, `ctx.probe`, and `ctx.host` are
  absent unless the effect's `with.capabilities` grants them. Use `http.methods`
  for HTTP, `fs.read` / `fs.write` path lists for filesystem access,
  `vcs: read` for local git probes, `github.issues: read` for
  `gh.issue.list`, and `host.verbs` for exact `ctx.host.call` verbs. Add
  `http.cassette_required: true` when a script must only run with an injected
  HTTP cassette/replay client.
- **Outputs flow through the return dict.** There is no `ctx.world.set`. If a
  value isn't in the returned dict, the effect's `bind:` can't see it. For small
  generated files, write through `ctx.fs.write(path, content)` and return the
  written path.
- **`fail(msg)` is the only error channel** — there are no exceptions. It maps to
  `Result.Error`, sets `world.last_error`, and fires `on_error:`. Validate inputs
  and branch on `resp.status` (a non-2xx is *not* an error; a response is truthy
  iff 2xx) before you reach for the data.
- **Only `json`, `math`, and decode-only `yaml`** are predeclared. Reaching for
  `time`/`random` (or any other module) is a resolve error — by design, so a
  recorded run replays byte-for-byte. See [`stdlib.md`](stdlib.md) for the
  exact function signatures.
- **No `%.2f`.** Starlark's `%` has no precision specifier. Round with `math`
  and assemble strings yourself — see the `fixed()` helper in
  [`stories/weather-report/scripts/weather_report.star`](../../../../stories/weather-report/scripts/weather_report.star).
- **Determinism is enforced upstream too:** maps cross the boundary key-sorted,
  so any iteration order a script observes is stable across runs.

## The validation loop (fast → thorough, all no-LLM)

1. **Static, ~1s — does it match the sandbox?**
   ```bash
   .agents/skills/starlark/tools/starcheck/  →  go run . -kitsoki scripts/derive.star
   ```
   The `-kitsoki` profile pins the real surface: `predeclared={json,math,yaml}`,
   strict dialect (no `set`/`while`/recursion/global-reassign), and requires a
   top-level `def main`. It parses + resolves **without executing**, so it is
   safe and instant. Catches the common mistakes the general resolver passes:
   an entry point named anything but `main`, or a reference to a non-sandbox
   name. (`buildifier -type=default` formats it.)

2. **Load-time — does the app accept it?**
   The loader's `validateStarlarkEffects` checks the script path resolves inside
   the app root, that the `.star.yaml` sidecar exists and parses, and that
   static `ctx.http`/`ctx.fs`/`ctx.probe`/`ctx.host` usage has matching
   `with.capabilities`. Any `kitsoki` command that loads the app surfaces these;
   the cheapest is the next step.

3. **Behavioural, no network/no cost — does it produce the right outputs?**
   Write a flow fixture with an HTTP cassette and run the **real** script:
   ```yaml
   # flows/forecast.yaml
   test_kind: flow
   app: ../app.yaml
   starlark_http_cassette: ../cassettes/forecast.http.yaml   # serves ctx.http from disk
   turns:
     - intent: { name: forecast, slots: { location: "Tokyo" } }
       expect_state: report
       expect_world: { place: "Tokyo, Japan" }
       expect_host_calls: [ { handler: host.starlark.run } ]
   ```
   ```bash
   kitsoki test flows stories/<story>/app.yaml      # or: make test-flows
   ```
   This injects a replay client, runs your `main(ctx)` for real with its HTTP
   served from the cassette — deterministic, no LLM, no socket. The cassette
   format, record modes (`none|once|new_episodes|all`), matchers, and secret
   redaction are documented in
   [hosts.md §Record / replay](../../../../docs/architecture/hosts.md#record--replay-http-cassettes).
   Recording a first cassette: set `record_mode: once` (or
   `KITSOKI_HTTP_CASSETTE_RECORD=once`), run once against the live API, commit
   the redacted result, then revert to `none`.

## Tracing

Each `ctx.http` call rides the `harness.returned` trace event as a body-free
`{method, url, status}` summary under the reserved `__http_exchanges` key. Each
`ctx.fs` / `ctx.probe` call likewise rides as `{op, target, status}` under the
reserved `__inspections` key. Both keys are added automatically; never declare
outputs by those names. Full HTTP bodies and file/probe payloads live only in
cassettes — never the trace. So a recorded session shows exactly what the script
called without leaking payloads or secrets.

## Worked examples in the tree

- [`stories/starlark-enrich/`](../../../../stories/starlark-enrich/) — minimal:
  one input, one GET, one output; happy + 404 paths with a record-once cassette.
- [`stories/weather-report/`](../../../../stories/weather-report/) — fuller: free-
  text input, geocode + dataset chained GETs, a branch on operator-chosen mode,
  object/list outputs rendered as markdown tables, and an `on_error:` failed room.

## CodeAct uses the same sandbox

`host.agent.codeact` is an agent loop over the same Starlark evaluator: each
agent step emits a snippet defining `def main(ctx): ...`, Kitsoki runs it with
the effect's shared `with.capabilities`, and the next step sees either the
returned dict or a structured Starlark error. The final `done(payload)` is
validated by the effect's `schema:` when one is present.

Use CodeAct when the deterministic script is not known yet but the agent should
remain inside the Starlark capability sandbox. Do not add `sandbox:` to a
CodeAct effect; that knob belongs to `host.agent.task` and write-capable
`host.agent.converse`. Once a CodeAct trajectory stabilizes, promote the final
snippet into `host.starlark.run` with a sidecar and prove the promoted flow runs
with no `host.agent.codeact` dispatch.
