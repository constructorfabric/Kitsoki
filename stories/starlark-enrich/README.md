# Starlark Enrichment

A small, runnable example of **`host.starlark.run`** — kitsoki's deterministic
Starlark glue capability. The story takes a bare numeric user id, calls a plain
JSON HTTP API, and reshapes the response into a display-ready profile, entirely
inside a sandboxed Starlark script.

This is the canonical use case for `host.starlark.run`: deterministic data
wrangling plus a single HTTP call that is **too fiddly for YAML effects and too
small for a bespoke Go host handler**. A shell-out (`host.run`) could do it, but
it would be opaque to the trace, unsandboxed, and non-portable. A Starlark
script is a real expression language that is nonetheless deterministic,
introspectable, and replayable.

## What it demonstrates

| Piece | File | Shows |
|-------|------|-------|
| the script | [`scripts/enrich_user.star`](./scripts/enrich_user.star) | `main(ctx)` reading `ctx.inputs[...]`, calling `ctx.http.get`, reshaping JSON with the `json`/string stdlib, returning a typed dict; the `INPUTS`/`OUTPUTS` convention dicts |
| the typed contract | [`scripts/enrich_user.star.yaml`](./scripts/enrich_user.star.yaml) | the **sidecar**: typed, validated `inputs:` / `outputs:` the engine enforces (the in-script dicts are documentation only) |
| the wiring | [`rooms/profile.yaml`](./rooms/profile.yaml) | a room that `invoke:`s `host.starlark.run` with `with.script` + `with.inputs`, binds the outputs, uses `once: true` (reload-safe) and an `on_error: failed` arc |
| the rendering | [`rooms/profile.yaml`](./rooms/profile.yaml) + [`views/base.pongo`](./views/base.pongo) | the bound outputs rendered through typed view elements (`kv`, `list`, `prose`) + a pongo2 base template |
| the allow-list | [`app.yaml`](./app.yaml) | `hosts: [host.starlark.run]` — the capability must be declared |

## How the call is wired

```yaml
on_enter:
  - invoke: host.starlark.run
    id: enrich_user
    with:
      script: scripts/enrich_user.star      # resolved against the app root; sidecar must sit beside it
      inputs:
        user_id: "{{ world.user_id }}"      # single-expr template → stays a typed int
        api_base: "https://jsonplaceholder.typicode.com"
    bind:
      display_name: display_name            # script output → world key
      contact:      contact
      domain:       domain
      is_complete:  is_complete
    once: true                              # skip when all bind targets are set; re-arm when cleared
    on_error: failed                        # script fail()/bad-output/non-2xx → world.last_error + route here
```

The script's interface is **typed and validated twice**: `with.inputs` is checked
against the sidecar's `inputs:` *before* the script runs (wrong type / missing
required → the `on_error:` arc), and the return dict is checked against
`outputs:` *after* it runs (every declared output must be returned; no
undeclared key may be). Outputs flow **only** through `main()`'s return dict —
there is no `ctx.world.set`, so a Starlark effect can never mutate world
out-of-band.

The single `ctx.http.get` is the only side effect; it is recorded as a body-free
`{method, url, status}` summary that rides the trace under the reserved
`__http_exchanges` key (never bound into world unless an author asks for it).

## Run it

```sh
kitsoki run stories/starlark-enrich/app.yaml
```

Type `enrich` to run the script and see the derived profile; `back` clears the
derived fields and re-arms the `once:` call; `quit` to leave. (Running live
makes a real HTTP request to the public demo API.)

## Test it

Fast, offline, CI-safe, **no LLM and no network** — the real `host.starlark.run`
handler runs the on-disk script + sidecar, and only its HTTP is replayed from a
cassette via the fixture's `starlark_http_cassette:` field:

```sh
go test ./internal/testrunner/ -run TestStarlarkEnrichExample
```

The [`flows/`](./flows/) fixtures drive both paths end-to-end:

- [`enrich.yaml`](./flows/enrich.yaml) — happy path: `GET /users/1` is served
  from [`cassettes/enrich_user.http.yaml`](./cassettes/enrich_user.http.yaml),
  the four derived outputs are asserted via `expect_world`
  (`display_name: "Leanne Graham (@Bret)"`, `domain: "hildegard.org"`, …).
- [`enrich_error.yaml`](./flows/enrich_error.yaml) — error path: the lookup
  404s, the script `fail()`s, and the `on_error: failed` arc routes the session
  into the `failed` room with `world.last_error` set.

## Why so small?

The capability is the subject. The story is two rooms (plus a lobby and an error
room) so the `host.starlark.run` call, its sidecar contract, the bind, and the
record/replay seam are the only things to read. For the full author-facing
reference see [`docs/architecture/hosts.md`](../../docs/architecture/hosts.md)
(`host.starlark.run`) and the sandbox design in
[`internal/host/starlark/doc.go`](../../internal/host/starlark/doc.go).
