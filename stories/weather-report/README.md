# Weather & Climate

A runnable example of **`host.starlark.run`** — kitsoki's deterministic Starlark
glue capability — driving **real HTTP** against a free, key-less public API
([Open-Meteo](https://open-meteo.com)). The operator types a free-text place
name and picks a mode; a single sandboxed Starlark script geocodes the place,
fetches the right dataset, and reshapes it into a tidy report.

Where [`starlark-enrich`](../starlark-enrich/) is the *smallest* `host.starlark.run`
example (one GET, scalar outputs), this one is the *realistic* one: **two
chained HTTP calls**, a **branch** on operator input, **structured** (object +
list) outputs, and genuine data wrangling — turning 365 daily temperatures into
a 12-month climate profile.

## What it demonstrates

| Piece | File | Shows |
|-------|------|-------|
| free-text input | [`app.yaml`](./app.yaml) + [`rooms/lobby.yaml`](./rooms/lobby.yaml) | the `forecast` / `climate` intents each carry a required `location` **slot**; the rooms capture it with a `choice:` **param** field (a labelled free-text box in both the TUI and web UI — no LLM needed to resolve the input) |
| the script | [`scripts/weather_report.star`](./scripts/weather_report.star) | `main(ctx)` geocoding via `ctx.http`, **branching** on `mode`, a second `ctx.http` call, WMO-code → label maps, and bucketing 365 daily values into 12 monthly aggregates with the `json` + `math` stdlib |
| the typed contract | [`scripts/weather_report.star.yaml`](./scripts/weather_report.star.yaml) | the sidecar: typed `inputs:` (location, mode) / `outputs:` (string, **object**, **list**) the engine validates either side of the run |
| the wiring | [`rooms/report.yaml`](./rooms/report.yaml) | a room that `invoke:`s `host.starlark.run` with `with.inputs` from world, binds the structured outputs, uses `once: true` (reload-safe) and an `on_error: failed` arc |
| the rendering | [`rooms/report.yaml`](./rooms/report.yaml) | bound outputs rendered through typed view elements (`kv`, `template` markdown tables) gated by `when:` on the chosen mode |
| the allow-list | [`app.yaml`](./app.yaml) | `hosts: [host.starlark.run]` — the capability must be declared |

## How the call is wired

```yaml
on_enter:
  - invoke: host.starlark.run
    id: weather_report
    with:
      script: scripts/weather_report.star    # resolved against the app root; sidecar sits beside it
      inputs:
        location: "{{ world.location }}"      # the free-text place from the slot
        mode: "{{ world.mode }}"              # "forecast" | "climate"
    bind:
      place:           place                  # script outputs → world keys
      coords:          coords
      headline:        headline
      current:         current                # object (forecast) / {} (climate)
      rows:            rows                    # list of per-day / per-month rows
      climate_summary: climate_summary         # object (climate) / {} (forecast)
    once: true                                # skip when bind targets are set; re-arm when cleared
    on_error: failed                          # script fail()/bad-output/non-2xx → world.last_error + route here
```

The script's interface is **typed and validated twice**: `with.inputs` against
the sidecar's `inputs:` *before* the script runs, and the return dict against
`outputs:` *after*. Outputs flow **only** through `main()`'s return dict —
there is no `ctx.world.set`, so a Starlark effect can never mutate world
out-of-band. Each `ctx.http` call is recorded as a body-free `{method, url,
status}` summary that rides the trace under the reserved `__http_exchanges` key.

## The two modes

| Mode | Endpoints | Output |
|------|-----------|--------|
| `forecast` | geocoding → `api.open-meteo.com/v1/forecast` (current + 5 daily) | current conditions + a 5-day table |
| `climate` | geocoding → `archive-api.open-meteo.com/v1/archive` (a fixed full year, 2023) | a 12-month mean-temp/precip table + annual summary |

The `climate` request uses a **fixed** historical year (not "today") so it is
fully deterministic; the script aggregates the 365 daily values into the monthly
table itself — the kind of fiddly work that is awkward in YAML effects and too
small for a bespoke Go host handler.

## Run it

```sh
kitsoki run stories/weather-report/app.yaml
```

Type `forecast Tokyo` or `climate Oslo` (any city). `back` looks up another
place; `quit` to leave. Running live makes real HTTP requests to the public
Open-Meteo API (no key required).

## Test it

Fast, offline, CI-safe, **no LLM and no network** — the real `host.starlark.run`
handler runs the on-disk script + sidecar, and only its HTTP is replayed from
cassettes via each fixture's `starlark_http_cassette:` field:

```sh
go test ./internal/testrunner/ -run TestWeatherReportExample
```

The HTTP cassettes follow a VCR.py-style model — record modes
(`none`/`once`/`new_episodes`/`all`), configurable `match_on`, and
redact-on-write — documented in
[`docs/architecture/hosts.md`](../../docs/architecture/hosts.md#record--replay-http-cassettes).
The committed cassettes were recorded against the live API and trimmed to the
`Content-Type` header; to re-record:

```sh
KITSOKI_HTTP_CASSETTE_RECORD=all \
  go test ./internal/testrunner/ -run TestWeatherReportExample -count=1
```

> Note: the `forecast` cassette is a point-in-time snapshot (its dates and
> current conditions are whatever the API returned when it was recorded), so the
> `forecast.yaml` fixture asserts the geocode-derived `place` / `coords` plus the
> recorded `headline`. The `climate` cassette is the fixed 2023 archive, so the
> `climate.yaml` fixture asserts the **computed** annual summary exactly.

The [`flows/`](./flows/) fixtures drive all three paths end-to-end:

- [`forecast.yaml`](./flows/forecast.yaml) — `forecast Tokyo`: geocode + forecast
  served from [`cassettes/forecast.http.yaml`](./cassettes/forecast.http.yaml).
- [`climate.yaml`](./flows/climate.yaml) — `climate Tokyo`: geocode + 2023
  archive; the aggregated annual summary is asserted via `expect_world`.
- [`not_found.yaml`](./flows/not_found.yaml) — an unknown place: geocoding
  returns no results, the script `fail()`s, and the `on_error: failed` arc routes
  into the `failed` room with `world.last_error` set.

## Watch it

A Playwright spec drives a real `kitsoki web --flow tour.yaml` server in the
no-LLM posture and records a screen-capture tour (video + per-scene screenshots)
to `.artifacts/weather-report-tour/` — the trace timeline shows the genuine
`host.starlark.run` events and their `__http_exchanges` payload:

```sh
cd tools/runstatus && pnpm exec playwright test weather-report-tour --project=chromium
# fast (no dwells): WEB_CHAT_PACE=0 pnpm exec playwright test weather-report-tour …
```

This works because the web `--flow` posture honours `starlark_http_cassette:`
(see `cmd/kitsoki/runtime.go`): the REAL handler runs with its `ctx.http` GETs
replayed, no LLM and no socket.

## See also

- [`stories/starlark-enrich/`](../starlark-enrich/) — the minimal one-GET sibling.
- [`docs/architecture/hosts.md`](../../docs/architecture/hosts.md)
  (`host.starlark.run`) — the author-facing reference.
- [`internal/host/starlark/doc.go`](../../internal/host/starlark/doc.go) — the
  sandbox design.
