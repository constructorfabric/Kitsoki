# Docker end-to-end test

A reusable, hermetic check that the kitsoki CLI **builds with the documented
build dependencies** and **runs with the documented runtime dependencies**, in
a clean container — nothing borrowed from the host.

## Run it

```sh
make e2e-docker
# or
./test/e2e/run.sh
```

Exit code `0` means pass. Needs only Docker on the host (BuildKit is enabled
automatically).

## What it proves

The image has two stages, each pinning down one half of "system dependencies
are correct":

| Stage     | Base                  | Proves |
|-----------|-----------------------|--------|
| `builder` | `golang:1.25-bookworm` + Node 22 + pnpm 11.3.0 | The shipping build path `make install` works — fetch modules, build & embed the runstatus SPA via pnpm, compile the binary. A wrong/missing **build** dep fails the image build here. |
| `runtime` | `debian:bookworm-slim` + `git`, `bash`, `gh` | The binary runs carrying only the **runtime** deps kitsoki `exec()`s. (Pure-Go modernc SQLite ⇒ no cgo/libsqlite needed, so a slim glibc base suffices.) |

Inside `runtime`, [`smoke.sh`](./smoke.sh) asserts, and exits non-zero on any
required failure:

1. **System deps** — `kitsoki`, `git`, `bash` on PATH (required); `gh` (optional, warns if absent).
2. **Binary smoke** — `kitsoki version`, `kitsoki docs` (embedded assets), `kitsoki viz` (YAML load + emit).
3. **Deterministic flow suites** — `kitsoki test flows` for `cloak`, `dev-story`,
   `parallel_smoke`, `proposal_smoke`, `timeout`. These are Mode-2 fixtures: no
   LLM, no API key, no network. They exercise YAML loading, the state machine,
   the SQLite session store, expr evaluation, and host builtins.

> `background_jobs` and `choice_smoke` are intentionally excluded — they have
> pre-existing fixture failures unrelated to packaging/deps.

## Knobs

| Env | Effect |
|-----|--------|
| `KITSOKI_E2E_APPS="cloak dev-story"` | Narrow/extend the flow-suite set. |
| `KEEP_IMAGE=1` | Leave the built image around after the run. |
| `KITSOKI_E2E_IMAGE=name:tag` | Override the image tag. |

## Why these checks catch real breakage

`smoke.sh` fails loudly when a required dependency is missing — verified by
deleting `git` from a built image, after which the suite exits non-zero at the
system-deps stage. The LLM-driven paths (`claude` CLI, live API) are
deliberately out of scope: they need credentials and a network, so they can't be
part of a hermetic deps check. This test covers everything that should work with
zero secrets.
